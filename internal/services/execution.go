package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const MaxExecutionOwnerCharacters = 255

var errQueuedInvocationChanged = errors.New("queued Invocation changed after its Session was locked")

type ClaimDisposition string

const (
	Claimed          ClaimDisposition = "claimed"
	ClaimMissing     ClaimDisposition = "missing"
	ClaimAlreadyHeld ClaimDisposition = "already_held"
	ClaimNotRunnable ClaimDisposition = "not_runnable"
)

type executionStore interface {
	ports.SessionRepository
	ports.InvocationRepository
	ports.InvocationStateRepository
}

type InvocationExecutionService struct {
	store executionStore
	txm   ports.TransactionManager
	clock ports.Clock
	ids   ports.IDGenerator
}

func NewInvocationExecutionService(
	store executionStore,
	txm ports.TransactionManager,
	clock ports.Clock,
	ids ports.IDGenerator,
) *InvocationExecutionService {
	return &InvocationExecutionService{store: store, txm: txm, clock: clock, ids: ids}
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
	err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		for {
			candidate, err := s.store.FindNextQueuedInvocationForUpdate(txCtx)
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
	now := s.clock.Now().UTC()
	claimed, err := s.store.ClaimInvocation(ctx, invocation.ID, owner, now.Add(leaseDuration), revision, now)
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
	if err := validateExecutionResult(result); err != nil {
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
		settled, err := s.store.SettleInvocation(
			txCtx, invocation.ID, claim.Owner, claim.Attempt,
			result.Status, revision, result.Error, now,
		)
		if err != nil {
			return err
		}
		return s.store.AppendInvocationState(txCtx, lifecycleState(
			settled, stateID, revision, result.Status, currentState.ThroughMessageSequence, now,
		))
	})
}

func (s *InvocationExecutionService) ReapExpired(ctx context.Context, limit int) ([]domain.Invocation, error) {
	if s == nil || s.store == nil || s.txm == nil || s.clock == nil || s.ids == nil {
		return nil, fmt.Errorf("Invocation execution service is not configured")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("reaper batch limit must be positive")
	}
	now := s.clock.Now().UTC()
	candidates, err := s.store.ListExpiredInvocationLeases(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	reaped := make([]domain.Invocation, 0, len(candidates))
	var candidateErrors []error
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
		payload := invocationFailure("execution_lost", "The execution owner was lost.")
		reaped, err = s.store.ReapInvocationLease(
			txCtx, invocation.ID, invocation.LeaseAttempt, revision, payload, now,
		)
		if errors.Is(err, ports.ErrLeaseLost) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.store.AppendInvocationState(txCtx, lifecycleState(
			reaped, stateID, revision, domain.InvocationFailed, currentState.ThroughMessageSequence, now,
		)); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return reaped, changed, err
}

func (s *InvocationExecutionService) ready(owner string, leaseDuration time.Duration) error {
	if s == nil || s.store == nil || s.txm == nil || s.clock == nil || s.ids == nil {
		return fmt.Errorf("Invocation execution service is not configured")
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
	if result.Status != domain.InvocationCompleted && result.Status != domain.InvocationFailed {
		return fmt.Errorf("execution result status must be completed or failed")
	}
	if result.Status == domain.InvocationCompleted && len(result.Error) != 0 {
		return fmt.Errorf("completed execution result cannot contain an error")
	}
	if result.Status == domain.InvocationFailed && (len(result.Error) == 0 || !json.Valid(result.Error)) {
		return fmt.Errorf("failed execution result requires a valid JSON error")
	}
	return nil
}

func claimOwns(invocation domain.Invocation, claim domain.InvocationClaim, now time.Time) bool {
	return invocation.Status == domain.InvocationRunning &&
		invocation.LeaseOwner != nil && *invocation.LeaseOwner == claim.Owner &&
		invocation.LeaseAttempt == claim.Attempt &&
		invocation.LeaseExpiresAt != nil && invocation.LeaseExpiresAt.After(now)
}

func claimFromInvocation(invocation domain.Invocation) domain.InvocationClaim {
	return domain.InvocationClaim{
		Invocation: invocation, Owner: *invocation.LeaseOwner,
		Attempt: invocation.LeaseAttempt, LeaseExpiresAt: *invocation.LeaseExpiresAt,
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
		ID: stateID, InvocationID: invocation.ID, SessionID: invocation.SessionID,
		AccountID: invocation.AccountID, TenantPartitionID: invocation.TenantPartitionID,
		AgentID: invocation.AgentID, Revision: revision, Status: status,
		LeaseAttempt:           invocation.LeaseAttempt,
		ThroughMessageSequence: throughMessageSequence, CreatedAt: now,
	}
}

func invocationFailure(code, message string) json.RawMessage {
	payload, _ := json.Marshal(map[string]string{"code": code, "message": message})
	return payload
}
