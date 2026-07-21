package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

var errCallbackRequestInvalid = errors.New("callback request evidence is invalid")

type CallbackDeliveryConfig struct {
	LeaseDuration time.Duration
	RetryBase     time.Duration
	RetryMaximum  time.Duration
	MaxAttempts   int
	Retention     time.Duration
	BatchLimit    int
	ExecutionMode InvocationExecutionMode
	DispatchQueue string
}

func DefaultCallbackDeliveryConfig() CallbackDeliveryConfig {
	return CallbackDeliveryConfig{
		LeaseDuration: 30 * time.Second,
		RetryBase:     time.Second,
		RetryMaximum:  time.Minute,
		MaxAttempts:   5,
		Retention:     7 * 24 * time.Hour,
		BatchLimit:    100,
		ExecutionMode: InvocationExecutionEmbedded,
	}
}

func ValidateCallbackDeliveryConfig(config CallbackDeliveryConfig) error {
	if config.LeaseDuration <= 0 || config.RetryBase <= 0 || config.RetryMaximum < config.RetryBase {
		return fmt.Errorf("callback lease and retry durations are invalid")
	}
	if config.MaxAttempts < 1 || config.MaxAttempts > 20 {
		return fmt.Errorf("callback max attempts must be between 1 and 20")
	}
	if config.Retention <= 0 {
		return fmt.Errorf("callback retention must be positive")
	}
	if config.BatchLimit < 1 || config.BatchLimit > 1000 {
		return fmt.Errorf("callback batch limit must be from 1 through 1000")
	}
	if config.ExecutionMode != InvocationExecutionEmbedded && config.ExecutionMode != InvocationExecutionCloudTasks {
		return fmt.Errorf("callback Invocation execution mode is invalid")
	}
	if config.ExecutionMode == InvocationExecutionCloudTasks && strings.TrimSpace(config.DispatchQueue) == "" {
		return fmt.Errorf("callback dispatch queue is required in cloud_tasks mode")
	}
	return nil
}

type callbackDeliveryStore interface {
	ports.CallbackDeliveryRepository
	ports.TenantPartitionRepository
	ports.SessionRepository
	ports.SessionMessageRepository
	ports.InvocationRepository
	ports.InvocationStateRepository
	ports.ToolCallRepository
	ports.ExecutionDispatchRepository
	GetAgentByID(context.Context, string) (domain.Agent, error)
}

type CallbackDeliveryService struct {
	store     callbackDeliveryStore
	txm       ports.TransactionManager
	clock     ports.Clock
	ids       ports.IDGenerator
	signaller ports.WorkSignaller
	config    CallbackDeliveryConfig
	logger    *slog.Logger
}

func NewCallbackDeliveryService(
	store callbackDeliveryStore,
	txm ports.TransactionManager,
	clock ports.Clock,
	ids ports.IDGenerator,
	signaller ports.WorkSignaller,
	config CallbackDeliveryConfig,
	logger *slog.Logger,
) (*CallbackDeliveryService, error) {
	if store == nil || txm == nil || clock == nil || ids == nil {
		return nil, fmt.Errorf("callback delivery dependencies are required")
	}
	if err := ValidateCallbackDeliveryConfig(config); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &CallbackDeliveryService{
		store:     store,
		txm:       txm,
		clock:     clock,
		ids:       ids,
		signaller: signaller,
		config:    config,
		logger:    logger,
	}, nil
}

func (s *CallbackDeliveryService) ClaimNext(
	ctx context.Context,
	owner string,
) (domain.CallbackDeliveryClaim, bool, error) {
	if strings.TrimSpace(owner) == "" || len(owner) > MaxExecutionOwnerCharacters {
		return domain.CallbackDeliveryClaim{}, false, fmt.Errorf("callback delivery owner is invalid")
	}
	now := s.clock.Now().UTC()
	delivery, err := s.store.ClaimNextCallbackDelivery(
		ctx,
		owner,
		now,
		now.Add(s.config.LeaseDuration),
	)
	if errors.Is(err, ports.ErrNotFound) {
		return domain.CallbackDeliveryClaim{}, false, nil
	}
	if err != nil {
		return domain.CallbackDeliveryClaim{}, false, err
	}
	claim := domain.CallbackDeliveryClaim{
		Delivery: delivery,
		Owner:    owner,
		Attempt:  delivery.Attempt,
	}
	s.logger.Info(
		"Callback delivery claimed",
		"event",
		"callback_delivery_claimed",
		"delivery_id",
		delivery.ID,
		"tool_call_id",
		delivery.ToolCallID,
		"attempt",
		delivery.Attempt,
	)
	return claim, true, nil
}

func (s *CallbackDeliveryService) ProcessClaim(
	ctx context.Context,
	transport ports.CallbackTransport,
	claim domain.CallbackDeliveryClaim,
) error {
	if transport == nil {
		return fmt.Errorf("callback transport is required")
	}
	if claim.Delivery.Status != domain.CallbackDeliveryDelivering ||
		claim.Delivery.Owner == nil || *claim.Delivery.Owner != claim.Owner ||
		claim.Delivery.LeaseExpiresAt == nil ||
		!claim.Delivery.LeaseExpiresAt.After(s.clock.Now().UTC()) {
		s.logStaleClaim(claim, "lease_expired_or_invalid")
		return ports.ErrCallbackDeliveryLeaseLost
	}
	call, err := s.store.GetToolCall(ctx, claim.Delivery.ToolCallID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return s.abandonClaim(ctx, claim, "tool_call_missing")
		}
		return err
	}
	invocation, err := s.store.GetInvocation(ctx, claim.Delivery.InvocationID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return s.abandonClaim(ctx, claim, "invocation_missing")
		}
		return err
	}
	if !callbackClaimMatches(claim, call, invocation) {
		return s.abandonClaim(ctx, claim, "scope_mismatch")
	}
	if invocation.Status.Terminal() || call.Status.Terminal() {
		return s.abandonClaim(ctx, claim, "work_terminal")
	}
	if invocation.Status != domain.InvocationWaiting {
		return s.retryClaim(ctx, claim, call, "invocation_not_waiting")
	}
	if claim.Attempt > int64(s.config.MaxAttempts) {
		return s.settleClaim(
			ctx,
			claim,
			callbackFailureContent(),
			true,
			domain.CallbackDeliveryFailed,
			"attempts_exhausted",
			nil,
		)
	}
	now := s.clock.Now().UTC()
	if !call.DeadlineAt.After(now) || !invocation.WallClockDeadlineAt.After(now) {
		return s.abandonClaim(ctx, claim, "deadline_exceeded")
	}
	body, err := s.callbackRequestBody(ctx, claim, call, invocation)
	if err != nil {
		if !errors.Is(err, errCallbackRequestInvalid) {
			return err
		}
		return s.settleClaim(
			ctx,
			claim,
			callbackFailureContent(),
			true,
			domain.CallbackDeliveryFailed,
			"request_invalid",
			nil,
		)
	}
	sendAt := s.clock.Now().UTC()
	if !claim.Delivery.LeaseExpiresAt.After(sendAt) {
		s.logStaleClaim(claim, "lease_expired_before_send")
		return ports.ErrCallbackDeliveryLeaseLost
	}
	if !call.DeadlineAt.After(sendAt) || !invocation.WallClockDeadlineAt.After(sendAt) {
		return s.abandonClaim(ctx, claim, "deadline_exceeded")
	}
	result := transport.Send(ctx, ports.CallbackTransportRequest{
		EndpointURL: claim.Delivery.EndpointURL,
		DeliveryID:  claim.Delivery.ID,
		ToolCallID:  claim.Delivery.ToolCallID,
		Body:        body,
	})
	if result.ErrorCode != "" {
		if result.Retryable && claim.Attempt < int64(s.config.MaxAttempts) {
			return s.retryClaim(ctx, claim, call, result.ErrorCode)
		}
		return s.settleClaim(
			ctx,
			claim,
			callbackFailureContent(),
			true,
			domain.CallbackDeliveryFailed,
			result.ErrorCode,
			optionalStatus(result.StatusCode),
		)
	}
	content, isError, err := decodeCallbackResponse(result.ContentType, result.Body)
	if err != nil {
		return s.settleClaim(
			ctx,
			claim,
			callbackFailureContent(),
			true,
			domain.CallbackDeliveryFailed,
			"response_invalid",
			optionalStatus(result.StatusCode),
		)
	}
	return s.settleClaim(
		ctx,
		claim,
		content,
		isError,
		domain.CallbackDeliverySucceeded,
		"",
		optionalStatus(result.StatusCode),
	)
}

func (s *CallbackDeliveryService) RecoverExpired(ctx context.Context) (int64, error) {
	return s.store.RecoverExpiredCallbackDeliveries(
		ctx,
		s.clock.Now().UTC(),
		s.config.BatchLimit,
	)
}

func (s *CallbackDeliveryService) Prune(ctx context.Context) (int64, error) {
	return s.store.PruneTerminalCallbackDeliveries(
		ctx,
		s.clock.Now().UTC().Add(-s.config.Retention),
		s.config.BatchLimit,
	)
}

type callbackRequestEnvelope struct {
	Nvoken callbackRequestContext `json:"nvoken"`
	Input  json.RawMessage        `json:"input"`
}

type callbackRequestContext struct {
	SchemaVersion int                   `json:"schema_version"`
	DeliveryID    string                `json:"delivery_id"`
	ToolCallID    string                `json:"tool_call_id"`
	InvocationID  string                `json:"invocation_id"`
	SessionID     string                `json:"session_id"`
	AgentRef      string                `json:"agent_ref"`
	TenantRef     *string               `json:"tenant_ref,omitempty"`
	Actor         *callbackRequestActor `json:"actor,omitempty"`
}

// callbackRequestActor reserves the v1 wire shape without claiming that
// nvoken owns delegated actor identity yet. It stays absent until admission
// persists an authenticated actor claim.
type callbackRequestActor struct {
	PrincipalID   string `json:"principal_id"`
	PrincipalType string `json:"principal_type"`
}

func (s *CallbackDeliveryService) callbackRequestBody(
	ctx context.Context,
	claim domain.CallbackDeliveryClaim,
	call domain.ToolCall,
	invocation domain.Invocation,
) (json.RawMessage, error) {
	agent, err := s.store.GetAgentByID(ctx, invocation.AgentID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil, errCallbackRequestInvalid
		}
		return nil, err
	}
	if agent.AccountID != invocation.AccountID {
		return nil, errCallbackRequestInvalid
	}
	partition, err := s.store.GetTenantPartition(ctx, invocation.TenantPartitionID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil, errCallbackRequestInvalid
		}
		return nil, err
	}
	if partition.AccountID != invocation.AccountID {
		return nil, errCallbackRequestInvalid
	}
	input, err := storedCallbackToolInput(ctx, s.store, call)
	if err != nil {
		return nil, err
	}
	return json.Marshal(callbackRequestEnvelope{
		Nvoken: callbackRequestContext{
			SchemaVersion: 1,
			DeliveryID:    claim.Delivery.ID,
			ToolCallID:    call.ID,
			InvocationID:  invocation.ID,
			SessionID:     invocation.SessionID,
			AgentRef:      agent.AgentRef,
			TenantRef:     partition.TenantRef,
		},
		Input: input,
	})
}

func storedCallbackToolInput(
	ctx context.Context,
	store ports.SessionMessageRepository,
	call domain.ToolCall,
) (json.RawMessage, error) {
	messages, err := store.ListSessionMessages(ctx, call.SessionID)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		if message.ID != call.RequestMessageID {
			continue
		}
		if message.InvocationID != call.InvocationID || message.SessionID != call.SessionID ||
			message.AccountID != call.AccountID ||
			message.TenantPartitionID != call.TenantPartitionID ||
			message.AgentID != call.AgentID || message.Role != domain.MessageRoleAssistant {
			return nil, errCallbackRequestInvalid
		}
		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return nil, errCallbackRequestInvalid
		}
		for _, block := range blocks {
			var kind string
			var id string
			var name string
			if json.Unmarshal(block["type"], &kind) != nil || kind != "tool_use" ||
				json.Unmarshal(block["id"], &id) != nil || id != call.ID ||
				json.Unmarshal(block["name"], &name) != nil || name != call.Name {
				continue
			}
			var input json.RawMessage
			if err := json.Unmarshal(block["input"], &input); err != nil {
				return nil, errCallbackRequestInvalid
			}
			digest, err := toolRequestDigest(call.Name, call.Mode, input)
			if err != nil || !bytes.Equal(digest, call.RequestDigest) {
				return nil, errCallbackRequestInvalid
			}
			return input, nil
		}
		return nil, errCallbackRequestInvalid
	}
	return nil, errCallbackRequestInvalid
}

func (s *CallbackDeliveryService) retryClaim(
	ctx context.Context,
	claim domain.CallbackDeliveryClaim,
	call domain.ToolCall,
	errorCode string,
) error {
	now := s.clock.Now().UTC()
	delay := s.retryDelay(claim.Attempt)
	availableAt := now.Add(delay)
	if claim.Attempt >= int64(s.config.MaxAttempts) || !call.DeadlineAt.After(availableAt) {
		return s.settleClaim(
			ctx,
			claim,
			callbackFailureContent(),
			true,
			domain.CallbackDeliveryFailed,
			errorCode,
			nil,
		)
	}
	_, err := s.store.ReturnCallbackDeliveryPending(
		ctx,
		claim.Delivery.ID,
		claim.Owner,
		claim.Attempt,
		availableAt,
		errorCode,
		now,
	)
	if err == nil {
		s.logger.Warn(
			"Callback delivery scheduled for retry",
			"event",
			"callback_delivery_retry",
			"delivery_id",
			claim.Delivery.ID,
			"tool_call_id",
			claim.Delivery.ToolCallID,
			"attempt",
			claim.Attempt,
			"reason_code",
			errorCode,
		)
	} else if errors.Is(err, ports.ErrCallbackDeliveryLeaseLost) {
		s.logStaleClaim(claim, "lease_lost")
	}
	return err
}

func (s *CallbackDeliveryService) abandonClaim(
	ctx context.Context,
	claim domain.CallbackDeliveryClaim,
	errorCode string,
) error {
	_, err := s.store.SettleCallbackDelivery(
		ctx,
		claim.Delivery.ID,
		claim.Owner,
		claim.Attempt,
		domain.CallbackDeliveryAbandoned,
		errorCode,
		nil,
		s.clock.Now().UTC(),
	)
	if errors.Is(err, ports.ErrCallbackDeliveryLeaseLost) {
		s.logStaleClaim(claim, "lease_lost")
		return nil
	}
	if err == nil {
		s.logger.Info(
			"Callback delivery abandoned",
			"event",
			"callback_delivery_abandoned",
			"delivery_id",
			claim.Delivery.ID,
			"tool_call_id",
			claim.Delivery.ToolCallID,
			"attempt",
			claim.Attempt,
			"reason_code",
			errorCode,
		)
	}
	return err
}

func (s *CallbackDeliveryService) settleClaim(
	ctx context.Context,
	claim domain.CallbackDeliveryClaim,
	content json.RawMessage,
	isError bool,
	deliveryStatus domain.CallbackDeliveryStatus,
	errorCode string,
	responseStatus *int,
) error {
	queued := false
	settled := false
	settledStatus := deliveryStatus
	settledReason := errorCode
	err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, claim.Delivery.SessionID); err != nil {
			return err
		}
		invocation, err := s.store.GetInvocationForUpdate(txCtx, claim.Delivery.InvocationID)
		if err != nil {
			return err
		}
		call, err := s.store.GetToolCallForUpdate(txCtx, claim.Delivery.ToolCallID)
		if err != nil {
			return err
		}
		delivery, err := s.store.GetCallbackDeliveryForUpdate(txCtx, claim.Delivery.ID)
		if err != nil {
			return err
		}
		if !callbackClaimMatches(claim, call, invocation) || delivery.ToolCallID != call.ID {
			return ports.ErrToolCallConflict
		}
		if delivery.Status.Terminal() {
			settledStatus = delivery.Status
			return nil
		}
		now := s.clock.Now().UTC()
		if invocation.Status.Terminal() || call.Status.Terminal() {
			_, err := s.store.SettleCallbackDelivery(
				txCtx,
				delivery.ID,
				claim.Owner,
				claim.Attempt,
				domain.CallbackDeliveryAbandoned,
				"work_terminal",
				nil,
				now,
			)
			if err == nil {
				settled = true
				settledStatus = domain.CallbackDeliveryAbandoned
				settledReason = "work_terminal"
			}
			return err
		}
		if invocation.Status != domain.InvocationWaiting ||
			call.Mode != domain.ToolCallModeCallback ||
			call.Status != domain.ToolCallPending ||
			call.Iteration != invocation.CurrentIteration {
			return ports.ErrToolCallNotRunnable
		}
		if !call.DeadlineAt.After(now) || !invocation.WallClockDeadlineAt.After(now) {
			_, err := s.store.SettleCallbackDelivery(
				txCtx,
				delivery.ID,
				claim.Owner,
				claim.Attempt,
				domain.CallbackDeliveryAbandoned,
				"deadline_exceeded",
				nil,
				now,
			)
			if err == nil {
				settled = true
				settledStatus = domain.CallbackDeliveryAbandoned
				settledReason = "deadline_exceeded"
			}
			return err
		}
		payload, err := toolResultPayload(call.ID, content, isError)
		if err != nil {
			return err
		}
		messageID, err := s.ids.NewID(domain.PrefixSessionMessage)
		if err != nil {
			return err
		}
		messageSequence, err := s.store.ReserveMessageSequence(txCtx, invocation.SessionID)
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
			Sequence:          messageSequence,
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
		if _, err := s.store.SettleToolCall(
			txCtx,
			call.ID,
			status,
			domain.ToolCallResultCallback,
			messageID,
			messageSequence,
			now,
		); err != nil {
			return err
		}
		checkpointID, err := s.ids.NewID(domain.PrefixInvocationCheckpoint)
		if err != nil {
			return err
		}
		callID := call.ID
		checkpointSequence := invocation.CurrentCheckpointSequence + 1
		if err := s.store.CreateInvocationCheckpoint(txCtx, domain.InvocationCheckpoint{
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
			return err
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
		if _, err := s.store.SettleCallbackDelivery(
			txCtx,
			delivery.ID,
			claim.Owner,
			claim.Attempt,
			deliveryStatus,
			errorCode,
			responseStatus,
			now,
		); err != nil {
			return err
		}
		settled = true
		open, err := s.store.ListOpenToolCallsForUpdate(txCtx, invocation.ID)
		if err != nil {
			return err
		}
		for _, openCall := range open {
			if (openCall.Mode != domain.ToolCallModeClient && openCall.Mode != domain.ToolCallModeCallback) ||
				openCall.Iteration != invocation.CurrentIteration ||
				openCall.Status != domain.ToolCallPending {
				return ports.ErrExecutionResultInvalid
			}
		}
		if len(open) != 0 {
			return nil
		}
		currentState, err := s.store.GetCurrentInvocationState(txCtx, invocation.ID)
		if err != nil {
			return err
		}
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
		through := currentState.ThroughMessageSequence
		if through == nil || messageSequence > *through {
			through = &messageSequence
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
		if s.config.ExecutionMode == InvocationExecutionCloudTasks {
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
				Queue:             s.config.DispatchQueue,
				Status:            domain.ExecutionDispatchPending,
				AvailableAt:       now,
				CreatedAt:         now,
				UpdatedAt:         now,
			}); err != nil {
				return err
			}
		}
		queued = true
		return nil
	})
	if err != nil {
		if errors.Is(err, ports.ErrCallbackDeliveryLeaseLost) {
			s.logStaleClaim(claim, "lease_lost")
		}
		return err
	}
	if !settled {
		s.logStaleClaim(claim, "already_terminal_"+string(settledStatus))
		return nil
	}
	if queued && s.config.ExecutionMode == InvocationExecutionEmbedded && s.signaller != nil {
		s.signaller.Notify(ctx, ports.InvocationExecutionQueue)
	}
	s.logger.Info(
		"Callback delivery settled",
		"event",
		"callback_delivery_settled",
		"delivery_id",
		claim.Delivery.ID,
		"tool_call_id",
		claim.Delivery.ToolCallID,
		"attempt",
		claim.Attempt,
		"delivery_status",
		settledStatus,
		"reason_code",
		settledReason,
		"resume_queued",
		queued,
	)
	return nil
}

func (s *CallbackDeliveryService) logStaleClaim(
	claim domain.CallbackDeliveryClaim,
	reasonCode string,
) {
	s.logger.Info(
		"Callback delivery fence is stale",
		"event",
		"callback_delivery_stale",
		"delivery_id",
		claim.Delivery.ID,
		"tool_call_id",
		claim.Delivery.ToolCallID,
		"attempt",
		claim.Attempt,
		"reason_code",
		reasonCode,
	)
}

func (s *CallbackDeliveryService) retryDelay(attempt int64) time.Duration {
	delay := s.config.RetryBase
	for index := int64(1); index < attempt && delay < s.config.RetryMaximum; index++ {
		if delay > s.config.RetryMaximum/2 {
			return s.config.RetryMaximum
		}
		delay *= 2
	}
	return min(delay, s.config.RetryMaximum)
}

func callbackClaimMatches(
	claim domain.CallbackDeliveryClaim,
	call domain.ToolCall,
	invocation domain.Invocation,
) bool {
	delivery := claim.Delivery
	return delivery.ID != "" && delivery.ToolCallID == call.ID &&
		delivery.InvocationID == invocation.ID &&
		delivery.SessionID == invocation.SessionID &&
		delivery.AccountID == invocation.AccountID &&
		delivery.TenantPartitionID == invocation.TenantPartitionID &&
		delivery.AgentID == invocation.AgentID &&
		call.InvocationID == invocation.ID &&
		call.SessionID == invocation.SessionID &&
		call.AccountID == invocation.AccountID &&
		call.TenantPartitionID == invocation.TenantPartitionID &&
		call.AgentID == invocation.AgentID &&
		call.Mode == domain.ToolCallModeCallback &&
		claim.Owner != "" && claim.Attempt == delivery.Attempt
}

func decodeCallbackResponse(contentType string, body []byte) (json.RawMessage, bool, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return nil, false, fmt.Errorf("callback response must be application/json")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, false, fmt.Errorf("callback response must be a JSON object")
	}
	seen := make(map[string]struct{}, 2)
	var content json.RawMessage
	var isError bool
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, false, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, false, fmt.Errorf("callback response member is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, false, fmt.Errorf("callback response member is duplicated")
		}
		seen[key] = struct{}{}
		switch key {
		case "content":
			if err := decoder.Decode(&content); err != nil {
				return nil, false, err
			}
		case "is_error":
			if err := decoder.Decode(&isError); err != nil {
				return nil, false, err
			}
		default:
			return nil, false, fmt.Errorf("callback response member is unknown")
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return nil, false, fmt.Errorf("callback response object is incomplete")
	}
	if err := requireCallbackResponseEOF(decoder); err != nil {
		return nil, false, err
	}
	if _, ok := seen["content"]; !ok || validateToolResultContent(content) != nil {
		return nil, false, fmt.Errorf("callback response content is invalid")
	}
	return content, isError, nil
}

func requireCallbackResponseEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("callback response has trailing data")
		}
		return err
	}
	return nil
}

func validateToolResultContent(content json.RawMessage) error {
	if len(content) == 0 || len(content) > 256<<10 || !json.Valid(content) {
		return fmt.Errorf("tool result content must be bounded valid JSON")
	}
	depth, err := clientToolJSONDepth(content)
	if err != nil || depth > 32 {
		return fmt.Errorf("tool result content exceeds the maximum depth")
	}
	return nil
}

func callbackFailureContent() json.RawMessage {
	return json.RawMessage(`{"code":"callback_delivery_failed"}`)
}

func optionalStatus(status int) *int {
	if status == 0 {
		return nil
	}
	return &status
}
