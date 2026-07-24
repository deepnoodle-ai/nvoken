package postgres

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestRetainedWorkSurvivesCompatibleUpgradeAndRollback(t *testing.T) {
	pool, _ := testDatabase(t, true)
	ctx := context.Background()
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := newMutableClock(time.Now().UTC())
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(ctx, store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	auth := runtimeAuth(account.ID)
	runtime := services.NewRuntimeService(
		store,
		txm,
		clock,
		ids,
		services.WithCallbackTools(true),
	)
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)

	waitingAck, clientCallID := seedWaitingCallbackWork(
		t,
		ctx,
		runtime,
		ownership,
		coordinator,
		auth,
	)
	queuedAck := admitUpgradeFixture(t, ctx, runtime, auth, "upgrade-queued")
	runningAck := admitUpgradeFixture(t, ctx, runtime, auth, "upgrade-running")
	runningClaim, disposition, err := ownership.ClaimExact(ctx, runningAck.InvocationID, "binary-n-running", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim running fixture = %#v, %q, %v", runningClaim, disposition, err)
	}
	terminalAck := admitUpgradeFixture(t, ctx, runtime, auth, "upgrade-terminal")
	terminal, err := runtime.CancelInvocation(ctx, auth, terminalAck.InvocationID)
	if err != nil || terminal.Status != domain.InvocationCancelled {
		t.Fatalf("cancel terminal fixture = %#v, %v", terminal, err)
	}

	// This test-only N+1 fixture models an ordinary expand migration one
	// step past the embedded head. The production rule requires the same
	// two values in its SQL and manifest.
	if _, err := pool.Exec(ctx, `
		UPDATE nvoken_schema_migrations SET version = 18;
		UPDATE nvoken_schema_compatibility
		SET schema_version = 18, minimum_binary_schema_version = 17
	`); err != nil {
		t.Fatalf("apply compatible fixture migration: %v", err)
	}
	previousStatus, err := InspectSchemaForVersion(ctx, pool, 17)
	if err != nil || previousStatus.State != SchemaCompatibleNewer {
		t.Fatalf("binary N status after migration = %#v, %v", previousStatus, err)
	}
	nextStatus, err := InspectSchemaForVersion(ctx, pool, 18)
	if err != nil || nextStatus.State != SchemaCompatible {
		t.Fatalf("binary N+1 status after migration = %#v, %v", nextStatus, err)
	}

	queuedClaim, disposition, err := ownership.ClaimExact(ctx, queuedAck.InvocationID, "binary-n-plus-one", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim retained queued work = %#v, %q, %v", queuedClaim, disposition, err)
	}
	if err := ownership.Settle(ctx, queuedClaim, completedResult()); err != nil {
		t.Fatalf("settle retained queued work: %v", err)
	}
	if err := ownership.Settle(ctx, runningClaim, completedResult()); err != nil {
		t.Fatalf("settle retained running work: %v", err)
	}
	if err := ownership.Settle(ctx, runningClaim, completedResult()); err == nil {
		t.Fatal("duplicate running settlement succeeded")
	}

	deliveryService, err := services.NewCallbackDeliveryService(
		store,
		txm,
		clock,
		ids,
		nil,
		services.DefaultCallbackDeliveryConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("new callback delivery service: %v", err)
	}
	deliveryClaim, found, err := deliveryService.ClaimNext(ctx, "binary-n-plus-one-callback")
	if err != nil || !found {
		t.Fatalf("claim retained callback = %#v, %t, %v", deliveryClaim, found, err)
	}
	if err := deliveryService.ProcessClaim(ctx, &callbackIntegrationTransport{}, deliveryClaim); err != nil {
		t.Fatalf("process retained callback: %v", err)
	}
	if _, found, err := deliveryService.ClaimNext(ctx, "duplicate-callback"); err != nil || found {
		t.Fatalf("duplicate callback claim found = %t, error = %v", found, err)
	}

	clientResult := services.HostToolResultInput{
		ToolCallID: clientCallID,
		Content:    json.RawMessage(`{"accepted":true}`),
	}
	resumed, err := runtime.SubmitHostToolResults(ctx, auth, waitingAck.InvocationID, services.SubmitHostToolResultsInput{
		Results: []services.HostToolResultInput{clientResult},
	})
	if err != nil || resumed.Status != domain.InvocationQueued {
		t.Fatalf("resume retained waiting work = %#v, %v", resumed, err)
	}

	// Roll back only the application binary. Schema 18 remains in place and
	// binary N reuses the accepted results under the same fences.
	rollbackStatus, err := InspectSchemaForVersion(ctx, pool, 17)
	if err != nil || rollbackStatus.State != SchemaCompatibleNewer {
		t.Fatalf("rollback schema status = %#v, %v", rollbackStatus, err)
	}
	replay, err := runtime.SubmitHostToolResults(ctx, auth, waitingAck.InvocationID, services.SubmitHostToolResultsInput{
		Results: []services.HostToolResultInput{clientResult},
	})
	if err != nil || len(replay.Results) != 1 || !replay.Results[0].Deduplicated {
		t.Fatalf("rollback client result replay = %#v, %v", replay, err)
	}
	resumedClaim, disposition, err := ownership.ClaimExact(ctx, waitingAck.InvocationID, "binary-n-rollback", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("rollback claim = %#v, %q, %v", resumedClaim, disposition, err)
	}
	rollbackUsage := domain.ModelUsage{
		InputTokens:  2,
		OutputTokens: 1,
		Iterations:   1,
	}
	rollbackProvenance := testModelProvenance()
	if err := ownership.Settle(ctx, resumedClaim, domain.InvocationExecutionResult{
		Status:               domain.InvocationFailed,
		Error:                json.RawMessage(`{"code":"budget_exceeded","message":"The execution budget was exceeded.","details":{"kind":"iterations"}}`),
		MessagesCheckpointed: true,
		Usage:                &rollbackUsage,
		Provenance:           &rollbackProvenance,
	}); err != nil {
		t.Fatalf("rollback settlement: %v", err)
	}
	terminalAgain, err := runtime.CancelInvocation(ctx, auth, terminalAck.InvocationID)
	if err != nil || terminalAgain.Status != domain.InvocationCancelled {
		t.Fatalf("terminal readback after rollback = %#v, %v", terminalAgain, err)
	}

	for _, invocationID := range []string{
		waitingAck.InvocationID,
		queuedAck.InvocationID,
		runningAck.InvocationID,
		terminalAck.InvocationID,
	} {
		if _, err := store.GetInvocation(ctx, invocationID); err != nil {
			t.Fatalf("retained Invocation %s: %v", invocationID, err)
		}
	}
}

func admitUpgradeFixture(
	t *testing.T,
	ctx context.Context,
	runtime *services.RuntimeService,
	auth domain.RuntimeAuthContext,
	key string,
) services.InvocationAcknowledgement {
	t.Helper()
	input := runtimeInput()
	input.SessionKey = pointerString(key)
	input.IdempotencyKey = key
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit %s: %v", key, err)
	}
	return ack
}

func seedWaitingCallbackWork(
	t *testing.T,
	ctx context.Context,
	runtime *services.RuntimeService,
	ownership *services.InvocationExecutionService,
	coordinator *services.ToolCheckpointService,
	auth domain.RuntimeAuthContext,
) (services.InvocationAcknowledgement, string) {
	t.Helper()
	input := runtimeInputWithTwoIterations()
	input.SessionKey = pointerString("upgrade-waiting")
	input.IdempotencyKey = "upgrade-waiting"
	input.Spec.Tools = []services.HostToolSpec{
		{
			Name:        "upgrade_callback",
			Description: "Exercise retained callback delivery",
			Mode:        string(domain.ToolCallModeCallback),
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Callback: &services.CallbackTarget{
				URL: "https://callbacks.example.test/tools/upgrade",
			},
		},
		{
			Name:        "upgrade_client",
			Description: "Exercise retained client delivery",
			Mode:        string(domain.ToolCallModeHost),
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit waiting callback fixture: %v", err)
	}
	claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "binary-n-tools", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim waiting callback fixture = %#v, %q, %v", claim, disposition, err)
	}
	usage := domain.ModelUsage{
		InputTokens:  2,
		OutputTokens: 1,
		Iterations:   1,
	}
	provenance := testModelProvenance()
	recorded, err := coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role: domain.MessageRoleAssistant,
			Content: json.RawMessage(`[
				{"type":"tool_use","id":"provider-callback","name":"upgrade_callback","input":{}},
				{"type":"tool_use","id":"provider-client","name":"upgrade_client","input":{}}
			]`),
		},
		Usage:      usage,
		Provenance: provenance,
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "provider-callback",
				Name:           "upgrade_callback",
				Mode:           domain.ToolCallModeCallback,
				Input:          json.RawMessage(`{}`),
				CallbackURL:    "https://callbacks.example.test/tools/upgrade",
			},
			{
				ProviderCallID: "provider-client",
				Name:           "upgrade_client",
				Mode:           domain.ToolCallModeHost,
				Input:          json.RawMessage(`{}`),
			},
		},
	})
	if err != nil || len(recorded.ToolCalls) != 2 {
		t.Fatalf("record waiting callback fixture = %#v, %v", recorded, err)
	}
	if err := ownership.Settle(ctx, claim, domain.InvocationExecutionResult{
		Status:               domain.InvocationWaiting,
		MessagesCheckpointed: true,
		Usage:                &usage,
		Provenance:           &provenance,
	}); err != nil {
		t.Fatalf("park waiting callback fixture: %v", err)
	}
	return ack, recorded.ToolCalls[1].ID
}
