package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
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

type durableResumeContinuationGenerator struct {
	coordinator ports.ToolCallCoordinator
}

func (g durableResumeContinuationGenerator) Generate(
	ctx context.Context,
	request domain.GenerationRequest,
) (domain.GenerationResponse, error) {
	if request.Claim == nil || request.Resume == nil || request.Resume.Iteration != 1 ||
		len(request.Resume.OpenToolCalls) != 0 || len(request.Messages) != 3 ||
		request.Messages[2].Role != domain.MessageRoleTool {
		return domain.GenerationResponse{}, ports.ErrGenerationRecoveryInvalid
	}
	continuationUsage := domain.ModelUsage{
		InputTokens:  5,
		OutputTokens: 2,
		Iterations:   1,
	}
	if _, err := g.coordinator.RecordModelCheckpoint(
		ctx,
		*request.Claim,
		domain.ModelCheckpointInput{
			Iteration: 2,
			Message: domain.GenerationMessage{
				Role:    domain.MessageRoleAssistant,
				Content: json.RawMessage(`[{"type":"text","text":"continued after accepted result"}]`),
			},
			Usage:      continuationUsage,
			Provenance: testModelProvenance(),
		},
	); err != nil {
		return domain.GenerationResponse{}, err
	}
	usage := request.Resume.Usage
	usage.InputTokens += continuationUsage.InputTokens
	usage.OutputTokens += continuationUsage.OutputTokens
	usage.Iterations += continuationUsage.Iterations
	return domain.GenerationResponse{
		Usage:                usage,
		ServedModel:          "test-model-served",
		MessagesCheckpointed: true,
	}, nil
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

func TestDurableClientToolResultsResumeOnAnotherEngine(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	input := runtimeInputWithTwoIterations()
	input.Spec.Tools = []services.ClientToolSpec{
		{
			Name:        "lookup_order",
			Description: "Look up an order",
			Mode:        "client",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}},"additionalProperties":false}`),
		},
		{
			Name:        "notify_user",
			Description: "Notify a user",
			Mode:        "client",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"additionalProperties":false}`),
		},
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "client-tool-owner-a", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim disposition = %q, error = %v", disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role: domain.MessageRoleAssistant,
			Content: json.RawMessage(`[
				{"type":"tool_use","id":"provider-lookup","name":"lookup_order","input":{"order_id":"order-1"}},
				{"type":"tool_use","id":"provider-notify","name":"notify_user","input":{"message":"ready"}}
			]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  3,
			OutputTokens: 1,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "provider-lookup",
				Name:           "lookup_order",
				Mode:           domain.ToolCallModeClient,
				Input:          json.RawMessage(`{"order_id":"order-1"}`),
			},
			{
				ProviderCallID: "provider-notify",
				Name:           "notify_user",
				Mode:           domain.ToolCallModeClient,
				Input:          json.RawMessage(`{"message":"ready"}`),
			},
		},
	})
	if err != nil || len(recorded.ToolCalls) != 2 {
		t.Fatalf("record client checkpoint = %#v, error = %v", recorded, err)
	}
	usage := domain.ModelUsage{
		InputTokens:  3,
		OutputTokens: 1,
		Iterations:   1,
	}
	if err := ownership.Settle(ctx, claim, domain.InvocationExecutionResult{
		Status:               domain.InvocationWaiting,
		MessagesCheckpointed: true,
		Usage:                &usage,
		Provenance:           pointerModelProvenance(testModelProvenance()),
	}); err != nil {
		t.Fatalf("park client tool Invocation: %v", err)
	}

	parked, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil || parked.Status != domain.InvocationWaiting || parked.LeaseOwner != nil ||
		parked.LeaseExpiresAt != nil || parked.ExecutionDeadlineAt != nil ||
		parked.ActiveSegmentStartedAt != nil {
		t.Fatalf("parked Invocation = %#v, error = %v", parked, err)
	}
	parkedActiveExecutionMS := parked.ActiveExecutionMS
	read, err := runtime.GetInvocation(ctx, auth, ack.InvocationID)
	if err != nil || len(read.PendingToolCalls) != 2 ||
		read.PendingToolCalls[0].ID != recorded.ToolCalls[0].ID ||
		read.PendingToolCalls[1].ID != recorded.ToolCalls[1].ID {
		t.Fatalf("pending client tools = %#v, error = %v", read.PendingToolCalls, err)
	}
	sessionRead, err := runtime.GetSession(ctx, auth, ack.SessionID)
	if err != nil || len(sessionRead.PendingToolCalls) != 2 ||
		sessionRead.ActiveInvocationStatus == nil ||
		*sessionRead.ActiveInvocationStatus != domain.InvocationWaiting {
		t.Fatalf("Session pending client tools = %#v, error = %v", sessionRead, err)
	}

	firstResult := services.ClientToolResultInput{
		ToolCallID: recorded.ToolCalls[1].ID,
		Content:    json.RawMessage(`{"notified":true}`),
	}
	type concurrentSubmission struct {
		result services.SubmitClientToolResultsResult
		err    error
	}
	const submitters = 12
	start := make(chan struct{})
	submissions := make(chan concurrentSubmission, submitters)
	var group sync.WaitGroup
	for range submitters {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, submitErr := runtime.SubmitClientToolResults(
				ctx,
				auth,
				ack.InvocationID,
				services.SubmitClientToolResultsInput{
					Results: []services.ClientToolResultInput{firstResult},
				},
			)
			submissions <- concurrentSubmission{
				result: result,
				err:    submitErr,
			}
		}()
	}
	close(start)
	group.Wait()
	close(submissions)
	newResults := 0
	for submission := range submissions {
		if submission.err != nil || submission.result.Status != domain.InvocationWaiting ||
			len(submission.result.PendingToolCalls) != 1 || len(submission.result.Results) != 1 {
			t.Fatalf("concurrent partial client result = %#v, error = %v", submission.result, submission.err)
		}
		if !submission.result.Results[0].Deduplicated {
			newResults++
		}
	}
	if newResults != 1 {
		t.Fatalf("new concurrent client results = %d, want 1", newResults)
	}
	stillParked, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil || stillParked.Status != domain.InvocationWaiting ||
		stillParked.ActiveExecutionMS != parkedActiveExecutionMS {
		t.Fatalf("partially settled Invocation = %#v, error = %v", stillParked, err)
	}
	deduplicated, err := runtime.SubmitClientToolResults(ctx, auth, ack.InvocationID, services.SubmitClientToolResultsInput{
		Results: []services.ClientToolResultInput{firstResult},
	})
	if err != nil || len(deduplicated.Results) != 1 || !deduplicated.Results[0].Deduplicated {
		t.Fatalf("equal partial replay = %#v, error = %v", deduplicated, err)
	}
	changed := firstResult
	changed.Content = json.RawMessage(`{"notified":false}`)
	if _, err := runtime.SubmitClientToolResults(ctx, auth, ack.InvocationID, services.SubmitClientToolResultsInput{
		Results: []services.ClientToolResultInput{changed},
	}); !publicErrorHasCode(err, services.CodeToolResultConflict) {
		t.Fatalf("changed partial replay error = %v", err)
	}
	secondResult := services.ClientToolResultInput{
		ToolCallID: recorded.ToolCalls[0].ID,
		Content:    json.RawMessage(`{"state":"ready"}`),
	}
	resumed, err := runtime.SubmitClientToolResults(ctx, auth, ack.InvocationID, services.SubmitClientToolResultsInput{
		Results: []services.ClientToolResultInput{
			firstResult,
			secondResult,
		},
	})
	if err != nil || resumed.Status != domain.InvocationQueued || len(resumed.PendingToolCalls) != 0 ||
		len(resumed.Results) != 2 || !resumed.Results[0].Deduplicated || resumed.Results[1].Deduplicated {
		t.Fatalf("final client results = %#v, error = %v", resumed, err)
	}
	messages, err := store.ListSessionMessages(ctx, ack.SessionID)
	if err != nil || len(messages) != 4 {
		t.Fatalf("partial result transcript = %#v, error = %v", messages, err)
	}

	secondClaim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "client-tool-owner-b", time.Minute)
	if err != nil || disposition != services.Claimed || secondClaim.Attempt != claim.Attempt+1 {
		t.Fatalf("resume claim = %#v, disposition = %q, error = %v", secondClaim, disposition, err)
	}
	executor := services.NewGenerationExecutor(
		store,
		durableResumeContinuationGenerator{
			coordinator: coordinator,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	result, err := executor.Execute(ctx, secondClaim)
	if err != nil || result.Status != domain.InvocationCompleted {
		t.Fatalf("resume execution = %#v, error = %v", result, err)
	}
	if err := ownership.Settle(ctx, secondClaim, result); err != nil {
		t.Fatalf("settle resumed Invocation: %v", err)
	}
	completed, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil || completed.Status != domain.InvocationCompleted ||
		completed.CurrentIteration != 2 || completed.CurrentCheckpointSequence != 4 {
		t.Fatalf("completed resumed Invocation = %#v, error = %v", completed, err)
	}
	replay, err := runtime.SubmitClientToolResults(ctx, auth, ack.InvocationID, services.SubmitClientToolResultsInput{
		Results: []services.ClientToolResultInput{
			secondResult,
			firstResult,
		},
	})
	if err != nil || replay.Status != domain.InvocationCompleted ||
		len(replay.Results) != 2 || !replay.Results[0].Deduplicated || !replay.Results[1].Deduplicated {
		t.Fatalf("terminal equal replay = %#v, error = %v", replay, err)
	}
}

func TestExpiredLeaseParksCommittedClientToolWithoutProviderReplay(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	input := runtimeInputWithTwoIterations()
	input.Spec.Tools = []services.ClientToolSpec{
		{
			Name:        "lookup_order",
			Description: "Look up an order",
			Mode:        "client",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := newMutableClock(time.Now().UTC())
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	firstClaim, disposition, err := ownership.ClaimExact(
		ctx,
		ack.InvocationID,
		"lost-client-owner",
		time.Minute,
	)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("first claim = %#v, disposition = %q, error = %v", firstClaim, disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(ctx, firstClaim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role:    domain.MessageRoleAssistant,
			Content: json.RawMessage(`[{"type":"tool_use","id":"provider-lookup","name":"lookup_order","input":{"order_id":"order-1"}}]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  3,
			OutputTokens: 1,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "provider-lookup",
				Name:           "lookup_order",
				Mode:           domain.ToolCallModeClient,
				Input:          json.RawMessage(`{"order_id":"order-1"}`),
			},
		},
	})
	if err != nil || len(recorded.ToolCalls) != 1 {
		t.Fatalf("record client checkpoint = %#v, error = %v", recorded, err)
	}
	clock.Advance(time.Minute + time.Nanosecond)
	recovered, err := ownership.ReapExpired(ctx, 10)
	if err != nil || len(recovered) != 1 || recovered[0].Status != domain.InvocationQueued {
		t.Fatalf("recovered = %#v, error = %v", recovered, err)
	}
	secondClaim, disposition, err := ownership.ClaimExact(
		ctx,
		ack.InvocationID,
		"replacement-client-owner",
		time.Minute,
	)
	if err != nil || disposition != services.Claimed || secondClaim.Attempt != 2 {
		t.Fatalf("replacement claim = %#v, disposition = %q, error = %v", secondClaim, disposition, err)
	}
	generator := &postgresModelGenerator{}
	result, err := services.NewGenerationExecutor(store, generator, nil).Execute(ctx, secondClaim)
	if err != nil || result.Status != domain.InvocationWaiting || !result.MessagesCheckpointed {
		t.Fatalf("recover client wait = %#v, error = %v", result, err)
	}
	if len(generator.Requests()) != 0 {
		t.Fatalf("provider replayed %d times", len(generator.Requests()))
	}
	if err := ownership.Settle(ctx, secondClaim, result); err != nil {
		t.Fatalf("park recovered client wait: %v", err)
	}
	parked, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil || parked.Status != domain.InvocationWaiting || parked.LeaseOwner != nil ||
		parked.CurrentCheckpointSequence != recorded.Checkpoint.Sequence {
		t.Fatalf("recovered parked Invocation = %#v, error = %v", parked, err)
	}
	pending, err := runtime.GetInvocation(ctx, auth, ack.InvocationID)
	if err != nil || len(pending.PendingToolCalls) != 1 ||
		pending.PendingToolCalls[0].ID != recorded.ToolCalls[0].ID {
		t.Fatalf("recovered pending tools = %#v, error = %v", pending.PendingToolCalls, err)
	}
}

func TestClientWaitClosesPendingBuiltinSiblings(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	input := runtimeInputWithTwoIterations()
	input.Spec.Tools = []services.ClientToolSpec{
		{
			Name:        "lookup_order",
			Description: "Look up an order",
			Mode:        "client",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "mixed-tool-owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim = %#v, disposition = %q, error = %v", claim, disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role: domain.MessageRoleAssistant,
			Content: json.RawMessage(`[
				{"type":"tool_use","id":"provider-client","name":"lookup_order","input":{}},
				{"type":"tool_use","id":"provider-builtin","name":"nvoken_test_echo","input":{"value":"ok"}}
			]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  3,
			OutputTokens: 1,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "provider-client",
				Name:           "lookup_order",
				Mode:           domain.ToolCallModeClient,
				Input:          json.RawMessage(`{}`),
			},
			{
				ProviderCallID: "provider-builtin",
				Name:           "nvoken_test_echo",
				Mode:           domain.ToolCallModeBuiltin,
				Input:          json.RawMessage(`{"value":"ok"}`),
			},
		},
	})
	if err != nil || len(recorded.ToolCalls) != 2 {
		t.Fatalf("record mixed checkpoint = %#v, error = %v", recorded, err)
	}
	runningBuiltin, err := coordinator.StartBuiltinToolCall(
		ctx,
		claim,
		1,
		"provider-builtin",
	)
	if err != nil || runningBuiltin.Call.Status != domain.ToolCallRunning {
		t.Fatalf("start mixed builtin = %#v, error = %v", runningBuiltin, err)
	}
	usage := domain.ModelUsage{
		InputTokens:  3,
		OutputTokens: 1,
		Iterations:   1,
	}
	if err := ownership.Settle(ctx, claim, domain.InvocationExecutionResult{
		Status:               domain.InvocationWaiting,
		MessagesCheckpointed: true,
		Usage:                &usage,
		Provenance:           pointerModelProvenance(testModelProvenance()),
	}); err != nil {
		t.Fatalf("park mixed client wait: %v", err)
	}
	builtin, err := store.GetToolCall(ctx, recorded.ToolCalls[1].ID)
	if err != nil || builtin.Status != domain.ToolCallFailed || builtin.ResultOrigin == nil ||
		*builtin.ResultOrigin != domain.ToolCallResultSystem {
		t.Fatalf("settled builtin sibling = %#v, error = %v", builtin, err)
	}
	builtinAttempt, err := store.GetCurrentToolCallAttemptForUpdate(
		ctx,
		builtin.ID,
		runningBuiltin.Attempt.Attempt,
	)
	if err != nil || builtinAttempt.Status != domain.ToolCallFailed {
		t.Fatalf("settled builtin attempt = %#v, error = %v", builtinAttempt, err)
	}
	client, err := store.GetToolCall(ctx, recorded.ToolCalls[0].ID)
	if err != nil || client.Status != domain.ToolCallPending || client.ResultOrigin != nil {
		t.Fatalf("pending client call = %#v, error = %v", client, err)
	}
	checkpoints, err := store.ListInvocationCheckpoints(ctx, ack.InvocationID)
	if err != nil || len(checkpoints) != 2 || checkpoints[1].ToolCallID == nil ||
		*checkpoints[1].ToolCallID != builtin.ID {
		t.Fatalf("mixed checkpoints = %#v, error = %v", checkpoints, err)
	}
}

func TestClientToolResultsRejectSystemSettledCalls(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		pool, runtime, store, auth := newRuntimeFixture(t)
		ctx := context.Background()
		clock := identity.SystemClock{}
		ids := identity.NewUUIDv7Generator(clock)
		txm := NewTransactionManager(pool)
		ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
		coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
		ack, call := admitAndParkSingleClientTool(
			t,
			ctx,
			runtime,
			ownership,
			coordinator,
			auth,
			"cancelled-client-owner",
		)
		if _, err := runtime.CancelInvocation(ctx, auth, ack.InvocationID); err != nil {
			t.Fatalf("cancel waiting Invocation: %v", err)
		}
		settled, err := store.GetToolCall(ctx, call.ID)
		if err != nil || settled.Status != domain.ToolCallCancelled || settled.ResultOrigin == nil ||
			*settled.ResultOrigin != domain.ToolCallResultSystem {
			t.Fatalf("cancelled client call = %#v, error = %v", settled, err)
		}
		_, err = runtime.SubmitClientToolResults(
			ctx,
			auth,
			ack.InvocationID,
			services.SubmitClientToolResultsInput{
				Results: []services.ClientToolResultInput{
					{
						ToolCallID: call.ID,
						Content:    json.RawMessage(`{"late":true}`),
					},
				},
			},
		)
		if !publicErrorHasCode(err, services.CodeInvocationNotWaiting) {
			t.Fatalf("post-cancellation result error = %v", err)
		}
	})

	t.Run("wall deadline", func(t *testing.T) {
		pool, _ := testDatabase(t, true)
		ctx := context.Background()
		store := NewStore(pool)
		txm := NewTransactionManager(pool)
		clock := newMutableClock(time.Now().UTC())
		ids := identity.NewUUIDv7Generator(clock)
		account, err := services.BootstrapInstallation(ctx, store, txm, clock, ids)
		if err != nil {
			t.Fatalf("bootstrap installation: %v", err)
		}
		auth := runtimeAuth(account.ID)
		runtime := services.NewRuntimeService(store, txm, clock, ids)
		ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
		coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
		ack, call := admitAndParkSingleClientTool(
			t,
			ctx,
			runtime,
			ownership,
			coordinator,
			auth,
			"expired-client-owner",
		)
		clock.Advance(31 * time.Minute)
		reaped, err := ownership.ReapExpired(ctx, 10)
		if err != nil || len(reaped) != 1 || reaped[0].Status != domain.InvocationFailed {
			t.Fatalf("deadline reap = %#v, error = %v", reaped, err)
		}
		settled, err := store.GetToolCall(ctx, call.ID)
		if err != nil || settled.Status != domain.ToolCallFailed || settled.ResultOrigin == nil ||
			*settled.ResultOrigin != domain.ToolCallResultSystem {
			t.Fatalf("deadline-settled client call = %#v, error = %v", settled, err)
		}
		_, err = runtime.SubmitClientToolResults(
			ctx,
			auth,
			ack.InvocationID,
			services.SubmitClientToolResultsInput{
				Results: []services.ClientToolResultInput{
					{
						ToolCallID: call.ID,
						Content:    json.RawMessage(`{"late":true}`),
					},
				},
			},
		)
		if !publicErrorHasCode(err, services.CodeToolResultExpired) {
			t.Fatalf("post-deadline result error = %v", err)
		}
	})
}

func admitAndParkSingleClientTool(
	t *testing.T,
	ctx context.Context,
	runtime *services.RuntimeService,
	ownership *services.InvocationExecutionService,
	coordinator *services.ToolCheckpointService,
	auth domain.RuntimeAuthContext,
	owner string,
) (services.InvocationAcknowledgement, domain.ToolCall) {
	t.Helper()
	input := runtimeInputWithTwoIterations()
	input.Spec.Tools = []services.ClientToolSpec{
		{
			Name:        "lookup_order",
			Description: "Look up an order",
			Mode:        "client",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, owner, time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim = %#v, disposition = %q, error = %v", claim, disposition, err)
	}
	recorded, err := coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role:    domain.MessageRoleAssistant,
			Content: json.RawMessage(`[{"type":"tool_use","id":"provider-lookup","name":"lookup_order","input":{}}]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  2,
			OutputTokens: 1,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "provider-lookup",
				Name:           "lookup_order",
				Mode:           domain.ToolCallModeClient,
				Input:          json.RawMessage(`{}`),
			},
		},
	})
	if err != nil || len(recorded.ToolCalls) != 1 {
		t.Fatalf("record client checkpoint = %#v, error = %v", recorded, err)
	}
	usage := domain.ModelUsage{
		InputTokens:  2,
		OutputTokens: 1,
		Iterations:   1,
	}
	if err := ownership.Settle(ctx, claim, domain.InvocationExecutionResult{
		Status:               domain.InvocationWaiting,
		MessagesCheckpointed: true,
		Usage:                &usage,
		Provenance:           pointerModelProvenance(testModelProvenance()),
	}); err != nil {
		t.Fatalf("park client Invocation: %v", err)
	}
	return ack, recorded.ToolCalls[0]
}

func publicErrorHasCode(err error, code services.ErrorCode) bool {
	var public *services.PublicError
	return errors.As(err, &public) && public.Code == code
}

func TestClientToolFinalResultCreatesSuccessorDispatch(t *testing.T) {
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
	input := runtimeInputWithTwoIterations()
	input.Spec.Tools = []services.ClientToolSpec{
		{
			Name:        "lookup_order",
			Description: "Look up an order",
			Mode:        "client",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}
	auth := runtimeAuth(account.ID)
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	initialDispatch := activeInvocationDispatch(t, store, ack.InvocationID)
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "client-dispatch-owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim disposition = %q, error = %v", disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role:    domain.MessageRoleAssistant,
			Content: json.RawMessage(`[{"type":"tool_use","id":"provider-call","name":"lookup_order","input":{}}]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  1,
			OutputTokens: 1,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "provider-call",
				Name:           "lookup_order",
				Mode:           domain.ToolCallModeClient,
				Input:          json.RawMessage(`{}`),
			},
		},
	})
	if err != nil || len(recorded.ToolCalls) != 1 {
		t.Fatalf("record client call = %#v, error = %v", recorded, err)
	}
	usage := domain.ModelUsage{
		InputTokens:  1,
		OutputTokens: 1,
		Iterations:   1,
	}
	if err := ownership.SettleDispatch(ctx, claim, domain.InvocationExecutionResult{
		Status:               domain.InvocationWaiting,
		MessagesCheckpointed: true,
		Usage:                &usage,
		Provenance:           pointerModelProvenance(testModelProvenance()),
	}, initialDispatch.ID); err != nil {
		t.Fatalf("park attached dispatch: %v", err)
	}
	settledInitial, err := store.GetExecutionDispatch(ctx, initialDispatch.ID)
	if err != nil || settledInitial.Status != domain.ExecutionDispatchSettled {
		t.Fatalf("settled initial dispatch = %#v, error = %v", settledInitial, err)
	}
	result, err := runtime.SubmitClientToolResults(ctx, auth, ack.InvocationID, services.SubmitClientToolResultsInput{
		Results: []services.ClientToolResultInput{
			{
				ToolCallID: recorded.ToolCalls[0].ID,
				Content:    json.RawMessage(`{"state":"ready"}`),
			},
		},
	})
	if err != nil || result.Status != domain.InvocationQueued {
		t.Fatalf("queue final client result = %#v, error = %v", result, err)
	}
	successor := activeInvocationDispatch(t, store, ack.InvocationID)
	if successor.ID == initialDispatch.ID || successor.Status != domain.ExecutionDispatchPending {
		t.Fatalf("successor dispatch = %#v, initial = %#v", successor, initialDispatch)
	}
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
				CallbackURL:    "https://callbacks.example.test/tools/host_callback",
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
	var abandoned int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM callback_deliveries WHERE invocation_id = $1 AND status = 'abandoned'",
		ack.InvocationID,
	).Scan(&abandoned); err != nil || abandoned != 1 {
		t.Fatalf("abandoned callback deliveries = %d, error = %v", abandoned, err)
	}
}

type callbackIntegrationTransport struct {
	requests []ports.CallbackTransportRequest
	result   *ports.CallbackTransportResult
}

func (t *callbackIntegrationTransport) Send(
	_ context.Context,
	request ports.CallbackTransportRequest,
) ports.CallbackTransportResult {
	t.requests = append(t.requests, request)
	if t.result != nil {
		return *t.result
	}
	return ports.CallbackTransportResult{
		StatusCode:  http.StatusOK,
		ContentType: "application/json",
		Body:        []byte(`{"content":{"answer":42}}`),
	}
}

func callbackDeliveryIDForToolCall(
	t *testing.T,
	pool *pgxpool.Pool,
	toolCallID string,
) string {
	t.Helper()
	var deliveryID string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT id FROM callback_deliveries WHERE tool_call_id = $1",
		toolCallID,
	).Scan(&deliveryID); err != nil {
		t.Fatalf("read callback delivery ID: %v", err)
	}
	return deliveryID
}

func TestCallbackDeliveryParksClaimsAndResumes(t *testing.T) {
	pool, disabledRuntime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	clock := newMutableClock(time.Now().UTC())
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	runtime := services.NewRuntimeService(
		store,
		txm,
		clock,
		ids,
		services.WithCallbackTools(true),
	)
	input := runtimeInput()
	input.IdempotencyKey = "callback-delivery"
	input.SessionKey = pointerString("callback-delivery")
	maxIterations := 3
	input.Spec.Budgets = &services.InvocationBudgetInput{
		MaxIterations: &maxIterations,
	}
	input.Spec.Tools = []services.ClientToolSpec{
		{
			Name:        "lookup_callback",
			Description: "Look up a value through the host callback",
			Mode:        string(domain.ToolCallModeCallback),
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
			Callback: &services.CallbackTarget{
				URL: "https://callbacks.example.test/tools/lookup",
			},
		},
		{
			Name:        "confirm_client",
			Description: "Confirm the callback result in the host client",
			Mode:        string(domain.ToolCallModeClient),
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		},
	}
	if _, err := disabledRuntime.Admit(ctx, auth, input); err == nil ||
		!strings.Contains(err.Error(), "callback mode is not configured") {
		t.Fatalf("disabled callback admission error = %v", err)
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit callback Invocation: %v", err)
	}
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "callback-engine", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim callback Invocation = %q, %v", disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
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
				{"type":"tool_use","id":"provider-callback","name":"lookup_callback","input":{"key":"value"}},
				{"type":"tool_use","id":"provider-client","name":"confirm_client","input":{"accepted":true}}
			]`),
		},
		Usage:      usage,
		Provenance: provenance,
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "provider-callback",
				Name:           "lookup_callback",
				Mode:           domain.ToolCallModeCallback,
				Input:          json.RawMessage(`{"key":"value"}`),
				CallbackURL:    "https://callbacks.example.test/tools/lookup",
			},
			{
				ProviderCallID: "provider-client",
				Name:           "confirm_client",
				Mode:           domain.ToolCallModeClient,
				Input:          json.RawMessage(`{"accepted":true}`),
			},
		},
	})
	if err != nil || len(recorded.ToolCalls) != 2 {
		t.Fatalf("record callback checkpoint = %#v, %v", recorded, err)
	}
	var deliveryID string
	var deliveryStatus string
	var availableAt *time.Time
	if err := pool.QueryRow(
		ctx,
		"SELECT id, status, available_at FROM callback_deliveries WHERE tool_call_id = $1",
		recorded.ToolCalls[0].ID,
	).Scan(&deliveryID, &deliveryStatus, &availableAt); err != nil {
		t.Fatalf("read blocked callback delivery: %v", err)
	}
	if deliveryStatus != string(domain.CallbackDeliveryBlocked) || availableAt != nil {
		t.Fatalf("blocked callback delivery = %q, %#v", deliveryStatus, availableAt)
	}
	if _, err := store.ClaimNextCallbackDelivery(
		ctx,
		"premature-callback-worker",
		clock.Now(),
		clock.Now().Add(time.Minute),
	); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("blocked callback delivery claim error = %v", err)
	}
	if err := ownership.Settle(ctx, claim, domain.InvocationExecutionResult{
		Status:               domain.InvocationWaiting,
		MessagesCheckpointed: true,
		Usage:                &usage,
		Provenance:           &provenance,
	}); err != nil {
		t.Fatalf("park callback Invocation: %v", err)
	}
	deliveryConfig := services.DefaultCallbackDeliveryConfig()
	var callbackLogs bytes.Buffer
	deliveryService, err := services.NewCallbackDeliveryService(
		store,
		txm,
		clock,
		ids,
		nil,
		deliveryConfig,
		slog.New(slog.NewJSONHandler(&callbackLogs, nil)),
	)
	if err != nil {
		t.Fatalf("configure callback delivery service: %v", err)
	}
	claims := make(chan domain.CallbackDeliveryClaim, 20)
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			candidate, found, claimErr := deliveryService.ClaimNext(
				ctx,
				fmt.Sprintf("callback-worker-%d", worker),
			)
			if claimErr != nil {
				t.Errorf("claim callback delivery: %v", claimErr)
				return
			}
			if found {
				claims <- candidate
			}
		}(index)
	}
	group.Wait()
	close(claims)
	var winners []domain.CallbackDeliveryClaim
	for winner := range claims {
		winners = append(winners, winner)
	}
	if len(winners) != 1 || winners[0].Delivery.ID != deliveryID {
		t.Fatalf("callback claim winners = %#v", winners)
	}
	retryTransport := &callbackIntegrationTransport{
		result: &ports.CallbackTransportResult{
			StatusCode: http.StatusServiceUnavailable,
			ErrorCode:  "http_503",
			Retryable:  true,
		},
	}
	if err := deliveryService.ProcessClaim(ctx, retryTransport, winners[0]); err != nil {
		t.Fatalf("schedule callback retry: %v", err)
	}
	retrying, err := store.GetCallbackDelivery(ctx, deliveryID)
	if err != nil || retrying.Status != domain.CallbackDeliveryPending ||
		retrying.Attempt != 1 || retrying.AvailableAt == nil ||
		!retrying.AvailableAt.Equal(clock.Now().Add(time.Second)) {
		t.Fatalf("persisted callback retry = %#v, %v", retrying, err)
	}
	if _, found, err := deliveryService.ClaimNext(ctx, "early-callback-worker"); err != nil || found {
		t.Fatalf("early callback retry claim found = %t, error = %v", found, err)
	}
	clock.Advance(time.Second)
	secondClaim, found, err := deliveryService.ClaimNext(ctx, "replacement-callback-worker")
	if err != nil || !found || secondClaim.Attempt != 2 {
		t.Fatalf("replacement callback claim = %#v, found = %t, error = %v", secondClaim, found, err)
	}
	clock.Advance(deliveryConfig.LeaseDuration)
	staleTransport := &callbackIntegrationTransport{}
	if err := deliveryService.ProcessClaim(ctx, staleTransport, secondClaim); !errors.Is(err, ports.ErrCallbackDeliveryLeaseLost) {
		t.Fatalf("expired callback claim error = %v", err)
	}
	if len(staleTransport.requests) != 0 {
		t.Fatalf("expired callback claim sent requests = %#v", staleTransport.requests)
	}
	recovered, err := deliveryService.RecoverExpired(ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("recover expired callback claim = %d, %v", recovered, err)
	}
	thirdClaim, found, err := deliveryService.ClaimNext(ctx, "final-callback-worker")
	if err != nil || !found || thirdClaim.Attempt != 3 {
		t.Fatalf("final callback claim = %#v, found = %t, error = %v", thirdClaim, found, err)
	}
	transport := &callbackIntegrationTransport{}
	if err := deliveryService.ProcessClaim(ctx, transport, thirdClaim); err != nil {
		t.Fatalf("process callback delivery: %v", err)
	}
	if len(transport.requests) != 1 || transport.requests[0].ToolCallID != recorded.ToolCalls[0].ID {
		t.Fatalf("callback requests = %#v", transport.requests)
	}
	callbackBody := string(transport.requests[0].Body)
	for _, expected := range []string{
		`"schema_version":1`,
		`"delivery_id":"` + deliveryID + `"`,
		`"tool_call_id":"` + recorded.ToolCalls[0].ID + `"`,
		`"agent_ref":"support"`,
		`"input":{"key":"value"}`,
	} {
		if !strings.Contains(callbackBody, expected) {
			t.Fatalf("callback body omitted %q: %s", expected, callbackBody)
		}
	}
	if strings.Contains(callbackBody, `"actor"`) || strings.Contains(callbackBody, `"tenant_ref"`) {
		t.Fatalf("callback body included unowned optional identity: %s", callbackBody)
	}
	if len(retryTransport.requests) != 1 ||
		retryTransport.requests[0].DeliveryID != transport.requests[0].DeliveryID ||
		retryTransport.requests[0].ToolCallID != transport.requests[0].ToolCallID ||
		!bytes.Equal(retryTransport.requests[0].Body, transport.requests[0].Body) {
		t.Fatalf("callback retry identity changed: first = %#v, final = %#v", retryTransport.requests, transport.requests)
	}
	invocation, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil || invocation.Status != domain.InvocationWaiting {
		t.Fatalf("partially resumed callback Invocation = %#v, %v", invocation, err)
	}
	pending, err := runtime.GetInvocation(ctx, auth, ack.InvocationID)
	if err != nil || len(pending.PendingToolCalls) != 1 ||
		pending.PendingToolCalls[0].ID != recorded.ToolCalls[1].ID {
		t.Fatalf("callback-hidden client projection = %#v, %v", pending.PendingToolCalls, err)
	}
	clientResult, err := runtime.SubmitClientToolResults(
		ctx,
		auth,
		ack.InvocationID,
		services.SubmitClientToolResultsInput{
			Results: []services.ClientToolResultInput{
				{
					ToolCallID: recorded.ToolCalls[1].ID,
					Content:    json.RawMessage(`{"confirmed":true}`),
				},
			},
		},
	)
	if err != nil || clientResult.Status != domain.InvocationQueued {
		t.Fatalf("final mixed client result = %#v, %v", clientResult, err)
	}
	call, err := store.GetToolCall(ctx, recorded.ToolCalls[0].ID)
	if err != nil || call.Status != domain.ToolCallCompleted ||
		call.ResultOrigin == nil || *call.ResultOrigin != domain.ToolCallResultCallback {
		t.Fatalf("settled callback ToolCall = %#v, %v", call, err)
	}
	delivery, err := store.GetCallbackDelivery(ctx, deliveryID)
	if err != nil || delivery.Status != domain.CallbackDeliverySucceeded {
		t.Fatalf("settled callback delivery = %#v, %v", delivery, err)
	}
	for _, secret := range []string{
		"https://callbacks.example.test/tools/lookup",
		`\"key\":\"value\"`,
		`\"answer\":42`,
		"0123456789abcdef0123456789abcdef",
	} {
		if strings.Contains(callbackLogs.String(), secret) {
			t.Fatalf("callback log exposed sensitive material %q: %s", secret, callbackLogs.String())
		}
	}

	exhaustionClaim, disposition, err := ownership.ClaimExact(
		ctx,
		ack.InvocationID,
		"callback-exhaustion-engine",
		time.Minute,
	)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim callback exhaustion Invocation = %q, %v", disposition, err)
	}
	exhaustedCheckpoint, err := coordinator.RecordModelCheckpoint(ctx, exhaustionClaim, domain.ModelCheckpointInput{
		Iteration: 2,
		Message: domain.GenerationMessage{
			Role: domain.MessageRoleAssistant,
			Content: json.RawMessage(`[
				{"type":"tool_use","id":"provider-callback-exhausted","name":"lookup_callback","input":{"key":"retry"}}
			]`),
		},
		Usage:      usage,
		Provenance: provenance,
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "provider-callback-exhausted",
				Name:           "lookup_callback",
				Mode:           domain.ToolCallModeCallback,
				Input:          json.RawMessage(`{"key":"retry"}`),
				CallbackURL:    "https://callbacks.example.test/tools/lookup",
			},
		},
	})
	if err != nil || len(exhaustedCheckpoint.ToolCalls) != 1 {
		t.Fatalf("record exhausted callback checkpoint = %#v, %v", exhaustedCheckpoint, err)
	}
	cumulativeUsage := domain.ModelUsage{
		InputTokens:  4,
		OutputTokens: 2,
		Iterations:   2,
	}
	if err := ownership.Settle(ctx, exhaustionClaim, domain.InvocationExecutionResult{
		Status:               domain.InvocationWaiting,
		MessagesCheckpointed: true,
		Usage:                &cumulativeUsage,
		Provenance:           &provenance,
	}); err != nil {
		t.Fatalf("park exhausted callback Invocation: %v", err)
	}
	exhaustionConfig := deliveryConfig
	exhaustionConfig.MaxAttempts = 5
	exhaustionService, err := services.NewCallbackDeliveryService(
		store,
		txm,
		clock,
		ids,
		nil,
		exhaustionConfig,
		slog.New(slog.NewJSONHandler(&callbackLogs, nil)),
	)
	if err != nil {
		t.Fatalf("configure callback exhaustion service: %v", err)
	}
	exhaustionTransport := &callbackIntegrationTransport{
		result: &ports.CallbackTransportResult{
			StatusCode: http.StatusServiceUnavailable,
			ErrorCode:  "http_503",
			Retryable:  true,
		},
	}
	var exhaustedClaim domain.CallbackDeliveryClaim
	for expectedAttempt := int64(1); expectedAttempt <= int64(exhaustionConfig.MaxAttempts); expectedAttempt++ {
		var found bool
		exhaustedClaim, found, err = exhaustionService.ClaimNext(
			ctx,
			fmt.Sprintf("callback-exhaustion-worker-%d", expectedAttempt),
		)
		if err != nil || !found || exhaustedClaim.Attempt != expectedAttempt {
			t.Fatalf(
				"claim exhausted callback attempt %d = %#v, found = %t, error = %v",
				expectedAttempt,
				exhaustedClaim,
				found,
				err,
			)
		}
		if err := exhaustionService.ProcessClaim(ctx, exhaustionTransport, exhaustedClaim); err != nil {
			t.Fatalf("exhaust callback delivery attempt %d: %v", expectedAttempt, err)
		}
		if expectedAttempt < int64(exhaustionConfig.MaxAttempts) {
			retrying, err := store.GetCallbackDelivery(ctx, exhaustedClaim.Delivery.ID)
			if err != nil || retrying.Status != domain.CallbackDeliveryPending ||
				retrying.Attempt != expectedAttempt {
				t.Fatalf("exhaustion retry %d = %#v, %v", expectedAttempt, retrying, err)
			}
			clock.Advance(time.Second << (expectedAttempt - 1))
		}
	}
	if len(exhaustionTransport.requests) != exhaustionConfig.MaxAttempts {
		t.Fatalf("exhaustion callback requests = %d", len(exhaustionTransport.requests))
	}
	exhaustedDelivery, err := store.GetCallbackDelivery(ctx, exhaustedClaim.Delivery.ID)
	if err != nil || exhaustedDelivery.Status != domain.CallbackDeliveryFailed ||
		exhaustedDelivery.LastErrorCode == nil || *exhaustedDelivery.LastErrorCode != "http_503" {
		t.Fatalf("exhausted callback delivery = %#v, %v", exhaustedDelivery, err)
	}
	exhaustedCall, err := store.GetToolCall(ctx, exhaustedCheckpoint.ToolCalls[0].ID)
	if err != nil || exhaustedCall.Status != domain.ToolCallFailed ||
		exhaustedCall.ResultOrigin == nil || *exhaustedCall.ResultOrigin != domain.ToolCallResultCallback {
		t.Fatalf("exhausted callback ToolCall = %#v, %v", exhaustedCall, err)
	}
	exhaustedInvocation, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil || exhaustedInvocation.Status != domain.InvocationQueued {
		t.Fatalf("queued exhausted callback Invocation = %#v, %v", exhaustedInvocation, err)
	}
	messages, err := store.ListSessionMessages(ctx, ack.SessionID)
	if err != nil || len(messages) == 0 ||
		!bytes.Contains(messages[len(messages)-1].Content, []byte("callback_delivery_failed")) ||
		bytes.Contains(messages[len(messages)-1].Content, []byte("http_503")) {
		t.Fatalf("bounded callback failure evidence = %#v, %v", messages, err)
	}

	deadlineClaim, disposition, err := ownership.ClaimExact(
		ctx,
		ack.InvocationID,
		"callback-deadline-engine",
		time.Minute,
	)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim callback deadline Invocation = %q, %v", disposition, err)
	}
	deadlineCheckpoint, err := coordinator.RecordModelCheckpoint(ctx, deadlineClaim, domain.ModelCheckpointInput{
		Iteration: 3,
		Message: domain.GenerationMessage{
			Role: domain.MessageRoleAssistant,
			Content: json.RawMessage(`[
				{"type":"tool_use","id":"provider-callback-deadline","name":"lookup_callback","input":{"key":"deadline"}}
			]`),
		},
		Usage:      usage,
		Provenance: provenance,
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "provider-callback-deadline",
				Name:           "lookup_callback",
				Mode:           domain.ToolCallModeCallback,
				Input:          json.RawMessage(`{"key":"deadline"}`),
				CallbackURL:    "https://callbacks.example.test/tools/lookup",
			},
		},
	})
	if err != nil || len(deadlineCheckpoint.ToolCalls) != 1 {
		t.Fatalf("record deadline callback checkpoint = %#v, %v", deadlineCheckpoint, err)
	}
	deadlineUsage := domain.ModelUsage{
		InputTokens:  6,
		OutputTokens: 3,
		Iterations:   3,
	}
	if err := ownership.Settle(ctx, deadlineClaim, domain.InvocationExecutionResult{
		Status:               domain.InvocationWaiting,
		MessagesCheckpointed: true,
		Usage:                &deadlineUsage,
		Provenance:           &provenance,
	}); err != nil {
		t.Fatalf("park deadline callback Invocation: %v", err)
	}
	deadlineInvocation, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil {
		t.Fatalf("read deadline callback Invocation: %v", err)
	}
	clock.Advance(deadlineInvocation.WallClockDeadlineAt.Sub(clock.Now()) + time.Millisecond)
	if _, found, err := deliveryService.ClaimNext(ctx, "late-callback-worker"); err != nil || found {
		t.Fatalf("deadline callback claim found = %t, error = %v", found, err)
	}
	reaped, err := ownership.ReapExpired(ctx, 10)
	if err != nil || len(reaped) != 1 || reaped[0].Status != domain.InvocationFailed {
		t.Fatalf("reap deadline callback Invocation = %#v, %v", reaped, err)
	}
	deadlineDelivery, err := store.GetCallbackDelivery(ctx, callbackDeliveryIDForToolCall(
		t,
		pool,
		deadlineCheckpoint.ToolCalls[0].ID,
	))
	if err != nil || deadlineDelivery.Status != domain.CallbackDeliveryAbandoned {
		t.Fatalf("deadline callback delivery = %#v, %v", deadlineDelivery, err)
	}

}

func TestCallbackDeliveryRetentionPrunesOnlyTransportRows(t *testing.T) {
	pool, _, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	clock := newMutableClock(time.Now().UTC())
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	runtime := services.NewRuntimeService(
		store,
		txm,
		clock,
		ids,
		services.WithCallbackTools(true),
	)
	input := runtimeInput()
	input.IdempotencyKey = "callback-retention"
	input.SessionKey = pointerString("callback-retention")
	maxIterations := 2
	input.Spec.Budgets = &services.InvocationBudgetInput{
		MaxIterations: &maxIterations,
	}
	for _, name := range []string{"retention_callback_a", "retention_callback_b", "retention_callback_c"} {
		input.Spec.Tools = append(input.Spec.Tools, services.ClientToolSpec{
			Name:        name,
			Description: "Produce retained callback evidence",
			Mode:        string(domain.ToolCallModeCallback),
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
			Callback: &services.CallbackTarget{
				URL: "https://callbacks.example.test/tools/retention",
			},
		})
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit callback retention Invocation: %v", err)
	}
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "callback-retention-engine", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim callback retention Invocation = %q, %v", disposition, err)
	}
	usage := domain.ModelUsage{
		InputTokens:  2,
		OutputTokens: 1,
		Iterations:   1,
	}
	provenance := testModelProvenance()
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role: domain.MessageRoleAssistant,
			Content: json.RawMessage(`[
				{"type":"tool_use","id":"retention-a","name":"retention_callback_a","input":{"value":"a"}},
				{"type":"tool_use","id":"retention-b","name":"retention_callback_b","input":{"value":"b"}},
				{"type":"tool_use","id":"retention-c","name":"retention_callback_c","input":{"value":"c"}},
				{"type":"tool_use","id":"retention-builtin","name":"nvoken_test_echo","input":{"value":"retained"}}
			]`),
		},
		Usage:      usage,
		Provenance: provenance,
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "retention-a",
				Name:           "retention_callback_a",
				Mode:           domain.ToolCallModeCallback,
				Input:          json.RawMessage(`{"value":"a"}`),
				CallbackURL:    "https://callbacks.example.test/tools/retention",
			},
			{
				ProviderCallID: "retention-b",
				Name:           "retention_callback_b",
				Mode:           domain.ToolCallModeCallback,
				Input:          json.RawMessage(`{"value":"b"}`),
				CallbackURL:    "https://callbacks.example.test/tools/retention",
			},
			{
				ProviderCallID: "retention-c",
				Name:           "retention_callback_c",
				Mode:           domain.ToolCallModeCallback,
				Input:          json.RawMessage(`{"value":"c"}`),
				CallbackURL:    "https://callbacks.example.test/tools/retention",
			},
			{
				ProviderCallID: "retention-builtin",
				Name:           "nvoken_test_echo",
				Mode:           domain.ToolCallModeBuiltin,
				Input:          json.RawMessage(`{"value":"retained"}`),
			},
		},
	})
	if err != nil || len(recorded.ToolCalls) != 4 {
		t.Fatalf("record callback retention checkpoint = %#v, %v", recorded, err)
	}
	builtin, err := coordinator.StartBuiltinToolCall(ctx, claim, 1, "retention-builtin")
	if err != nil {
		t.Fatalf("start retention builtin ToolCall: %v", err)
	}
	if _, err := coordinator.AcceptBuiltinToolResult(
		ctx,
		claim,
		builtin,
		json.RawMessage(`{"echo":"retained"}`),
		false,
	); err != nil {
		t.Fatalf("settle retention builtin ToolCall: %v", err)
	}
	if err := ownership.Settle(ctx, claim, domain.InvocationExecutionResult{
		Status:               domain.InvocationWaiting,
		MessagesCheckpointed: true,
		Usage:                &usage,
		Provenance:           &provenance,
	}); err != nil {
		t.Fatalf("park callback retention Invocation: %v", err)
	}
	deliveryConfig := services.DefaultCallbackDeliveryConfig()
	deliveryConfig.BatchLimit = 2
	deliveryService, err := services.NewCallbackDeliveryService(
		store,
		txm,
		clock,
		ids,
		nil,
		deliveryConfig,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("configure callback retention service: %v", err)
	}
	var deliveryIDs []string
	transport := &callbackIntegrationTransport{}
	for index := range 3 {
		deliveryClaim, found, err := deliveryService.ClaimNext(
			ctx,
			fmt.Sprintf("callback-retention-worker-%d", index),
		)
		if err != nil || !found {
			t.Fatalf("claim callback retention delivery %d = %#v, %t, %v", index, deliveryClaim, found, err)
		}
		deliveryIDs = append(deliveryIDs, deliveryClaim.Delivery.ID)
		if err := deliveryService.ProcessClaim(ctx, transport, deliveryClaim); err != nil {
			t.Fatalf("settle callback retention delivery %d: %v", index, err)
		}
	}
	clock.Advance(deliveryConfig.Retention + time.Millisecond)
	pruned, err := deliveryService.Prune(ctx)
	if err != nil || pruned != 2 {
		t.Fatalf("prune terminal callback deliveries = %d, %v", pruned, err)
	}
	pruned, err = deliveryService.Prune(ctx)
	if err != nil || pruned != 1 {
		t.Fatalf("second callback delivery prune = %d, %v", pruned, err)
	}
	for _, deliveryID := range deliveryIDs {
		if _, err := store.GetCallbackDelivery(ctx, deliveryID); !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("pruned callback delivery %s error = %v", deliveryID, err)
		}
	}
	invocationRead, err := runtime.GetInvocation(ctx, auth, ack.InvocationID)
	if err != nil || invocationRead.Status != domain.InvocationQueued {
		t.Fatalf("retained callback Invocation = %#v, %v", invocationRead, err)
	}
	sessionRead, err := runtime.GetSession(ctx, auth, ack.SessionID)
	if err != nil || sessionRead.ID != ack.SessionID {
		t.Fatalf("retained callback Session = %#v, %v", sessionRead, err)
	}
	messages, err := store.ListSessionMessages(ctx, ack.SessionID)
	if err != nil || len(messages) != 6 {
		t.Fatalf("retained callback transcript = %#v, %v", messages, err)
	}
	for _, toolCall := range recorded.ToolCalls {
		if _, err := store.GetToolCall(ctx, toolCall.ID); err != nil {
			t.Fatalf("retained ToolCall %s: %v", toolCall.ID, err)
		}
	}
	receipts, err := store.ListModelUsageReceipts(ctx, ack.InvocationID)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("retained callback usage receipts = %#v, %v", receipts, err)
	}
	checkpoints, err := store.ListInvocationCheckpoints(ctx, ack.InvocationID)
	if err != nil || len(checkpoints) != 5 {
		t.Fatalf("retained callback checkpoints = %#v, %v", checkpoints, err)
	}
	var attempts int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM tool_call_attempts WHERE invocation_id = $1",
		ack.InvocationID,
	).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("retained ToolCall attempts = %d, %v", attempts, err)
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

func TestExpiredLeasePreservesAndRestartsOpenBuiltin(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	ack, err := runtime.Admit(ctx, auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := newMutableClock(time.Now().UTC())
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	executionService := services.NewInvocationExecutionService(store, txm, clock, ids)
	firstClaim, disposition, err := executionService.ClaimExact(
		ctx,
		ack.InvocationID,
		"lost-tool-owner",
		time.Minute,
	)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("first claim = %#v, disposition = %q, error = %v", firstClaim, disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(
		ctx,
		firstClaim,
		modelToolCheckpoint("resumed-provider-call", 2, 1),
	)
	if err != nil {
		t.Fatalf("record checkpoint: %v", err)
	}
	firstExecution, err := coordinator.StartBuiltinToolCall(
		ctx,
		firstClaim,
		1,
		"resumed-provider-call",
	)
	if err != nil {
		t.Fatalf("start first attempt: %v", err)
	}
	clock.Advance(time.Minute + time.Nanosecond)
	recovered, err := executionService.ReapExpired(ctx, 10)
	if err != nil || len(recovered) != 1 || recovered[0].Status != domain.InvocationQueued {
		t.Fatalf("recovered = %#v, error = %v", recovered, err)
	}
	open, err := store.GetToolCall(ctx, recorded.ToolCalls[0].ID)
	if err != nil || open.Status != domain.ToolCallRunning || open.CurrentAttempt != 1 {
		t.Fatalf("preserved ToolCall = %#v, error = %v", open, err)
	}
	secondClaim, disposition, err := executionService.ClaimExact(
		ctx,
		ack.InvocationID,
		"replacement-tool-owner",
		time.Minute,
	)
	if err != nil || disposition != services.Claimed || secondClaim.Attempt != 2 {
		t.Fatalf("replacement claim = %#v, disposition = %q, error = %v", secondClaim, disposition, err)
	}
	secondExecution, err := coordinator.StartBuiltinToolCall(
		ctx,
		secondClaim,
		1,
		"resumed-provider-call",
	)
	if err != nil {
		t.Fatalf("restart builtin: %v", err)
	}
	if secondExecution.Call.ID != firstExecution.Call.ID || secondExecution.Attempt.Attempt != 2 {
		t.Fatalf("restarted execution = %#v, first = %#v", secondExecution, firstExecution)
	}
	if _, err := coordinator.AcceptBuiltinToolResult(
		ctx,
		secondClaim,
		secondExecution,
		json.RawMessage(`"resumed"`),
		false,
	); err != nil {
		t.Fatalf("accept replacement result: %v", err)
	}
	if _, err := coordinator.AcceptBuiltinToolResult(
		ctx,
		firstClaim,
		firstExecution,
		json.RawMessage(`"late"`),
		false,
	); !errors.Is(err, ports.ErrLeaseLost) {
		t.Fatalf("stale result error = %v", err)
	}
	rows, err := pool.Query(
		ctx,
		"SELECT attempt, status FROM tool_call_attempts WHERE tool_call_id = $1 ORDER BY attempt",
		firstExecution.Call.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var attempts []string
	for rows.Next() {
		var attempt int
		var status string
		if err := rows.Scan(&attempt, &status); err != nil {
			t.Fatal(err)
		}
		attempts = append(attempts, fmt.Sprintf("%d:%s", attempt, status))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(attempts, []string{"1:failed", "2:completed"}) {
		t.Fatalf("attempts = %#v", attempts)
	}
}

func TestExpiredLeaseReplaysCommittedFinalCheckpointWithoutProviderCall(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	ack, err := runtime.Admit(ctx, auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := newMutableClock(time.Now().UTC())
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	executionService := services.NewInvocationExecutionService(store, txm, clock, ids)
	firstClaim, disposition, err := executionService.ClaimExact(
		ctx,
		ack.InvocationID,
		"lost-final-owner",
		time.Minute,
	)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("first claim = %#v, disposition = %q, error = %v", firstClaim, disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(ctx, firstClaim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role:    domain.MessageRoleAssistant,
			Content: json.RawMessage(`[{"type":"text","text":"durable final"}]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  4,
			OutputTokens: 2,
			Iterations:   1,
		},
		Provenance: domain.ModelProvenance{
			Provider:         "anthropic",
			RequestedModel:   "claude-test",
			ServedModel:      "claude-served",
			CredentialSource: "installation_byok",
		},
	})
	if err != nil {
		t.Fatalf("record final checkpoint: %v", err)
	}
	clock.Advance(time.Minute + time.Nanosecond)
	recovered, err := executionService.ReapExpired(ctx, 10)
	if err != nil || len(recovered) != 1 || recovered[0].Status != domain.InvocationQueued {
		t.Fatalf("recovered = %#v, error = %v", recovered, err)
	}
	secondClaim, disposition, err := executionService.ClaimExact(
		ctx,
		ack.InvocationID,
		"replacement-final-owner",
		time.Minute,
	)
	if err != nil || disposition != services.Claimed || secondClaim.Attempt != 2 {
		t.Fatalf("replacement claim = %#v, disposition = %q, error = %v", secondClaim, disposition, err)
	}
	generator := &postgresModelGenerator{}
	result, err := services.NewGenerationExecutor(store, generator, nil).Execute(ctx, secondClaim)
	if err != nil {
		t.Fatalf("replay final checkpoint: %v", err)
	}
	if len(generator.Requests()) != 0 || result.Status != domain.InvocationCompleted || !result.MessagesCheckpointed {
		t.Fatalf("terminal replay = %#v, provider calls = %d", result, len(generator.Requests()))
	}
	if err := executionService.Settle(ctx, secondClaim, result); err != nil {
		t.Fatalf("settle terminal replay: %v", err)
	}
	stored, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil || stored.Status != domain.InvocationCompleted ||
		stored.CurrentCheckpointSequence != recorded.Checkpoint.Sequence ||
		stored.CurrentIteration != 1 {
		t.Fatalf("settled Invocation = %#v, error = %v", stored, err)
	}
	messages, err := store.ListSessionMessages(ctx, ack.SessionID)
	if err != nil || len(messages) != 2 || messages[1].ID != recorded.Message.ID {
		t.Fatalf("settled transcript = %#v, error = %v", messages, err)
	}
}

func TestExpiredLeaseContinuesAfterCommittedToolResultWithoutRerun(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	input := runtimeInput()
	maxIterations := 3
	input.Spec.Budgets = &services.InvocationBudgetInput{
		MaxIterations: &maxIterations,
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := newMutableClock(time.Now().UTC())
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	executionService := services.NewInvocationExecutionService(store, txm, clock, ids)
	firstClaim, disposition, err := executionService.ClaimExact(
		ctx,
		ack.InvocationID,
		"lost-after-result-owner",
		time.Minute,
	)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("first claim = %#v, disposition = %q, error = %v", firstClaim, disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(
		ctx,
		firstClaim,
		modelToolCheckpoint("result-window-provider-call", 2, 1),
	)
	if err != nil {
		t.Fatalf("record checkpoint: %v", err)
	}
	firstExecution, err := coordinator.StartBuiltinToolCall(
		ctx,
		firstClaim,
		1,
		"result-window-provider-call",
	)
	if err != nil {
		t.Fatalf("start builtin: %v", err)
	}
	accepted, err := coordinator.AcceptBuiltinToolResult(
		ctx,
		firstClaim,
		firstExecution,
		json.RawMessage(`"accepted-before-crash"`),
		false,
	)
	if err != nil {
		t.Fatalf("accept builtin: %v", err)
	}
	clock.Advance(time.Minute + time.Nanosecond)
	if recovered, err := executionService.ReapExpired(ctx, 10); err != nil ||
		len(recovered) != 1 || recovered[0].Status != domain.InvocationQueued {
		t.Fatalf("recovered = %#v, error = %v", recovered, err)
	}
	secondClaim, disposition, err := executionService.ClaimExact(
		ctx,
		ack.InvocationID,
		"replacement-after-result-owner",
		time.Minute,
	)
	if err != nil || disposition != services.Claimed || secondClaim.Attempt != 2 {
		t.Fatalf("replacement claim = %#v, disposition = %q, error = %v", secondClaim, disposition, err)
	}
	generator := durableResumeContinuationGenerator{
		coordinator: coordinator,
	}
	result, err := services.NewGenerationExecutor(store, generator, nil).Execute(ctx, secondClaim)
	if err != nil {
		t.Fatalf("continue after result: %v", err)
	}
	if err := executionService.Settle(ctx, secondClaim, result); err != nil {
		t.Fatalf("settle continuation: %v", err)
	}
	stored, err := store.GetToolCall(ctx, recorded.ToolCalls[0].ID)
	if err != nil || stored.ID != accepted.ID || stored.CurrentAttempt != 1 ||
		stored.Status != domain.ToolCallCompleted {
		t.Fatalf("accepted ToolCall = %#v, error = %v", stored, err)
	}
	var attemptCount int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM tool_call_attempts WHERE tool_call_id = $1",
		stored.ID,
	).Scan(&attemptCount); err != nil || attemptCount != 1 {
		t.Fatalf("tool attempt count = %d, error = %v", attemptCount, err)
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
		"ToolCall result origin": func() error {
			_, err := pool.Exec(ctx, "UPDATE tool_calls SET result_origin = 'system' WHERE id = $1", toolCallID)
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
