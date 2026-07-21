package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/worksignal"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func TestRunnerBoundsConcurrencyAndSettlesBacklog(t *testing.T) {
	ownership := newFakeOwnership(5, time.Second)
	gate := make(chan struct{})
	executor := &trackingExecutor{gate: gate}
	runner := newTestRunner(t, ownership, executor, nil, testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	done := runRunner(runner, ctx)
	waitUntil(t, time.Second, func() bool { return executor.started.Load() == 2 })
	if got := executor.maximum.Load(); got != 2 {
		t.Fatalf("maximum concurrency = %d, want 2", got)
	}
	close(gate)
	waitUntil(t, time.Second, func() bool { return ownership.settlementCount() == 5 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := executor.maximum.Load(); got > 2 {
		t.Fatalf("maximum concurrency = %d, exceeds 2", got)
	}
}

func TestRunnerHeartbeatRetriesTransientFailureAndLosesDefinitiveLease(t *testing.T) {
	t.Run("transient renewal recovers before deadline", func(t *testing.T) {
		ownership := newFakeOwnership(1, 200*time.Millisecond)
		ownership.renewErrors = []error{errors.New("temporary database failure"), nil}
		executor := &delayExecutor{delay: 140 * time.Millisecond}
		config := testConfig()
		config.LeaseDuration = 200 * time.Millisecond
		config.HeartbeatInterval = 40 * time.Millisecond
		runner := newTestRunner(t, ownership, executor, nil, config)
		ctx, cancel := context.WithCancel(context.Background())
		done := runRunner(runner, ctx)
		waitUntil(t, time.Second, func() bool { return ownership.settlementCount() == 1 })
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("run: %v", err)
		}
		if ownership.renewalCount() < 2 {
			t.Fatalf("renewals = %d, want at least 2", ownership.renewalCount())
		}
	})

	t.Run("definitive loss cancels and cannot settle", func(t *testing.T) {
		ownership := newFakeOwnership(1, time.Second)
		ownership.renewErrors = []error{ports.ErrLeaseLost}
		executor := &cancellationExecutor{cancelled: make(chan struct{})}
		config := testConfig()
		config.LeaseDuration = 200 * time.Millisecond
		config.HeartbeatInterval = 40 * time.Millisecond
		runner := newTestRunner(t, ownership, executor, nil, config)
		ctx, cancel := context.WithCancel(context.Background())
		done := runRunner(runner, ctx)
		select {
		case <-executor.cancelled:
		case <-time.After(time.Second):
			t.Fatal("executor was not cancelled after lease loss")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := ownership.settlementCount(); got != 0 {
			t.Fatalf("settlements = %d, want 0", got)
		}
	})
}

func TestRunnerWakeAndPollingFallback(t *testing.T) {
	t.Run("signal wakes subscribed runner", func(t *testing.T) {
		ownership := newFakeOwnership(0, time.Second)
		signaller := worksignal.NewInProcess()
		config := testConfig()
		config.PollInterval = 2 * time.Second
		runner := newTestRunner(t, ownership, immediateExecutor{}, signaller, config)
		ctx, cancel := context.WithCancel(context.Background())
		done := runRunner(runner, ctx)
		waitUntil(t, time.Second, func() bool { return ownership.reapCount() > 0 })
		ownership.addClaims(1, time.Second)
		started := time.Now()
		signaller.Notify(context.Background(), ports.InvocationExecutionQueue)
		waitUntil(t, 500*time.Millisecond, func() bool { return ownership.settlementCount() == 1 })
		if time.Since(started) >= time.Second {
			t.Fatal("signal did not beat the polling interval")
		}
		cancel()
		<-done
	})

	t.Run("dropped signal falls back to polling", func(t *testing.T) {
		ownership := newFakeOwnership(0, time.Second)
		config := testConfig()
		config.PollInterval = 20 * time.Millisecond
		runner := newTestRunner(t, ownership, immediateExecutor{}, worksignal.NewInProcess(), config)
		ctx, cancel := context.WithCancel(context.Background())
		done := runRunner(runner, ctx)
		waitUntil(t, time.Second, func() bool { return ownership.reapCount() > 0 })
		ownership.addClaims(1, time.Second)
		waitUntil(t, time.Second, func() bool { return ownership.settlementCount() == 1 })
		cancel()
		<-done
	})
}

func TestRunnerDrainsBeforeCancellingAndJoinsAfterGrace(t *testing.T) {
	t.Run("execution finishes during grace", func(t *testing.T) {
		ownership := newFakeOwnership(1, time.Second)
		release := make(chan struct{})
		executor := &trackingExecutor{gate: release}
		config := testConfig()
		config.Concurrency = 1
		config.DrainGrace = 200 * time.Millisecond
		runner := newTestRunner(t, ownership, executor, nil, config)
		ctx, cancel := context.WithCancel(context.Background())
		done := runRunner(runner, ctx)
		waitUntil(t, time.Second, func() bool { return executor.started.Load() == 1 })
		cancel()
		time.Sleep(20 * time.Millisecond)
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := ownership.settlementCount(); got != 1 {
			t.Fatalf("settlements = %d, want 1", got)
		}
	})

	t.Run("blocking execution is cancelled after grace", func(t *testing.T) {
		ownership := newFakeOwnership(2, time.Second)
		executor := &cancellationExecutor{started: make(chan struct{}, 1), cancelled: make(chan struct{})}
		config := testConfig()
		config.Concurrency = 1
		config.DrainGrace = 50 * time.Millisecond
		runner := newTestRunner(t, ownership, executor, nil, config)
		ctx, cancel := context.WithCancel(context.Background())
		done := runRunner(runner, ctx)
		select {
		case <-executor.started:
		case <-time.After(time.Second):
			t.Fatal("executor did not start")
		}
		startedDrain := time.Now()
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("run: %v", err)
		}
		if time.Since(startedDrain) < config.DrainGrace {
			t.Fatal("execution cancelled before drain grace")
		}
		select {
		case <-executor.cancelled:
		default:
			t.Fatal("blocking executor did not observe cancellation")
		}
		if got := ownership.settlementCount(); got != 0 {
			t.Fatalf("settlements = %d, want 0", got)
		}
		if got := ownership.queuedCount(); got != 1 {
			t.Fatalf("queued claims = %d, want 1 unclaimed after shutdown", got)
		}
	})
}

func TestRunnerLogsOperationalFieldsWithoutInvocationPayloads(t *testing.T) {
	ownership := newFakeOwnership(1, time.Second)
	ownership.mu.Lock()
	ownership.claims[0].Invocation.IdempotencyKey = "never-log-this-secret"
	ownership.claims[0].Invocation.Error = []byte(`{"message":"never-log-this-secret"}`)
	ownership.mu.Unlock()
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	runner, err := NewRunner("runner-1", ownership, immediateExecutor{}, nil, logger, testConfig())
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := runRunner(runner, ctx)
	waitUntil(t, time.Second, func() bool { return ownership.settlementCount() == 1 })
	cancel()
	<-done
	logs := output.String()
	if strings.Contains(logs, "never-log-this-secret") {
		t.Fatalf("logs contained Invocation payload: %s", logs)
	}
	if !strings.Contains(logs, "invocation_id") || !strings.Contains(logs, "lease_attempt") {
		t.Fatalf("logs omitted operational claim fields: %s", logs)
	}
}

type fakeOwnership struct {
	mu            sync.Mutex
	claims        []domain.InvocationClaim
	settlements   []domain.InvocationExecutionResult
	renewErrors   []error
	renewals      int
	reaps         int
	leaseDuration time.Duration
	nextID        int
}

func newFakeOwnership(count int, leaseDuration time.Duration) *fakeOwnership {
	fake := &fakeOwnership{leaseDuration: leaseDuration}
	fake.addClaims(count, leaseDuration)
	return fake
}

func (f *fakeOwnership) addClaims(count int, leaseDuration time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for range count {
		f.nextID++
		f.claims = append(f.claims, domain.InvocationClaim{
			Invocation: domain.Invocation{
				ID:     "invocation-" + time.Now().Add(time.Duration(f.nextID)).Format("150405.000000000"),
				Status: domain.InvocationRunning, CreatedAt: time.Now().Add(-time.Second),
			},
			Owner: "runner-1", Attempt: 1, LeaseExpiresAt: time.Now().Add(leaseDuration),
		})
	}
}

func (f *fakeOwnership) ClaimNext(_ context.Context, owner string, _ time.Duration) (domain.InvocationClaim, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.claims) == 0 {
		return domain.InvocationClaim{}, false, nil
	}
	claim := f.claims[0]
	f.claims = f.claims[1:]
	claim.Owner = owner
	return claim, true, nil
}

func (f *fakeOwnership) Renew(_ context.Context, _ domain.InvocationClaim, leaseDuration time.Duration) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewals++
	if len(f.renewErrors) > 0 {
		err := f.renewErrors[0]
		f.renewErrors = f.renewErrors[1:]
		if err != nil {
			return time.Time{}, err
		}
	}
	return time.Now().Add(leaseDuration), nil
}

func (f *fakeOwnership) Settle(_ context.Context, _ domain.InvocationClaim, result domain.InvocationExecutionResult) error {
	f.mu.Lock()
	f.settlements = append(f.settlements, result)
	f.mu.Unlock()
	return nil
}

func (f *fakeOwnership) ReapExpired(context.Context, int) ([]domain.Invocation, error) {
	f.mu.Lock()
	f.reaps++
	f.mu.Unlock()
	return nil, nil
}

func (f *fakeOwnership) settlementCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.settlements)
}

func (f *fakeOwnership) renewalCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.renewals
}

func (f *fakeOwnership) reapCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reaps
}

func (f *fakeOwnership) queuedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.claims)
}

type trackingExecutor struct {
	gate    <-chan struct{}
	started atomic.Int64
	active  atomic.Int64
	maximum atomic.Int64
}

func (e *trackingExecutor) Execute(ctx context.Context, _ domain.InvocationClaim) (domain.InvocationExecutionResult, error) {
	e.started.Add(1)
	active := e.active.Add(1)
	defer e.active.Add(-1)
	for {
		maximum := e.maximum.Load()
		if active <= maximum || e.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return domain.InvocationExecutionResult{}, ctx.Err()
	case <-e.gate:
		return completed(), nil
	}
}

type delayExecutor struct{ delay time.Duration }

func (e *delayExecutor) Execute(ctx context.Context, _ domain.InvocationClaim) (domain.InvocationExecutionResult, error) {
	timer := time.NewTimer(e.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return domain.InvocationExecutionResult{}, ctx.Err()
	case <-timer.C:
		return completed(), nil
	}
}

type cancellationExecutor struct {
	started   chan struct{}
	cancelled chan struct{}
	once      sync.Once
}

func (e *cancellationExecutor) Execute(ctx context.Context, _ domain.InvocationClaim) (domain.InvocationExecutionResult, error) {
	if e.started != nil {
		select {
		case e.started <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	e.once.Do(func() { close(e.cancelled) })
	return domain.InvocationExecutionResult{}, ctx.Err()
}

type immediateExecutor struct{}

func (immediateExecutor) Execute(context.Context, domain.InvocationClaim) (domain.InvocationExecutionResult, error) {
	return completed(), nil
}

func completed() domain.InvocationExecutionResult {
	return domain.InvocationExecutionResult{Status: domain.InvocationCompleted}
}

func testConfig() Config {
	return Config{
		Concurrency: 2, PollInterval: 10 * time.Millisecond,
		LeaseDuration: time.Second, HeartbeatInterval: 100 * time.Millisecond,
		ReaperInterval: 20 * time.Millisecond, ReaperBatchLimit: 10,
		DrainGrace: 100 * time.Millisecond,
	}
}

func newTestRunner(
	t *testing.T,
	ownership Ownership,
	executor ports.InvocationExecutor,
	signaller ports.WorkSignaller,
	config Config,
) *Runner {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner, err := NewRunner("runner-1", ownership, executor, signaller, logger, config)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	return runner
}

func runRunner(runner *Runner, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	return done
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
