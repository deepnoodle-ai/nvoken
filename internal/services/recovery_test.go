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
	recoveryAccountID    = "acct_019b0a12-0000-7000-8000-000000000001"
	recoveryPartitionID  = "tprt_019b0a12-0000-7000-8000-000000000002"
	recoveryAgentID      = "agnt_019b0a12-0000-7000-8000-000000000003"
	recoverySessionID    = "sesn_019b0a12-0000-7000-8000-000000000004"
	recoveryInvocationID = "invk_019b0a12-0000-7000-8000-000000000005"
)

func TestRecoveryCollectionCursorBindsScopeAndFilters(t *testing.T) {
	createdAt := time.Date(2026, 7, 21, 12, 0, 0, 123, time.UTC)
	filters := collectionFilter{TenantScope: "ref:tenant-a", AgentID: recoveryAgentID}
	cursor, err := encodeCollectionCursor("sessions", recoveryAccountID, filters, createdAt, recoverySessionID)
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}

	decodedTime, decodedID, err := decodeCollectionCursor(cursor, "sessions", recoveryAccountID, filters)
	if err != nil || !decodedTime.Equal(createdAt) || decodedID != recoverySessionID {
		t.Fatalf("decode = %s %q %v", decodedTime, decodedID, err)
	}
	for name, test := range map[string]struct {
		kind, account string
		filters       collectionFilter
	}{
		"collection": {kind: "invocations", account: recoveryAccountID, filters: filters},
		"account":    {kind: "sessions", account: "acct_019b0a12-0000-7000-8000-000000000099", filters: filters},
		"tenant":     {kind: "sessions", account: recoveryAccountID, filters: collectionFilter{TenantScope: "ref:tenant-b", AgentID: recoveryAgentID}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeCollectionCursor(cursor, test.kind, test.account, test.filters); err == nil {
				t.Fatal("mismatched cursor accepted")
			}
		})
	}
	if _, _, err := decodeCollectionCursor("not-base64", "sessions", recoveryAccountID, filters); err == nil {
		t.Fatal("malformed cursor accepted")
	}
}

func TestRecoveryCursorEncodingReportsMarshalFailure(t *testing.T) {
	if _, err := encodeRecoveryCursor(func() {}); err == nil {
		t.Fatal("unsupported cursor value encoded without an error")
	}
}

func TestTranscriptFixedCutDrainsMessagesBeforeLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	store := &recoveryTestStore{
		session: domain.Session{
			ID: recoverySessionID, AccountID: recoveryAccountID,
			TenantPartitionID: recoveryPartitionID, AgentID: recoveryAgentID,
			NextMessageSequence: 3, NextLifecycleRevision: 4,
		},
		partition: domain.TenantPartition{ID: recoveryPartitionID, AccountID: recoveryAccountID},
		messages: []domain.SessionMessage{
			{ID: "smsg_019b0a12-0000-7000-8000-000000000006", SessionID: recoverySessionID, AccountID: recoveryAccountID, TenantPartitionID: recoveryPartitionID, AgentID: recoveryAgentID, InvocationID: recoveryInvocationID, Sequence: 1, Role: domain.MessageRoleUser, Content: json.RawMessage(`[{"type":"text","text":"question"}]`), CreatedAt: now},
			{ID: "smsg_019b0a12-0000-7000-8000-000000000007", SessionID: recoverySessionID, AccountID: recoveryAccountID, TenantPartitionID: recoveryPartitionID, AgentID: recoveryAgentID, InvocationID: recoveryInvocationID, Sequence: 2, Role: domain.MessageRoleAssistant, Content: json.RawMessage(`[{"type":"text","text":"answer"}]`), CreatedAt: now.Add(time.Second)},
		},
		changes: []domain.InvocationLifecycleChange{
			{InvocationState: stateAt(1, domain.InvocationQueued, 1, now)},
			{InvocationState: stateAt(2, domain.InvocationRunning, 1, now.Add(time.Second))},
			{InvocationState: stateAt(3, domain.InvocationCompleted, 2, now.Add(2*time.Second)), Usage: json.RawMessage(`{"input_tokens":2,"output_tokens":1}`), Provenance: json.RawMessage(`{"provider":"anthropic"}`)},
		},
	}
	service := newRecoveryTestService(store)
	auth := recoveryAuth(domain.OperationGetTranscript)

	page, err := service.GetSessionTranscript(context.Background(), auth, recoverySessionID, TranscriptInput{Limit: 1})
	if err != nil || len(page.Messages) != 1 || len(page.InvocationChanges) != 0 || page.NextPageToken == nil {
		t.Fatalf("first page = %#v, err = %v", page, err)
	}

	// These writes commit after the first page. Every continuation must retain
	// the original high cut (message 2, lifecycle revision 3).
	store.session.NextMessageSequence = 4
	store.session.NextLifecycleRevision = 5
	store.messages = append(store.messages, domain.SessionMessage{
		ID: "smsg_019b0a12-0000-7000-8000-000000000008", SessionID: recoverySessionID,
		AccountID: recoveryAccountID, TenantPartitionID: recoveryPartitionID,
		AgentID: recoveryAgentID, InvocationID: recoveryInvocationID, Sequence: 3,
		Role: domain.MessageRoleUser, Content: json.RawMessage(`[{"type":"text","text":"later"}]`), CreatedAt: now.Add(3 * time.Second),
	})
	store.changes = append(store.changes, domain.InvocationLifecycleChange{
		InvocationState: stateAt(4, domain.InvocationFailed, 3, now.Add(4*time.Second)),
		Error:           json.RawMessage(`{"code":"provider_error","message":"later"}`),
	})

	seenMessages := []int64{page.Messages[0].Sequence}
	seenRevisions := []int64{}
	for page.HasMore {
		page, err = service.GetSessionTranscript(context.Background(), auth, recoverySessionID, TranscriptInput{
			PageToken: *page.NextPageToken, Limit: 1,
		})
		if err != nil {
			t.Fatalf("continue fixed cut: %v", err)
		}
		for _, message := range page.Messages {
			seenMessages = append(seenMessages, message.Sequence)
		}
		for _, change := range page.InvocationChanges {
			if len(seenMessages) != 2 {
				t.Fatalf("lifecycle revision %d arrived after only %d messages", change.Revision, len(seenMessages))
			}
			seenRevisions = append(seenRevisions, change.Revision)
		}
	}
	if got, want := seenMessages, []int64{1, 2}; !equalInt64s(got, want) {
		t.Fatalf("fixed-cut messages = %v, want %v", got, want)
	}
	if got, want := seenRevisions, []int64{1, 2, 3}; !equalInt64s(got, want) {
		t.Fatalf("fixed-cut revisions = %v, want %v", got, want)
	}
	finalCursor := page.ResumeCursor

	next, err := service.GetSessionTranscript(context.Background(), auth, recoverySessionID, TranscriptInput{Cursor: finalCursor, Limit: 1})
	if err != nil || len(next.Messages) != 1 || next.Messages[0].Sequence != 3 || len(next.InvocationChanges) != 0 || !next.HasMore {
		t.Fatalf("next cut message page = %#v, err = %v", next, err)
	}
	next, err = service.GetSessionTranscript(context.Background(), auth, recoverySessionID, TranscriptInput{PageToken: *next.NextPageToken, Limit: 1})
	if err != nil || len(next.Messages) != 0 || len(next.InvocationChanges) != 1 || next.InvocationChanges[0].Revision != 4 || next.HasMore {
		t.Fatalf("next cut lifecycle page = %#v, err = %v", next, err)
	}
}

func TestTranscriptRejectsAheadAndCrossSessionCursors(t *testing.T) {
	store := &recoveryTestStore{
		session:   domain.Session{ID: recoverySessionID, AccountID: recoveryAccountID, TenantPartitionID: recoveryPartitionID, AgentID: recoveryAgentID, NextMessageSequence: 1, NextLifecycleRevision: 1},
		partition: domain.TenantPartition{ID: recoveryPartitionID, AccountID: recoveryAccountID},
	}
	service := newRecoveryTestService(store)
	auth := recoveryAuth(domain.OperationGetTranscript)
	ahead, err := encodeTranscriptCursor(recoveryAccountID, recoverySessionID, transcriptPosition{MessageSequence: 1})
	if err != nil {
		t.Fatalf("encode ahead cursor: %v", err)
	}
	if _, err := service.GetSessionTranscript(context.Background(), auth, recoverySessionID, TranscriptInput{Cursor: ahead}); err == nil {
		t.Fatal("ahead-of-head cursor accepted")
	}
	other, err := encodeTranscriptCursor(recoveryAccountID, "sesn_019b0a12-0000-7000-8000-000000000099", transcriptPosition{})
	if err != nil {
		t.Fatalf("encode other cursor: %v", err)
	}
	if _, err := service.GetSessionTranscript(context.Background(), auth, recoverySessionID, TranscriptInput{Cursor: other}); err == nil {
		t.Fatal("cross-Session cursor accepted")
	}
}

func TestSessionConstraintHidesOtherSessionsAndNarrowsCollections(t *testing.T) {
	store := &recoveryTestStore{
		session: domain.Session{
			ID: recoverySessionID, AccountID: recoveryAccountID,
			TenantPartitionID: recoveryPartitionID, AgentID: recoveryAgentID,
			NextMessageSequence: 1, NextLifecycleRevision: 1,
		},
		partition: domain.TenantPartition{ID: recoveryPartitionID, AccountID: recoveryAccountID},
	}
	service := newRecoveryTestService(store)
	matching := recoveryAuth(domain.OperationGetSession, domain.OperationListSessions, domain.OperationGetTranscript, domain.OperationListInvocations)
	matching.SessionConstraint = stringTestPointer(recoverySessionID)

	if session, err := service.GetSession(context.Background(), matching, recoverySessionID); err != nil || session.ID != recoverySessionID {
		t.Fatalf("matching constrained Session = %#v, %v", session, err)
	}
	listed, err := service.ListSessions(context.Background(), matching, SessionListInput{})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != recoverySessionID {
		t.Fatalf("matching constrained Session list = %#v, %v", listed, err)
	}
	otherAgent := "agnt_019b0a12-0000-7000-8000-000000000099"
	listed, err = service.ListSessions(context.Background(), matching, SessionListInput{AgentID: &otherAgent})
	if err != nil || len(listed.Items) != 0 {
		t.Fatalf("mismatched constrained Session list = %#v, %v", listed, err)
	}
	otherSession := "sesn_019b0a12-0000-7000-8000-000000000099"
	other := recoveryAuth(domain.OperationGetSession, domain.OperationGetTranscript, domain.OperationListInvocations)
	other.SessionConstraint = &otherSession
	if _, err := service.GetSession(context.Background(), other, recoverySessionID); !publicErrorCodeIs(err, CodeNotFound) {
		t.Fatalf("cross-Session read error = %v", err)
	}
	if _, err := service.GetSessionTranscript(context.Background(), other, recoverySessionID, TranscriptInput{}); !publicErrorCodeIs(err, CodeNotFound) {
		t.Fatalf("cross-Session transcript error = %v", err)
	}
	requestedSession := recoverySessionID
	invocations, err := service.ListInvocations(context.Background(), other, InvocationListInput{SessionID: &requestedSession})
	if err != nil || len(invocations.Items) != 0 {
		t.Fatalf("cross-Session invocation list = %#v, %v", invocations, err)
	}
}

type recoveryTestStore struct {
	admissionStore
	session   domain.Session
	partition domain.TenantPartition
	messages  []domain.SessionMessage
	changes   []domain.InvocationLifecycleChange
}

func (s *recoveryTestStore) GetSession(context.Context, string) (domain.Session, error) {
	if s.session.ID == "" {
		return domain.Session{}, ports.ErrNotFound
	}
	return s.session, nil
}

func (s *recoveryTestStore) GetTenantPartition(context.Context, string) (domain.TenantPartition, error) {
	if s.partition.ID == "" {
		return domain.TenantPartition{}, ports.ErrNotFound
	}
	return s.partition, nil
}

func (s *recoveryTestStore) GetNonterminalInvocationBySession(context.Context, string) (domain.Invocation, error) {
	return domain.Invocation{}, ports.ErrNotFound
}

func (s *recoveryTestStore) ListSessionMessagesRange(_ context.Context, _ string, after, through int64, limit int) ([]domain.SessionMessage, error) {
	items := []domain.SessionMessage{}
	for _, item := range s.messages {
		if item.Sequence > after && item.Sequence <= through {
			items = append(items, item)
			if len(items) == limit {
				break
			}
		}
	}
	return items, nil
}

func (s *recoveryTestStore) ListInvocationLifecycleChanges(_ context.Context, _ string, after, through int64, limit int) ([]domain.InvocationLifecycleChange, error) {
	items := []domain.InvocationLifecycleChange{}
	for _, item := range s.changes {
		if item.Revision > after && item.Revision <= through {
			items = append(items, item)
			if len(items) == limit {
				break
			}
		}
	}
	return items, nil
}

type recoveryTestTx struct{}

func (recoveryTestTx) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type recoveryTestClock struct{}

func (recoveryTestClock) Now() time.Time { return time.Now() }

type recoveryTestIDs struct{}

func (recoveryTestIDs) NewID(domain.StableIDPrefix) (string, error) {
	return "", errors.New("not used")
}

func newRecoveryTestService(store *recoveryTestStore) *RuntimeService {
	return NewRuntimeService(store, recoveryTestTx{}, recoveryTestClock{}, recoveryTestIDs{})
}

func recoveryAuth(operations ...domain.RuntimeOperation) domain.RuntimeAuthContext {
	allowed := make(map[domain.RuntimeOperation]struct{}, len(operations))
	for _, operation := range operations {
		allowed[operation] = struct{}{}
	}
	return domain.RuntimeAuthContext{AccountID: recoveryAccountID, Operations: allowed}
}

func stateAt(revision int64, status domain.InvocationStatus, through int64, at time.Time) domain.InvocationState {
	return domain.InvocationState{
		InvocationID: recoveryInvocationID, SessionID: recoverySessionID,
		AccountID: recoveryAccountID, TenantPartitionID: recoveryPartitionID,
		AgentID: recoveryAgentID, Revision: revision, Status: status,
		ThroughMessageSequence: &through, CreatedAt: at,
	}
}

func equalInt64s(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
