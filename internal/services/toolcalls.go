package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type toolCheckpointStore interface {
	ports.SessionRepository
	ports.SessionMessageRepository
	ports.InvocationRepository
	ports.ToolCallRepository
}

// ToolCheckpointService commits model/tool boundaries under the same
// Invocation fence used by settlement. It never stores tool payloads outside
// the canonical SessionMessage transcript.
type ToolCheckpointService struct {
	store toolCheckpointStore
	txm   ports.TransactionManager
	clock ports.Clock
	ids   ports.IDGenerator
}

func NewToolCheckpointService(store toolCheckpointStore, txm ports.TransactionManager, clock ports.Clock, ids ports.IDGenerator) *ToolCheckpointService {
	return &ToolCheckpointService{
		store: store,
		txm:   txm,
		clock: clock,
		ids:   ids,
	}
}

func (s *ToolCheckpointService) RecordModelCheckpoint(ctx context.Context, claim domain.InvocationClaim, input domain.ModelCheckpointInput) (domain.ModelCheckpointResult, error) {
	if err := s.ready(); err != nil {
		return domain.ModelCheckpointResult{}, err
	}
	if input.Iteration <= 0 || input.Message.Role != domain.MessageRoleAssistant {
		return domain.ModelCheckpointResult{}, fmt.Errorf("model checkpoint iteration and assistant message are required")
	}
	if err := validateModelUsage(input.Usage); err != nil {
		return domain.ModelCheckpointResult{}, fmt.Errorf("model checkpoint usage: %w", err)
	}
	if err := validateModelProvenance(input.Provenance); err != nil {
		return domain.ModelCheckpointResult{}, fmt.Errorf("model checkpoint provenance: %w", err)
	}
	usagePayload, err := json.Marshal(input.Usage)
	if err != nil {
		return domain.ModelCheckpointResult{}, err
	}
	provenancePayload, err := json.Marshal(input.Provenance)
	if err != nil {
		return domain.ModelCheckpointResult{}, err
	}
	evidenceDigest := joinedDigest(usagePayload, provenancePayload)

	var result domain.ModelCheckpointResult
	err = s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, claim.Invocation.SessionID); err != nil {
			return err
		}
		invocation, err := s.store.GetInvocationForUpdate(txCtx, claim.Invocation.ID)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		if !claimOwns(invocation, claim, now) {
			return ports.ErrLeaseLost
		}
		if input.Iteration <= invocation.CurrentIteration {
			return s.replayModelCheckpoint(txCtx, invocation, input, evidenceDigest, &result)
		}
		if input.Iteration != invocation.CurrentIteration+1 {
			return ports.ErrToolCallConflict
		}

		messageID, err := s.ids.NewID(domain.PrefixSessionMessage)
		if err != nil {
			return err
		}
		sequence, err := s.store.ReserveMessageSequence(txCtx, invocation.SessionID)
		if err != nil {
			return err
		}
		calls, normalizedContent, err := s.prepareToolCalls(invocation, input, messageID, sequence, now)
		if err != nil {
			return err
		}
		message := domain.SessionMessage{
			ID:                messageID,
			InvocationID:      invocation.ID,
			SessionID:         invocation.SessionID,
			AccountID:         invocation.AccountID,
			TenantPartitionID: invocation.TenantPartitionID,
			AgentID:           invocation.AgentID,
			Sequence:          sequence,
			Role:              domain.MessageRoleAssistant,
			Content:           normalizedContent,
			CreatedAt:         now,
		}
		if err := s.store.AppendSessionMessage(txCtx, message); err != nil {
			return err
		}
		for _, call := range calls {
			if err := s.store.CreateToolCall(txCtx, call); err != nil {
				return err
			}
		}
		receiptID, err := s.ids.NewID(domain.PrefixModelUsageReceipt)
		if err != nil {
			return err
		}
		receipt := domain.ModelUsageReceipt{
			ID:                receiptID,
			InvocationID:      invocation.ID,
			SessionID:         invocation.SessionID,
			AccountID:         invocation.AccountID,
			TenantPartitionID: invocation.TenantPartitionID,
			AgentID:           invocation.AgentID,
			Iteration:         input.Iteration,
			MessageID:         message.ID,
			MessageSequence:   message.Sequence,
			Usage:             usagePayload,
			Provenance:        provenancePayload,
			EvidenceDigest:    evidenceDigest,
			CreatedAt:         now,
		}
		if err := s.store.CreateModelUsageReceipt(txCtx, receipt); err != nil {
			return err
		}
		checkpointID, err := s.ids.NewID(domain.PrefixInvocationCheckpoint)
		if err != nil {
			return err
		}
		checkpoint := domain.InvocationCheckpoint{
			ID:                     checkpointID,
			InvocationID:           invocation.ID,
			SessionID:              invocation.SessionID,
			AccountID:              invocation.AccountID,
			TenantPartitionID:      invocation.TenantPartitionID,
			AgentID:                invocation.AgentID,
			Sequence:               invocation.CurrentCheckpointSequence + 1,
			Iteration:              input.Iteration,
			Kind:                   domain.InvocationCheckpointModel,
			LeaseAttempt:           claim.Attempt,
			ThroughMessageSequence: sequence,
			UsageReceiptID:         &receiptID,
			CreatedAt:              now,
		}
		if err := s.store.CreateInvocationCheckpoint(txCtx, checkpoint); err != nil {
			return err
		}
		if _, err := s.store.AdvanceInvocationCheckpoint(txCtx, invocation.ID, claim.Owner, claim.Attempt, now, checkpoint.Sequence, input.Iteration); err != nil {
			return err
		}
		result = domain.ModelCheckpointResult{
			Checkpoint: checkpoint,
			Message:    message,
			ToolCalls:  calls,
			Usage:      input.Usage,
		}
		return nil
	})
	return result, err
}

func (s *ToolCheckpointService) StartBuiltinToolCall(ctx context.Context, claim domain.InvocationClaim, iteration int, providerCallID string) (domain.ToolCallExecution, error) {
	if err := s.ready(); err != nil {
		return domain.ToolCallExecution{}, err
	}
	var execution domain.ToolCallExecution
	err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, claim.Invocation.SessionID); err != nil {
			return err
		}
		invocation, err := s.store.GetInvocationForUpdate(txCtx, claim.Invocation.ID)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		if !claimOwns(invocation, claim, now) {
			return ports.ErrLeaseLost
		}
		call, err := s.store.GetToolCallByProviderIdentityForUpdate(txCtx, invocation.ID, iteration, providerCallID)
		if err != nil {
			return err
		}
		if call.Mode != domain.ToolCallModeBuiltin || call.Status != domain.ToolCallPending || !call.DeadlineAt.After(now) {
			return ports.ErrToolCallNotRunnable
		}
		call, err = s.store.StartToolCallAttempt(txCtx, call.ID, now)
		if err != nil {
			return err
		}
		attemptID, err := s.ids.NewID(domain.PrefixToolCallAttempt)
		if err != nil {
			return err
		}
		attempt := domain.ToolCallAttempt{
			ID:                     attemptID,
			ToolCallID:             call.ID,
			InvocationID:           call.InvocationID,
			SessionID:              call.SessionID,
			AccountID:              call.AccountID,
			TenantPartitionID:      call.TenantPartitionID,
			AgentID:                call.AgentID,
			Attempt:                call.CurrentAttempt,
			InvocationLeaseAttempt: claim.Attempt,
			Status:                 domain.ToolCallRunning,
			StartedAt:              now,
		}
		if err := s.store.CreateToolCallAttempt(txCtx, attempt); err != nil {
			return err
		}
		execution = domain.ToolCallExecution{
			Call:    call,
			Attempt: attempt,
		}
		return nil
	})
	return execution, err
}

func (s *ToolCheckpointService) AcceptBuiltinToolResult(ctx context.Context, claim domain.InvocationClaim, execution domain.ToolCallExecution, content json.RawMessage, isError bool) (domain.ToolCall, error) {
	if err := s.ready(); err != nil {
		return domain.ToolCall{}, err
	}
	if len(content) == 0 || !json.Valid(content) {
		return domain.ToolCall{}, fmt.Errorf("tool result content must be valid JSON")
	}
	var settled domain.ToolCall
	err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, claim.Invocation.SessionID); err != nil {
			return err
		}
		invocation, err := s.store.GetInvocationForUpdate(txCtx, claim.Invocation.ID)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		if !claimOwns(invocation, claim, now) {
			return ports.ErrLeaseLost
		}
		call, err := s.store.GetToolCallForUpdate(txCtx, execution.Call.ID)
		if err != nil {
			return err
		}
		if call.InvocationID != invocation.ID || call.Mode != domain.ToolCallModeBuiltin {
			return ports.ErrToolCallConflict
		}
		payload, err := toolResultPayload(call.ID, content, isError)
		if err != nil {
			return err
		}
		if call.Status.Terminal() {
			if equal, err := s.equalStoredToolResult(txCtx, call, payload); err != nil {
				return err
			} else if !equal {
				return ports.ErrToolCallConflict
			}
			settled = call
			return nil
		}
		if call.Status != domain.ToolCallRunning || call.CurrentAttempt != execution.Attempt.Attempt ||
			execution.Attempt.InvocationLeaseAttempt != claim.Attempt || !call.DeadlineAt.After(now) {
			return ports.ErrToolCallNotRunnable
		}
		messageID, err := s.ids.NewID(domain.PrefixSessionMessage)
		if err != nil {
			return err
		}
		sequence, err := s.store.ReserveMessageSequence(txCtx, invocation.SessionID)
		if err != nil {
			return err
		}
		if err := s.store.AppendSessionMessage(txCtx, domain.SessionMessage{
			ID:                messageID,
			InvocationID:      invocation.ID,
			SessionID:         invocation.SessionID,
			AccountID:         invocation.AccountID,
			TenantPartitionID: invocation.TenantPartitionID,
			AgentID:           invocation.AgentID,
			Sequence:          sequence,
			Role:              domain.MessageRoleTool,
			Content:           payload,
			CreatedAt:         now,
		}); err != nil {
			return err
		}
		status := domain.ToolCallCompleted
		if isError {
			status = domain.ToolCallFailed
		}
		settled, err = s.store.SettleToolCall(txCtx, call.ID, status, messageID, sequence, now)
		if err != nil {
			return err
		}
		if _, err := s.store.SettleToolCallAttempt(txCtx, execution.Attempt.ID, status, now); err != nil {
			return err
		}
		checkpointID, err := s.ids.NewID(domain.PrefixInvocationCheckpoint)
		if err != nil {
			return err
		}
		toolCallID := call.ID
		checkpoint := domain.InvocationCheckpoint{
			ID:                     checkpointID,
			InvocationID:           invocation.ID,
			SessionID:              invocation.SessionID,
			AccountID:              invocation.AccountID,
			TenantPartitionID:      invocation.TenantPartitionID,
			AgentID:                invocation.AgentID,
			Sequence:               invocation.CurrentCheckpointSequence + 1,
			Iteration:              call.Iteration,
			Kind:                   domain.InvocationCheckpointTool,
			LeaseAttempt:           claim.Attempt,
			ThroughMessageSequence: sequence,
			ToolCallID:             &toolCallID,
			CreatedAt:              now,
		}
		if err := s.store.CreateInvocationCheckpoint(txCtx, checkpoint); err != nil {
			return err
		}
		_, err = s.store.AdvanceInvocationCheckpoint(txCtx, invocation.ID, claim.Owner, claim.Attempt, now, checkpoint.Sequence, invocation.CurrentIteration)
		return err
	})
	return settled, err
}

func (s *ToolCheckpointService) prepareToolCalls(invocation domain.Invocation, input domain.ModelCheckpointInput, messageID string, sequence int64, now time.Time) ([]domain.ToolCall, json.RawMessage, error) {
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(input.Message.Content, &blocks); err != nil || len(blocks) == 0 {
		return nil, nil, fmt.Errorf("model checkpoint content is invalid")
	}
	byProvider := make(map[string]domain.ToolCallRequest, len(input.ToolCalls))
	for _, requested := range input.ToolCalls {
		if requested.ProviderCallID == "" || requested.Name == "" || requested.Mode == "" || !json.Valid(requested.Input) {
			return nil, nil, fmt.Errorf("tool call request is invalid")
		}
		if _, exists := byProvider[requested.ProviderCallID]; exists {
			return nil, nil, ports.ErrToolCallConflict
		}
		byProvider[requested.ProviderCallID] = requested
	}
	deadline := invocation.WallClockDeadlineAt
	if invocation.ExecutionDeadlineAt != nil && invocation.ExecutionDeadlineAt.Before(deadline) {
		deadline = *invocation.ExecutionDeadlineAt
	}
	calls := make([]domain.ToolCall, 0, len(input.ToolCalls))
	ordinal := 0
	for _, block := range blocks {
		var kind string
		_ = json.Unmarshal(block["type"], &kind)
		if kind != "tool_use" {
			continue
		}
		var providerID, name string
		var rawInput json.RawMessage
		if json.Unmarshal(block["id"], &providerID) != nil || json.Unmarshal(block["name"], &name) != nil ||
			json.Unmarshal(block["input"], &rawInput) != nil {
			return nil, nil, fmt.Errorf("model tool_use block is invalid")
		}
		requested, ok := byProvider[providerID]
		if !ok || requested.Name != name || !jsonEqual(requested.Input, rawInput) {
			return nil, nil, ports.ErrToolCallConflict
		}
		callID, err := s.ids.NewID(domain.PrefixToolCall)
		if err != nil {
			return nil, nil, err
		}
		digest, err := toolRequestDigest(name, requested.Mode, rawInput)
		if err != nil {
			return nil, nil, err
		}
		call := domain.ToolCall{
			ID:                     callID,
			InvocationID:           invocation.ID,
			SessionID:              invocation.SessionID,
			AccountID:              invocation.AccountID,
			TenantPartitionID:      invocation.TenantPartitionID,
			AgentID:                invocation.AgentID,
			Iteration:              input.Iteration,
			BatchOrdinal:           ordinal,
			ProviderCallID:         providerID,
			Name:                   name,
			Mode:                   requested.Mode,
			RequestMessageID:       messageID,
			RequestMessageSequence: sequence,
			RequestDigest:          digest,
			Status:                 domain.ToolCallPending,
			DeadlineAt:             deadline,
			CreatedAt:              now,
			UpdatedAt:              now,
		}
		calls = append(calls, call)
		encodedID, _ := json.Marshal(callID)
		block["id"] = encodedID
		delete(byProvider, providerID)
		ordinal++
	}
	if len(byProvider) != 0 || len(calls) != len(input.ToolCalls) {
		return nil, nil, ports.ErrToolCallConflict
	}
	normalized, err := json.Marshal(blocks)
	return calls, normalized, err
}

func (s *ToolCheckpointService) replayModelCheckpoint(ctx context.Context, invocation domain.Invocation, input domain.ModelCheckpointInput, digest []byte, result *domain.ModelCheckpointResult) error {
	receipt, err := s.store.GetModelUsageReceiptByIteration(ctx, invocation.ID, input.Iteration)
	if err != nil || !bytes.Equal(receipt.EvidenceDigest, digest) {
		return ports.ErrToolCallConflict
	}
	calls, err := s.store.ListToolCallsByIteration(ctx, invocation.ID, input.Iteration)
	if err != nil {
		return err
	}
	checkpoints, err := s.store.ListInvocationCheckpoints(ctx, invocation.ID)
	if err != nil {
		return err
	}
	messages, err := s.store.ListSessionMessages(ctx, invocation.SessionID)
	if err != nil {
		return err
	}
	var checkpoint domain.InvocationCheckpoint
	for _, item := range checkpoints {
		if item.Kind == domain.InvocationCheckpointModel && item.Iteration == input.Iteration {
			checkpoint = item
			break
		}
	}
	var message domain.SessionMessage
	for _, item := range messages {
		if item.ID == receipt.MessageID {
			message = item
			break
		}
	}
	if checkpoint.ID == "" || message.ID == "" {
		return ports.ErrToolCallConflict
	}
	normalized, err := normalizeReplayedModelContent(input.Message.Content, input.ToolCalls, calls)
	if err != nil || !jsonEqual(normalized, message.Content) {
		return ports.ErrToolCallConflict
	}
	result.Checkpoint, result.Message, result.ToolCalls, result.Usage = checkpoint, message, calls, input.Usage
	return nil
}

func normalizeReplayedModelContent(content json.RawMessage, requested []domain.ToolCallRequest, calls []domain.ToolCall) (json.RawMessage, error) {
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, err
	}
	byProvider := make(map[string]domain.ToolCall, len(calls))
	for _, call := range calls {
		byProvider[call.ProviderCallID] = call
	}
	requestByProvider := make(map[string]domain.ToolCallRequest, len(requested))
	for _, request := range requested {
		requestByProvider[request.ProviderCallID] = request
	}
	seen := 0
	for _, block := range blocks {
		var kind string
		_ = json.Unmarshal(block["type"], &kind)
		if kind != "tool_use" {
			continue
		}
		var providerID, name string
		var input json.RawMessage
		if json.Unmarshal(block["id"], &providerID) != nil || json.Unmarshal(block["name"], &name) != nil || json.Unmarshal(block["input"], &input) != nil {
			return nil, ports.ErrToolCallConflict
		}
		call, ok := byProvider[providerID]
		request, requestedOK := requestByProvider[providerID]
		if !ok || !requestedOK || name != call.Name || request.Name != call.Name || request.Mode != call.Mode || !jsonEqual(request.Input, input) {
			return nil, ports.ErrToolCallConflict
		}
		digest, err := toolRequestDigest(name, request.Mode, input)
		if err != nil || !bytes.Equal(digest, call.RequestDigest) {
			return nil, ports.ErrToolCallConflict
		}
		stableID, _ := json.Marshal(call.ID)
		block["id"] = stableID
		seen++
	}
	if seen != len(calls) || seen != len(requested) {
		return nil, ports.ErrToolCallConflict
	}
	return json.Marshal(blocks)
}

func (s *ToolCheckpointService) equalStoredToolResult(ctx context.Context, call domain.ToolCall, expected json.RawMessage) (bool, error) {
	if call.ResultMessageID == nil {
		return false, nil
	}
	messages, err := s.store.ListSessionMessages(ctx, call.SessionID)
	if err != nil {
		return false, err
	}
	for _, message := range messages {
		if message.ID == *call.ResultMessageID {
			return jsonEqual(message.Content, expected), nil
		}
	}
	return false, nil
}

func (s *ToolCheckpointService) ready() error {
	if s == nil || s.store == nil || s.txm == nil || s.clock == nil || s.ids == nil {
		return fmt.Errorf("tool checkpoint service is not configured")
	}
	return nil
}

func toolRequestDigest(name string, mode domain.ToolCallMode, input json.RawMessage) ([]byte, error) {
	canonical, err := canonicalJSON(input)
	if err != nil {
		return nil, err
	}
	return joinedDigest([]byte(name), []byte(mode), canonical), nil
}

func joinedDigest(parts ...[]byte) []byte {
	hash := sha256.New()
	var size [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(part)
	}
	return hash.Sum(nil)
}

func jsonEqual(left, right []byte) bool {
	canonicalLeft, leftErr := canonicalJSON(left)
	canonicalRight, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(canonicalLeft, canonicalRight)
}

func canonicalJSON(payload []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := writeCanonicalJSON(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeCanonicalJSON(output *bytes.Buffer, value any) error {
	switch current := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if current {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case string:
		encoded, err := json.Marshal(current)
		if err != nil {
			return err
		}
		output.Write(encoded)
	case json.Number:
		canonical, err := canonicalJSONNumber(current.String())
		if err != nil {
			return err
		}
		output.WriteString(canonical)
	case []any:
		output.WriteByte('[')
		for index, item := range current {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonicalJSON(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return err
			}
			output.Write(encodedKey)
			output.WriteByte(':')
			if err := writeCanonicalJSON(output, current[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON value %T", value)
	}
	return nil
}

func canonicalJSONNumber(raw string) (string, error) {
	negative := strings.HasPrefix(raw, "-")
	if negative {
		raw = strings.TrimPrefix(raw, "-")
	}
	exponent := new(big.Int)
	if separator := strings.IndexAny(raw, "eE"); separator >= 0 {
		exponentText := raw[separator+1:]
		exponentText = strings.TrimPrefix(exponentText, "+")
		if _, ok := exponent.SetString(exponentText, 10); !ok {
			return "", fmt.Errorf("invalid JSON number exponent")
		}
		raw = raw[:separator]
	}
	integerPart := raw
	fractionalPart := ""
	if separator := strings.IndexByte(raw, '.'); separator >= 0 {
		integerPart = raw[:separator]
		fractionalPart = raw[separator+1:]
	}
	digits := strings.TrimLeft(integerPart+fractionalPart, "0")
	if digits == "" {
		return "0", nil
	}
	exponent.Sub(exponent, big.NewInt(int64(len(fractionalPart))))
	for strings.HasSuffix(digits, "0") {
		digits = strings.TrimSuffix(digits, "0")
		exponent.Add(exponent, big.NewInt(1))
	}
	scientificExponent := new(big.Int).Add(exponent, big.NewInt(int64(len(digits)-1)))
	coefficient := digits[:1]
	if len(digits) > 1 {
		coefficient += "." + digits[1:]
	}
	if scientificExponent.Sign() != 0 {
		coefficient += "e" + scientificExponent.String()
	}
	if negative {
		coefficient = "-" + coefficient
	}
	return coefficient, nil
}

func toolResultPayload(toolCallID string, content json.RawMessage, isError bool) (json.RawMessage, error) {
	payload, err := json.Marshal([]struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error,omitempty"`
	}{{
		Type:      "tool_result",
		ToolUseID: toolCallID,
		Content:   content,
		IsError:   isError,
	}})
	return payload, err
}

func syntheticToolResultPayload(calls []domain.ToolCall, reason string) (json.RawMessage, error) {
	type block struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		Content   string `json:"content"`
		IsError   bool   `json:"is_error"`
	}
	if strings.TrimSpace(reason) == "" {
		reason = "Tool execution stopped because the Invocation ended."
	}
	reasonRunes := []rune(reason)
	if len(reasonRunes) > 512 {
		reason = string(reasonRunes[:512])
	}
	blocks := make([]block, len(calls))
	for index, call := range calls {
		blocks[index] = block{
			Type:      "tool_result",
			ToolUseID: call.ID,
			Content:   reason,
			IsError:   true,
		}
	}
	return json.Marshal(blocks)
}

var _ ports.ToolCallCoordinator = (*ToolCheckpointService)(nil)

type terminalToolStore interface {
	ports.SessionRepository
	ports.SessionMessageRepository
	ports.ToolCallRepository
}

// closeOpenToolCallsForTerminal runs only after the Session and Invocation are
// locked by the terminal writer. That writer is authoritative even when an
// execution lease has expired; stale executors remain fenced by the Invocation
// transition that follows in the same transaction.
func closeOpenToolCallsForTerminal(
	ctx context.Context,
	store any,
	ids ports.IDGenerator,
	invocation domain.Invocation,
	terminalStatus domain.InvocationStatus,
	reason string,
	now time.Time,
) (*int64, error) {
	durable, ok := store.(terminalToolStore)
	if !ok {
		return nil, nil
	}
	var watermark *int64
	latest, err := durable.GetLatestInvocationCheckpoint(ctx, invocation.ID)
	if err == nil {
		sequence := latest.ThroughMessageSequence
		watermark = &sequence
	} else if !errors.Is(err, ports.ErrNotFound) {
		return nil, err
	}
	open, err := durable.ListOpenToolCallsForUpdate(ctx, invocation.ID)
	if err != nil || len(open) == 0 {
		return watermark, err
	}
	if invocation.LeaseAttempt <= 0 {
		return nil, fmt.Errorf("open ToolCall has no Invocation lease attempt")
	}
	payload, err := syntheticToolResultPayload(open, reason)
	if err != nil {
		return nil, err
	}
	messageID, err := ids.NewID(domain.PrefixSessionMessage)
	if err != nil {
		return nil, err
	}
	messageSequence, err := durable.ReserveMessageSequence(ctx, invocation.SessionID)
	if err != nil {
		return nil, err
	}
	if err := durable.AppendSessionMessage(ctx, domain.SessionMessage{
		ID:                messageID,
		InvocationID:      invocation.ID,
		SessionID:         invocation.SessionID,
		AccountID:         invocation.AccountID,
		TenantPartitionID: invocation.TenantPartitionID,
		AgentID:           invocation.AgentID,
		Sequence:          messageSequence,
		Role:              domain.MessageRoleTool,
		Content:           payload,
		CreatedAt:         now,
	}); err != nil {
		return nil, err
	}
	callStatus := domain.ToolCallFailed
	if terminalStatus == domain.InvocationCancelled {
		callStatus = domain.ToolCallCancelled
	}
	checkpointSequence := invocation.CurrentCheckpointSequence
	iteration := invocation.CurrentIteration
	for _, call := range open {
		if _, err := durable.SettleToolCall(ctx, call.ID, callStatus, messageID, messageSequence, now); err != nil {
			return nil, err
		}
		if _, err := durable.SettleRunningToolCallAttempts(ctx, call.ID, callStatus, now); err != nil {
			return nil, err
		}
		checkpointID, err := ids.NewID(domain.PrefixInvocationCheckpoint)
		if err != nil {
			return nil, err
		}
		checkpointSequence++
		if call.Iteration > iteration {
			iteration = call.Iteration
		}
		callID := call.ID
		if err := durable.CreateInvocationCheckpoint(ctx, domain.InvocationCheckpoint{
			ID:                     checkpointID,
			InvocationID:           invocation.ID,
			SessionID:              invocation.SessionID,
			AccountID:              invocation.AccountID,
			TenantPartitionID:      invocation.TenantPartitionID,
			AgentID:                invocation.AgentID,
			Sequence:               checkpointSequence,
			Iteration:              call.Iteration,
			Kind:                   domain.InvocationCheckpointTool,
			LeaseAttempt:           invocation.LeaseAttempt,
			ThroughMessageSequence: messageSequence,
			ToolCallID:             &callID,
			CreatedAt:              now,
		}); err != nil {
			return nil, err
		}
	}
	if _, err := durable.AdvanceInvocationCheckpointForTerminal(ctx, invocation.ID, checkpointSequence, iteration); err != nil {
		return nil, err
	}
	return &messageSequence, nil
}

func validateUsageProjection(ctx context.Context, store any, invocationID string, result domain.InvocationExecutionResult) error {
	if result.Usage == nil {
		return nil
	}
	durable, ok := store.(ports.ToolCallRepository)
	if !ok {
		return nil
	}
	receipts, err := durable.ListModelUsageReceipts(ctx, invocationID)
	if err != nil {
		return err
	}
	if len(receipts) == 0 {
		if result.MessagesCheckpointed {
			return fmt.Errorf("checkpointed result has no model usage receipts")
		}
		return nil
	}
	var aggregate domain.ModelUsage
	for _, receipt := range receipts {
		var usage domain.ModelUsage
		if err := json.Unmarshal(receipt.Usage, &usage); err != nil {
			return fmt.Errorf("decode model usage receipt: %w", err)
		}
		addModelUsage(&aggregate, usage)
	}
	if !modelUsageProjectionEqual(aggregate, *result.Usage) {
		return fmt.Errorf("terminal usage projection does not equal accepted model usage receipts")
	}
	return nil
}

func addModelUsage(total *domain.ModelUsage, usage domain.ModelUsage) {
	total.InputTokens += usage.InputTokens
	total.OutputTokens += usage.OutputTokens
	total.CacheCreationInputTokens += usage.CacheCreationInputTokens
	total.CacheReadInputTokens += usage.CacheReadInputTokens
	total.ReasoningTokens += usage.ReasoningTokens
	total.Iterations += usage.Iterations
	if usage.EstimatedCost != nil {
		if total.EstimatedCost == nil {
			copy := *usage.EstimatedCost
			total.EstimatedCost = &copy
			return
		}
		total.EstimatedCost.Input += usage.EstimatedCost.Input
		total.EstimatedCost.Output += usage.EstimatedCost.Output
		total.EstimatedCost.CacheRead += usage.EstimatedCost.CacheRead
		total.EstimatedCost.CacheWrite += usage.EstimatedCost.CacheWrite
		total.EstimatedCost.Total += usage.EstimatedCost.Total
	}
}

func modelUsageProjectionEqual(left, right domain.ModelUsage) bool {
	if left.InputTokens != right.InputTokens || left.OutputTokens != right.OutputTokens ||
		left.CacheCreationInputTokens != right.CacheCreationInputTokens ||
		left.CacheReadInputTokens != right.CacheReadInputTokens ||
		left.ReasoningTokens != right.ReasoningTokens || left.Iterations != right.Iterations ||
		(left.EstimatedCost == nil) != (right.EstimatedCost == nil) {
		return false
	}
	if left.EstimatedCost == nil {
		return true
	}
	closeEnough := func(a, b float64) bool { return math.Abs(a-b) <= 1e-9 }
	return closeEnough(left.EstimatedCost.Input, right.EstimatedCost.Input) &&
		closeEnough(left.EstimatedCost.Output, right.EstimatedCost.Output) &&
		closeEnough(left.EstimatedCost.CacheRead, right.EstimatedCost.CacheRead) &&
		closeEnough(left.EstimatedCost.CacheWrite, right.EstimatedCost.CacheWrite) &&
		closeEnough(left.EstimatedCost.Total, right.EstimatedCost.Total)
}
