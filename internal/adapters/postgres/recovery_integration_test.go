package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestRecoveryKeysetAndRangeIndexesAreUsable(t *testing.T) {
	pool, _, _, auth := newRuntimeFixture(t)
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

	tests := []struct {
		name, index, query string
		args               []any
	}{
		{
			name: "Sessions", index: "sessions_account_created_keyset",
			query: "SELECT * FROM sessions WHERE account_id = $1 ORDER BY created_at DESC, id DESC LIMIT 201",
			args:  []any{auth.AccountID},
		},
		{
			name: "Invocations", index: "invocations_account_created_keyset",
			query: "SELECT * FROM invocations WHERE account_id = $1 ORDER BY created_at DESC, id DESC LIMIT 201",
			args:  []any{auth.AccountID},
		},
		{
			name: "Messages", index: "session_messages_session_sequence_unique",
			query: "SELECT * FROM session_messages WHERE session_id = $1 AND sequence > $2 AND sequence <= $3 ORDER BY sequence LIMIT 201",
			args:  []any{recoveryMissingSessionID, int64(0), int64(100)},
		},
		{
			name: "Lifecycle", index: "invocation_states_session_revision_unique",
			query: "SELECT * FROM invocation_states WHERE session_id = $1 AND revision > $2 AND revision <= $3 ORDER BY revision LIMIT 201",
			args:  []any{recoveryMissingSessionID, int64(0), int64(100)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows, err := conn.Query(ctx, "EXPLAIN (COSTS OFF) "+test.query, test.args...)
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			var plan strings.Builder
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					rows.Close()
					t.Fatalf("scan plan: %v", err)
				}
				plan.WriteString(line)
				plan.WriteByte('\n')
			}
			rows.Close()
			if !strings.Contains(plan.String(), test.index) {
				t.Fatalf("plan does not use %s:\n%s", test.index, plan.String())
			}
		})
	}
}

const recoveryMissingSessionID = "sesn_019b0a12-0000-7000-8000-000000000099"

func TestRecoveryReadsPageFilterAndPreserveTranscriptOrdering(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	first, err := runtime.Admit(ctx, auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit default Invocation: %v", err)
	}
	tenantRef := "tenant-a"
	secondInput := runtimeInput()
	secondInput.TenantRef = &tenantRef
	secondInput.SessionKey = pointerString("tenant-ticket")
	secondInput.IdempotencyKey = "tenant-request"
	second, err := runtime.Admit(ctx, auth, secondInput)
	if err != nil {
		t.Fatalf("admit tenant Invocation: %v", err)
	}
	otherAccount := domain.Account{ID: testID(t, domain.PrefixAccount), CreatedAt: time.Now().UTC()}
	otherDefault := domain.TenantPartition{
		ID: testID(t, domain.PrefixTenantPartition), AccountID: otherAccount.ID, CreatedAt: otherAccount.CreatedAt,
	}
	if err := NewTransactionManager(pool).WithTransaction(ctx, func(txCtx context.Context) error {
		if err := store.CreateAccount(txCtx, otherAccount); err != nil {
			return err
		}
		return store.CreateTenantPartition(txCtx, otherDefault)
	}); err != nil {
		t.Fatalf("create other Account: %v", err)
	}
	otherAuth := runtimeAuth(otherAccount.ID)
	other, err := runtime.Admit(ctx, otherAuth, runtimeInput())
	if err != nil {
		t.Fatalf("admit other Account Invocation: %v", err)
	}
	equalCreatedAt := time.Now().UTC().Truncate(time.Second)
	if _, err := pool.Exec(ctx,
		"UPDATE sessions SET created_at = $1 WHERE id = ANY($2::text[])",
		equalCreatedAt, []string{first.SessionID, second.SessionID},
	); err != nil {
		t.Fatalf("align Session timestamps: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"UPDATE invocations SET created_at = $1::timestamptz, wall_clock_deadline_at = $1::timestamptz + wall_clock_timeout_ms * interval '1 millisecond' WHERE id = ANY($2::text[])",
		equalCreatedAt, []string{first.InvocationID, second.InvocationID},
	); err != nil {
		t.Fatalf("align Invocation timestamps: %v", err)
	}

	clock := identity.SystemClock{}
	execution := services.NewInvocationExecutionService(
		store, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock),
	)
	claim, disposition, err := execution.ClaimExact(ctx, first.InvocationID, "recovery-owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim = %#v, disposition = %q, error = %v", claim, disposition, err)
	}
	if err := execution.Settle(ctx, claim, completedResult()); err != nil {
		t.Fatalf("settle: %v", err)
	}

	read, err := runtime.GetInvocation(ctx, auth, first.InvocationID)
	if err != nil || read.Status != domain.InvocationCompleted || len(read.Usage) == 0 || len(read.Provenance) == 0 {
		t.Fatalf("completed read = %#v, error = %v", read, err)
	}
	firstSession, err := runtime.GetSession(ctx, auth, first.SessionID)
	if err != nil || firstSession.ActiveInvocationID != nil || firstSession.ActiveInvocationStatus != nil {
		t.Fatalf("terminal Session read = %#v, error = %v", firstSession, err)
	}
	secondSession, err := runtime.GetSession(ctx, auth, second.SessionID)
	if err != nil || secondSession.ActiveInvocationID == nil || *secondSession.ActiveInvocationID != second.InvocationID ||
		secondSession.ActiveInvocationStatus == nil || *secondSession.ActiveInvocationStatus != domain.InvocationQueued {
		t.Fatalf("active Session read = %#v, error = %v", secondSession, err)
	}

	all, err := runtime.ListSessions(ctx, auth, services.SessionListInput{Limit: 1})
	if err != nil || len(all.Items) != 1 || !all.HasMore || all.NextCursor == nil {
		t.Fatalf("first Session page = %#v, error = %v", all, err)
	}
	allNext, err := runtime.ListSessions(ctx, auth, services.SessionListInput{Limit: 1, Cursor: *all.NextCursor})
	if err != nil || len(allNext.Items) != 1 || allNext.HasMore {
		t.Fatalf("second Session page = %#v, error = %v", allNext, err)
	}
	if all.Items[0].ID == allNext.Items[0].ID {
		t.Fatalf("Session cursor duplicated %s", all.Items[0].ID)
	}
	otherSessions, err := runtime.ListSessions(ctx, otherAuth, services.SessionListInput{})
	if err != nil || len(otherSessions.Items) != 1 || otherSessions.Items[0].ID != other.SessionID {
		t.Fatalf("other Account Sessions = %#v, error = %v", otherSessions, err)
	}
	if _, err := runtime.ListSessions(ctx, otherAuth, services.SessionListInput{Limit: 1, Cursor: *all.NextCursor}); err == nil {
		t.Fatal("cross-Account collection cursor accepted")
	}
	invocationPage, err := runtime.ListInvocations(ctx, auth, services.InvocationListInput{Limit: 1})
	if err != nil || len(invocationPage.Items) != 1 || !invocationPage.HasMore || invocationPage.NextCursor == nil {
		t.Fatalf("first Invocation page = %#v, error = %v", invocationPage, err)
	}
	invocationNext, err := runtime.ListInvocations(ctx, auth, services.InvocationListInput{Limit: 1, Cursor: *invocationPage.NextCursor})
	if err != nil || len(invocationNext.Items) != 1 || invocationNext.HasMore || invocationPage.Items[0].ID == invocationNext.Items[0].ID {
		t.Fatalf("second Invocation page = %#v, error = %v", invocationNext, err)
	}
	defaultOnly, err := runtime.ListSessions(ctx, auth, services.SessionListInput{DefaultTenant: true})
	if err != nil || len(defaultOnly.Items) != 1 || defaultOnly.Items[0].ID != first.SessionID {
		t.Fatalf("default Sessions = %#v, error = %v", defaultOnly, err)
	}
	tenantOnly, err := runtime.ListInvocations(ctx, auth, services.InvocationListInput{TenantRef: &tenantRef})
	if err != nil || len(tenantOnly.Items) != 1 || tenantOnly.Items[0].ID != second.InvocationID {
		t.Fatalf("tenant Invocations = %#v, error = %v", tenantOnly, err)
	}
	constrained := auth
	constrained.TenantConstraint = &tenantRef
	constrainedSessions, err := runtime.ListSessions(ctx, constrained, services.SessionListInput{})
	if err != nil || len(constrainedSessions.Items) != 1 || constrainedSessions.Items[0].ID != second.SessionID {
		t.Fatalf("constrained Sessions = %#v, error = %v", constrainedSessions, err)
	}
	if _, err := runtime.ListSessions(ctx, constrained, services.SessionListInput{DefaultTenant: true}); err == nil {
		t.Fatal("constrained credential listed default tenant")
	}

	messages, err := runtime.ListSessionMessages(ctx, auth, first.SessionID, services.MessageListInput{Limit: 1})
	if err != nil || len(messages.Items) != 1 || messages.Items[0].Sequence != 1 || !messages.HasMore || messages.NextCursor == nil {
		t.Fatalf("first message page = %#v, error = %v", messages, err)
	}
	messages, err = runtime.ListSessionMessages(ctx, auth, first.SessionID, services.MessageListInput{Limit: 1, Cursor: *messages.NextCursor})
	if err != nil || len(messages.Items) != 1 || messages.Items[0].Sequence != 2 || messages.HasMore {
		t.Fatalf("second message page = %#v, error = %v", messages, err)
	}

	snapshot, err := runtime.GetSessionTranscript(ctx, auth, first.SessionID, services.TranscriptInput{Limit: 1})
	if err != nil || len(snapshot.Messages) != 1 || len(snapshot.InvocationChanges) != 0 {
		t.Fatalf("first transcript page = %#v, error = %v", snapshot, err)
	}
	seenMessages, seenChanges := 1, 0
	for snapshot.HasMore {
		if snapshot.NextPageToken == nil {
			t.Fatal("transcript has_more without next_page_token")
		}
		snapshot, err = runtime.GetSessionTranscript(ctx, auth, first.SessionID, services.TranscriptInput{
			PageToken: *snapshot.NextPageToken, Limit: 1,
		})
		if err != nil {
			t.Fatalf("continue transcript: %v", err)
		}
		seenMessages += len(snapshot.Messages)
		for _, change := range snapshot.InvocationChanges {
			if seenMessages != 2 {
				t.Fatalf("revision %d arrived after %d messages", change.Revision, seenMessages)
			}
			seenChanges++
		}
	}
	if seenMessages != 2 || seenChanges != 3 {
		t.Fatalf("transcript delivered %d messages and %d changes", seenMessages, seenChanges)
	}
}
