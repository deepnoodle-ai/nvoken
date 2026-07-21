package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const (
	DefaultExecutionDispatchQueue = "execution"
	maxDispatchErrorCharacters    = 1024
)

type DispatchConfig struct {
	Queue                 string
	PublicationLease      time.Duration
	PublishRetryBase      time.Duration
	PublishRetryMax       time.Duration
	StaleAfter            time.Duration
	Retention             time.Duration
	BatchLimit            int
	SyntheticAttemptDelay time.Duration
}

func DefaultDispatchConfig() DispatchConfig {
	return DispatchConfig{
		Queue: DefaultExecutionDispatchQueue, PublicationLease: 30 * time.Second,
		PublishRetryBase: time.Second, PublishRetryMax: time.Minute,
		StaleAfter: 5 * time.Minute, Retention: 7 * 24 * time.Hour, BatchLimit: 100,
	}
}

func ValidateDispatchConfig(cfg DispatchConfig) error {
	if strings.TrimSpace(cfg.Queue) == "" || len(cfg.Queue) > 512 {
		return fmt.Errorf("dispatch queue must be nonblank and at most 512 bytes")
	}
	if cfg.PublicationLease <= 0 || cfg.PublishRetryBase <= 0 || cfg.PublishRetryMax < cfg.PublishRetryBase {
		return fmt.Errorf("dispatch publication lease and retry bounds must be positive and ordered")
	}
	if cfg.StaleAfter <= cfg.PublicationLease {
		return fmt.Errorf("dispatch stale age must exceed publication lease")
	}
	if cfg.Retention <= cfg.StaleAfter {
		return fmt.Errorf("dispatch retention must exceed stale age")
	}
	if cfg.BatchLimit <= 0 || cfg.BatchLimit > 1000 {
		return fmt.Errorf("dispatch batch limit must be from 1 through 1000")
	}
	if cfg.SyntheticAttemptDelay < 0 {
		return fmt.Errorf("synthetic dispatch attempt delay cannot be negative")
	}
	return nil
}

type DispatchAttemptOutcome string

const (
	DispatchAttemptSettled DispatchAttemptOutcome = "settled"
	DispatchAttemptNoop    DispatchAttemptOutcome = "noop"
)

type DispatchService struct {
	repository ports.ExecutionDispatchRepository
	txm        ports.TransactionManager
	clock      ports.Clock
	ids        ports.IDGenerator
	config     DispatchConfig
	logger     *slog.Logger
}

func NewDispatchService(repository ports.ExecutionDispatchRepository, txm ports.TransactionManager, clock ports.Clock, ids ports.IDGenerator, cfg DispatchConfig, logger *slog.Logger) (*DispatchService, error) {
	if repository == nil || txm == nil || clock == nil || ids == nil {
		return nil, fmt.Errorf("dispatch service dependencies are required")
	}
	if err := ValidateDispatchConfig(cfg); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &DispatchService{repository: repository, txm: txm, clock: clock, ids: ids, config: cfg, logger: logger}, nil
}

func (s *DispatchService) CreateSynthetic(ctx context.Context) (domain.SyntheticDispatchWork, domain.ExecutionDispatch, error) {
	workID, err := s.ids.NewID(domain.PrefixSyntheticDispatchWork)
	if err != nil {
		return domain.SyntheticDispatchWork{}, domain.ExecutionDispatch{}, fmt.Errorf("create synthetic work ID: %w", err)
	}
	dispatchID, err := s.ids.NewID(domain.PrefixExecutionDispatch)
	if err != nil {
		return domain.SyntheticDispatchWork{}, domain.ExecutionDispatch{}, fmt.Errorf("create dispatch ID: %w", err)
	}
	now := s.clock.Now()
	work := domain.SyntheticDispatchWork{
		ID: workID, Status: domain.SyntheticDispatchWorkPending,
		CreatedAt: now, UpdatedAt: now,
	}
	dispatch := domain.ExecutionDispatch{
		ID: dispatchID, Kind: domain.ExecutionDispatchSynthetic, WorkID: work.ID,
		Queue: s.config.Queue, Status: domain.ExecutionDispatchPending,
		AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if err := s.repository.CreateSyntheticDispatchWork(txCtx, work); err != nil {
			return err
		}
		return s.repository.CreateExecutionDispatch(txCtx, dispatch)
	}); err != nil {
		return domain.SyntheticDispatchWork{}, domain.ExecutionDispatch{}, fmt.Errorf("create synthetic dispatch: %w", err)
	}
	return work, dispatch, nil
}

func (s *DispatchService) GetSynthetic(ctx context.Context, id string) (domain.SyntheticDispatchWork, error) {
	return s.repository.GetSyntheticDispatchWork(ctx, id)
}

func (s *DispatchService) GetDispatch(ctx context.Context, id string) (domain.ExecutionDispatch, error) {
	return s.repository.GetExecutionDispatch(ctx, id)
}

func (s *DispatchService) Attempt(ctx context.Context, dispatchID string) (DispatchAttemptOutcome, error) {
	if !domain.ValidStableID(dispatchID, domain.PrefixExecutionDispatch) {
		return DispatchAttemptNoop, nil
	}
	if s.config.SyntheticAttemptDelay > 0 {
		dispatch, err := s.repository.GetExecutionDispatch(ctx, dispatchID)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && dispatch.Status.Terminal()) {
			return DispatchAttemptNoop, nil
		}
		if err != nil {
			return DispatchAttemptNoop, fmt.Errorf("load delayed synthetic dispatch: %w", err)
		}
		timer := time.NewTimer(s.config.SyntheticAttemptDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return DispatchAttemptNoop, ctx.Err()
		case <-timer.C:
		}
	}
	outcome := DispatchAttemptNoop
	err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		dispatch, err := s.repository.GetExecutionDispatchForUpdate(txCtx, dispatchID)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if dispatch.Status.Terminal() {
			return nil
		}
		if dispatch.Kind != domain.ExecutionDispatchSynthetic {
			_, err = s.repository.AbandonExecutionDispatch(txCtx, dispatch.ID, "unsupported dispatch kind", s.clock.Now())
			return err
		}
		work, err := s.repository.GetSyntheticDispatchWorkForUpdate(txCtx, dispatch.WorkID)
		if errors.Is(err, ports.ErrNotFound) {
			_, err = s.repository.AbandonExecutionDispatch(txCtx, dispatch.ID, "authoritative work missing", s.clock.Now())
			return err
		}
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if work.Status == domain.SyntheticDispatchWorkPending {
			if _, err := s.repository.SettleSyntheticDispatchWork(txCtx, work.ID, now); err != nil {
				return err
			}
			outcome = DispatchAttemptSettled
		}
		_, err = s.repository.SettleExecutionDispatch(txCtx, dispatch.ID, now)
		return err
	})
	if err != nil {
		return DispatchAttemptNoop, fmt.Errorf("attempt execution dispatch: %w", err)
	}
	return outcome, nil
}

func (s *DispatchService) ClaimNext(ctx context.Context, owner string) (domain.ExecutionDispatchClaim, bool, error) {
	if strings.TrimSpace(owner) == "" || len(owner) > 255 {
		return domain.ExecutionDispatchClaim{}, false, fmt.Errorf("publisher owner must be nonblank and at most 255 bytes")
	}
	now := s.clock.Now()
	dispatch, err := s.repository.ClaimNextExecutionDispatch(ctx, s.config.Queue, owner, now, now.Add(s.config.PublicationLease))
	if errors.Is(err, ports.ErrNotFound) {
		return domain.ExecutionDispatchClaim{}, false, nil
	}
	if err != nil {
		return domain.ExecutionDispatchClaim{}, false, fmt.Errorf("claim execution dispatch: %w", err)
	}
	return domain.ExecutionDispatchClaim{Dispatch: dispatch, Owner: owner, Attempt: dispatch.PublisherAttempt}, true, nil
}

func (s *DispatchService) RenewPublication(ctx context.Context, claim domain.ExecutionDispatchClaim) error {
	now := s.clock.Now()
	_, err := s.repository.RenewExecutionDispatchPublication(ctx, claim.Dispatch.ID, claim.Owner, claim.Attempt, now, now.Add(s.config.PublicationLease))
	return err
}

func (s *DispatchService) PublishClaim(ctx context.Context, tasks ports.ExecutionTaskQueue, claim domain.ExecutionDispatchClaim) error {
	if tasks == nil {
		return fmt.Errorf("execution task queue is required")
	}
	publishCtx, cancel := context.WithCancel(ctx)
	renewalDone := make(chan error, 1)
	go func() {
		interval := s.config.PublicationLease / 3
		if interval <= 0 {
			interval = time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-publishCtx.Done():
				renewalDone <- nil
				return
			case <-ticker.C:
				if err := s.RenewPublication(publishCtx, claim); err != nil {
					renewalDone <- err
					cancel()
					return
				}
			}
		}
	}()
	taskName, createErr := tasks.CreateTask(publishCtx, ports.ExecutionTask{
		DispatchID: claim.Dispatch.ID, AvailableAt: claim.Dispatch.AvailableAt,
	})
	cancel()
	renewalErr := <-renewalDone
	if createErr == nil && renewalErr != nil {
		createErr = renewalErr
	}
	if createErr == nil && taskName == "" {
		createErr = fmt.Errorf("task queue returned an empty task name")
	}
	if createErr != nil && !errors.Is(createErr, ports.ErrTaskAlreadyExists) {
		now := s.clock.Now()
		message := boundedDispatchError(createErr)
		_, returnErr := s.repository.ReturnExecutionDispatchPending(
			ctx, claim.Dispatch.ID, claim.Owner, claim.Attempt,
			now.Add(s.publishBackoff(claim.Dispatch.PublishAttempts)), message, now,
		)
		if returnErr != nil && !errors.Is(returnErr, ports.ErrDispatchLeaseLost) {
			return fmt.Errorf("publish task failed (%v) and return to pending failed: %w", createErr, returnErr)
		}
		s.logger.Warn("execution dispatch publication failed",
			"event", "dispatch_publish_failure", "dispatch_id", claim.Dispatch.ID,
			"dispatch_kind", claim.Dispatch.Kind, "publish_attempts", claim.Dispatch.PublishAttempts,
			"error", message)
		return createErr
	}
	_, err := s.repository.MarkExecutionDispatchPublished(ctx, claim.Dispatch.ID, claim.Owner, claim.Attempt, taskName, s.clock.Now())
	if errors.Is(err, ports.ErrDispatchLeaseLost) {
		current, getErr := s.repository.GetExecutionDispatch(ctx, claim.Dispatch.ID)
		if getErr == nil && current.Status.Terminal() {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("mark execution dispatch published: %w", err)
	}
	return nil
}

func (s *DispatchService) publishBackoff(attempt int) time.Duration {
	backoff := s.config.PublishRetryBase
	for i := 1; i < attempt && backoff < s.config.PublishRetryMax; i++ {
		backoff *= 2
		if backoff > s.config.PublishRetryMax {
			return s.config.PublishRetryMax
		}
	}
	return backoff
}

func boundedDispatchError(err error) string {
	message := strings.TrimSpace(strings.ToValidUTF8(err.Error(), "?"))
	if len(message) > maxDispatchErrorCharacters {
		message = message[:maxDispatchErrorCharacters]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return message
}

type ReconcileResult struct {
	Existing  int
	Settled   int
	Succeeded int
}

func (s *DispatchService) Reconcile(ctx context.Context, tasks ports.ExecutionTaskQueue) (ReconcileResult, error) {
	if tasks == nil {
		return ReconcileResult{}, fmt.Errorf("execution task queue is required")
	}
	stale, err := s.repository.ListStalePublishedExecutionDispatches(ctx, s.clock.Now().Add(-s.config.StaleAfter), s.config.BatchLimit)
	if err != nil {
		return ReconcileResult{}, err
	}
	var result ReconcileResult
	for _, dispatch := range stale {
		if dispatch.TaskName == nil {
			return result, fmt.Errorf("published dispatch %s has no task name", dispatch.ID)
		}
		exists, err := tasks.TaskExists(ctx, *dispatch.TaskName)
		if err != nil {
			return result, fmt.Errorf("inspect task for dispatch %s: %w", dispatch.ID, err)
		}
		if exists {
			result.Existing++
			continue
		}
		created, err := s.reconcileMissing(ctx, dispatch.ID)
		if err != nil {
			return result, err
		}
		if created {
			result.Succeeded++
		} else {
			result.Settled++
		}
	}
	return result, nil
}

func (s *DispatchService) reconcileMissing(ctx context.Context, dispatchID string) (bool, error) {
	successorID, err := s.ids.NewID(domain.PrefixExecutionDispatch)
	if err != nil {
		return false, err
	}
	created := false
	err = s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		dispatch, err := s.repository.GetExecutionDispatchForUpdate(txCtx, dispatchID)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && dispatch.Status.Terminal()) {
			return nil
		}
		if err != nil {
			return err
		}
		if dispatch.Status != domain.ExecutionDispatchPublished {
			return nil
		}
		if dispatch.Kind != domain.ExecutionDispatchSynthetic {
			_, err = s.repository.AbandonExecutionDispatch(txCtx, dispatch.ID, "unsupported reconciliation kind", s.clock.Now())
			return err
		}
		work, err := s.repository.GetSyntheticDispatchWorkForUpdate(txCtx, dispatch.WorkID)
		if errors.Is(err, ports.ErrNotFound) {
			_, err = s.repository.AbandonExecutionDispatch(txCtx, dispatch.ID, "authoritative work missing during reconciliation", s.clock.Now())
			return err
		}
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if _, err := s.repository.SettleExecutionDispatch(txCtx, dispatch.ID, now); err != nil {
			return err
		}
		if work.Status != domain.SyntheticDispatchWorkPending {
			return nil
		}
		successor := domain.ExecutionDispatch{
			ID: successorID, Kind: dispatch.Kind, WorkID: dispatch.WorkID,
			AccountID: dispatch.AccountID, TenantPartitionID: dispatch.TenantPartitionID,
			Queue: dispatch.Queue, Status: domain.ExecutionDispatchPending,
			AvailableAt: now, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.repository.CreateExecutionDispatch(txCtx, successor); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("reconcile missing dispatch task: %w", err)
	}
	return created, nil
}

func (s *DispatchService) LogAged(ctx context.Context) error {
	items, err := s.repository.ListAgedExecutionDispatches(ctx, s.clock.Now().Add(-s.config.StaleAfter), s.config.BatchLimit)
	if err != nil {
		return err
	}
	for _, dispatch := range items {
		event := "dispatch_aged_pending"
		if dispatch.Status == domain.ExecutionDispatchPublished {
			event = "dispatch_stale_published"
		}
		s.logger.Warn("execution dispatch is stale",
			"event", event, "dispatch_id", dispatch.ID, "dispatch_kind", dispatch.Kind,
			"dispatch_status", dispatch.Status, "publish_attempts", dispatch.PublishAttempts,
			"age_ms", s.clock.Now().Sub(dispatch.UpdatedAt).Milliseconds())
	}
	return nil
}

func (s *DispatchService) Prune(ctx context.Context) (int64, error) {
	return s.repository.PruneTerminalExecutionDispatches(ctx, s.clock.Now().Add(-s.config.Retention), s.config.BatchLimit)
}
