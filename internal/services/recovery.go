package services

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const (
	DefaultRecoveryPageSize = 100
	MaxRecoveryPageSize     = 200
)

type InvocationListInput struct {
	TenantRef     *string
	DefaultTenant bool
	SessionID     *string
	AgentID       *string
	Status        *domain.InvocationStatus
	Cursor        string
	Limit         int
}

type SessionListInput struct {
	TenantRef     *string
	DefaultTenant bool
	AgentID       *string
	SessionKey    *string
	Cursor        string
	Limit         int
}

type InvocationList struct {
	Items      []InvocationRead
	HasMore    bool
	NextCursor *string
}

type SessionList struct {
	Items      []SessionRead
	HasMore    bool
	NextCursor *string
}

type MessageListInput struct {
	Cursor string
	Limit  int
}

type SessionMessageList struct {
	Items      []domain.SessionMessage
	HasMore    bool
	NextCursor *string
}

type TranscriptInput struct {
	Cursor    string
	PageToken string
	Limit     int
}

type TranscriptSnapshot struct {
	Messages          []domain.SessionMessage
	InvocationChanges []domain.InvocationLifecycleChange
	HasMore           bool
	ResumeCursor      string
	NextPageToken     *string
}

func (s *RuntimeService) ListInvocations(ctx context.Context, auth domain.RuntimeAuthContext, input InvocationListInput) (InvocationList, error) {
	if err := s.ready(); err != nil {
		return InvocationList{}, err
	}
	if err := authorize(auth, domain.OperationListInvocations); err != nil {
		return InvocationList{}, err
	}
	limit, err := validateRecoveryLimit(input.Limit)
	if err != nil {
		return InvocationList{}, err
	}
	if err := validateOptionalStableID("session_id", input.SessionID, domain.PrefixSession); err != nil {
		return InvocationList{}, err
	}
	if err := validateOptionalStableID("agent_id", input.AgentID, domain.PrefixAgent); err != nil {
		return InvocationList{}, err
	}
	if input.Status != nil && !validInvocationStatus(*input.Status) {
		return InvocationList{}, invalidRequest("status is invalid.")
	}
	partitionID, tenantScope, err := s.resolveListTenantScope(ctx, auth, input.TenantRef, input.DefaultTenant)
	if err != nil {
		return InvocationList{}, err
	}
	filters := collectionFilter{TenantScope: tenantScope}
	if input.SessionID != nil {
		filters.SessionID = *input.SessionID
	}
	if input.AgentID != nil {
		filters.AgentID = *input.AgentID
	}
	if input.Status != nil {
		filters.Status = string(*input.Status)
	}
	var beforeTime *time.Time
	var beforeID *string
	if input.Cursor != "" {
		decodedTime, decodedID, err := decodeCollectionCursor(input.Cursor, "invocations", auth.AccountID, filters)
		if err != nil {
			return InvocationList{}, err
		}
		if !domain.ValidStableID(decodedID, domain.PrefixInvocation) {
			return InvocationList{}, invalidRequest("cursor is invalid for this collection and filter set.")
		}
		beforeTime, beforeID = &decodedTime, &decodedID
	}
	rows, err := s.store.ListInvocations(ctx, ports.InvocationListQuery{
		AccountID: auth.AccountID, TenantPartitionID: partitionID,
		SessionID: input.SessionID, AgentID: input.AgentID, Status: input.Status,
		BeforeCreatedAt: beforeTime, BeforeInvocationID: beforeID, Limit: limit + 1,
	})
	if err != nil {
		return InvocationList{}, err
	}
	page := InvocationList{Items: make([]InvocationRead, 0, min(len(rows), limit))}
	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, invocationReadFromDomain(row))
	}
	if page.HasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		cursor, err := encodeCollectionCursor("invocations", auth.AccountID, filters, last.CreatedAt, last.ID)
		if err != nil {
			return InvocationList{}, recoveryCursorEncodingError(err)
		}
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *RuntimeService) ListSessions(ctx context.Context, auth domain.RuntimeAuthContext, input SessionListInput) (SessionList, error) {
	if err := s.ready(); err != nil {
		return SessionList{}, err
	}
	if err := authorize(auth, domain.OperationListSessions); err != nil {
		return SessionList{}, err
	}
	limit, err := validateRecoveryLimit(input.Limit)
	if err != nil {
		return SessionList{}, err
	}
	if err := validateOptionalStableID("agent_id", input.AgentID, domain.PrefixAgent); err != nil {
		return SessionList{}, err
	}
	if input.SessionKey != nil {
		if err := validateBoundedString("session_key", *input.SessionKey, MaxReferenceCharacters); err != nil {
			return SessionList{}, err
		}
	}
	partitionID, tenantScope, err := s.resolveListTenantScope(ctx, auth, input.TenantRef, input.DefaultTenant)
	if err != nil {
		return SessionList{}, err
	}
	filters := collectionFilter{TenantScope: tenantScope}
	if input.AgentID != nil {
		filters.AgentID = *input.AgentID
	}
	if input.SessionKey != nil {
		filters.SessionKey = *input.SessionKey
	}
	var beforeTime *time.Time
	var beforeID *string
	if input.Cursor != "" {
		decodedTime, decodedID, err := decodeCollectionCursor(input.Cursor, "sessions", auth.AccountID, filters)
		if err != nil {
			return SessionList{}, err
		}
		if !domain.ValidStableID(decodedID, domain.PrefixSession) {
			return SessionList{}, invalidRequest("cursor is invalid for this collection and filter set.")
		}
		beforeTime, beforeID = &decodedTime, &decodedID
	}
	rows, err := s.store.ListSessions(ctx, ports.SessionListQuery{
		AccountID: auth.AccountID, TenantPartitionID: partitionID,
		AgentID: input.AgentID, SessionKey: input.SessionKey,
		BeforeCreatedAt: beforeTime, BeforeSessionID: beforeID, Limit: limit + 1,
	})
	if err != nil {
		return SessionList{}, err
	}
	page := SessionList{Items: make([]SessionRead, 0, min(len(rows), limit))}
	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, SessionRead{
			ID: row.Session.ID, AgentID: row.Session.AgentID, TenantRef: cloneString(row.TenantRef),
			SessionKey: cloneString(row.Session.SessionKey), ActiveInvocationID: cloneString(row.ActiveInvocationID),
			ActiveInvocationStatus: cloneStatus(row.ActiveInvocationStatus),
			CreatedAt:              row.Session.CreatedAt, UpdatedAt: row.Session.UpdatedAt,
		})
	}
	if page.HasMore && len(rows) > 0 {
		last := rows[len(rows)-1].Session
		cursor, err := encodeCollectionCursor("sessions", auth.AccountID, filters, last.CreatedAt, last.ID)
		if err != nil {
			return SessionList{}, recoveryCursorEncodingError(err)
		}
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *RuntimeService) ListSessionMessages(ctx context.Context, auth domain.RuntimeAuthContext, sessionID string, input MessageListInput) (SessionMessageList, error) {
	if err := s.ready(); err != nil {
		return SessionMessageList{}, err
	}
	if err := authorize(auth, domain.OperationListMessages); err != nil {
		return SessionMessageList{}, err
	}
	limit, err := validateRecoveryLimit(input.Limit)
	if err != nil {
		return SessionMessageList{}, err
	}
	session, _, err := s.authorizedSession(ctx, auth, sessionID)
	if err != nil {
		return SessionMessageList{}, err
	}
	after := int64(0)
	if input.Cursor != "" {
		after, err = decodeMessageCursor(input.Cursor, auth.AccountID, session.ID)
		if err != nil {
			return SessionMessageList{}, err
		}
		if after > session.NextMessageSequence-1 {
			return SessionMessageList{}, invalidRequest("cursor is ahead of the committed Session transcript.")
		}
	}
	rows, err := s.store.ListSessionMessagesRange(ctx, session.ID, after, math.MaxInt64, limit+1)
	if err != nil {
		return SessionMessageList{}, err
	}
	page := SessionMessageList{Items: rows}
	if len(rows) > limit {
		page.HasMore = true
		page.Items = rows[:limit]
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, err := encodeMessageCursor(auth.AccountID, session.ID, page.Items[len(page.Items)-1].Sequence)
		if err != nil {
			return SessionMessageList{}, recoveryCursorEncodingError(err)
		}
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *RuntimeService) GetSessionTranscript(ctx context.Context, auth domain.RuntimeAuthContext, sessionID string, input TranscriptInput) (TranscriptSnapshot, error) {
	if err := s.ready(); err != nil {
		return TranscriptSnapshot{}, err
	}
	if err := authorize(auth, domain.OperationGetTranscript); err != nil {
		return TranscriptSnapshot{}, err
	}
	if input.Cursor != "" && input.PageToken != "" {
		return TranscriptSnapshot{}, invalidRequest("cursor and page_token are mutually exclusive.")
	}
	limit, err := validateRecoveryLimit(input.Limit)
	if err != nil {
		return TranscriptSnapshot{}, err
	}
	session, _, err := s.authorizedSession(ctx, auth, sessionID)
	if err != nil {
		return TranscriptSnapshot{}, err
	}
	head := transcriptPosition{
		MessageSequence:   session.NextMessageSequence - 1,
		LifecycleRevision: session.NextLifecycleRevision - 1,
	}
	lower := transcriptPosition{}
	high := head
	if input.PageToken != "" {
		lower, high, err = decodeTranscriptPageToken(input.PageToken, auth.AccountID, session.ID)
	} else if input.Cursor != "" {
		lower, err = decodeTranscriptCursor(input.Cursor, auth.AccountID, session.ID)
	}
	if err != nil {
		return TranscriptSnapshot{}, err
	}
	if !lower.atOrBefore(high) || !high.atOrBefore(head) {
		return TranscriptSnapshot{}, invalidRequest("transcript position is ahead of the committed Session head.")
	}

	snapshot := TranscriptSnapshot{
		Messages: []domain.SessionMessage{}, InvocationChanges: []domain.InvocationLifecycleChange{},
	}
	if lower.MessageSequence < high.MessageSequence {
		rows, err := s.store.ListSessionMessagesRange(ctx, session.ID, lower.MessageSequence, high.MessageSequence, limit+1)
		if err != nil {
			return TranscriptSnapshot{}, err
		}
		if len(rows) == 0 {
			return TranscriptSnapshot{}, &PublicError{Code: CodeInternal, Message: "The request could not be completed."}
		}
		if len(rows) > limit {
			rows = rows[:limit]
			lower.MessageSequence = rows[len(rows)-1].Sequence
		} else {
			lower.MessageSequence = high.MessageSequence
		}
		snapshot.Messages = rows
		snapshot.HasMore = lower != high
		if lower != high {
			token, err := encodeTranscriptPageToken(auth.AccountID, session.ID, lower, high)
			if err != nil {
				return TranscriptSnapshot{}, recoveryCursorEncodingError(err)
			}
			snapshot.NextPageToken = &token
		}
		snapshot.ResumeCursor, err = encodeTranscriptCursor(auth.AccountID, session.ID, lower)
		if err != nil {
			return TranscriptSnapshot{}, recoveryCursorEncodingError(err)
		}
		return snapshot, nil
	}

	if lower.LifecycleRevision < high.LifecycleRevision {
		rows, err := s.store.ListInvocationLifecycleChanges(ctx, session.ID, lower.LifecycleRevision, high.LifecycleRevision, limit+1)
		if err != nil {
			return TranscriptSnapshot{}, err
		}
		if len(rows) == 0 {
			return TranscriptSnapshot{}, &PublicError{Code: CodeInternal, Message: "The request could not be completed."}
		}
		if len(rows) > limit {
			rows = rows[:limit]
			lower.LifecycleRevision = rows[len(rows)-1].Revision
		} else {
			lower.LifecycleRevision = high.LifecycleRevision
		}
		snapshot.InvocationChanges = rows
	}
	snapshot.HasMore = lower != high
	if snapshot.HasMore {
		token, err := encodeTranscriptPageToken(auth.AccountID, session.ID, lower, high)
		if err != nil {
			return TranscriptSnapshot{}, recoveryCursorEncodingError(err)
		}
		snapshot.NextPageToken = &token
	}
	snapshot.ResumeCursor, err = encodeTranscriptCursor(auth.AccountID, session.ID, lower)
	if err != nil {
		return TranscriptSnapshot{}, recoveryCursorEncodingError(err)
	}
	return snapshot, nil
}

func (s *RuntimeService) authorizedSession(ctx context.Context, auth domain.RuntimeAuthContext, sessionID string) (domain.Session, domain.TenantPartition, error) {
	if !domain.ValidStableID(sessionID, domain.PrefixSession) {
		return domain.Session{}, domain.TenantPartition{}, invalidRequest("session_id is invalid.")
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if errors.Is(err, ports.ErrNotFound) {
		return domain.Session{}, domain.TenantPartition{}, notFound()
	}
	if err != nil {
		return domain.Session{}, domain.TenantPartition{}, err
	}
	if session.AccountID != auth.AccountID {
		return domain.Session{}, domain.TenantPartition{}, notFound()
	}
	partition, err := s.store.GetTenantPartition(ctx, session.TenantPartitionID)
	if err != nil || partition.AccountID != auth.AccountID || !tenantMatches(auth.TenantConstraint, partition.TenantRef) {
		if errors.Is(err, ports.ErrNotFound) || err == nil {
			return domain.Session{}, domain.TenantPartition{}, notFound()
		}
		return domain.Session{}, domain.TenantPartition{}, err
	}
	return session, partition, nil
}

func (s *RuntimeService) resolveListTenantScope(ctx context.Context, auth domain.RuntimeAuthContext, tenantRef *string, defaultTenant bool) (*string, string, error) {
	if tenantRef != nil && defaultTenant {
		return nil, "", invalidRequest("tenant_ref and default_tenant are mutually exclusive.")
	}
	if tenantRef != nil {
		if err := validateBoundedString("tenant_ref", *tenantRef, MaxReferenceCharacters); err != nil {
			return nil, "", err
		}
	}
	if auth.TenantConstraint != nil {
		if defaultTenant || (tenantRef != nil && *tenantRef != *auth.TenantConstraint) {
			return nil, "", forbidden("The requested tenant filter conflicts with the credential constraint.")
		}
		partition, err := s.store.GetTenantPartitionByRef(ctx, auth.AccountID, *auth.TenantConstraint)
		if errors.Is(err, ports.ErrNotFound) {
			missing := ""
			return &missing, "ref:" + *auth.TenantConstraint, nil
		}
		if err != nil {
			return nil, "", err
		}
		return &partition.ID, "ref:" + *auth.TenantConstraint, nil
	}
	if defaultTenant {
		partition, err := s.store.GetDefaultTenantPartition(ctx, auth.AccountID)
		if errors.Is(err, ports.ErrNotFound) {
			missing := ""
			return &missing, "default", nil
		}
		if err != nil {
			return nil, "", err
		}
		return &partition.ID, "default", nil
	}
	if tenantRef != nil {
		partition, err := s.store.GetTenantPartitionByRef(ctx, auth.AccountID, *tenantRef)
		if errors.Is(err, ports.ErrNotFound) {
			missing := ""
			return &missing, "ref:" + *tenantRef, nil
		}
		if err != nil {
			return nil, "", err
		}
		return &partition.ID, "ref:" + *tenantRef, nil
	}
	return nil, "all", nil
}

func validateRecoveryLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultRecoveryPageSize, nil
	}
	if limit < 1 || limit > MaxRecoveryPageSize {
		return 0, invalidRequest("limit must be between 1 and 200.")
	}
	return limit, nil
}

func validateOptionalStableID(name string, value *string, prefix domain.StableIDPrefix) error {
	if value != nil && !domain.ValidStableID(*value, prefix) {
		return invalidRequest(name + " is invalid.")
	}
	return nil
}

func validInvocationStatus(status domain.InvocationStatus) bool {
	switch status {
	case domain.InvocationQueued, domain.InvocationRunning, domain.InvocationWaiting,
		domain.InvocationCompleted, domain.InvocationFailed, domain.InvocationCancelled:
		return true
	default:
		return false
	}
}

func cloneStatus(value *domain.InvocationStatus) *domain.InvocationStatus {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
