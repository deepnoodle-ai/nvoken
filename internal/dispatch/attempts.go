package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type attemptStore interface {
	ports.SessionRepository
	ports.InvocationRepository
	ports.ExecutionDispatchRepository
}

type AttemptService struct {
	dispatches    *services.DispatchService
	invocations   *services.InvocationExecutionService
	executor      ports.InvocationExecutor
	store         attemptStore
	txm           ports.TransactionManager
	clock         ports.Clock
	owner         string
	leaseDuration time.Duration
	engineConfig  engine.Config
	cancellations ports.CancellationSignaller
	logger        *slog.Logger
	cancelMu      sync.Mutex
	claimCancels  map[string]context.CancelCauseFunc
}

func NewAttemptService(
	dispatches *services.DispatchService,
	invocations *services.InvocationExecutionService,
	executor ports.InvocationExecutor,
	store attemptStore,
	txm ports.TransactionManager,
	clock ports.Clock,
	owner string,
	engineConfig engine.Config,
	cancellations ports.CancellationSignaller,
	logger *slog.Logger,
) (*AttemptService, error) {
	if dispatches == nil || invocations == nil || executor == nil || store == nil || txm == nil || clock == nil || owner == "" {
		return nil, fmt.Errorf("dispatch attempt dependencies are required")
	}
	if err := engine.ValidateConfig(engineConfig); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &AttemptService{
		dispatches: dispatches, invocations: invocations, executor: executor,
		store: store, txm: txm, clock: clock, owner: owner,
		leaseDuration: engineConfig.LeaseDuration, engineConfig: engineConfig,
		cancellations: cancellations, logger: logger,
		claimCancels: make(map[string]context.CancelCauseFunc),
	}, nil
}

var errCancellationNotification = errors.New("Invocation cancellation notification received")

// Run listens once per executor process and routes coalescable cancellation
// hints to active request-bound attempts. Durable state and lease fencing remain
// the fallback if a notification is missed.
func (s *AttemptService) Run(ctx context.Context) error {
	if s.cancellations == nil {
		<-ctx.Done()
		return nil
	}
	subscription := s.cancellations.SubscribeCancellations(ctx)
	defer subscription.Close()
	for ctx.Err() == nil {
		invocationID, ok := subscription.Wait(ctx, time.Second)
		if !ok {
			continue
		}
		s.cancelMu.Lock()
		cancel := s.claimCancels[invocationID]
		s.cancelMu.Unlock()
		if cancel != nil {
			cancel(errCancellationNotification)
		}
	}
	return nil
}

func (s *AttemptService) Attempt(ctx context.Context, dispatchID string) (services.DispatchAttemptOutcome, error) {
	if !domain.ValidStableID(dispatchID, domain.PrefixExecutionDispatch) {
		return services.DispatchAttemptNoop, nil
	}
	dispatch, err := s.store.GetExecutionDispatch(ctx, dispatchID)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && dispatch.Status.Terminal()) {
		return services.DispatchAttemptNoop, nil
	}
	if err != nil {
		return services.DispatchAttemptNoop, fmt.Errorf("load execution dispatch: %w", err)
	}
	switch dispatch.Kind {
	case domain.ExecutionDispatchSynthetic:
		return s.dispatches.Attempt(ctx, dispatchID)
	case domain.ExecutionDispatchInvocation:
		return s.attemptInvocation(ctx, dispatch)
	default:
		return services.DispatchAttemptNoop, s.abandon(ctx, dispatch.ID, "unsupported dispatch kind")
	}
}

func (s *AttemptService) attemptInvocation(ctx context.Context, dispatch domain.ExecutionDispatch) (services.DispatchAttemptOutcome, error) {
	if !domain.ValidStableID(dispatch.WorkID, domain.PrefixInvocation) || dispatch.AccountID == nil || dispatch.TenantPartitionID == nil {
		return services.DispatchAttemptNoop, s.abandon(ctx, dispatch.ID, "invalid Invocation dispatch identity")
	}
	invocation, err := s.store.GetInvocation(ctx, dispatch.WorkID)
	if errors.Is(err, ports.ErrNotFound) {
		return services.DispatchAttemptNoop, s.abandon(ctx, dispatch.ID, "authoritative Invocation missing")
	}
	if err != nil {
		return services.DispatchAttemptNoop, fmt.Errorf("load dispatched Invocation: %w", err)
	}
	if *dispatch.AccountID != invocation.AccountID || *dispatch.TenantPartitionID != invocation.TenantPartitionID {
		return services.DispatchAttemptNoop, s.abandon(ctx, dispatch.ID, "Invocation dispatch scope mismatch")
	}

	claim, disposition, err := s.invocations.ClaimExact(ctx, invocation.ID, s.owner, s.leaseDuration)
	if err != nil {
		return services.DispatchAttemptNoop, fmt.Errorf("claim dispatched Invocation: %w", err)
	}
	switch disposition {
	case services.ClaimMissing:
		return services.DispatchAttemptNoop, s.abandon(ctx, dispatch.ID, "authoritative Invocation missing")
	case services.ClaimAlreadyHeld:
		return services.DispatchAttemptNoop, ports.ErrDispatchAttemptActive
	case services.ClaimNotRunnable:
		if err := s.settleNoop(ctx, dispatch.ID); err != nil {
			return services.DispatchAttemptNoop, err
		}
		return services.DispatchAttemptNoop, nil
	case services.Claimed:
	default:
		return services.DispatchAttemptNoop, fmt.Errorf("unknown Invocation claim disposition %q", disposition)
	}

	bound := dispatchBoundOwnership{InvocationExecutionService: s.invocations, dispatchID: dispatch.ID}
	runner, err := engine.NewRunner(s.owner, bound, s.executor, nil, s.logger, s.engineConfig)
	if err != nil {
		return services.DispatchAttemptNoop, err
	}
	started := time.Now()
	executionCtx, cancelExecution := context.WithCancelCause(ctx)
	s.cancelMu.Lock()
	s.claimCancels[invocation.ID] = cancelExecution
	s.cancelMu.Unlock()
	defer func() {
		s.cancelMu.Lock()
		delete(s.claimCancels, invocation.ID)
		s.cancelMu.Unlock()
		cancelExecution(context.Canceled)
	}()
	outcome, err := runner.ExecuteClaim(executionCtx, claim)
	if err != nil {
		return services.DispatchAttemptNoop, err
	}
	if outcome.Settled {
		settlementMarginMS := int64(0)
		if claim.Invocation.ExecutionDeadlineAt != nil {
			settlementMarginMS = claim.Invocation.ExecutionDeadlineAt.Sub(s.clock.Now().UTC()).Milliseconds()
		}
		s.logger.Info("request-bound Invocation dispatch settled",
			"dispatch_id", dispatch.ID, "invocation_id", invocation.ID,
			"lease_attempt", claim.Attempt, "attempt_latency_ms", time.Since(started).Milliseconds(),
			"settlement_margin_ms", settlementMarginMS)
		return services.DispatchAttemptSettled, nil
	}
	if err := s.settleNoop(ctx, dispatch.ID); err != nil {
		return services.DispatchAttemptNoop, err
	}
	return services.DispatchAttemptNoop, nil
}

func (s *AttemptService) settleNoop(ctx context.Context, dispatchID string) error {
	dispatch, err := s.store.GetExecutionDispatch(ctx, dispatchID)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && dispatch.Status.Terminal()) {
		return nil
	}
	if err != nil {
		return err
	}
	invocation, err := s.store.GetInvocation(ctx, dispatch.WorkID)
	if errors.Is(err, ports.ErrNotFound) {
		return s.abandon(ctx, dispatchID, "authoritative Invocation missing")
	}
	if err != nil {
		return err
	}
	return s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.store.GetSessionForUpdate(txCtx, invocation.SessionID); err != nil {
			return err
		}
		currentInvocation, err := s.store.GetInvocationForUpdate(txCtx, invocation.ID)
		if err != nil {
			return err
		}
		currentDispatch, err := s.store.GetExecutionDispatchForUpdate(txCtx, dispatchID)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && currentDispatch.Status.Terminal()) {
			return nil
		}
		if err != nil {
			return err
		}
		if currentDispatch.Kind != domain.ExecutionDispatchInvocation ||
			currentDispatch.WorkID != currentInvocation.ID || currentDispatch.AccountID == nil ||
			currentDispatch.TenantPartitionID == nil || *currentDispatch.AccountID != currentInvocation.AccountID ||
			*currentDispatch.TenantPartitionID != currentInvocation.TenantPartitionID {
			_, err = s.store.AbandonExecutionDispatch(txCtx, currentDispatch.ID, "Invocation dispatch identity mismatch", s.clock.Now().UTC())
			return err
		}
		if currentInvocation.Status == domain.InvocationRunning || currentInvocation.Status == domain.InvocationQueued {
			return ports.ErrDispatchAttemptPending
		}
		_, err = s.store.SettleExecutionDispatch(txCtx, currentDispatch.ID, s.clock.Now().UTC())
		return err
	})
}

func (s *AttemptService) abandon(ctx context.Context, dispatchID, reason string) error {
	return s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		dispatch, err := s.store.GetExecutionDispatchForUpdate(txCtx, dispatchID)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && dispatch.Status.Terminal()) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = s.store.AbandonExecutionDispatch(txCtx, dispatch.ID, reason, s.clock.Now().UTC())
		return err
	})
}

type dispatchBoundOwnership struct {
	*services.InvocationExecutionService
	dispatchID string
}

func (o dispatchBoundOwnership) Settle(ctx context.Context, claim domain.InvocationClaim, result domain.InvocationExecutionResult) error {
	return o.InvocationExecutionService.SettleDispatch(ctx, claim, result, o.dispatchID)
}
