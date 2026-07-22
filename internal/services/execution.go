package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
)

const MaxExecutionOwnerCharacters = 255

var errQueuedInvocationChanged = errors.New("queued Invocation changed after its Session was locked")
var errQueuedInvocationExpired = errors.New("queued Invocation reached its wall-clock deadline")

type ClaimDisposition string

const (
	Claimed          ClaimDisposition = "claimed"
	ClaimMissing     ClaimDisposition = "missing"
	ClaimAlreadyHeld ClaimDisposition = "already_held"
	ClaimNotRunnable ClaimDisposition = "not_runnable"
)

type executionStore interface {
	ports.SessionRepository
	ports.SessionMessageRepository
	ports.InvocationRepository
	ports.InvocationStateRepository
	ports.ExecutionDispatchRepository
	ports.ExecutionSpecSnapshotRepository
	ports.ToolCallRepository
}

type InvocationExecutionService struct {
	store          executionStore
	txm            ports.TransactionManager
	clock          ports.Clock
	ids            ports.IDGenerator
	segmentCeiling time.Duration
}

type InvocationExecutionOption func(*InvocationExecutionService)

func WithExecutionSegmentCeiling(ceiling time.Duration) InvocationExecutionOption {
	return func(service *InvocationExecutionService) { service.segmentCeiling = ceiling }
}

func NewInvocationExecutionService(
	store executionStore,
	txm ports.TransactionManager,
	clock ports.Clock,
	ids ports.IDGenerator,
	options ...InvocationExecutionOption,
) *InvocationExecutionService {
	service := &InvocationExecutionService{store: store, txm: txm, clock: clock, ids: ids, segmentCeiling: 15 * time.Minute}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func (s *InvocationExecutionService) ClaimNext(
	ctx context.Context,
	owner string,
	leaseDuration time.Duration,
) (domain.InvocationClaim, bool, error) {
	if err := s.ready(owner, leaseDuration); err != nil {
		return domain.InvocationClaim{}, false, err
	}
	var claim domain.InvocationClaim
	found := false
	observedAt := s.clock.Now().UTC()
	err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		for {
			candidate, err := s.store.FindNextQueuedInvocationForUpdate(txCtx, observedAt)
			if errors.Is(err, ports.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			claim, err = s.claimWithSessionLocked(txCtx, candidate, owner, leaseDuration)
			if errors.Is(err, errQueuedInvocationChanged) {
				// The SELECT may have started before another claimant committed,
				// then acquired the Session lock with an older joined Invocation
				// snapshot. A new statement gets a fresh READ COMMITTED snapshot.
				if err := txCtx.Err(); err != nil {
					return err
				}
				continue
			}
			if errors.Is(err, errQueuedInvocationExpired) {
				continue
			}
			if err != nil {
				return err
			}
			found = true
			return nil
		}
	})
	return claim, found, err
}

func (s *InvocationExecutionService) ClaimExact(
	ctx context.Context,
	invocationID, owner string,
	leaseDuration time.Duration,
) (domain.InvocationClaim, ClaimDisposition, error) {
	if err := s.ready(owner, leaseDuration); err != nil {
		return domain.InvocationClaim{}, ClaimNotRunnable, err
	}
	observed, err := s.store.GetInvocation(ctx, invocationID)
	if errors.Is(err, ports.ErrNotFound) {
		return domain.InvocationClaim{}, ClaimMissing, nil
	}
	if err != nil {
		return domain.InvocationClaim{}, ClaimNotRunnable, err
	}

	disposition := ClaimNotRunnable
	var claim domain.InvocationClaim
	err = s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, observed.SessionID); err != nil {
			return err
		}
		invocation, err := s.store.GetInvocationForUpdate(txCtx, invocationID)
		if errors.Is(err, ports.ErrNotFound) {
			disposition = ClaimMissing
			return nil
		}
		if err != nil {
			return err
		}
		if invocation.Status != domain.InvocationQueued {
			if invocation.Status == domain.InvocationRunning {
				disposition = ClaimAlreadyHeld
			}
			return nil
		}
		claim, err = s.claimWithSessionLocked(txCtx, invocation, owner, leaseDuration)
		if errors.Is(err, errQueuedInvocationExpired) {
			disposition = ClaimNotRunnable
			return nil
		}
		if err != nil {
			return err
		}
		disposition = Claimed
		return nil
	})
	return claim, disposition, err
}

// claimWithSessionLocked completes a queued claim after the caller has locked
// its Session. ClaimNext acquires that lock with SKIP LOCKED; ClaimExact uses
// GetSessionForUpdate. Both then take the Invocation lock in the same order.
func (s *InvocationExecutionService) claimWithSessionLocked(
	ctx context.Context,
	observed domain.Invocation,
	owner string,
	leaseDuration time.Duration,
) (domain.InvocationClaim, error) {
	invocation, err := s.store.GetInvocationForUpdate(ctx, observed.ID)
	if err != nil {
		return domain.InvocationClaim{}, err
	}
	if invocation.Status != domain.InvocationQueued {
		return domain.InvocationClaim{}, errQueuedInvocationChanged
	}
	now := s.clock.Now().UTC()
	if !invocation.WallClockDeadlineAt.After(now) {
		if _, _, err := s.reapDeadlineWithSessionLocked(ctx, invocation, now); err != nil {
			return domain.InvocationClaim{}, err
		}
		return domain.InvocationClaim{}, errQueuedInvocationExpired
	}
	if invocation.ActiveExecutionMS >= invocation.ActiveTimeoutMS {
		if _, _, err := s.reapDeadlineWithSessionLocked(ctx, invocation, now); err != nil {
			return domain.InvocationClaim{}, err
		}
		return domain.InvocationClaim{}, errQueuedInvocationExpired
	}
	currentState, err := s.store.GetCurrentInvocationState(ctx, invocation.ID)
	if err != nil {
		return domain.InvocationClaim{}, err
	}
	stateID, err := s.ids.NewID(domain.PrefixInvocationState)
	if err != nil {
		return domain.InvocationClaim{}, err
	}
	revision, err := s.store.ReserveLifecycleRevision(ctx, invocation.SessionID)
	if err != nil {
		return domain.InvocationClaim{}, err
	}
	executionDeadlineAt, deadlineScope, err := executionDeadline(invocation, now, s.segmentCeiling)
	if err != nil {
		return domain.InvocationClaim{}, err
	}
	claimed, err := s.store.ClaimInvocation(ctx, invocation.ID, owner, now.Add(leaseDuration), revision, now, executionDeadlineAt, deadlineScope)
	if errors.Is(err, ports.ErrNotFound) {
		return domain.InvocationClaim{}, fmt.Errorf("claim queued Invocation after row lock: %w", err)
	}
	if err != nil {
		return domain.InvocationClaim{}, err
	}
	state := lifecycleState(claimed, stateID, revision, domain.InvocationRunning, currentState.ThroughMessageSequence, now)
	if err := s.store.AppendInvocationState(ctx, state); err != nil {
		return domain.InvocationClaim{}, err
	}
	return claimFromInvocation(claimed), nil
}

func (s *InvocationExecutionService) Renew(
	ctx context.Context,
	claim domain.InvocationClaim,
	leaseDuration time.Duration,
) (time.Time, error) {
	if err := s.ready(claim.Owner, leaseDuration); err != nil {
		return time.Time{}, err
	}
	if claim.Attempt <= 0 {
		return time.Time{}, fmt.Errorf("claim attempt must be positive")
	}
	now := s.clock.Now().UTC()
	renewed, err := s.store.RenewInvocationLease(
		ctx, claim.Invocation.ID, claim.Owner, claim.Attempt, now.Add(leaseDuration), now,
	)
	if err != nil {
		return time.Time{}, err
	}
	if renewed.LeaseExpiresAt == nil {
		return time.Time{}, fmt.Errorf("renewed Invocation has no lease expiry")
	}
	return *renewed.LeaseExpiresAt, nil
}

func (s *InvocationExecutionService) Settle(
	ctx context.Context,
	claim domain.InvocationClaim,
	result domain.InvocationExecutionResult,
) error {
	return s.settle(ctx, claim, result, nil)
}

func (s *InvocationExecutionService) SettleDispatch(
	ctx context.Context,
	claim domain.InvocationClaim,
	result domain.InvocationExecutionResult,
	dispatchID string,
) error {
	if !domain.ValidStableID(dispatchID, domain.PrefixExecutionDispatch) {
		return fmt.Errorf("execution dispatch ID is invalid")
	}
	return s.settle(ctx, claim, result, &dispatchID)
}

func (s *InvocationExecutionService) settle(
	ctx context.Context,
	claim domain.InvocationClaim,
	result domain.InvocationExecutionResult,
	dispatchID *string,
) error {
	if err := validateExecutionResult(result); err != nil {
		return ports.ErrExecutionResultInvalid
	}
	usagePayload, provenancePayload, err := executionEvidencePayloads(result)
	if err != nil {
		return err
	}
	observed, err := s.store.GetInvocation(ctx, claim.Invocation.ID)
	if errors.Is(err, ports.ErrNotFound) {
		return ports.ErrLeaseLost
	}
	if err != nil {
		return err
	}

	return s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, observed.SessionID); err != nil {
			return err
		}
		invocation, err := s.store.GetInvocationForUpdate(txCtx, claim.Invocation.ID)
		if errors.Is(err, ports.ErrNotFound) {
			return ports.ErrLeaseLost
		}
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		if !claimOwns(invocation, claim, now) {
			return ports.ErrLeaseLost
		}
		settleDispatch := false
		if dispatchID != nil {
			dispatch, err := s.store.GetExecutionDispatchForUpdate(txCtx, *dispatchID)
			if err != nil {
				return fmt.Errorf("lock execution dispatch for Invocation settlement: %w", err)
			}
			if !dispatchMatchesInvocation(dispatch, invocation) {
				return fmt.Errorf("execution dispatch does not match the claimed Invocation")
			}
			settleDispatch = !dispatch.Status.Terminal()
		}
		currentState, err := s.store.GetCurrentInvocationState(txCtx, invocation.ID)
		if err != nil {
			return err
		}
		if err := validateUsageProjection(txCtx, s.store, invocation.ID, result); err != nil {
			return err
		}
		if result.Status == domain.InvocationWaiting {
			return s.parkForExternalTools(
				txCtx,
				claim,
				invocation,
				currentState,
				dispatchID,
				settleDispatch,
				now,
			)
		}
		outputPayload, outputProvenancePayload, err := validateStructuredOutputSettlement(
			txCtx,
			s.store,
			invocation,
			result,
		)
		if err != nil {
			return err
		}
		throughMessageSequence := currentState.ThroughMessageSequence
		checkpointWatermark, err := closeOpenToolCallsForTerminal(
			txCtx, s.store, s.ids, invocation, result.Status,
			"Tool execution stopped because the Invocation settled.", now,
		)
		if err != nil {
			return err
		}
		if checkpointWatermark != nil {
			if throughMessageSequence == nil || *checkpointWatermark > *throughMessageSequence {
				throughMessageSequence = checkpointWatermark
			}
		} else if result.MessagesCheckpointed {
			return fmt.Errorf("checkpointed execution result has no durable checkpoint")
		}
		for _, output := range result.AssistantMessages {
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
				SessionID:         invocation.SessionID,
				AccountID:         invocation.AccountID,
				TenantPartitionID: invocation.TenantPartitionID,
				AgentID:           invocation.AgentID,
				InvocationID:      invocation.ID,
				Sequence:          sequence,
				Role:              domain.MessageRoleAssistant,
				Content:           output.Content,
				CreatedAt:         now,
			}); err != nil {
				return err
			}
			sequenceCopy := sequence
			throughMessageSequence = &sequenceCopy
		}
		stateID, err := s.ids.NewID(domain.PrefixInvocationState)
		if err != nil {
			return err
		}
		revision, err := s.store.ReserveLifecycleRevision(txCtx, invocation.SessionID)
		if err != nil {
			return err
		}
		settled, err := s.store.SettleInvocation(
			txCtx, invocation.ID, claim.Owner, claim.Attempt,
			result.Status, revision, result.Error, usagePayload, provenancePayload,
			outputPayload, outputProvenancePayload, now,
		)
		if err != nil {
			return err
		}
		if err := s.store.AppendInvocationState(txCtx, lifecycleState(
			settled, stateID, revision, result.Status, throughMessageSequence, now,
		)); err != nil {
			return err
		}
		if settleDispatch {
			if _, err := s.store.SettleExecutionDispatch(txCtx, *dispatchID, now); err != nil {
				return fmt.Errorf("settle execution dispatch with Invocation: %w", err)
			}
		}
		return nil
	})
}

func (s *InvocationExecutionService) parkForExternalTools(
	ctx context.Context,
	claim domain.InvocationClaim,
	invocation domain.Invocation,
	currentState domain.InvocationState,
	dispatchID *string,
	settleDispatch bool,
	now time.Time,
) error {
	calls, err := s.store.ListOpenToolCallsForUpdate(ctx, invocation.ID)
	if err != nil {
		return err
	}
	if len(calls) == 0 || len(calls) > MaxClientTools {
		return ports.ErrExecutionResultInvalid
	}
	invocation, err = s.settlePendingBuiltinSiblingsForExternalWait(
		ctx,
		claim,
		invocation,
		calls,
		now,
	)
	if err != nil {
		return err
	}
	calls, err = s.store.ListOpenToolCallsForUpdate(ctx, invocation.ID)
	if err != nil || len(calls) == 0 {
		return ports.ErrExecutionResultInvalid
	}
	for _, call := range calls {
		if (call.Mode != domain.ToolCallModeClient && call.Mode != domain.ToolCallModeCallback) ||
			call.Iteration != invocation.CurrentIteration ||
			call.Status != domain.ToolCallPending {
			return ports.ErrExecutionResultInvalid
		}
	}
	latest, err := s.store.GetLatestInvocationCheckpoint(ctx, invocation.ID)
	if err != nil || latest.Iteration != invocation.CurrentIteration ||
		latest.Sequence != invocation.CurrentCheckpointSequence {
		return ports.ErrExecutionResultInvalid
	}
	stateID, err := s.ids.NewID(domain.PrefixInvocationState)
	if err != nil {
		return err
	}
	revision, err := s.store.ReserveLifecycleRevision(ctx, invocation.SessionID)
	if err != nil {
		return err
	}
	parked, err := s.store.ParkInvocationForClientTools(
		ctx,
		invocation.ID,
		claim.Owner,
		claim.Attempt,
		revision,
		now,
	)
	if err != nil {
		return err
	}
	through := latest.ThroughMessageSequence
	if currentState.ThroughMessageSequence != nil && *currentState.ThroughMessageSequence > through {
		through = *currentState.ThroughMessageSequence
	}
	if err := s.store.AppendInvocationState(ctx, lifecycleState(
		parked,
		stateID,
		revision,
		domain.InvocationWaiting,
		&through,
		now,
	)); err != nil {
		return err
	}
	if callbackStore, ok := any(s.store).(ports.CallbackDeliveryRepository); ok {
		if _, err := callbackStore.ActivateCallbackDeliveries(ctx, invocation.ID, now); err != nil {
			return err
		}
	} else {
		for _, call := range calls {
			if call.Mode == domain.ToolCallModeCallback {
				return fmt.Errorf("callback delivery repository is not configured")
			}
		}
	}
	if settleDispatch {
		if _, err := s.store.SettleExecutionDispatch(ctx, *dispatchID, now); err != nil {
			return fmt.Errorf("settle execution dispatch with parked Invocation: %w", err)
		}
	}
	return nil
}

func (s *InvocationExecutionService) settlePendingBuiltinSiblingsForExternalWait(
	ctx context.Context,
	claim domain.InvocationClaim,
	invocation domain.Invocation,
	calls []domain.ToolCall,
	now time.Time,
) (domain.Invocation, error) {
	builtins := make([]domain.ToolCall, 0, len(calls))
	externalCount := 0
	for _, call := range calls {
		if call.Iteration != invocation.CurrentIteration {
			return domain.Invocation{}, ports.ErrExecutionResultInvalid
		}
		switch call.Mode {
		case domain.ToolCallModeClient, domain.ToolCallModeCallback:
			if call.Status != domain.ToolCallPending {
				return domain.Invocation{}, ports.ErrExecutionResultInvalid
			}
			externalCount++
		case domain.ToolCallModeBuiltin:
			if call.Status != domain.ToolCallPending && call.Status != domain.ToolCallRunning {
				return domain.Invocation{}, ports.ErrExecutionResultInvalid
			}
			builtins = append(builtins, call)
		default:
			return domain.Invocation{}, ports.ErrExecutionResultInvalid
		}
	}
	if externalCount == 0 {
		return domain.Invocation{}, ports.ErrExecutionResultInvalid
	}
	if len(builtins) == 0 {
		return invocation, nil
	}
	payload, err := syntheticToolResultPayload(
		builtins,
		"Tool execution was deferred while the Invocation waited for a client tool result.",
	)
	if err != nil {
		return domain.Invocation{}, err
	}
	messageID, err := s.ids.NewID(domain.PrefixSessionMessage)
	if err != nil {
		return domain.Invocation{}, err
	}
	messageSequence, err := s.store.ReserveMessageSequence(ctx, invocation.SessionID)
	if err != nil {
		return domain.Invocation{}, err
	}
	if err := s.store.AppendSessionMessage(ctx, domain.SessionMessage{
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
		return domain.Invocation{}, err
	}
	checkpointSequence := invocation.CurrentCheckpointSequence
	for _, call := range builtins {
		if call.Status == domain.ToolCallRunning {
			if _, err := s.store.SettleRunningToolCallAttempts(
				ctx,
				call.ID,
				domain.ToolCallFailed,
				now,
			); err != nil {
				return domain.Invocation{}, err
			}
		}
		if _, err := s.store.SettleToolCall(
			ctx,
			call.ID,
			domain.ToolCallFailed,
			domain.ToolCallResultSystem,
			messageID,
			messageSequence,
			now,
		); err != nil {
			return domain.Invocation{}, err
		}
		checkpointID, err := s.ids.NewID(domain.PrefixInvocationCheckpoint)
		if err != nil {
			return domain.Invocation{}, err
		}
		checkpointSequence++
		callID := call.ID
		if err := s.store.CreateInvocationCheckpoint(ctx, domain.InvocationCheckpoint{
			ID:                     checkpointID,
			InvocationID:           invocation.ID,
			SessionID:              invocation.SessionID,
			AccountID:              invocation.AccountID,
			TenantPartitionID:      invocation.TenantPartitionID,
			AgentID:                invocation.AgentID,
			Sequence:               checkpointSequence,
			Iteration:              call.Iteration,
			Kind:                   domain.InvocationCheckpointTool,
			LeaseAttempt:           claim.Attempt,
			ThroughMessageSequence: messageSequence,
			ToolCallID:             &callID,
			CreatedAt:              now,
		}); err != nil {
			return domain.Invocation{}, err
		}
	}
	return s.store.AdvanceInvocationCheckpoint(
		ctx,
		invocation.ID,
		claim.Owner,
		claim.Attempt,
		now,
		checkpointSequence,
		invocation.CurrentIteration,
	)
}

func dispatchMatchesInvocation(dispatch domain.ExecutionDispatch, invocation domain.Invocation) bool {
	return dispatch.Kind == domain.ExecutionDispatchInvocation &&
		dispatch.WorkID == invocation.ID &&
		dispatch.AccountID != nil && *dispatch.AccountID == invocation.AccountID &&
		dispatch.TenantPartitionID != nil && *dispatch.TenantPartitionID == invocation.TenantPartitionID
}

func (s *InvocationExecutionService) ReapExpired(ctx context.Context, limit int) ([]domain.Invocation, error) {
	if s == nil || s.store == nil || s.txm == nil || s.clock == nil || s.ids == nil {
		return nil, fmt.Errorf("invocation execution service is not configured")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("reaper batch limit must be positive")
	}
	now := s.clock.Now().UTC()
	if credentialStore, ok := s.store.(ports.ProviderCredentialRepository); ok {
		if _, err := credentialStore.ClearExpiredProviderCredentialMaterial(ctx, now, limit); err != nil {
			return nil, fmt.Errorf("clear expired Invocation provider credentials: %w", err)
		}
	}
	deadlineCandidates, err := s.store.ListExpiredInvocationDeadlines(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	reaped := make([]domain.Invocation, 0, limit)
	var candidateErrors []error
	for _, candidate := range deadlineCandidates {
		invocation, changed, err := s.reapDeadlineCandidate(ctx, candidate, now)
		if err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("reap Invocation deadline %s: %w", candidate.ID, err))
			continue
		}
		if changed {
			reaped = append(reaped, invocation)
		}
	}
	if len(candidateErrors) != 0 {
		// Do not let lease recovery override a logical deadline after its
		// terminal transaction failed. Retry the authoritative deadline outcome
		// on the next scan.
		return reaped, errors.Join(candidateErrors...)
	}
	remaining := limit - len(reaped)
	if remaining <= 0 {
		return reaped, errors.Join(candidateErrors...)
	}
	candidates, err := s.store.ListExpiredInvocationLeases(ctx, now, remaining)
	if err != nil {
		return reaped, errors.Join(append(candidateErrors, err)...)
	}
	for _, candidate := range candidates {
		invocation, changed, err := s.reapCandidate(ctx, candidate, now)
		if err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("reap Invocation %s: %w", candidate.ID, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if changed {
			reaped = append(reaped, invocation)
		}
	}
	return reaped, errors.Join(candidateErrors...)
}

func (s *InvocationExecutionService) reapDeadlineCandidate(ctx context.Context, candidate domain.Invocation, now time.Time) (domain.Invocation, bool, error) {
	var reaped domain.Invocation
	changed := false
	err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, candidate.SessionID); err != nil {
			return err
		}
		invocation, err := s.store.GetInvocationForUpdate(txCtx, candidate.ID)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		reaped, changed, err = s.reapDeadlineWithSessionLocked(txCtx, invocation, now)
		return err
	})
	return reaped, changed, err
}

func (s *InvocationExecutionService) reapDeadlineWithSessionLocked(ctx context.Context, invocation domain.Invocation, now time.Time) (domain.Invocation, bool, error) {
	scope := ""
	if !invocation.WallClockDeadlineAt.After(now) {
		scope = "wall_clock"
	} else if effectiveActiveExecutionMS(invocation, now) >= invocation.ActiveTimeoutMS {
		scope = "active_execution"
	} else if invocation.Status == domain.InvocationRunning && invocation.ExecutionDeadlineAt != nil && !invocation.ExecutionDeadlineAt.After(now) {
		if invocation.ExecutionDeadlineScope != nil {
			scope = *invocation.ExecutionDeadlineScope
		}
	}
	if scope == "execution_segment" && invocation.LeaseExpiresAt != nil && !invocation.LeaseExpiresAt.After(now) {
		// An owner that reached its cutoff can settle a segment deadline while
		// its lease is live. Once ownership itself expires, the replacement
		// owner resumes from the durable prefix instead.
		return domain.Invocation{}, false, nil
	}
	if scope == "" || invocation.Status.Terminal() {
		return domain.Invocation{}, false, nil
	}
	currentState, err := s.store.GetCurrentInvocationState(ctx, invocation.ID)
	if err != nil {
		return domain.Invocation{}, false, err
	}
	stateID, err := s.ids.NewID(domain.PrefixInvocationState)
	if err != nil {
		return domain.Invocation{}, false, err
	}
	revision, err := s.store.ReserveLifecycleRevision(ctx, invocation.SessionID)
	if err != nil {
		return domain.Invocation{}, false, err
	}
	payload := invocationFailureWithDetails("deadline_exceeded", "The execution deadline was exceeded.", map[string]string{"scope": scope})
	throughMessageSequence := currentState.ThroughMessageSequence
	checkpointWatermark, err := closeOpenToolCallsForTerminal(
		ctx, s.store, s.ids, invocation, domain.InvocationFailed,
		"Tool execution stopped because the Invocation deadline was exceeded.", now,
	)
	if err != nil {
		return domain.Invocation{}, false, err
	}
	if checkpointWatermark != nil && (throughMessageSequence == nil || *checkpointWatermark > *throughMessageSequence) {
		throughMessageSequence = checkpointWatermark
	}
	reaped, err := s.store.ReapInvocationDeadline(ctx, invocation.ID, revision, payload, now)
	if errors.Is(err, ports.ErrLeaseLost) {
		return domain.Invocation{}, false, nil
	}
	if err != nil {
		return domain.Invocation{}, false, err
	}
	if err := s.store.AppendInvocationState(ctx, lifecycleState(
		reaped, stateID, revision, domain.InvocationFailed, throughMessageSequence, now,
	)); err != nil {
		return domain.Invocation{}, false, err
	}
	if _, err := s.store.SettleActiveExecutionDispatchForWork(ctx, domain.ExecutionDispatchInvocation, invocation.ID, now); err != nil {
		return domain.Invocation{}, false, err
	}
	return reaped, true, nil
}

func effectiveActiveExecutionMS(invocation domain.Invocation, now time.Time) int64 {
	active := invocation.ActiveExecutionMS
	if invocation.Status != domain.InvocationRunning || invocation.ActiveSegmentStartedAt == nil {
		return active
	}
	through := now
	if invocation.LeaseExpiresAt != nil && invocation.LeaseExpiresAt.Before(through) {
		through = *invocation.LeaseExpiresAt
	}
	if invocation.ExecutionDeadlineAt != nil && invocation.ExecutionDeadlineAt.Before(through) {
		through = *invocation.ExecutionDeadlineAt
	}
	if through.After(*invocation.ActiveSegmentStartedAt) {
		active += through.Sub(*invocation.ActiveSegmentStartedAt).Milliseconds()
	}
	return active
}

func (s *InvocationExecutionService) reapCandidate(
	ctx context.Context,
	candidate domain.Invocation,
	now time.Time,
) (domain.Invocation, bool, error) {
	var reaped domain.Invocation
	changed := false
	err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, candidate.SessionID); err != nil {
			return err
		}
		invocation, err := s.store.GetInvocationForUpdate(txCtx, candidate.ID)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if invocation.Status != domain.InvocationRunning ||
			invocation.LeaseAttempt != candidate.LeaseAttempt ||
			invocation.LeaseExpiresAt == nil || invocation.LeaseExpiresAt.After(now) {
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
		throughMessageSequence := currentState.ThroughMessageSequence
		latest, err := s.store.GetLatestInvocationCheckpoint(txCtx, invocation.ID)
		if err == nil {
			checkpointWatermark := latest.ThroughMessageSequence
			if throughMessageSequence == nil || checkpointWatermark > *throughMessageSequence {
				throughMessageSequence = &checkpointWatermark
			}
		} else if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		reaped, err = s.store.RecoverInvocationLease(
			txCtx,
			invocation.ID,
			invocation.LeaseAttempt,
			revision,
			now,
		)
		if errors.Is(err, ports.ErrLeaseLost) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.store.AppendInvocationState(txCtx, lifecycleState(
			reaped,
			stateID,
			revision,
			domain.InvocationQueued,
			throughMessageSequence,
			now,
		)); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return reaped, changed, err
}

func (s *InvocationExecutionService) ready(owner string, leaseDuration time.Duration) error {
	if s == nil || s.store == nil || s.txm == nil || s.clock == nil || s.ids == nil || s.segmentCeiling <= 0 {
		return fmt.Errorf("invocation execution service is not configured")
	}
	if strings.TrimSpace(owner) == "" || len(owner) > MaxExecutionOwnerCharacters {
		return fmt.Errorf("execution owner must contain 1 to %d bytes", MaxExecutionOwnerCharacters)
	}
	if leaseDuration <= 0 {
		return fmt.Errorf("lease duration must be positive")
	}
	return nil
}

func validateExecutionResult(result domain.InvocationExecutionResult) error {
	if result.Status != domain.InvocationCompleted &&
		result.Status != domain.InvocationFailed &&
		result.Status != domain.InvocationWaiting {
		return fmt.Errorf("execution result status must be completed, failed, or waiting")
	}
	if result.Status == domain.InvocationWaiting {
		if !result.MessagesCheckpointed || len(result.AssistantMessages) != 0 || len(result.Error) != 0 ||
			result.Usage == nil || result.Provenance == nil || result.StructuredOutput != nil {
			return fmt.Errorf("waiting execution result requires checkpointed evidence only")
		}
	}
	if result.Status == domain.InvocationCompleted && len(result.Error) != 0 {
		return fmt.Errorf("completed execution result cannot contain an error")
	}
	if result.Status == domain.InvocationCompleted && len(result.AssistantMessages) == 0 && !result.MessagesCheckpointed {
		return fmt.Errorf("completed execution result requires an assistant message")
	}
	if result.MessagesCheckpointed && len(result.AssistantMessages) != 0 {
		return fmt.Errorf("checkpointed execution result cannot contain unpublished assistant messages")
	}
	if result.Status == domain.InvocationCompleted && (result.Usage == nil || result.Provenance == nil) {
		return fmt.Errorf("completed execution result requires usage and provenance")
	}
	if result.Status == domain.InvocationFailed && (len(result.Error) == 0 || !json.Valid(result.Error)) {
		return fmt.Errorf("failed execution result requires a valid JSON error")
	}
	if result.Status == domain.InvocationFailed && len(result.AssistantMessages) != 0 {
		return fmt.Errorf("failed execution result cannot contain assistant messages")
	}
	if result.Status != domain.InvocationCompleted && result.StructuredOutput != nil {
		return fmt.Errorf("only completed execution may contain structured output")
	}
	if result.StructuredOutput != nil {
		if len(result.StructuredOutput.Value) == 0 || !json.Valid(result.StructuredOutput.Value) {
			return fmt.Errorf("structured output value must be valid JSON")
		}
		if result.StructuredOutput.Provenance.Source != structuredoutput.ProvenanceSource ||
			!domain.ValidStableID(result.StructuredOutput.Provenance.ToolCallID, domain.PrefixToolCall) ||
			len(result.StructuredOutput.Provenance.SchemaSHA256) != sha256.Size*2 {
			return fmt.Errorf("structured output provenance is invalid")
		}
	}
	if (result.Usage == nil) != (result.Provenance == nil) {
		return fmt.Errorf("execution evidence must contain both usage and provenance")
	}
	for _, message := range result.AssistantMessages {
		if message.Role != domain.MessageRoleAssistant {
			return fmt.Errorf("execution output message role must be assistant")
		}
		if err := validateGenerationMessage(message, true); err != nil {
			return err
		}
	}
	if result.Usage != nil {
		if err := validateModelUsage(*result.Usage); err != nil {
			return err
		}
	}
	if result.Provenance != nil {
		if err := validateModelProvenance(*result.Provenance); err != nil {
			return err
		}
	}
	return nil
}

func validateStructuredOutputSettlement(
	ctx context.Context,
	store executionStore,
	invocation domain.Invocation,
	result domain.InvocationExecutionResult,
) ([]byte, []byte, error) {
	if len(invocation.OutputSchemaDigest) == 0 {
		if result.StructuredOutput != nil {
			return nil, nil, fmt.Errorf("output-free Invocation cannot publish structured output")
		}
		return nil, nil, nil
	}
	if len(invocation.OutputSchemaDigest) != sha256.Size {
		return nil, nil, fmt.Errorf("Invocation output schema digest is invalid")
	}
	if result.Status != domain.InvocationCompleted {
		if result.StructuredOutput != nil {
			return nil, nil, fmt.Errorf("failed Invocation cannot publish structured output")
		}
		return nil, nil, nil
	}
	if result.StructuredOutput == nil {
		return nil, nil, fmt.Errorf("completed schema-bearing Invocation requires structured output")
	}
	snapshot, err := store.GetExecutionSpecSnapshot(ctx, invocation.SpecSnapshotID)
	if err != nil {
		return nil, nil, err
	}
	spec, err := decodeInlineSpec(snapshot.Spec)
	if err != nil {
		return nil, nil, fmt.Errorf("decode structured-output contract: %w", err)
	}
	if spec.Output == nil {
		return nil, nil, fmt.Errorf("structured-output contract is missing from the execution snapshot")
	}
	digest, err := structuredOutputSchemaDigest(spec.Output.Schema)
	if err != nil || !bytes.Equal(digest, invocation.OutputSchemaDigest) {
		return nil, nil, fmt.Errorf("structured-output schema digest does not match Invocation")
	}
	provenance := result.StructuredOutput.Provenance
	if provenance.Source != structuredoutput.ProvenanceSource ||
		provenance.SchemaSHA256 != hex.EncodeToString(invocation.OutputSchemaDigest) {
		return nil, nil, fmt.Errorf("structured-output provenance does not match Invocation")
	}
	call, err := store.GetToolCall(ctx, provenance.ToolCallID)
	if err != nil {
		return nil, nil, err
	}
	if call.InvocationID != invocation.ID || call.Name != structuredoutput.ReservedToolName ||
		call.Mode != domain.ToolCallModeBuiltin || call.Status != domain.ToolCallCompleted ||
		call.ResultMessageID == nil {
		return nil, nil, fmt.Errorf("structured-output ToolCall is not an accepted reserved call")
	}
	input, err := storedToolCallInput(ctx, store, call)
	if err != nil || !jsonEqual(input, result.StructuredOutput.Value) {
		return nil, nil, fmt.Errorf("structured output does not equal accepted ToolCall request")
	}
	requestDigest, err := toolRequestDigest(call.Name, call.Mode, input)
	if err != nil || !bytes.Equal(requestDigest, call.RequestDigest) {
		return nil, nil, fmt.Errorf("structured-output ToolCall request digest does not match")
	}
	compiled, err := structuredoutput.CompileSchema(spec.Output.Schema)
	if err != nil {
		return nil, nil, fmt.Errorf("structured-output settlement schema is invalid")
	}
	if err := compiled.ValidateValue(result.StructuredOutput.Value); err != nil {
		return nil, nil, fmt.Errorf("structured-output settlement value failed validation")
	}
	outputPayload, err := canonicalJSON(result.StructuredOutput.Value)
	if err != nil {
		return nil, nil, err
	}
	provenancePayload, err := json.Marshal(provenance)
	if err != nil {
		return nil, nil, err
	}
	return outputPayload, provenancePayload, nil
}

func storedToolCallInput(
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
		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return nil, err
		}
		for _, block := range blocks {
			var kind, id, name string
			if json.Unmarshal(block["type"], &kind) != nil || kind != "tool_use" ||
				json.Unmarshal(block["id"], &id) != nil || id != call.ID ||
				json.Unmarshal(block["name"], &name) != nil || name != call.Name {
				continue
			}
			var input json.RawMessage
			if err := json.Unmarshal(block["input"], &input); err != nil {
				return nil, err
			}
			return input, nil
		}
	}
	return nil, fmt.Errorf("structured-output ToolCall request message is missing")
}

func executionEvidencePayloads(result domain.InvocationExecutionResult) ([]byte, []byte, error) {
	var usagePayload, provenancePayload []byte
	var err error
	if result.Usage != nil {
		usagePayload, err = json.Marshal(result.Usage)
		if err != nil {
			return nil, nil, fmt.Errorf("encode model usage: %w", err)
		}
	}
	if result.Provenance != nil {
		provenancePayload, err = json.Marshal(result.Provenance)
		if err != nil {
			return nil, nil, fmt.Errorf("encode model provenance: %w", err)
		}
	}
	return usagePayload, provenancePayload, nil
}

func claimOwns(invocation domain.Invocation, claim domain.InvocationClaim, now time.Time) bool {
	return invocation.Status == domain.InvocationRunning &&
		invocation.LeaseOwner != nil && *invocation.LeaseOwner == claim.Owner &&
		invocation.LeaseAttempt == claim.Attempt &&
		invocation.LeaseExpiresAt != nil && invocation.LeaseExpiresAt.After(now)
}

func claimFromInvocation(invocation domain.Invocation) domain.InvocationClaim {
	return domain.InvocationClaim{
		Invocation:     invocation,
		Owner:          *invocation.LeaseOwner,
		Attempt:        invocation.LeaseAttempt,
		LeaseExpiresAt: *invocation.LeaseExpiresAt,
	}
}

func lifecycleState(
	invocation domain.Invocation,
	stateID string,
	revision int64,
	status domain.InvocationStatus,
	throughMessageSequence *int64,
	now time.Time,
) domain.InvocationState {
	return domain.InvocationState{
		ID:                     stateID,
		InvocationID:           invocation.ID,
		SessionID:              invocation.SessionID,
		AccountID:              invocation.AccountID,
		TenantPartitionID:      invocation.TenantPartitionID,
		AgentID:                invocation.AgentID,
		Revision:               revision,
		Status:                 status,
		LeaseAttempt:           invocation.LeaseAttempt,
		ThroughMessageSequence: throughMessageSequence,
		CreatedAt:              now,
	}
}

func invocationFailure(code, message string) json.RawMessage {
	payload, _ := json.Marshal(map[string]string{"code": code, "message": message})
	return payload
}

func invocationFailureWithDetails(code, message string, details map[string]string) json.RawMessage {
	payload, _ := json.Marshal(struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	}{Code: code, Message: message, Details: details})
	return payload
}

func executionDeadline(invocation domain.Invocation, startedAt time.Time, segmentCeiling time.Duration) (time.Time, string, error) {
	if segmentCeiling <= 0 || invocation.ActiveTimeoutMS <= 0 {
		return time.Time{}, "", fmt.Errorf("invocation execution controls are invalid")
	}
	remainingActive := time.Duration(invocation.ActiveTimeoutMS-invocation.ActiveExecutionMS) * time.Millisecond
	if remainingActive <= 0 {
		return time.Time{}, "", fmt.Errorf("invocation active execution budget is exhausted")
	}
	deadline := startedAt.Add(segmentCeiling)
	scope := "execution_segment"
	if activeDeadline := startedAt.Add(remainingActive); activeDeadline.Before(deadline) {
		deadline, scope = activeDeadline, "active_execution"
	}
	if invocation.WallClockDeadlineAt.Before(deadline) {
		deadline, scope = invocation.WallClockDeadlineAt, "wall_clock"
	}
	return deadline, scope, nil
}
