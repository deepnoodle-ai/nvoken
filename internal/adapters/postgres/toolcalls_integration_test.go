package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	dispatchruntime "github.com/deepnoodle-ai/nvoken/internal/dispatch"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type durableCheckpointGenerator struct {
	coordinator ports.ToolCallCoordinator
}

func (g durableCheckpointGenerator) Generate(ctx context.Context, request domain.GenerationRequest) (domain.GenerationResponse, error) {
	if request.Claim == nil {
		return domain.GenerationResponse{}, errors.New("Invocation claim is required")
	}
	claim := *request.Claim
	_, err := g.coordinator.RecordModelCheckpoint(ctx, claim, modelToolCheckpoint("topology-provider-call", 3, 1))
	if err != nil {
		return domain.GenerationResponse{}, err
	}
	running, err := g.coordinator.StartBuiltinToolCall(ctx, claim, 1, "topology-provider-call")
	if err != nil {
		return domain.GenerationResponse{}, err
	}
	if _, err := g.coordinator.AcceptBuiltinToolResult(ctx, claim, running, json.RawMessage(`{"echo":"topology"}`), false); err != nil {
		return domain.GenerationResponse{}, err
	}
	if _, err := g.coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
		Iteration: 2,
		Message: domain.GenerationMessage{
			Role:    domain.MessageRoleAssistant,
			Content: json.RawMessage(`[{"type":"text","text":"topology complete"}]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  5,
			OutputTokens: 2,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
	}); err != nil {
		return domain.GenerationResponse{}, err
	}
	return domain.GenerationResponse{
		Usage: domain.ModelUsage{
			InputTokens:  8,
			OutputTokens: 3,
			Iterations:   2,
		},
		ServedModel:          "test-model-served",
		MessagesCheckpointed: true,
	}, nil
}

func TestDurableToolCallCheckpointFlow(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	input := runtimeInput()
	maxIterations := 2
	input.Spec.Budgets = &services.InvocationBudgetInput{
		MaxIterations: &maxIterations,
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	executionService := services.NewInvocationExecutionService(store, txm, clock, ids)
	claim, disposition, err := executionService.ClaimExact(ctx, ack.InvocationID, "tool-owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim disposition = %q, error = %v", disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	first := modelToolCheckpoint("provider-call-1", 3, 1)
	recorded, err := coordinator.RecordModelCheckpoint(ctx, claim, first)
	if err != nil {
		t.Fatalf("record first model checkpoint: %v", err)
	}
	if len(recorded.ToolCalls) != 1 || recorded.ToolCalls[0].Status != domain.ToolCallPending {
		t.Fatalf("recorded ToolCalls = %#v", recorded.ToolCalls)
	}
	stableCallID := recorded.ToolCalls[0].ID
	if !domain.ValidStableID(stableCallID, domain.PrefixToolCall) ||
		!domain.ValidStableID(recorded.Checkpoint.ID, domain.PrefixInvocationCheckpoint) {
		t.Fatalf("stable identities = ToolCall %q checkpoint %q", stableCallID, recorded.Checkpoint.ID)
	}
	if string(recorded.Message.Content) == string(first.Message.Content) ||
		!jsonContainsToolUseID(recorded.Message.Content, stableCallID) {
		t.Fatalf("normalized request message = %s", recorded.Message.Content)
	}
	otherInput := runtimeInput()
	otherInput.SessionKey = pointerString("other-tool-session")
	otherInput.IdempotencyKey = "other-tool-request"
	other, err := runtime.Admit(ctx, auth, otherInput)
	if err != nil {
		t.Fatalf("admit other Session: %v", err)
	}
	otherMessages, err := store.ListSessionMessages(ctx, other.SessionID)
	if err != nil || len(otherMessages) != 1 {
		t.Fatalf("other Session messages = %#v, error = %v", otherMessages, err)
	}
	mismatched := recorded.ToolCalls[0]
	mismatched.ID, err = ids.NewID(domain.PrefixToolCall)
	if err != nil {
		t.Fatalf("generate mismatched ToolCall ID: %v", err)
	}
	mismatched.ProviderCallID = "mismatched-provider-call"
	mismatched.BatchOrdinal = 1
	mismatched.RequestMessageID = otherMessages[0].ID
	mismatched.RequestMessageSequence = otherMessages[0].Sequence
	if err := store.CreateToolCall(ctx, mismatched); err == nil {
		t.Fatal("cross-Session request message reference unexpectedly committed")
	}
	replayed, err := coordinator.RecordModelCheckpoint(ctx, claim, first)
	if err != nil || replayed.Message.ID != recorded.Message.ID || replayed.ToolCalls[0].ID != stableCallID {
		t.Fatalf("equal replay = %#v, error = %v", replayed, err)
	}
	changed := first
	changed.Message.Content = json.RawMessage(`[{"type":"tool_use","id":"provider-call-1","name":"nvoken_test_echo","input":{"value":"changed"}}]`)
	changed.ToolCalls = []domain.ToolCallRequest{{
		ProviderCallID: "provider-call-1",
		Name:           "nvoken_test_echo",
		Mode:           domain.ToolCallModeBuiltin,
		Input:          json.RawMessage(`{"value":"changed"}`),
	}}
	if _, err := coordinator.RecordModelCheckpoint(ctx, claim, changed); !errors.Is(err, ports.ErrToolCallConflict) {
		t.Fatalf("changed replay error = %v", err)
	}

	running, err := coordinator.StartBuiltinToolCall(ctx, claim, 1, "provider-call-1")
	if err != nil || running.Call.Status != domain.ToolCallRunning || running.Attempt.Attempt != 1 {
		t.Fatalf("start ToolCall = %#v, error = %v", running, err)
	}
	result := json.RawMessage(`{"echo":"ok"}`)
	const submitters = 20
	start := make(chan struct{})
	errorsBySubmitter := make(chan error, submitters)
	var submissions sync.WaitGroup
	for range submitters {
		submissions.Add(1)
		go func() {
			defer submissions.Done()
			<-start
			_, submitErr := coordinator.AcceptBuiltinToolResult(ctx, claim, running, result, false)
			errorsBySubmitter <- submitErr
		}()
	}
	close(start)
	submissions.Wait()
	close(errorsBySubmitter)
	for submitErr := range errorsBySubmitter {
		if submitErr != nil {
			t.Fatalf("equal concurrent result: %v", submitErr)
		}
	}
	if _, err := coordinator.AcceptBuiltinToolResult(
		ctx,
		claim,
		running,
		json.RawMessage(`{"echo":"different"}`),
		false,
	); !errors.Is(err, ports.ErrToolCallConflict) {
		t.Fatalf("changed result error = %v", err)
	}

	second := domain.ModelCheckpointInput{
		Iteration: 2,
		Message: domain.GenerationMessage{
			Role:    domain.MessageRoleAssistant,
			Content: json.RawMessage(`[{"type":"text","text":"done"}]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  5,
			OutputTokens: 2,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
	}
	if _, err := coordinator.RecordModelCheckpoint(ctx, claim, second); err != nil {
		t.Fatalf("record second model checkpoint: %v", err)
	}
	aggregate := domain.ModelUsage{
		InputTokens:  8,
		OutputTokens: 3,
		Iterations:   2,
	}
	if err := executionService.Settle(ctx, claim, domain.InvocationExecutionResult{
		Status:               domain.InvocationCompleted,
		MessagesCheckpointed: true,
		Usage:                &aggregate,
		Provenance:           pointerModelProvenance(testModelProvenance()),
	}); err != nil {
		t.Fatalf("settle checkpointed Invocation: %v", err)
	}

	assertCompletedToolTrace(t, store, ack, stableCallID)
	assertToolSchemaHasNoPayloadColumns(t, pool)
	assertToolEvidenceIsImmutable(t, pool, ack.InvocationID, stableCallID, running.Attempt.ID)
}

func TestToolCoordinatorRunsThroughEmbeddedAndExactDispatchExecution(t *testing.T) {
	t.Run("embedded", func(t *testing.T) {
		pool, runtime, store, auth := newRuntimeFixture(t)
		ctx := context.Background()
		input := runtimeInputWithTwoIterations()
		ack, err := runtime.Admit(ctx, auth, input)
		if err != nil {
			t.Fatalf("admit: %v", err)
		}
		clock := identity.SystemClock{}
		ids := identity.NewUUIDv7Generator(clock)
		txm := NewTransactionManager(pool)
		ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
		claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "embedded-tool-owner", time.Minute)
		if err != nil || disposition != services.Claimed {
			t.Fatalf("claim disposition = %q, error = %v", disposition, err)
		}
		coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
		executor := services.NewGenerationExecutor(
			store,
			durableCheckpointGenerator{
				coordinator: coordinator,
			},
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)
		result, err := executor.Execute(ctx, claim)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if err := ownership.Settle(ctx, claim, result); err != nil {
			t.Fatalf("settle: %v", err)
		}
		calls, err := store.ListToolCallsByIteration(ctx, ack.InvocationID, 1)
		if err != nil || len(calls) != 1 {
			t.Fatalf("ToolCalls = %#v, error = %v", calls, err)
		}
		assertCompletedToolTrace(t, store, ack, calls[0].ID)
	})

	t.Run("exact dispatch", func(t *testing.T) {
		pool, _ := testDatabase(t, true)
		ctx := context.Background()
		store := NewStore(pool)
		txm := NewTransactionManager(pool)
		clock := identity.SystemClock{}
		ids := identity.NewUUIDv7Generator(clock)
		account, err := services.BootstrapInstallation(ctx, store, txm, clock, ids)
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		runtime := services.NewRuntimeService(
			store,
			txm,
			clock,
			ids,
			services.WithInvocationExecutionMode(
				services.InvocationExecutionCloudTasks,
				services.DefaultExecutionDispatchQueue,
			),
		)
		ack, err := runtime.Admit(ctx, runtimeAuth(account.ID), runtimeInputWithTwoIterations())
		if err != nil {
			t.Fatalf("admit: %v", err)
		}
		dispatches := newDispatchServiceForInvocationTest(t, store, txm, clock, ids)
		dispatch := activeInvocationDispatch(t, store, ack.InvocationID)
		ownership := services.NewInvocationExecutionService(
			store,
			txm,
			clock,
			ids,
			services.WithExecutionSegmentCeiling(time.Second),
		)
		coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
		var attemptLogs bytes.Buffer
		attemptLogger := slog.New(slog.NewTextHandler(&attemptLogs, nil))
		executor := services.NewGenerationExecutor(
			store,
			durableCheckpointGenerator{
				coordinator: coordinator,
			},
			attemptLogger,
		)
		attempts, err := dispatchruntime.NewAttemptService(
			dispatches,
			ownership,
			executor,
			store,
			txm,
			clock,
			"exact-tool-owner",
			dispatchEngineConfig(),
			nil,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)
		if err != nil {
			t.Fatalf("configure attempt service: %v", err)
		}
		outcome, err := attempts.Attempt(ctx, dispatch.ID)
		if err != nil || outcome != services.DispatchAttemptSettled {
			t.Fatalf("attempt outcome = %q, error = %v", outcome, err)
		}
		calls, err := store.ListToolCallsByIteration(ctx, ack.InvocationID, 1)
		if err != nil || len(calls) != 1 {
			t.Fatalf("ToolCalls = %#v, error = %v", calls, err)
		}
		invocation, err := store.GetInvocation(ctx, ack.InvocationID)
		if err != nil || invocation.Status != domain.InvocationCompleted {
			t.Fatalf("completed Invocation = %#v, error = %v, logs = %s", invocation, err, attemptLogs.String())
		}
		assertCompletedToolTrace(t, store, ack, calls[0].ID)
		storedDispatch, err := store.GetExecutionDispatch(ctx, dispatch.ID)
		if err != nil || storedDispatch.Status != domain.ExecutionDispatchSettled {
			t.Fatalf("settled dispatch = %#v, error = %v", storedDispatch, err)
		}
	})
}

func TestCallbackAndClientToolCallsRemainInert(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	ack, err := runtime.Admit(ctx, auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	executionService := services.NewInvocationExecutionService(store, txm, clock, ids)
	claim, disposition, err := executionService.ClaimExact(ctx, ack.InvocationID, "inert-tool-owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim disposition = %q, error = %v", disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role: domain.MessageRoleAssistant,
			Content: json.RawMessage(`[
                {"type":"tool_use","id":"callback-provider-call","name":"host_callback","input":{"value":1}},
                {"type":"tool_use","id":"client-provider-call","name":"client_prompt","input":{"value":2}}
            ]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  2,
			OutputTokens: 1,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "callback-provider-call",
				Name:           "host_callback",
				Mode:           domain.ToolCallModeCallback,
				Input:          json.RawMessage(`{"value":1}`),
			},
			{
				ProviderCallID: "client-provider-call",
				Name:           "client_prompt",
				Mode:           domain.ToolCallModeClient,
				Input:          json.RawMessage(`{"value":2}`),
			},
		},
	})
	if err != nil || len(recorded.ToolCalls) != 2 {
		t.Fatalf("record inert ToolCalls = %#v, error = %v", recorded.ToolCalls, err)
	}
	for _, call := range recorded.ToolCalls {
		if _, err := coordinator.StartBuiltinToolCall(ctx, claim, call.Iteration, call.ProviderCallID); !errors.Is(err, ports.ErrToolCallNotRunnable) {
			t.Fatalf("start %s ToolCall error = %v", call.Mode, err)
		}
	}
	var attempts int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM tool_call_attempts WHERE invocation_id = $1", ack.InvocationID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("inert attempt count = %d, error = %v", attempts, err)
	}
	if _, err := runtime.CancelInvocation(ctx, auth, ack.InvocationID); err != nil {
		t.Fatalf("cancel inert ToolCalls: %v", err)
	}
}

func TestTerminalInvocationClosesOpenToolCalls(t *testing.T) {
	for _, test := range []struct {
		name       string
		terminal   domain.InvocationStatus
		toolStatus domain.ToolCallStatus
		finish     func(context.Context, *services.RuntimeService, *services.InvocationExecutionService, domain.RuntimeAuthContext, services.InvocationAcknowledgement, *mutableClock) error
	}{
		{
			name:       "cancellation",
			terminal:   domain.InvocationCancelled,
			toolStatus: domain.ToolCallCancelled,
			finish: func(ctx context.Context, runtime *services.RuntimeService, _ *services.InvocationExecutionService, auth domain.RuntimeAuthContext, ack services.InvocationAcknowledgement, _ *mutableClock) error {
				_, err := runtime.CancelInvocation(ctx, auth, ack.InvocationID)
				return err
			},
		},
		{
			name:       "deadline",
			terminal:   domain.InvocationFailed,
			toolStatus: domain.ToolCallFailed,
			finish: func(ctx context.Context, _ *services.RuntimeService, execution *services.InvocationExecutionService, _ domain.RuntimeAuthContext, _ services.InvocationAcknowledgement, clock *mutableClock) error {
				clock.Advance(2 * time.Second)
				_, err := execution.ReapExpired(ctx, 10)
				return err
			},
		},
		{
			name:       "lease loss",
			terminal:   domain.InvocationFailed,
			toolStatus: domain.ToolCallFailed,
			finish: func(ctx context.Context, _ *services.RuntimeService, execution *services.InvocationExecutionService, _ domain.RuntimeAuthContext, _ services.InvocationAcknowledgement, clock *mutableClock) error {
				clock.Advance(time.Minute + time.Second)
				_, err := execution.ReapExpired(ctx, 10)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool, runtime, store, auth := newRuntimeFixture(t)
			ctx := context.Background()
			input := runtimeInput()
			if test.name == "deadline" {
				wallClockSeconds := int64(1)
				input.Spec.Budgets = &services.InvocationBudgetInput{
					WallClockTimeoutSeconds: &wallClockSeconds,
				}
			}
			ack, err := runtime.Admit(ctx, auth, input)
			if err != nil {
				t.Fatalf("admit: %v", err)
			}
			clock := newMutableClock(time.Now().UTC())
			ids := identity.NewUUIDv7Generator(clock)
			txm := NewTransactionManager(pool)
			executionService := services.NewInvocationExecutionService(store, txm, clock, ids)
			claim, disposition, err := executionService.ClaimExact(ctx, ack.InvocationID, "terminal-owner", time.Minute)
			if err != nil || disposition != services.Claimed {
				t.Fatalf("claim disposition = %q, error = %v", disposition, err)
			}
			coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
			recorded, err := coordinator.RecordModelCheckpoint(ctx, claim, modelToolCheckpoint("terminal-provider-call", 2, 1))
			if err != nil {
				t.Fatalf("record checkpoint: %v", err)
			}
			running, err := coordinator.StartBuiltinToolCall(ctx, claim, 1, "terminal-provider-call")
			if err != nil {
				t.Fatalf("start ToolCall: %v", err)
			}
			if err := test.finish(ctx, runtime, executionService, auth, ack, clock); err != nil {
				t.Fatalf("terminalize: %v", err)
			}
			invocation, err := store.GetInvocation(ctx, ack.InvocationID)
			if err != nil || invocation.Status != test.terminal || len(invocation.Usage) != 0 {
				t.Fatalf("terminal Invocation = %#v, error = %v", invocation, err)
			}
			call, err := store.GetToolCall(ctx, recorded.ToolCalls[0].ID)
			if err != nil || call.Status != test.toolStatus || call.ResultMessageID == nil {
				t.Fatalf("terminal ToolCall = %#v, error = %v", call, err)
			}
			messages, err := store.ListSessionMessages(ctx, ack.SessionID)
			if err != nil || len(messages) != 3 || messages[2].Role != domain.MessageRoleTool ||
				!jsonContainsToolUseID(messages[2].Content, call.ID) {
				t.Fatalf("terminal transcript = %#v, error = %v", messages, err)
			}
			states, err := store.ListInvocationStates(ctx, ack.SessionID)
			if err != nil || states[len(states)-1].ThroughMessageSequence == nil ||
				*states[len(states)-1].ThroughMessageSequence != messages[2].Sequence {
				t.Fatalf("terminal lifecycle = %#v, error = %v", states, err)
			}
			if _, err := coordinator.AcceptBuiltinToolResult(
				ctx,
				claim,
				running,
				json.RawMessage(`{"late":true}`),
				false,
			); !errors.Is(err, ports.ErrLeaseLost) {
				t.Fatalf("stale result error = %v", err)
			}
			var attemptStatus string
			if err := pool.QueryRow(ctx, "SELECT status FROM tool_call_attempts WHERE id = $1", running.Attempt.ID).Scan(&attemptStatus); err != nil || attemptStatus != string(test.toolStatus) {
				t.Fatalf("attempt status = %q, error = %v", attemptStatus, err)
			}
		})
	}
}

func TestModelCheckpointWriteFailuresRollBackTheWholeCut(t *testing.T) {
	for _, target := range []string{
		"session_messages",
		"tool_calls",
		"model_usage_receipts",
		"invocation_checkpoints",
		"invocations",
	} {
		t.Run(target, func(t *testing.T) {
			pool, runtime, store, auth := newRuntimeFixture(t)
			ctx := context.Background()
			ack, err := runtime.Admit(ctx, auth, runtimeInput())
			if err != nil {
				t.Fatalf("admit: %v", err)
			}
			clock := identity.SystemClock{}
			ids := identity.NewUUIDv7Generator(clock)
			txm := NewTransactionManager(pool)
			executionService := services.NewInvocationExecutionService(store, txm, clock, ids)
			claim, disposition, err := executionService.ClaimExact(ctx, ack.InvocationID, "fault-owner", time.Minute)
			if err != nil || disposition != services.Claimed {
				t.Fatalf("claim disposition = %q, error = %v", disposition, err)
			}
			functionName := "fail_checkpoint_" + target
			if _, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'injected checkpoint failure';
END;
$$`, functionName)); err != nil {
				t.Fatalf("create failure function: %v", err)
			}
			when := ""
			action := "INSERT OR UPDATE"
			if target == "session_messages" {
				when = " WHEN (NEW.role = 'assistant')"
			}
			if target == "invocations" {
				action = "UPDATE"
				when = " WHEN (NEW.current_checkpoint_sequence > OLD.current_checkpoint_sequence)"
			}
			if _, err := pool.Exec(ctx, fmt.Sprintf(
				"CREATE TRIGGER injected_checkpoint_failure BEFORE %s ON %s FOR EACH ROW%s EXECUTE FUNCTION %s()",
				action,
				target,
				when,
				functionName,
			)); err != nil {
				t.Fatalf("create failure trigger: %v", err)
			}
			coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
			if _, err := coordinator.RecordModelCheckpoint(ctx, claim, modelToolCheckpoint("fault-provider-call", 2, 1)); err == nil {
				t.Fatal("model checkpoint unexpectedly committed")
			}
			messages, err := store.ListSessionMessages(ctx, ack.SessionID)
			if err != nil || len(messages) != 1 {
				t.Fatalf("rolled-back messages = %#v, error = %v", messages, err)
			}
			invocation, err := store.GetInvocation(ctx, ack.InvocationID)
			if err != nil || invocation.CurrentCheckpointSequence != 0 || invocation.CurrentIteration != 0 {
				t.Fatalf("rolled-back cursor = %#v, error = %v", invocation, err)
			}
			for _, table := range []string{
				"tool_calls",
				"tool_call_attempts",
				"model_usage_receipts",
				"invocation_checkpoints",
			} {
				var count int
				if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("%s row count = %d, error = %v", table, count, err)
				}
			}
		})
	}
}

func modelToolCheckpoint(providerCallID string, inputTokens, outputTokens int) domain.ModelCheckpointInput {
	return domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role:    domain.MessageRoleAssistant,
			Content: json.RawMessage(`[{"type":"tool_use","id":"` + providerCallID + `","name":"nvoken_test_echo","input":{"value":"ok"}}]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
		ToolCalls: []domain.ToolCallRequest{{
			ProviderCallID: providerCallID,
			Name:           "nvoken_test_echo",
			Mode:           domain.ToolCallModeBuiltin,
			Input:          json.RawMessage(`{"value":"ok"}`),
		}},
	}
}

func runtimeInputWithTwoIterations() services.CreateInvocationInput {
	input := runtimeInput()
	maxIterations := 2
	input.Spec.Budgets = &services.InvocationBudgetInput{
		MaxIterations: &maxIterations,
	}
	return input
}

func testModelProvenance() domain.ModelProvenance {
	return domain.ModelProvenance{
		Provider:         "anthropic",
		RequestedModel:   "test-model",
		ServedModel:      "test-model-served",
		CredentialSource: "installation_byok",
	}
}

func pointerModelProvenance(value domain.ModelProvenance) *domain.ModelProvenance {
	return &value
}

func jsonContainsToolUseID(payload json.RawMessage, id string) bool {
	var blocks []struct {
		ID        string `json:"id"`
		ToolUseID string `json:"tool_use_id"`
	}
	if json.Unmarshal(payload, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if block.ID == id || block.ToolUseID == id {
			return true
		}
	}
	return false
}

func assertCompletedToolTrace(t *testing.T, store *Store, ack services.InvocationAcknowledgement, toolCallID string) {
	t.Helper()
	ctx := context.Background()
	invocation, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil || invocation.Status != domain.InvocationCompleted || invocation.CurrentCheckpointSequence != 3 || invocation.CurrentIteration != 2 {
		t.Fatalf("completed Invocation = %#v, error = %v", invocation, err)
	}
	messages, err := store.ListSessionMessages(ctx, ack.SessionID)
	if err != nil || len(messages) != 4 {
		t.Fatalf("completed transcript = %#v, error = %v", messages, err)
	}
	wantRoles := []domain.MessageRole{
		domain.MessageRoleUser,
		domain.MessageRoleAssistant,
		domain.MessageRoleTool,
		domain.MessageRoleAssistant,
	}
	for index, want := range wantRoles {
		if messages[index].Role != want {
			t.Fatalf("message %d role = %q, want %q", index, messages[index].Role, want)
		}
	}
	if !jsonContainsToolUseID(messages[1].Content, toolCallID) || !jsonContainsToolUseID(messages[2].Content, toolCallID) {
		t.Fatalf("tool transcript pair = %s / %s", messages[1].Content, messages[2].Content)
	}
	receipts, err := store.ListModelUsageReceipts(ctx, ack.InvocationID)
	if err != nil || len(receipts) != 2 {
		t.Fatalf("usage receipts = %#v, error = %v", receipts, err)
	}
	checkpoints, err := store.ListInvocationCheckpoints(ctx, ack.InvocationID)
	if err != nil || len(checkpoints) != 3 || checkpoints[2].ThroughMessageSequence != messages[3].Sequence {
		t.Fatalf("checkpoints = %#v, error = %v", checkpoints, err)
	}
	states, err := store.ListInvocationStates(ctx, ack.SessionID)
	if err != nil || states[len(states)-1].ThroughMessageSequence == nil ||
		*states[len(states)-1].ThroughMessageSequence != messages[3].Sequence {
		t.Fatalf("lifecycle states = %#v, error = %v", states, err)
	}
}

func assertToolSchemaHasNoPayloadColumns(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var count int
	err := pool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM information_schema.columns
         WHERE table_schema = current_schema()
           AND table_name = 'tool_calls'
           AND column_name IN ('request', 'request_payload', 'result', 'result_payload', 'content')`,
	).Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("ToolCall payload columns = %d, error = %v", count, err)
	}
}

func assertToolEvidenceIsImmutable(t *testing.T, pool *pgxpool.Pool, invocationID, toolCallID, attemptID string) {
	t.Helper()
	ctx := context.Background()
	for name, mutate := range map[string]func() error{
		"ToolCall request identity": func() error {
			_, err := pool.Exec(ctx, "UPDATE tool_calls SET name = 'changed' WHERE id = $1", toolCallID)
			return err
		},
		"terminal ToolCall attempt": func() error {
			_, err := pool.Exec(ctx, "UPDATE tool_call_attempts SET status = 'failed' WHERE id = $1", attemptID)
			return err
		},
		"ToolCall deletion": func() error {
			_, err := pool.Exec(ctx, "DELETE FROM tool_calls WHERE id = $1", toolCallID)
			return err
		},
		"ToolCall attempt deletion": func() error {
			_, err := pool.Exec(ctx, "DELETE FROM tool_call_attempts WHERE id = $1", attemptID)
			return err
		},
		"usage receipt": func() error {
			_, err := pool.Exec(ctx, "UPDATE model_usage_receipts SET usage = '{}' WHERE invocation_id = $1", invocationID)
			return err
		},
		"checkpoint": func() error {
			_, err := pool.Exec(ctx, "DELETE FROM invocation_checkpoints WHERE invocation_id = $1", invocationID)
			return err
		},
		"checkpoint cursor": func() error {
			_, err := pool.Exec(ctx, "UPDATE invocations SET current_checkpoint_sequence = 1 WHERE id = $1", invocationID)
			return err
		},
	} {
		if err := mutate(); err == nil {
			t.Fatalf("%s mutation unexpectedly succeeded", name)
		}
	}
}
