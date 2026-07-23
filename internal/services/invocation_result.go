package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

// InvocationResultRead is the one result model shared by every delivery
// mode: the authoritative Invocation, the turn's canonical messages composed
// at read time, and the assistant-text convenience projection. Nothing is
// stored for this read; the transcript remains the sole durable
// representation of content.
type InvocationResultRead struct {
	Invocation InvocationRead
	Messages   []domain.SessionMessage
	OutputText *string
}

// GetInvocationResult composes the result at any status. Authentication,
// tenant scoping, and the nondisclosing not_found rule match GetInvocation
// exactly. The Invocation row and its message rows are read in one
// repeatable-read snapshot, so the payload cannot show a terminal status
// with a missing message tail.
func (s *RuntimeService) GetInvocationResult(ctx context.Context, auth domain.RuntimeAuthContext, invocationID string) (InvocationResultRead, error) {
	if err := s.ready(); err != nil {
		return InvocationResultRead{}, err
	}
	if err := authorize(auth, domain.OperationGetInvocation); err != nil {
		return InvocationResultRead{}, err
	}
	if !domain.ValidStableID(invocationID, domain.PrefixInvocation) {
		return InvocationResultRead{}, invalidRequest("invocation_id is invalid.")
	}
	var result InvocationResultRead
	err := s.withReadSnapshot(ctx, func(ctx context.Context) error {
		invocation, err := s.store.GetInvocation(ctx, invocationID)
		if errors.Is(err, ports.ErrNotFound) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if invocation.AccountID != auth.AccountID || !auth.AllowsSession(invocation.SessionID) {
			return notFound()
		}
		partition, err := s.store.GetTenantPartition(ctx, invocation.TenantPartitionID)
		if err != nil || partition.AccountID != auth.AccountID || !tenantMatches(auth.TenantConstraint, partition.TenantKey) {
			if errors.Is(err, ports.ErrNotFound) || err == nil {
				return notFound()
			}
			return err
		}
		read := invocationReadFromDomain(invocation)
		read.PendingToolCalls, err = s.pendingHostToolCalls(ctx, invocation)
		if err != nil {
			return err
		}
		messages, err := s.store.ListSessionMessagesByInvocation(ctx, invocationID)
		if err != nil {
			return err
		}
		outputText, err := assistantOutputText(invocation.Status, messages)
		if err != nil {
			return err
		}
		result = InvocationResultRead{
			Invocation: read,
			Messages:   messages,
			OutputText: outputText,
		}
		return nil
	})
	if err != nil {
		return InvocationResultRead{}, err
	}
	return result, nil
}

// withReadSnapshot uses the transaction manager's repeatable-read snapshot
// when it offers one and falls back to sequential reads when it does not.
func (s *RuntimeService) withReadSnapshot(ctx context.Context, fn func(context.Context) error) error {
	if snapshots, ok := s.txm.(ports.ReadSnapshotManager); ok {
		return snapshots.WithReadSnapshot(ctx, fn)
	}
	return fn(ctx)
}

// assistantOutputText concatenates the text content blocks of the
// assistant-role messages in transcript order without separators. It is
// non-nil only for a completed Invocation with at least one assistant text
// block: evidence from failed and cancelled turns must not masquerade as
// successful output. Undecodable assistant content is an error, never a
// silently shortened answer.
func assistantOutputText(status domain.InvocationStatus, messages []domain.SessionMessage) (*string, error) {
	if status != domain.InvocationCompleted {
		return nil, nil
	}
	var text strings.Builder
	found := false
	for _, message := range messages {
		if message.Role != domain.MessageRoleAssistant {
			continue
		}
		var blocks []struct {
			Type string          `json:"type"`
			Text json.RawMessage `json:"text"`
		}
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return nil, fmt.Errorf("decode assistant message %s content: %w", message.ID, err)
		}
		for _, block := range blocks {
			if block.Type != "text" {
				continue
			}
			var value string
			if err := json.Unmarshal(block.Text, &value); err != nil {
				return nil, fmt.Errorf("decode assistant message %s text block: %w", message.ID, err)
			}
			found = true
			text.WriteString(value)
		}
	}
	if !found {
		return nil, nil
	}
	value := text.String()
	return &value, nil
}
