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
	Concurrency       int
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	ReaperInterval    time.Duration
	ReaperBatchLimit  int
	DrainGrace        time.Duration
}

func DefaultConfig() Config {
	return Config{
		Concurrency: 8, PollInterval: time.Second, LeaseDuration: 30 * time.Second,
		HeartbeatInterval: 10 * time.Second, ReaperInterval: 10 * time.Second,
		ReaperBatchLimit: 100, DrainGrace: 30 * time.Second,
	}
}

type Runner struct {
	owner     string
	ownership Ownership
	executor  ports.InvocationExecutor
	signaller ports.WorkSignaller
	logger    *slog.Logger
	config    Config
	inflight  atomic.Int64
}

func NewRunner(
	owner string,
	ownership Ownership,
	executor ports.InvocationExecutor,
	signaller ports.WorkSignaller,
	logger *slog.Logger,
	config Config,
) (*Runner, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, fmt.Errorf("engine owner is required")
	}
	if ownership == nil || executor == nil {
		return nil, fmt.Errorf("engine ownership and executor are required")
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		owner: owner, ownership: ownership, executor: executor,
		signaller: signaller, logger: logger, config: config,
	}, nil
}

func validateConfig(config Config) error {
	if config.Concurrency <= 0 {
		return fmt.Errorf("engine concurrency must be positive")
	}
	if config.PollInterval <= 0 || config.LeaseDuration <= 0 ||
		config.HeartbeatInterval <= 0 || config.ReaperInterval <= 0 ||
		config.DrainGrace <= 0 {
		return fmt.Errorf("engine intervals, lease duration, and drain grace must be positive")
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
	var subscription ports.WorkSubscription
	if r.signaller != nil {
		// Subscribe before the startup reap and first claim so a notification
		// between the database check and wait cannot be lost.
		subscription = r.signaller.Subscribe(ctx, []string{ports.InvocationExecutionQueue})
		defer subscription.Close()
	}

	executionCtx, cancelExecutions := context.WithCancel(context.Background())
	defer cancelExecutions()
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
	if err != nil {
		if ctx.Err() == nil {
			r.logger.Warn("Invocation lease scan failed; retrying", "error", err.Error())
		}
		return
	}
	for _, invocation := range reaped {
		r.logger.Warn("Invocation lease reaped",
			"invocation_id", invocation.ID, "lease_attempt", invocation.LeaseAttempt,
			"status", invocation.Status)
	}
}

type claimState struct {
	mu        sync.Mutex
	settled   bool
	leaseLost atomic.Bool
}

func (r *Runner) runClaim(parent context.Context, claim domain.InvocationClaim) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	state := &claimState{}
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		r.heartbeat(ctx, cancel, claim, state, stopHeartbeat)
	}()

	started := time.Now()
	result, err := r.executor.Execute(ctx, claim)
	if err != nil && ctx.Err() == nil {
		r.logger.Warn("Invocation executor failed",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
			"error", err.Error())
		result = internalFailureResult()
	}
	if err == nil && !validResult(result) {
		r.logger.Warn("Invocation executor returned invalid result",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt)
		result = internalFailureResult()
	}

	if ctx.Err() == nil && !state.leaseLost.Load() {
		r.settleLoop(ctx, cancel, claim, result, state)
	}
	close(stopHeartbeat)
	<-heartbeatDone

	if state.settled {
		r.logger.Info("Invocation settled",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
			"status", result.Status, "execution_latency_ms", time.Since(started).Milliseconds())
	}
}

func (r *Runner) heartbeat(
	ctx context.Context,
	cancel context.CancelFunc,
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
			r.loseLease(cancel, claim, state, "lease deadline passed")
			return
		}
		state.mu.Lock()
		if state.settled {
			state.mu.Unlock()
			return
		}
		renewCtx, renewCancel := context.WithDeadline(ctx, leaseExpiresAt)
		renewedUntil, err := r.ownership.Renew(renewCtx, claim, r.config.LeaseDuration)
		renewCancel()
		state.mu.Unlock()
		if err == nil {
			leaseExpiresAt = renewedUntil
			continue
		}
		if errors.Is(err, ports.ErrLeaseLost) {
			r.loseLease(cancel, claim, state, "fence rejected renewal")
			return
		}
		if ctx.Err() != nil {
			return
		}
		r.logger.Warn("Invocation lease renewal failed; retrying",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
			"error", err.Error(), "lease_expires_at", leaseExpiresAt)
		if !time.Now().UTC().Before(leaseExpiresAt) {
			r.loseLease(cancel, claim, state, "renewal failures exhausted lease")
			return
		}
	}
}

func (r *Runner) settleLoop(
	ctx context.Context,
	cancel context.CancelFunc,
	claim domain.InvocationClaim,
	result domain.InvocationExecutionResult,
	state *claimState,
) {
	for ctx.Err() == nil && !state.leaseLost.Load() {
		state.mu.Lock()
		err := r.ownership.Settle(ctx, claim, result)
		if err == nil {
			state.settled = true
		}
		state.mu.Unlock()
		if err == nil {
			return
		}
		if errors.Is(err, ports.ErrLeaseLost) {
			r.loseLease(cancel, claim, state, "fence rejected settlement")
			return
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
	cancel context.CancelFunc,
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
	cancel()
}

func internalFailureResult() domain.InvocationExecutionResult {
	payload, _ := json.Marshal(map[string]string{
		"code": "internal", "message": "The execution failed.",
	})
	return domain.InvocationExecutionResult{Status: domain.InvocationFailed, Error: payload}
}

func validResult(result domain.InvocationExecutionResult) bool {
	if result.Status == domain.InvocationCompleted {
		return len(result.Error) == 0
	}
	return result.Status == domain.InvocationFailed && len(result.Error) > 0 && json.Valid(result.Error)
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
