package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const (
	resultAccountID    = "acct_019b0a12-0000-7000-8000-000000000201"
	resultPartitionID  = "tprt_019b0a12-0000-7000-8000-000000000202"
	resultAgentID      = "agnt_019b0a12-0000-7000-8000-000000000203"
	resultSessionID    = "sesn_019b0a12-0000-7000-8000-000000000204"
	resultInvocationID = "invk_019b0a12-0000-7000-8000-000000000205"
)

type invocationResultTestStore struct {
	admissionStore
	invocation domain.Invocation
	partition  domain.TenantPartition
	messages   []domain.SessionMessage
	toolCalls  []domain.ToolCall
}

func (s *invocationResultTestStore) GetInvocation(context.Context, string) (domain.Invocation, error) {
	if s.invocation.ID == "" {
		return domain.Invocation{}, ports.ErrNotFound
	}
	return s.invocation, nil
}

func (s *invocationResultTestStore) GetTenantPartition(context.Context, string) (domain.TenantPartition, error) {
	if s.partition.ID == "" {
		return domain.TenantPartition{}, ports.ErrNotFound
	}
	return s.partition, nil
}

func (s *invocationResultTestStore) ListSessionMessagesByInvocation(context.Context, string) ([]domain.SessionMessage, error) {
	return append([]domain.SessionMessage(nil), s.messages...), nil
}

func (s *invocationResultTestStore) ListSessionMessages(context.Context, string) ([]domain.SessionMessage, error) {
	return append([]domain.SessionMessage(nil), s.messages...), nil
}

func (s *invocationResultTestStore) ListToolCallsByInvocation(context.Context, string) ([]domain.ToolCall, error) {
	return append([]domain.ToolCall(nil), s.toolCalls...), nil
}

func resultTestMessage(sequence int64, role domain.MessageRole, content string) domain.SessionMessage {
	return domain.SessionMessage{
		ID:                "smsg_019b0a12-0000-7000-8000-0000000002" + string(rune('1'+sequence)),
		SessionID:         resultSessionID,
		AccountID:         resultAccountID,
		TenantPartitionID: resultPartitionID,
		AgentID:           resultAgentID,
		InvocationID:      resultInvocationID,
		Sequence:          sequence,
		Role:              role,
		Content:           json.RawMessage(content),
		CreatedAt:         time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
	}
}

func newInvocationResultTestService(store *invocationResultTestStore) *RuntimeService {
	return NewRuntimeService(store, recoveryTestTx{}, recoveryTestClock{}, recoveryTestIDs{})
}

func resultTestInvocation(status domain.InvocationStatus) domain.Invocation {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	return domain.Invocation{
		ID:                resultInvocationID,
		AccountID:         resultAccountID,
		TenantPartitionID: resultPartitionID,
		AgentID:           resultAgentID,
		SessionID:         resultSessionID,
		Status:            status,
		DeadlineAt:        now.Add(time.Hour),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func resultAuth() domain.RuntimeAuthContext {
	return domain.RuntimeAuthContext{
		AccountID: resultAccountID,
		Operations: map[domain.RuntimeOperation]struct{}{
			domain.OperationGetInvocation: {},
		},
	}
}

func TestGetInvocationResultComposesCompletedTextTurn(t *testing.T) {
	store := &invocationResultTestStore{
		invocation: resultTestInvocation(domain.InvocationCompleted),
		partition:  domain.TenantPartition{ID: resultPartitionID, AccountID: resultAccountID},
		messages: []domain.SessionMessage{
			resultTestMessage(1, domain.MessageRoleUser, `[{"type":"text","text":"What happened?"}]`),
			resultTestMessage(2, domain.MessageRoleAssistant, `[{"type":"text","text":"The charge was"},{"type":"tool_use","id":"tcal_1","name":"noop"},{"type":"text","text":" duplicated."}]`),
			resultTestMessage(3, domain.MessageRoleTool, `[{"type":"tool_result","tool_use_id":"tcal_1"}]`),
			resultTestMessage(4, domain.MessageRoleAssistant, `[{"type":"text","text":"A refund is queued."}]`),
		},
	}
	service := newInvocationResultTestService(store)

	result, err := service.GetInvocationResult(context.Background(), resultAuth(), resultInvocationID)
	if err != nil {
		t.Fatalf("GetInvocationResult: %v", err)
	}
	if result.Invocation.ID != resultInvocationID || result.Invocation.Status != domain.InvocationCompleted {
		t.Fatalf("invocation read = %#v", result.Invocation)
	}
	if len(result.Messages) != 4 {
		t.Fatalf("messages = %d, want every role in sequence order", len(result.Messages))
	}
	if result.OutputText == nil || *result.OutputText != "The charge was duplicated.\n\nA refund is queued." {
		t.Fatalf("output text = %v, want direct block concatenation and double-newline message joining", result.OutputText)
	}
}

func TestGetInvocationResultOutputTextRules(t *testing.T) {
	assistant := resultTestMessage(2, domain.MessageRoleAssistant, `[{"type":"text","text":"partial answer"}]`)
	toolOnly := resultTestMessage(2, domain.MessageRoleAssistant, `[{"type":"tool_use","id":"tcal_1","name":"emit"}]`)
	emptyText := resultTestMessage(2, domain.MessageRoleAssistant, `[{"type":"text","text":""}]`)
	toolRequest := resultTestMessage(3, domain.MessageRoleAssistant, `[{"type":"tool_use","id":"tcal_wait","name":"lookup","input":{"order_id":"o-1"}}]`)
	pendingCall := domain.ToolCall{
		ID:               "tcal_wait",
		InvocationID:     resultInvocationID,
		SessionID:        resultSessionID,
		Name:             "lookup",
		Mode:             domain.ToolCallModeHost,
		RequestMessageID: toolRequest.ID,
		Status:           domain.ToolCallPending,
		DeadlineAt:       time.Date(2026, time.July, 21, 13, 0, 0, 0, time.UTC),
	}
	emptyString := ""
	tests := []struct {
		name        string
		status      domain.InvocationStatus
		messages    []domain.SessionMessage
		toolCalls   []domain.ToolCall
		want        *string
		wantPending int
	}{
		{name: "queued", status: domain.InvocationQueued, messages: nil, want: nil},
		{name: "running with text", status: domain.InvocationRunning, messages: []domain.SessionMessage{assistant}, want: nil},
		{name: "waiting with text", status: domain.InvocationWaiting, messages: []domain.SessionMessage{assistant, toolRequest}, toolCalls: []domain.ToolCall{pendingCall}, want: nil, wantPending: 1},
		{name: "failed keeps evidence unprojected", status: domain.InvocationFailed, messages: []domain.SessionMessage{assistant}, want: nil},
		{name: "cancelled keeps evidence unprojected", status: domain.InvocationCancelled, messages: []domain.SessionMessage{assistant}, want: nil},
		{name: "completed schema-only turn", status: domain.InvocationCompleted, messages: []domain.SessionMessage{toolOnly}, want: nil},
		{name: "completed empty text block stays non-null", status: domain.InvocationCompleted, messages: []domain.SessionMessage{emptyText}, want: &emptyString},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocation := resultTestInvocation(test.status)
			if test.status == domain.InvocationFailed {
				invocation.Error = json.RawMessage(`{"code":"provider_error","message":"boom"}`)
			}
			store := &invocationResultTestStore{
				invocation: invocation,
				partition:  domain.TenantPartition{ID: resultPartitionID, AccountID: resultAccountID},
				messages:   test.messages,
				toolCalls:  test.toolCalls,
			}
			result, err := newInvocationResultTestService(store).GetInvocationResult(context.Background(), resultAuth(), resultInvocationID)
			if err != nil {
				t.Fatalf("GetInvocationResult: %v", err)
			}
			if (result.OutputText == nil) != (test.want == nil) {
				t.Fatalf("output text = %v, want %v", result.OutputText, test.want)
			}
			if test.want != nil && *result.OutputText != *test.want {
				t.Fatalf("output text = %q, want %q", *result.OutputText, *test.want)
			}
			if len(result.Messages) != len(test.messages) {
				t.Fatalf("messages = %d, want evidence readable at %s", len(result.Messages), test.status)
			}
			if len(result.Invocation.PendingToolCalls) != test.wantPending {
				t.Fatalf("pending tool calls = %d, want %d", len(result.Invocation.PendingToolCalls), test.wantPending)
			}
		})
	}
}

func TestGetInvocationResultRejectsUndecodableAssistantContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "content is not a block array", content: `{"type":"text","text":"flat"}`},
		{name: "text block carries a non-string", content: `[{"type":"text","text":"good"},{"type":"text","text":42}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &invocationResultTestStore{
				invocation: resultTestInvocation(domain.InvocationCompleted),
				partition:  domain.TenantPartition{ID: resultPartitionID, AccountID: resultAccountID},
				messages: []domain.SessionMessage{
					resultTestMessage(1, domain.MessageRoleAssistant, test.content),
				},
			}
			_, err := newInvocationResultTestService(store).GetInvocationResult(context.Background(), resultAuth(), resultInvocationID)
			if err == nil {
				t.Fatal("undecodable assistant content must fail the read, not shorten the answer")
			}
			var public *PublicError
			if errors.As(err, &public) {
				t.Fatalf("corruption surfaced as public error %v, want internal", public)
			}
		})
	}
}

type snapshotRecordingTx struct {
	recoveryTestTx
	active  bool
	entered int
}

func (s *snapshotRecordingTx) WithReadSnapshot(ctx context.Context, fn func(context.Context) error) error {
	s.active = true
	s.entered++
	defer func() { s.active = false }()
	return fn(ctx)
}

type snapshotAssertingStore struct {
	invocationResultTestStore
	t  *testing.T
	tx *snapshotRecordingTx
}

func (s *snapshotAssertingStore) GetInvocation(ctx context.Context, id string) (domain.Invocation, error) {
	if !s.tx.active {
		s.t.Fatal("GetInvocation ran outside the read snapshot")
	}
	return s.invocationResultTestStore.GetInvocation(ctx, id)
}

func (s *snapshotAssertingStore) GetTenantPartition(ctx context.Context, id string) (domain.TenantPartition, error) {
	if !s.tx.active {
		s.t.Fatal("GetTenantPartition ran outside the read snapshot")
	}
	return s.invocationResultTestStore.GetTenantPartition(ctx, id)
}

func (s *snapshotAssertingStore) ListSessionMessagesByInvocation(ctx context.Context, id string) ([]domain.SessionMessage, error) {
	if !s.tx.active {
		s.t.Fatal("ListSessionMessagesByInvocation ran outside the read snapshot")
	}
	return s.invocationResultTestStore.ListSessionMessagesByInvocation(ctx, id)
}

func (s *snapshotAssertingStore) ListToolCallsByInvocation(ctx context.Context, id string) ([]domain.ToolCall, error) {
	if !s.tx.active {
		s.t.Fatal("ListToolCallsByInvocation ran outside the read snapshot")
	}
	return s.invocationResultTestStore.ListToolCallsByInvocation(ctx, id)
}

func (s *snapshotAssertingStore) ListSessionMessages(ctx context.Context, id string) ([]domain.SessionMessage, error) {
	if !s.tx.active {
		s.t.Fatal("ListSessionMessages ran outside the read snapshot")
	}
	return s.invocationResultTestStore.ListSessionMessages(ctx, id)
}

func TestGetInvocationResultReadsInsideOneSnapshot(t *testing.T) {
	tx := &snapshotRecordingTx{}
	store := &snapshotAssertingStore{
		invocationResultTestStore: invocationResultTestStore{
			invocation: resultTestInvocation(domain.InvocationCompleted),
			partition:  domain.TenantPartition{ID: resultPartitionID, AccountID: resultAccountID},
			messages: []domain.SessionMessage{
				resultTestMessage(1, domain.MessageRoleAssistant, `[{"type":"text","text":"Hello"}]`),
			},
		},
		t:  t,
		tx: tx,
	}
	service := NewRuntimeService(store, tx, recoveryTestClock{}, recoveryTestIDs{})

	result, err := service.GetInvocationResult(context.Background(), resultAuth(), resultInvocationID)
	if err != nil {
		t.Fatalf("GetInvocationResult: %v", err)
	}
	if tx.entered != 1 {
		t.Fatalf("read snapshot entered %d times, want exactly one snapshot per read", tx.entered)
	}
	if result.OutputText == nil || *result.OutputText != "Hello" {
		t.Fatalf("output text = %v", result.OutputText)
	}
}

// TestGetInvocationResultWaitingReadsInsideOneSnapshot covers the one
// status whose composition performs extra reads: recovering pending
// host tool calls and their stored inputs must also stay inside the
// single snapshot.
func TestGetInvocationResultWaitingReadsInsideOneSnapshot(t *testing.T) {
	toolRequest := resultTestMessage(2, domain.MessageRoleAssistant, `[{"type":"tool_use","id":"tcal_wait","name":"lookup","input":{"order_id":"o-1"}}]`)
	tx := &snapshotRecordingTx{}
	store := &snapshotAssertingStore{
		invocationResultTestStore: invocationResultTestStore{
			invocation: resultTestInvocation(domain.InvocationWaiting),
			partition:  domain.TenantPartition{ID: resultPartitionID, AccountID: resultAccountID},
			messages: []domain.SessionMessage{
				resultTestMessage(1, domain.MessageRoleUser, `[{"type":"text","text":"look it up"}]`),
				toolRequest,
			},
			toolCalls: []domain.ToolCall{
				{
					ID:               "tcal_wait",
					InvocationID:     resultInvocationID,
					SessionID:        resultSessionID,
					Name:             "lookup",
					Mode:             domain.ToolCallModeHost,
					RequestMessageID: toolRequest.ID,
					Status:           domain.ToolCallPending,
					DeadlineAt:       time.Date(2026, time.July, 21, 13, 0, 0, 0, time.UTC),
				},
			},
		},
		t:  t,
		tx: tx,
	}
	service := NewRuntimeService(store, tx, recoveryTestClock{}, recoveryTestIDs{})

	result, err := service.GetInvocationResult(context.Background(), resultAuth(), resultInvocationID)
	if err != nil {
		t.Fatalf("GetInvocationResult: %v", err)
	}
	if tx.entered != 1 {
		t.Fatalf("read snapshot entered %d times, want exactly one snapshot per read", tx.entered)
	}
	if len(result.Invocation.PendingToolCalls) != 1 || result.OutputText != nil {
		t.Fatalf("waiting result = %#v", result)
	}
}

func TestGetInvocationResultScopingMatchesGetInvocation(t *testing.T) {
	store := &invocationResultTestStore{
		invocation: resultTestInvocation(domain.InvocationCompleted),
		partition:  domain.TenantPartition{ID: resultPartitionID, AccountID: resultAccountID},
		messages: []domain.SessionMessage{
			resultTestMessage(1, domain.MessageRoleAssistant, `[{"type":"text","text":"secret"}]`),
		},
	}
	service := newInvocationResultTestService(store)

	if _, err := service.GetInvocationResult(context.Background(), resultAuth(), "not-an-id"); !publicErrorCodeIs(err, CodeInvalidRequest) {
		t.Fatalf("invalid id error = %v", err)
	}

	absent := newInvocationResultTestService(&invocationResultTestStore{})
	if _, err := absent.GetInvocationResult(context.Background(), resultAuth(), resultInvocationID); !publicErrorCodeIs(err, CodeNotFound) {
		t.Fatalf("absent Invocation error = %v", err)
	}

	noOperation := resultAuth()
	noOperation.Operations = nil
	if _, err := service.GetInvocationResult(context.Background(), noOperation, resultInvocationID); !publicErrorCodeIs(err, CodeForbidden) {
		t.Fatalf("missing operation error = %v", err)
	}

	crossAccount := resultAuth()
	crossAccount.AccountID = "acct_019b0a12-0000-7000-8000-000000000999"
	if _, err := service.GetInvocationResult(context.Background(), crossAccount, resultInvocationID); !publicErrorCodeIs(err, CodeNotFound) {
		t.Fatalf("cross-account error = %v", err)
	}

	otherSession := "sesn_019b0a12-0000-7000-8000-000000000999"
	constrained := resultAuth()
	constrained.SessionConstraint = &otherSession
	if _, err := service.GetInvocationResult(context.Background(), constrained, resultInvocationID); !publicErrorCodeIs(err, CodeNotFound) {
		t.Fatalf("cross-Session error = %v", err)
	}

	tenant := "tenant-b"
	mismatch := resultAuth()
	mismatch.TenantConstraint = &tenant
	if _, err := service.GetInvocationResult(context.Background(), mismatch, resultInvocationID); !publicErrorCodeIs(err, CodeNotFound) {
		t.Fatalf("tenant mismatch error = %v", err)
	}
}
