package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/observability"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type PendingClientToolCall struct {
	ID         string
	Name       string
	Input      json.RawMessage
	DeadlineAt time.Time
}

type ClientToolResultInput struct {
	ToolCallID string          `json:"tool_call_id"`
	Content    json.RawMessage `json:"content"`
	IsError    bool            `json:"is_error,omitempty"`
}

type SubmitClientToolResultsInput struct {
	Results []ClientToolResultInput `json:"results"`
}

type ClientToolResultAcceptance struct {
	ToolCallID   string
	Status       domain.ToolCallStatus
	Deduplicated bool
}

type SubmitClientToolResultsResult struct {
	InvocationID     string
	SessionID        string
	Status           domain.InvocationStatus
	Results          []ClientToolResultAcceptance
	PendingToolCalls []PendingClientToolCall
}

func ValidateSubmitClientToolResults(input SubmitClientToolResultsInput) error {
	if len(input.Results) == 0 || len(input.Results) > MaxClientTools {
		return invalidRequest(fmt.Sprintf("results must contain between 1 and %d items.", MaxClientTools))
	}
	seen := make(map[string]struct{}, len(input.Results))
	for index, result := range input.Results {
		if !domain.ValidStableID(result.ToolCallID, domain.PrefixToolCall) {
			return invalidRequest(fmt.Sprintf("results[%d].tool_call_id is invalid.", index))
		}
		if _, duplicate := seen[result.ToolCallID]; duplicate {
			return invalidRequest("results must not contain duplicate tool_call_id values.")
		}
		seen[result.ToolCallID] = struct{}{}
		if err := validateToolResultContent(result.Content); err != nil {
			return invalidRequest(fmt.Sprintf("results[%d].content must be bounded valid JSON.", index))
		}
	}
	return nil
}

func (s *RuntimeService) SubmitClientToolResults(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	invocationID string,
	input SubmitClientToolResultsInput,
) (SubmitClientToolResultsResult, error) {
	if err := s.ready(); err != nil {
		return SubmitClientToolResultsResult{}, err
	}
	if err := authorize(auth, domain.OperationSubmitToolResults); err != nil {
		return SubmitClientToolResultsResult{}, err
	}
	if !domain.ValidStableID(invocationID, domain.PrefixInvocation) {
		return SubmitClientToolResultsResult{}, invalidRequest("invocation_id is invalid.")
	}
	if err := ValidateSubmitClientToolResults(input); err != nil {
		return SubmitClientToolResultsResult{}, err
	}
	observed, err := s.store.GetInvocation(ctx, invocationID)
	if errors.Is(err, ports.ErrNotFound) {
		return SubmitClientToolResultsResult{}, notFound()
	}
	if err != nil {
		return SubmitClientToolResultsResult{}, err
	}
	if err := s.authorizeInvocationScope(ctx, auth, observed); err != nil {
		return SubmitClientToolResultsResult{}, err
	}

	queued := false
	var result SubmitClientToolResultsResult
	err = s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, observed.SessionID); err != nil {
			return err
		}
		invocation, err := s.store.GetInvocationForUpdate(txCtx, invocationID)
		if err != nil {
			return err
		}
		if invocation.AccountID != auth.AccountID || invocation.SessionID != observed.SessionID {
			return notFound()
		}
		now := s.clock.Now().UTC()

		calls := make(map[string]domain.ToolCall, len(input.Results))
		lockOrder := make([]string, 0, len(input.Results))
		for _, item := range input.Results {
			lockOrder = append(lockOrder, item.ToolCallID)
		}
		sort.Strings(lockOrder)
		for _, callID := range lockOrder {
			call, err := s.store.GetToolCallForUpdate(txCtx, callID)
			if errors.Is(err, ports.ErrNotFound) {
				return notFound()
			}
			if err != nil {
				return err
			}
			if call.InvocationID != invocation.ID ||
				call.SessionID != invocation.SessionID ||
				call.AccountID != invocation.AccountID ||
				call.TenantPartitionID != invocation.TenantPartitionID ||
				call.AgentID != invocation.AgentID ||
				call.Mode != domain.ToolCallModeClient {
				return notFound()
			}
			calls[callID] = call
		}

		acceptances := make([]ClientToolResultAcceptance, len(input.Results))
		newItems := make([]ClientToolResultInput, 0, len(input.Results))
		for index, item := range input.Results {
			call := calls[item.ToolCallID]
			status := domain.ToolCallCompleted
			if item.IsError {
				status = domain.ToolCallFailed
			}
			acceptances[index] = ClientToolResultAcceptance{
				ToolCallID: item.ToolCallID,
				Status:     status,
			}
			if call.Status.Terminal() {
				if call.ResultOrigin == nil || *call.ResultOrigin != domain.ToolCallResultClient {
					if clientToolResultDeadlineExpired(invocation, now) {
						return toolResultExpired()
					}
					return invocationNotWaiting()
				}
				equal, err := equalStoredClientToolResult(txCtx, s.store, call, item)
				if err != nil {
					return err
				}
				if !equal {
					return &PublicError{
						Code:    CodeToolResultConflict,
						Message: "A different result was already accepted for this ToolCall.",
					}
				}
				acceptances[index].Status = call.Status
				acceptances[index].Deduplicated = true
				continue
			}
			if clientToolResultDeadlineExpired(invocation, now) {
				return toolResultExpired()
			}
			if invocation.Status != domain.InvocationWaiting ||
				call.Status != domain.ToolCallPending ||
				call.Iteration != invocation.CurrentIteration {
				return invocationNotWaiting()
			}
			if !call.DeadlineAt.After(now) {
				return toolResultExpired()
			}
			newItems = append(newItems, item)
		}

		var messageSequence *int64
		if len(newItems) != 0 {
			messageID, err := s.ids.NewID(domain.PrefixSessionMessage)
			if err != nil {
				return err
			}
			sequence, err := s.store.ReserveMessageSequence(txCtx, invocation.SessionID)
			if err != nil {
				return err
			}
			payload, err := clientToolResultPayload(newItems)
			if err != nil {
				return err
			}
			if err := s.store.AppendSessionMessage(txCtx, domain.SessionMessage{
				ID:                messageID,
				SessionID:         invocation.SessionID,
				AccountID:         invocation.AccountID,
				TenantPartitionID: invocation.TenantPartitionID,
				AgentID:           invocation.AgentID,
				InvocationID:      invocation.ID,
				Sequence:          sequence,
				Role:              domain.MessageRoleTool,
				Content:           payload,
				CreatedAt:         now,
			}); err != nil {
				return err
			}
			messageSequence = &sequence
			checkpointSequence := invocation.CurrentCheckpointSequence
			for _, item := range newItems {
				call := calls[item.ToolCallID]
				status := domain.ToolCallCompleted
				if item.IsError {
					status = domain.ToolCallFailed
				}
				if _, err := s.store.SettleToolCall(
					txCtx,
					call.ID,
					status,
					domain.ToolCallResultClient,
					messageID,
					sequence,
					now,
				); err != nil {
					return err
				}
				checkpointID, err := s.ids.NewID(domain.PrefixInvocationCheckpoint)
				if err != nil {
					return err
				}
				checkpointSequence++
				callID := call.ID
				if err := s.store.CreateInvocationCheckpoint(txCtx, domain.InvocationCheckpoint{
					ID:                     checkpointID,
					InvocationID:           invocation.ID,
					SessionID:              invocation.SessionID,
					AccountID:              invocation.AccountID,
					TenantPartitionID:      invocation.TenantPartitionID,
					AgentID:                invocation.AgentID,
					Sequence:               checkpointSequence,
					Iteration:              invocation.CurrentIteration,
					Kind:                   domain.InvocationCheckpointTool,
					LeaseAttempt:           invocation.LeaseAttempt,
					ThroughMessageSequence: sequence,
					ToolCallID:             &callID,
					CreatedAt:              now,
				}); err != nil {
					return err
				}
			}
			invocation, err = s.store.AdvanceWaitingInvocationCheckpoint(
				txCtx,
				invocation.ID,
				invocation.CurrentCheckpointSequence,
				checkpointSequence,
				invocation.CurrentIteration,
				now,
			)
			if err != nil {
				return err
			}
		}

		open, err := s.store.ListOpenToolCallsForUpdate(txCtx, invocation.ID)
		if err != nil {
			return err
		}
		for _, call := range open {
			if (call.Mode != domain.ToolCallModeClient && call.Mode != domain.ToolCallModeCallback) ||
				call.Iteration != invocation.CurrentIteration {
				return fmt.Errorf("waiting Invocation has an unsupported open ToolCall")
			}
		}
		if len(open) == 0 && invocation.Status == domain.InvocationWaiting {
			stateID, err := s.ids.NewID(domain.PrefixInvocationState)
			if err != nil {
				return err
			}
			revision, err := s.store.ReserveLifecycleRevision(txCtx, invocation.SessionID)
			if err != nil {
				return err
			}
			queuedInvocation, err := s.store.QueueWaitingInvocation(txCtx, invocation.ID, revision, now)
			if err != nil {
				return err
			}
			currentState, err := s.store.GetCurrentInvocationState(txCtx, invocation.ID)
			if err != nil {
				return err
			}
			through := currentState.ThroughMessageSequence
			if messageSequence != nil && (through == nil || *messageSequence > *through) {
				through = messageSequence
			}
			if err := s.store.AppendInvocationState(txCtx, lifecycleState(
				queuedInvocation,
				stateID,
				revision,
				domain.InvocationQueued,
				through,
				now,
			)); err != nil {
				return err
			}
			if s.executionMode == InvocationExecutionCloudTasks {
				dispatchID, err := s.ids.NewID(domain.PrefixExecutionDispatch)
				if err != nil {
					return err
				}
				accountID := invocation.AccountID
				partitionID := invocation.TenantPartitionID
				if err := s.store.CreateExecutionDispatch(txCtx, domain.ExecutionDispatch{
					ID:                dispatchID,
					Kind:              domain.ExecutionDispatchInvocation,
					WorkID:            invocation.ID,
					AccountID:         &accountID,
					TenantPartitionID: &partitionID,
					Queue:             s.dispatchQueue,
					Status:            domain.ExecutionDispatchPending,
					AvailableAt:       now,
					CreatedAt:         now,
					UpdatedAt:         now,
				}); err != nil {
					return err
				}
			}
			invocation = queuedInvocation
			queued = true
		}

		pending, err := s.pendingClientToolCalls(txCtx, invocation)
		if err != nil {
			return err
		}
		result = SubmitClientToolResultsResult{
			InvocationID:     invocation.ID,
			SessionID:        invocation.SessionID,
			Status:           invocation.Status,
			Results:          acceptances,
			PendingToolCalls: pending,
		}
		return nil
	})
	if err != nil {
		return SubmitClientToolResultsResult{}, err
	}
	if queued && s.executionMode == InvocationExecutionEmbedded && s.signaller != nil {
		s.signaller.Notify(ctx, ports.InvocationExecutionQueue)
	}
	deduplicated := 0
	for _, accepted := range result.Results {
		if accepted.Deduplicated {
			deduplicated++
		}
	}
	event := observability.EventClientToolResultPartial
	if result.Status == domain.InvocationQueued {
		event = observability.EventClientToolResumeQueued
	} else if deduplicated == len(result.Results) {
		event = observability.EventClientToolResultDeduplicated
	}
	s.logger.Info(
		"Client tool results accepted",
		"event",
		event,
		"invocation_id",
		result.InvocationID,
		"result_count",
		len(result.Results),
		"deduplicated_count",
		deduplicated,
		"pending_count",
		len(result.PendingToolCalls),
		"status",
		result.Status,
	)
	return result, nil
}

func (s *RuntimeService) authorizeInvocationScope(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	invocation domain.Invocation,
) error {
	if invocation.AccountID != auth.AccountID || !auth.AllowsSession(invocation.SessionID) {
		return notFound()
	}
	partition, err := s.store.GetTenantPartition(ctx, invocation.TenantPartitionID)
	if err != nil || partition.AccountID != auth.AccountID ||
		!tenantMatches(auth.TenantConstraint, partition.TenantRef) {
		if errors.Is(err, ports.ErrNotFound) || err == nil {
			return notFound()
		}
		return err
	}
	return nil
}

func (s *RuntimeService) pendingClientToolCalls(
	ctx context.Context,
	invocation domain.Invocation,
) ([]PendingClientToolCall, error) {
	if invocation.Status != domain.InvocationWaiting {
		return []PendingClientToolCall{}, nil
	}
	calls, err := s.store.ListToolCallsByInvocation(ctx, invocation.ID)
	if err != nil {
		return nil, err
	}
	pending := make([]PendingClientToolCall, 0, len(calls))
	for _, call := range calls {
		if call.Mode != domain.ToolCallModeClient || call.Status.Terminal() {
			continue
		}
		input, err := storedToolCallInput(ctx, s.store, call)
		if err != nil {
			return nil, err
		}
		pending = append(pending, PendingClientToolCall{
			ID:         call.ID,
			Name:       call.Name,
			Input:      input,
			DeadlineAt: call.DeadlineAt,
		})
	}
	return pending, nil
}

func invocationNotWaiting() error {
	return &PublicError{
		Code:    CodeInvocationNotWaiting,
		Message: "The Invocation is not waiting for this client tool result.",
	}
}

func toolResultExpired() error {
	return &PublicError{
		Code:    CodeToolResultExpired,
		Message: "The client tool result deadline has expired.",
	}
}

func clientToolResultDeadlineExpired(invocation domain.Invocation, now time.Time) bool {
	if !invocation.WallClockDeadlineAt.After(now) {
		return true
	}
	if invocation.Status != domain.InvocationFailed || len(invocation.Error) == 0 {
		return false
	}
	var failure struct {
		Code string `json:"code"`
	}
	return json.Unmarshal(invocation.Error, &failure) == nil && failure.Code == "deadline_exceeded"
}

func clientToolResultPayload(items []ClientToolResultInput) (json.RawMessage, error) {
	type block struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error,omitempty"`
	}
	blocks := make([]block, len(items))
	for index, item := range items {
		blocks[index] = block{
			Type:      "tool_result",
			ToolUseID: item.ToolCallID,
			Content:   item.Content,
			IsError:   item.IsError,
		}
	}
	return json.Marshal(blocks)
}

func equalStoredClientToolResult(
	ctx context.Context,
	store ports.SessionMessageRepository,
	call domain.ToolCall,
	want ClientToolResultInput,
) (bool, error) {
	if call.ResultMessageID == nil {
		return false, fmt.Errorf("terminal ToolCall has no result message")
	}
	messages, err := store.ListSessionMessages(ctx, call.SessionID)
	if err != nil {
		return false, err
	}
	for _, message := range messages {
		if message.ID != *call.ResultMessageID {
			continue
		}
		var blocks []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error,omitempty"`
		}
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return false, err
		}
		for _, block := range blocks {
			if block.Type != "tool_result" || block.ToolUseID != call.ID {
				continue
			}
			return block.IsError == want.IsError && jsonEqual(block.Content, want.Content), nil
		}
	}
	return false, fmt.Errorf("terminal ToolCall result block is missing")
}

func clientToolJSONDepth(raw json.RawMessage) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, err
	}
	if err := requireClientToolJSONEOF(decoder); err != nil {
		return 0, err
	}
	return clientToolValueDepth(value), nil
}

func clientToolValueDepth(value any) int {
	switch typed := value.(type) {
	case []any:
		depth := 1
		for _, item := range typed {
			depth = max(depth, 1+clientToolValueDepth(item))
		}
		return depth
	case map[string]any:
		depth := 1
		for _, item := range typed {
			depth = max(depth, 1+clientToolValueDepth(item))
		}
		return depth
	default:
		return 1
	}
}

func requireClientToolJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("unexpected trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
