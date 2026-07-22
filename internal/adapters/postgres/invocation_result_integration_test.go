package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestInvocationResultComposedRead(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	txm := NewTransactionManager(pool)
	ctx := context.Background()

	ack, err := runtime.Admit(ctx, auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}

	queued, err := runtime.GetInvocationResult(ctx, auth, ack.InvocationID)
	if err != nil {
		t.Fatalf("result at queued: %v", err)
	}
	if queued.Invocation.Status != domain.InvocationQueued || len(queued.Messages) != 1 || queued.OutputText != nil {
		t.Fatalf("queued result = %#v", queued)
	}
	if queued.Messages[0].Role != domain.MessageRoleUser || queued.Messages[0].InvocationID != ack.InvocationID {
		t.Fatalf("queued result message = %#v", queued.Messages[0])
	}

	invocation, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil {
		t.Fatalf("read admitted Invocation: %v", err)
	}
	now := time.Now().UTC()
	assistant := domain.SessionMessage{
		ID:                testID(t, domain.PrefixSessionMessage),
		SessionID:         invocation.SessionID,
		AccountID:         invocation.AccountID,
		TenantPartitionID: invocation.TenantPartitionID,
		AgentID:           invocation.AgentID,
		InvocationID:      invocation.ID,
		Sequence:          2,
		Role:              domain.MessageRoleAssistant,
		Content:           []byte(`[{"type":"text","text":"Hello"},{"type":"text","text":", world"}]`),
		CreatedAt:         now,
	}
	if err := txm.WithTransaction(ctx, func(ctx context.Context) error {
		return store.AppendSessionMessage(ctx, assistant)
	}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	if err := updateInvocationStatusForTest(ctx, pool, ack.InvocationID, domain.InvocationCompleted, 2, nil, &now); err != nil {
		t.Fatalf("settle completed: %v", err)
	}

	completed, err := runtime.GetInvocationResult(ctx, auth, ack.InvocationID)
	if err != nil {
		t.Fatalf("result at completed: %v", err)
	}
	if completed.Invocation.Status != domain.InvocationCompleted || len(completed.Messages) != 2 {
		t.Fatalf("completed result = %#v", completed)
	}
	if completed.Messages[0].Sequence != 1 || completed.Messages[1].Sequence != 2 {
		t.Fatalf("completed result messages out of order: %#v", completed.Messages)
	}
	if completed.OutputText == nil || *completed.OutputText != "Hello, world" {
		t.Fatalf("completed output text = %v", completed.OutputText)
	}
}

// TestInvocationResultAtWaitingComposesPendingToolCalls proves the one
// status whose composition needs extra reads: a parked client-tool turn
// recovers its pending calls and their stored inputs inside the same
// read-only snapshot that serves the Invocation and message rows.
func TestInvocationResultAtWaitingComposesPendingToolCalls(t *testing.T) {
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
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	txm := NewTransactionManager(pool)
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "result-waiting-owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim disposition = %q, error = %v", disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
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

	waiting, err := runtime.GetInvocationResult(ctx, auth, ack.InvocationID)
	if err != nil {
		t.Fatalf("result at waiting: %v", err)
	}
	if waiting.Invocation.Status != domain.InvocationWaiting || waiting.OutputText != nil {
		t.Fatalf("waiting result = %#v", waiting.Invocation)
	}
	if len(waiting.Messages) != 2 ||
		waiting.Messages[0].Role != domain.MessageRoleUser ||
		waiting.Messages[1].Role != domain.MessageRoleAssistant {
		t.Fatalf("waiting result messages = %#v", waiting.Messages)
	}
	if len(waiting.Invocation.PendingToolCalls) != 1 ||
		waiting.Invocation.PendingToolCalls[0].ID != recorded.ToolCalls[0].ID {
		t.Fatalf("waiting pending tool calls = %#v", waiting.Invocation.PendingToolCalls)
	}
	var pendingInput map[string]any
	if err := json.Unmarshal(waiting.Invocation.PendingToolCalls[0].Input, &pendingInput); err != nil {
		t.Fatalf("decode pending tool call input: %v", err)
	}
	if len(pendingInput) != 1 || pendingInput["order_id"] != "order-1" {
		t.Fatalf("waiting pending tool call input = %#v", pendingInput)
	}
}

// TestInvocationResultSnapshotExcludesConcurrentSettlement pins the
// settlement race: a result read concurrent with terminal settlement must
// return either the pre-terminal state or the terminal state with its
// complete message tail, never a mix. The read snapshot opens before
// settlement, settlement commits on another connection, and every read
// inside the snapshot still observes the pre-settlement state.
func TestInvocationResultSnapshotExcludesConcurrentSettlement(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	txm := NewTransactionManager(pool)
	ctx := context.Background()

	ack, err := runtime.Admit(ctx, auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	invocation, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil {
		t.Fatalf("read admitted Invocation: %v", err)
	}

	now := time.Now().UTC()
	if err := txm.WithReadSnapshot(ctx, func(snapshotCtx context.Context) error {
		before, err := store.GetInvocation(snapshotCtx, ack.InvocationID)
		if err != nil {
			t.Fatalf("snapshot invocation read: %v", err)
		}
		if before.Status != domain.InvocationQueued {
			t.Fatalf("snapshot invocation status = %s", before.Status)
		}

		// Terminal settlement commits on a separate autocommit connection
		// while the snapshot stays open.
		if err := execError(ctx, pool, `
			INSERT INTO session_messages (
			    id, session_id, account_id, tenant_partition_id, agent_id,
			    invocation_id, sequence, role, content, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, testID(t, domain.PrefixSessionMessage), invocation.SessionID, invocation.AccountID,
			invocation.TenantPartitionID, invocation.AgentID, invocation.ID,
			int64(2), "assistant", []byte(`[{"type":"text","text":"late"}]`), now); err != nil {
			t.Fatalf("concurrent assistant append: %v", err)
		}
		if err := updateInvocationStatusForTest(ctx, pool, ack.InvocationID, domain.InvocationCompleted, 2, nil, &now); err != nil {
			t.Fatalf("concurrent settlement: %v", err)
		}

		messages, err := store.ListSessionMessagesByInvocation(snapshotCtx, ack.InvocationID)
		if err != nil {
			t.Fatalf("snapshot message read: %v", err)
		}
		if len(messages) != 1 {
			t.Fatalf("snapshot sees %d messages after concurrent settlement, want the pre-settlement 1", len(messages))
		}
		after, err := store.GetInvocation(snapshotCtx, ack.InvocationID)
		if err != nil {
			t.Fatalf("snapshot invocation re-read: %v", err)
		}
		if after.Status != domain.InvocationQueued {
			t.Fatalf("snapshot invocation status moved to %s mid-read", after.Status)
		}
		return nil
	}); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}

	settled, err := runtime.GetInvocationResult(ctx, auth, ack.InvocationID)
	if err != nil {
		t.Fatalf("result after settlement: %v", err)
	}
	if settled.Invocation.Status != domain.InvocationCompleted || len(settled.Messages) != 2 {
		t.Fatalf("post-settlement result = %#v", settled)
	}
	if settled.OutputText == nil || *settled.OutputText != "late" {
		t.Fatalf("post-settlement output text = %v", settled.OutputText)
	}
}

func TestInvocationMessagesIndexIsUsable(t *testing.T) {
	pool, _, _, _ := newRuntimeFixture(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "RESET enable_seqscan; RESET enable_bitmapscan")
		conn.Release()
	}()
	if _, err := conn.Exec(ctx, "SET enable_seqscan = off"); err != nil {
		t.Fatalf("disable sequential scans: %v", err)
	}
	if _, err := conn.Exec(ctx, "SET enable_bitmapscan = off"); err != nil {
		t.Fatalf("disable bitmap scans: %v", err)
	}
	rows, err := conn.Query(ctx, "EXPLAIN (COSTS OFF) SELECT * FROM session_messages WHERE invocation_id = $1 ORDER BY sequence", "invk_019b0a12-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if !strings.Contains(plan.String(), "session_messages_by_invocation") {
		t.Fatalf("plan does not use session_messages_by_invocation:\n%s", plan.String())
	}
}
