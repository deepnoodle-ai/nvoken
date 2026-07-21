// Package engine runs durably claimed Invocations independently of how work is
// delivered. Postgres ownership, not this process, grants execution authority.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type Ownership interface {
	ClaimNext(context.Context, string, time.Duration) (domain.InvocationClaim, bool, error)
	Renew(context.Context, domain.InvocationClaim, time.Duration) (time.Time, error)
	Settle(context.Context, domain.InvocationClaim, domain.InvocationExecutionResult) error
	ReapExpired(context.Context, int) ([]domain.Invocation, error)
}

type Config struct {
	Concurrency             int
	PollInterval            time.Duration
	LeaseDuration           time.Duration
	HeartbeatInterval       time.Duration
	ReaperInterval          time.Duration
	ReaperBatchLimit        int
	DrainGrace              time.Duration
	ExecutionSegmentCeiling time.Duration
	SettlementReserve       time.Duration
}

func DefaultConfig() Config {
	return Config{
		Concurrency: 8, PollInterval: time.Second, LeaseDuration: 30 * time.Second,
		HeartbeatInterval: 10 * time.Second, ReaperInterval: 10 * time.Second,
		ReaperBatchLimit: 100, DrainGrace: 30 * time.Second,
		ExecutionSegmentCeiling: 15 * time.Minute, SettlementReserve: 5 * time.Second,
	}
}

type Runner struct {
	owner         string
	ownership     Ownership
	executor      ports.InvocationExecutor
	signaller     ports.WorkSignaller
	cancellations ports.CancellationSignaller
	logger        *slog.Logger
	config        Config
	inflight      atomic.Int64
	cancelMu      sync.Mutex
	claimCancels  map[string]context.CancelCauseFunc
}

type RunnerOption func(*Runner)

func WithCancellationSignaller(signaller ports.CancellationSignaller) RunnerOption {
	return func(runner *Runner) { runner.cancellations = signaller }
}

func NewRunner(
	owner string,
	ownership Ownership,
	executor ports.InvocationExecutor,
	signaller ports.WorkSignaller,
	logger *slog.Logger,
	config Config,
	options ...RunnerOption,
) (*Runner, error) {
	config = normalizedConfig(config)
	if strings.TrimSpace(owner) == "" {
		return nil, fmt.Errorf("engine owner is required")
	}
	if ownership == nil || executor == nil {
		return nil, fmt.Errorf("engine ownership and executor are required")
	}
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	runner := &Runner{
		owner: owner, ownership: ownership, executor: executor,
		signaller: signaller, logger: logger, config: config,
		claimCancels: make(map[string]context.CancelCauseFunc),
	}
	for _, option := range options {
		if option != nil {
			option(runner)
		}
	}
	return runner, nil
}

func normalizedConfig(config Config) Config {
	defaults := DefaultConfig()
	if config.ExecutionSegmentCeiling == 0 && config.SettlementReserve == 0 {
		config.ExecutionSegmentCeiling = defaults.ExecutionSegmentCeiling
		config.SettlementReserve = defaults.SettlementReserve
	}
	return config
}

func ValidateConfig(config Config) error {
	if config.Concurrency <= 0 {
		return fmt.Errorf("engine concurrency must be positive")
	}
	if config.PollInterval <= 0 || config.LeaseDuration <= 0 ||
		config.HeartbeatInterval <= 0 || config.ReaperInterval <= 0 ||
		config.DrainGrace <= 0 {
		return fmt.Errorf("engine intervals, lease duration, and drain grace must be positive")
	}
	if config.ExecutionSegmentCeiling <= 0 || config.SettlementReserve <= 0 || config.SettlementReserve >= config.ExecutionSegmentCeiling {
		return fmt.Errorf("engine execution segment ceiling must exceed the positive settlement reserve")
	}
	if config.HeartbeatInterval >= config.LeaseDuration/2 {
		return fmt.Errorf("engine heartbeat interval must be less than half the lease duration")
	}
	if config.ReaperBatchLimit <= 0 {
		return fmt.Errorf("engine reaper batch limit must be positive")
	}
	return nil
}

func (r *Runner) Run(ctx context.Context) error {
	executionCtx, cancelExecutions := context.WithCancel(context.Background())
	defer cancelExecutions()
	var subscription ports.WorkSubscription
	if r.signaller != nil {
		// Subscribe before the startup reap and first claim so a notification
		// between the database check and wait cannot be lost.
		subscription = r.signaller.Subscribe(ctx, []string{ports.InvocationExecutionQueue})
		defer subscription.Close()
	}
	var cancellationSubscription ports.CancellationSubscription
	if r.cancellations != nil {
		cancellationSubscription = r.cancellations.SubscribeCancellations(executionCtx)
		go r.listenForCancellations(executionCtx, cancellationSubscription)
		defer cancellationSubscription.Close()
	}
	var workers sync.WaitGroup
	nextReap := time.Time{}

	for ctx.Err() == nil {
		now := time.Now().UTC()
		if nextReap.IsZero() || !now.Before(nextReap) {
			r.reap(ctx)
			nextReap = now.Add(r.config.ReaperInterval)
		}

		claimFailed := false
		for ctx.Err() == nil && r.inflight.Load() < int64(r.config.Concurrency) {
			claim, ok, err := r.ownership.ClaimNext(ctx, r.owner, r.config.LeaseDuration)
			if err != nil {
				if ctx.Err() == nil {
					r.logger.Warn("Invocation claim failed; retrying",
						"owner", r.owner, "error", err.Error())
				}
				claimFailed = true
				break
			}
			if !ok {
				break
			}
			r.inflight.Add(1)
			workers.Add(1)
			r.logger.Info("Invocation claimed",
				"invocation_id", claim.Invocation.ID, "owner", claim.Owner,
				"lease_attempt", claim.Attempt,
				"queue_age_ms", max(0, time.Since(claim.Invocation.CreatedAt).Milliseconds()))
			go func() {
				defer workers.Done()
				defer r.inflight.Add(-1)
				r.runClaim(executionCtx, claim)
			}()
		}

		wait := r.config.PollInterval
		untilReap := time.Until(nextReap)
		if untilReap > 0 && untilReap < wait {
			wait = untilReap
		}
		if claimFailed && wait > 100*time.Millisecond {
			wait = 100 * time.Millisecond
		}
		if subscription != nil {
			subscription.Wait(ctx, wait)
		} else if !waitFor(ctx, wait) {
			break
		}
	}

	r.logger.Info("Invocation engine draining",
		"owner", r.owner, "inflight", r.inflight.Load(),
		"drain_grace_ms", r.config.DrainGrace.Milliseconds())
	drained := make(chan struct{})
	go func() {
		workers.Wait()
		close(drained)
	}()
	timer := time.NewTimer(r.config.DrainGrace)
	defer timer.Stop()
	select {
	case <-drained:
		r.logger.Info("Invocation engine drained", "owner", r.owner)
		return nil
	case <-timer.C:
		cancelExecutions()
		<-drained
		r.logger.Info("Invocation engine drain grace expired; executions joined",
			"owner", r.owner)
		return nil
	}
}

func (r *Runner) reap(ctx context.Context) {
	reaped, err := r.ownership.ReapExpired(ctx, r.config.ReaperBatchLimit)
	for _, invocation := range reaped {
		fields := []any{
			"invocation_id", invocation.ID, "lease_attempt", invocation.LeaseAttempt,
			"status", invocation.Status,
		}
		fields = append(fields, failureLogFields(invocation.Error)...)
		r.logger.Warn("Invocation ownership or deadline reaped", fields...)
	}
	if err != nil {
		if ctx.Err() == nil {
			r.logger.Warn("Invocation lease scan failed; retrying", "error", err.Error())
		}
	}
}

type claimState struct {
	settled   atomic.Bool
	leaseLost atomic.Bool
}

var errCancellationWake = errors.New("Invocation cancellation notification received")
var errExecutionDeadline = errors.New("Invocation execution deadline reached")

func (r *Runner) runClaim(executorParent context.Context, claim domain.InvocationClaim) {
	leaseCtx, cancelLease := context.WithCancel(context.Background())
	defer cancelLease()
	executorCtx, cancelExecutorCause := context.WithCancelCause(executorParent)
	cancelExecutor := func() { cancelExecutorCause(context.Canceled) }
	defer cancelExecutor()
	r.registerClaimCancellation(claim.Invocation.ID, cancelExecutorCause)
	defer r.unregisterClaimCancellation(claim.Invocation.ID)
	state := &claimState{}
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		r.heartbeat(leaseCtx, cancelLease, cancelExecutor, claim, state, stopHeartbeat)
	}()

	started := time.Now()
	modelCtx := executorCtx
	cancelDeadline := func() {}
	if claim.Invocation.ExecutionDeadlineAt != nil {
		cutoff := claim.Invocation.ExecutionDeadlineAt.Add(-r.config.SettlementReserve)
		modelCtx, cancelDeadline = context.WithDeadlineCause(executorCtx, cutoff, errExecutionDeadline)
	}
	result, err := r.executor.Execute(modelCtx, claim)
	cancelDeadline()
	hasResult := err == nil
	if errors.Is(context.Cause(modelCtx), errExecutionDeadline) {
		// Once the model cutoff fires, its durable deadline wins even if a
		// provider or custom executor returns a late success. Preserve only
		// paired accounting evidence from a valid result.
		result = executionDeadlineResult(claim, result)
		hasResult = true
	} else if err != nil && executorCtx.Err() == nil {
		r.logger.Warn("Invocation executor failed",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
			"error", err.Error())
		result = internalFailureResult()
		hasResult = true
	}
	if err == nil && !validResult(result) {
		r.logger.Warn("Invocation executor returned invalid result",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt)
		result = internalFailureResult()
	}

	if hasResult && !state.leaseLost.Load() {
		// Drain cancellation targets model execution, not an already-produced
		// result. Settlement remains bounded and is cancelled by lease loss.
		settleDeadline := time.Now().Add(r.config.LeaseDuration)
		if claim.Invocation.ExecutionDeadlineAt != nil && claim.Invocation.ExecutionDeadlineAt.Before(settleDeadline) {
			settleDeadline = *claim.Invocation.ExecutionDeadlineAt
		}
		settleCtx, cancelSettle := context.WithDeadline(leaseCtx, settleDeadline)
		r.settleLoop(settleCtx, cancelLease, cancelExecutor, claim, result, state)
		cancelSettle()
	}
	close(stopHeartbeat)
	<-heartbeatDone

	if state.settled.Load() {
		fields := []any{
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
			"status", result.Status, "execution_latency_ms", time.Since(started).Milliseconds(),
		}
		fields = append(fields, failureLogFields(result.Error)...)
		r.logger.Info("Invocation settled", fields...)
	}
}

func (r *Runner) listenForCancellations(ctx context.Context, subscription ports.CancellationSubscription) {
	for ctx.Err() == nil {
		invocationID, ok := subscription.Wait(ctx, r.config.PollInterval)
		if !ok {
			continue
		}
		r.cancelMu.Lock()
		cancel := r.claimCancels[invocationID]
		r.cancelMu.Unlock()
		if cancel != nil {
			cancel(errCancellationWake)
			r.logger.Info("Invocation cancellation wake delivered", "invocation_id", invocationID)
		}
	}
}

func (r *Runner) registerClaimCancellation(invocationID string, cancel context.CancelCauseFunc) {
	r.cancelMu.Lock()
	r.claimCancels[invocationID] = cancel
	r.cancelMu.Unlock()
}

func (r *Runner) unregisterClaimCancellation(invocationID string) {
	r.cancelMu.Lock()
	delete(r.claimCancels, invocationID)
	r.cancelMu.Unlock()
}

func (r *Runner) heartbeat(
	ctx context.Context,
	cancelLease context.CancelFunc,
	cancelExecutor context.CancelFunc,
	claim domain.InvocationClaim,
	state *claimState,
	stop <-chan struct{},
) {
	leaseExpiresAt := claim.LeaseExpiresAt
	ticker := time.NewTicker(r.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
		}
		if !time.Now().UTC().Before(leaseExpiresAt) {
			r.loseLease(cancelLease, cancelExecutor, claim, state, "lease deadline passed")
			return
		}
		if state.settled.Load() {
			return
		}
		renewCtx, renewCancel := context.WithDeadline(ctx, leaseExpiresAt)
		renewedUntil, err := r.ownership.Renew(renewCtx, claim, r.config.LeaseDuration)
		renewCancel()
		if err == nil {
			leaseExpiresAt = renewedUntil
			continue
		}
		if errors.Is(err, ports.ErrLeaseLost) {
			if state.settled.Load() {
				return
			}
			r.loseLease(cancelLease, cancelExecutor, claim, state, "fence rejected renewal")
			return
		}
		if ctx.Err() != nil {
			return
		}
		r.logger.Warn("Invocation lease renewal failed; retrying",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
			"error", err.Error(), "lease_expires_at", leaseExpiresAt)
		if !time.Now().UTC().Before(leaseExpiresAt) {
			r.loseLease(cancelLease, cancelExecutor, claim, state, "renewal failures exhausted lease")
			return
		}
	}
}

func (r *Runner) settleLoop(
	ctx context.Context,
	cancelLease context.CancelFunc,
	cancelExecutor context.CancelFunc,
	claim domain.InvocationClaim,
	result domain.InvocationExecutionResult,
	state *claimState,
) {
	validationFallbackUsed := false
	for ctx.Err() == nil && !state.leaseLost.Load() {
		err := r.ownership.Settle(ctx, claim, result)
		if err == nil {
			state.settled.Store(true)
		}
		if err == nil {
			return
		}
		if errors.Is(err, ports.ErrLeaseLost) {
			r.loseLease(cancelLease, cancelExecutor, claim, state, "fence rejected settlement")
			return
		}
		if errors.Is(err, ports.ErrExecutionResultInvalid) {
			if validationFallbackUsed {
				r.logger.Error("Internal failure result rejected; awaiting lease reaper",
					"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt)
				return
			}
			validationFallbackUsed = true
			result = internalFailureResult()
			r.logger.Warn("Invocation execution result rejected; settling internal failure",
				"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt)
			continue
		}
		r.logger.Warn("Invocation settlement failed; retrying",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
			"error", err.Error())
		if !waitFor(ctx, min(r.config.HeartbeatInterval, 100*time.Millisecond)) {
			return
		}
	}
}

func (r *Runner) loseLease(
	cancelLease context.CancelFunc,
	cancelExecutor context.CancelFunc,
	claim domain.InvocationClaim,
	state *claimState,
	reason string,
) {
	if state.leaseLost.Swap(true) {
		return
	}
	r.logger.Warn("Invocation lease lost; cancelling execution",
		"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
		"reason", reason)
	cancelLease()
	cancelExecutor()
}

func internalFailureResult() domain.InvocationExecutionResult {
	payload, _ := json.Marshal(map[string]string{
		"code": "internal", "message": "The execution failed.",
	})
	return domain.InvocationExecutionResult{Status: domain.InvocationFailed, Error: payload}
}

func validResult(result domain.InvocationExecutionResult) bool {
	if result.Status == domain.InvocationCompleted {
		return len(result.Error) == 0 && len(result.AssistantMessages) > 0 &&
			result.Usage != nil && result.Provenance != nil
	}
	return result.Status == domain.InvocationFailed && len(result.Error) > 0 && json.Valid(result.Error) &&
		len(result.AssistantMessages) == 0 && ((result.Usage == nil && result.Provenance == nil) ||
		(result.Usage != nil && result.Provenance != nil))
}

func executionDeadlineResult(
	claim domain.InvocationClaim,
	evidence domain.InvocationExecutionResult,
) domain.InvocationExecutionResult {
	scope := "execution_segment"
	if claim.Invocation.ExecutionDeadlineScope != nil {
		scope = *claim.Invocation.ExecutionDeadlineScope
	}
	payload, _ := json.Marshal(map[string]any{
		"code": "deadline_exceeded", "message": "The execution deadline was exceeded.",
		"details": map[string]string{"scope": scope},
	})
	result := domain.InvocationExecutionResult{Status: domain.InvocationFailed, Error: payload}
	if validResult(evidence) && evidence.Usage != nil && evidence.Provenance != nil {
		result.Usage = evidence.Usage
		result.Provenance = evidence.Provenance
	}
	return result
}

func failureLogFields(payload []byte) []any {
	if len(payload) == 0 {
		return nil
	}
	var failure struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	if json.Unmarshal(payload, &failure) != nil || failure.Code == "" {
		return nil
	}
	fields := []any{"terminal_reason", failure.Code}
	for _, key := range []string{"scope", "kind"} {
		if value, ok := failure.Details[key].(string); ok && value != "" {
			fields = append(fields, key, value)
		}
	}
	return fields
}

func waitFor(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
