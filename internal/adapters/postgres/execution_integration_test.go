package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestInvocationRunnerPollsQueuedWorkAfterRestart(t *testing.T) {
	pool, databaseURL := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	runtime := services.NewRuntimeService(store, txm, clock, ids)
	ack, err := runtime.Admit(context.Background(), runtimeAuth(account.ID), runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}

	restartedPool, err := OpenPool(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open restarted pool: %v", err)
	}
	t.Cleanup(restartedPool.Close)
	restartedStore := NewStore(restartedPool)
	execution := services.NewInvocationExecutionService(
		restartedStore, NewTransactionManager(restartedPool), clock, identity.NewUUIDv7Generator(clock),
	)
	config := engine.Config{
		Concurrency: 1, PollInterval: 10 * time.Millisecond,
		LeaseDuration: time.Second, HeartbeatInterval: 100 * time.Millisecond,
		ReaperInterval: 100 * time.Millisecond, ReaperBatchLimit: 10,
		DrainGrace: time.Second,
	}
	runner, err := engine.NewRunner(
		"restarted-runner", execution, postgresSyntheticExecutor{}, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)), config,
	)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	waitForInvocationStatus(t, restartedStore, ack.InvocationID, domain.InvocationCompleted)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestInvocationExecutionClaimNextSkipsContendedSessionsAcrossReplicas(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	const invocationCount = 24
	for index := range invocationCount {
		input := runtimeInput()
		input.SessionKey = pointerString(fmt.Sprintf("backlog-session-%02d", index))
		input.IdempotencyKey = fmt.Sprintf("backlog-request-%02d", index)
		if _, err := runtime.Admit(context.Background(), auth, input); err != nil {
			t.Fatalf("admit %d: %v", index, err)
		}
	}

	clock := identity.SystemClock{}
	start := make(chan struct{})
	results := make(chan struct {
		id  string
		err error
	}, invocationCount)
	var replicas sync.WaitGroup
	for replica := range 8 {
		replicas.Add(1)
		go func() {
			defer replicas.Done()
			execution := services.NewInvocationExecutionService(
				store, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock),
			)
			<-start
			for {
				claim, ok, err := execution.ClaimNext(
					context.Background(), fmt.Sprintf("replica-%d", replica), time.Minute,
				)
				if err != nil {
					results <- struct {
						id  string
						err error
					}{err: err}
					return
				}
				if !ok {
					return
				}
				results <- struct {
					id  string
					err error
				}{id: claim.Invocation.ID}
			}
		}()
	}
	close(start)
	go func() {
		replicas.Wait()
		close(results)
	}()

	claimed := make(map[string]struct{}, invocationCount)
	for result := range results {
		if result.err != nil {
			t.Fatalf("ClaimNext: %v", result.err)
		}
		if _, duplicate := claimed[result.id]; duplicate {
			t.Fatalf("Invocation %s was claimed twice", result.id)
		}
		claimed[result.id] = struct{}{}
	}
	if len(claimed) != invocationCount {
		t.Fatalf("claimed %d Invocations, want %d", len(claimed), invocationCount)
	}
}

func TestInvocationExecutionPollingSurvivesRestartAndClaimsFIFO(t *testing.T) {
	pool, databaseURL := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	runtime := services.NewRuntimeService(store, txm, clock, ids)
	auth := runtimeAuth(account.ID)
	want := make([]domain.Invocation, 0, 3)
	for index := range 3 {
		input := runtimeInput()
		input.SessionKey = pointerString(fmt.Sprintf("fifo-session-%d", index))
		input.IdempotencyKey = fmt.Sprintf("fifo-request-%d", index)
		ack, err := runtime.Admit(context.Background(), auth, input)
		if err != nil {
			t.Fatalf("admit %d: %v", index, err)
		}
		invocation, err := store.GetInvocation(context.Background(), ack.InvocationID)
		if err != nil {
			t.Fatalf("read %d: %v", index, err)
		}
		want = append(want, invocation)
	}

	restartedPool, err := OpenPool(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open restarted pool: %v", err)
	}
	t.Cleanup(restartedPool.Close)
	restartedStore := NewStore(restartedPool)
	execution := services.NewInvocationExecutionService(
		restartedStore, NewTransactionManager(restartedPool), clock, identity.NewUUIDv7Generator(clock),
	)
	for index := range want {
		claim, ok, err := execution.ClaimNext(context.Background(), "restarted-owner", time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim %d = %#v, ok = %v, error = %v", index, claim, ok, err)
		}
		if claim.Invocation.ID != want[index].ID {
			t.Fatalf("claim %d ID = %q, want %q", index, claim.Invocation.ID, want[index].ID)
		}
	}
	if _, ok, err := execution.ClaimNext(context.Background(), "restarted-owner", time.Minute); err != nil || ok {
		t.Fatalf("empty claim ok = %v, error = %v", ok, err)
	}
}

func TestInvocationExecutionClaimFenceAndSettlement(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ack, err := runtime.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := identity.SystemClock{}
	execution := services.NewInvocationExecutionService(
		store, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock),
	)

	start := make(chan struct{})
	results := make(chan struct {
		claim       domain.InvocationClaim
		disposition services.ClaimDisposition
		err         error
	}, 20)
	for index := range 20 {
		go func() {
			<-start
			claim, disposition, err := execution.ClaimExact(
				context.Background(), ack.InvocationID, fmt.Sprintf("owner-%02d", index), time.Second,
			)
			results <- struct {
				claim       domain.InvocationClaim
				disposition services.ClaimDisposition
				err         error
			}{claim: claim, disposition: disposition, err: err}
		}()
	}
	close(start)
	var winner domain.InvocationClaim
	claimed, alreadyHeld := 0, 0
	for range 20 {
		result := <-results
		if result.err != nil {
			t.Fatalf("claim: %v", result.err)
		}
		switch result.disposition {
		case services.Claimed:
			claimed++
			winner = result.claim
		case services.ClaimAlreadyHeld:
			alreadyHeld++
		default:
			t.Fatalf("claim disposition = %q", result.disposition)
		}
	}
	if claimed != 1 || alreadyHeld != 19 || winner.Attempt != 1 {
		t.Fatalf("claimed = %d, already held = %d, winner = %#v", claimed, alreadyHeld, winner)
	}

	stored, err := store.GetInvocation(context.Background(), ack.InvocationID)
	if err != nil || stored.Status != domain.InvocationRunning || stored.LeaseOwner == nil ||
		*stored.LeaseOwner != winner.Owner || stored.LeaseAttempt != 1 {
		t.Fatalf("stored claim = %#v, error = %v", stored, err)
	}
	assertPostgresCode(t, execError(
		context.Background(), pool,
		"UPDATE invocations SET lease_attempt = 0 WHERE id = $1", ack.InvocationID,
	), "23514")
	assertPostgresCode(t, execError(
		context.Background(), pool,
		"UPDATE invocations SET lease_owner = 'stolen-owner' WHERE id = $1", ack.InvocationID,
	), "23514")
	states, err := store.ListInvocationStates(context.Background(), ack.SessionID)
	if err != nil || len(states) != 2 || states[0].LeaseAttempt != 0 ||
		states[1].Status != domain.InvocationRunning || states[1].LeaseAttempt != 1 {
		t.Fatalf("claim states = %#v, error = %v", states, err)
	}

	wrongOwner := winner
	wrongOwner.Owner = "stale-owner"
	if _, err := execution.Renew(context.Background(), wrongOwner, time.Second); !errors.Is(err, ports.ErrLeaseLost) {
		t.Fatalf("wrong-owner renewal = %v, want lease lost", err)
	}
	oldAttempt := winner
	oldAttempt.Attempt = 0
	if err := execution.Settle(context.Background(), oldAttempt, completedResult()); !errors.Is(err, ports.ErrLeaseLost) {
		t.Fatalf("old-attempt settlement = %v, want lease lost", err)
	}
	renewedUntil, err := execution.Renew(context.Background(), winner, time.Second)
	if err != nil || !renewedUntil.After(winner.LeaseExpiresAt) {
		t.Fatalf("renewed until = %v, error = %v", renewedUntil, err)
	}
	if err := execution.Settle(context.Background(), winner, completedResult()); err != nil {
		t.Fatalf("settle: %v", err)
	}
	stored, err = store.GetInvocation(context.Background(), ack.InvocationID)
	if err != nil || stored.Status != domain.InvocationCompleted || stored.CompletedAt == nil ||
		stored.LeaseOwner != nil || stored.LeaseExpiresAt != nil || stored.LeaseAttempt != 1 {
		t.Fatalf("settled Invocation = %#v, error = %v", stored, err)
	}
	if err := execution.Settle(context.Background(), winner, completedResult()); !errors.Is(err, ports.ErrLeaseLost) {
		t.Fatalf("duplicate settlement = %v, want lease lost", err)
	}
	_, disposition, err := execution.ClaimExact(context.Background(), ack.InvocationID, "later-owner", time.Second)
	if err != nil || disposition != services.ClaimNotRunnable {
		t.Fatalf("terminal exact claim = %q, error = %v", disposition, err)
	}
}

func TestInvocationExecutionExpiredLeaseIsReapedAndSessionFreed(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ack, err := runtime.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := newMutableClock(time.Now().UTC())
	execution := services.NewInvocationExecutionService(
		store, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock),
	)
	claim, disposition, err := execution.ClaimExact(
		context.Background(), ack.InvocationID, "doomed-owner", time.Minute,
	)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim = %#v, disposition = %q, error = %v", claim, disposition, err)
	}
	clock.Advance(time.Minute + time.Nanosecond)
	if err := execution.Settle(context.Background(), claim, completedResult()); !errors.Is(err, ports.ErrLeaseLost) {
		t.Fatalf("expired settlement = %v, want lease lost", err)
	}
	reaped, err := execution.ReapExpired(context.Background(), 10)
	if err != nil || len(reaped) != 1 || reaped[0].ID != ack.InvocationID {
		t.Fatalf("reaped = %#v, error = %v", reaped, err)
	}
	stored, err := store.GetInvocation(context.Background(), ack.InvocationID)
	if err != nil || stored.Status != domain.InvocationFailed || stored.CompletedAt == nil ||
		stored.LeaseOwner != nil || stored.LeaseExpiresAt != nil {
		t.Fatalf("reaped Invocation = %#v, error = %v", stored, err)
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(stored.Error, &failure); err != nil || failure.Code != "execution_lost" {
		t.Fatalf("reaped error = %s, decode error = %v", stored.Error, err)
	}
	if _, err := execution.Renew(context.Background(), claim, time.Minute); !errors.Is(err, ports.ErrLeaseLost) {
		t.Fatalf("stale renewal = %v, want lease lost", err)
	}
	if err := execution.Settle(context.Background(), claim, completedResult()); !errors.Is(err, ports.ErrLeaseLost) {
		t.Fatalf("stale settlement = %v, want lease lost", err)
	}

	next := runtimeInput()
	next.SessionKey = nil
	next.SessionID = &ack.SessionID
	next.IdempotencyKey = "after-reap"
	next.Input.Content[0].Text = "try again"
	if nextAck, err := runtime.Admit(context.Background(), auth, next); err != nil || nextAck.SessionID != ack.SessionID {
		t.Fatalf("admit after reap = %#v, error = %v", nextAck, err)
	}
}

func TestInvocationExecutionRenewalWinsReaperScan(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ack, err := runtime.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := newMutableClock(time.Now().UTC())
	execution := services.NewInvocationExecutionService(
		store, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock),
	)
	claim, disposition, err := execution.ClaimExact(
		context.Background(), ack.InvocationID, "live-owner", time.Minute,
	)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim = %#v, disposition = %q, error = %v", claim, disposition, err)
	}
	clock.Advance(50 * time.Second)
	if _, err := execution.Renew(context.Background(), claim, time.Minute); err != nil {
		t.Fatalf("renew: %v", err)
	}
	clock.Advance(20 * time.Second) // Past the original expiry, before the renewed expiry.
	reaped, err := execution.ReapExpired(context.Background(), 10)
	if err != nil || len(reaped) != 0 {
		t.Fatalf("reaped live lease = %#v, error = %v", reaped, err)
	}
	stored, err := store.GetInvocation(context.Background(), ack.InvocationID)
	if err != nil || stored.Status != domain.InvocationRunning || stored.LeaseAttempt != 1 {
		t.Fatalf("renewed Invocation = %#v, error = %v", stored, err)
	}
}

func TestInvocationExecutionReaperContinuesAfterCandidateFailure(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	first, err := runtime.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit first: %v", err)
	}
	secondInput := runtimeInput()
	secondInput.SessionKey = pointerString("second-expired-session")
	secondInput.IdempotencyKey = "second-expired-request"
	second, err := runtime.Admit(context.Background(), auth, secondInput)
	if err != nil {
		t.Fatalf("admit second: %v", err)
	}
	clock := newMutableClock(time.Now().UTC())
	execution := services.NewInvocationExecutionService(
		store, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock),
	)
	for index, invocationID := range []string{first.InvocationID, second.InvocationID} {
		if _, disposition, err := execution.ClaimExact(
			context.Background(), invocationID, fmt.Sprintf("expired-owner-%d", index), time.Minute,
		); err != nil || disposition != services.Claimed {
			t.Fatalf("claim %d disposition = %q, error = %v", index, disposition, err)
		}
	}
	clock.Advance(time.Minute + time.Nanosecond)
	candidates, err := store.ListExpiredInvocationLeases(context.Background(), clock.Now(), 10)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("expired candidates = %#v, error = %v", candidates, err)
	}
	faults := &faultingExecutionStore{
		Store: store, failStatus: domain.InvocationFailed, failInvocationID: candidates[0].ID,
	}
	faulty := services.NewInvocationExecutionService(
		faults, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock),
	)
	reaped, err := faulty.ReapExpired(context.Background(), 10)
	if err == nil || len(reaped) != 1 || reaped[0].ID != candidates[1].ID {
		t.Fatalf("reaped = %#v, error = %v; want second candidate and joined error", reaped, err)
	}
	remaining, err := store.GetInvocation(context.Background(), candidates[0].ID)
	if err != nil || remaining.Status != domain.InvocationRunning {
		t.Fatalf("faulted candidate = %#v, error = %v", remaining, err)
	}
}

func TestInvocationExecutionTransitionsRollBackWithLifecycleState(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ack, err := runtime.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	faults := &faultingExecutionStore{Store: store, failStatus: domain.InvocationRunning}
	faulty := services.NewInvocationExecutionService(faults, NewTransactionManager(pool), clock, ids)
	if _, _, err := faulty.ClaimExact(context.Background(), ack.InvocationID, "owner", time.Minute); err == nil {
		t.Fatal("claim succeeded despite state fault")
	}
	assertInvocationExecutionShape(t, store, ack, domain.InvocationQueued, 0, 1)

	execution := services.NewInvocationExecutionService(store, NewTransactionManager(pool), clock, ids)
	claim, disposition, err := execution.ClaimExact(context.Background(), ack.InvocationID, "owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim = %#v, disposition = %q, error = %v", claim, disposition, err)
	}
	faults.failStatus = domain.InvocationCompleted
	if err := faulty.Settle(context.Background(), claim, completedResult()); err == nil {
		t.Fatal("settlement succeeded despite state fault")
	}
	assertInvocationExecutionShape(t, store, ack, domain.InvocationRunning, 1, 2)
	if err := execution.Settle(context.Background(), claim, completedResult()); err != nil {
		t.Fatalf("settle after rollback: %v", err)
	}
	assertInvocationExecutionShape(t, store, ack, domain.InvocationCompleted, 1, 3)
}

func TestAdmissionWakeOccursOnlyAfterFreshCommit(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	signaller := &countingSignaller{}
	runtime := services.NewRuntimeService(store, txm, clock, ids, services.WithWorkSignaller(signaller))
	input := runtimeInput()
	if _, err := runtime.Admit(context.Background(), runtimeAuth(account.ID), input); err != nil {
		t.Fatalf("fresh admit: %v", err)
	}
	if got := signaller.notifications.Load(); got != 1 {
		t.Fatalf("fresh notifications = %d, want 1", got)
	}
	if _, err := runtime.Admit(context.Background(), runtimeAuth(account.ID), input); err != nil {
		t.Fatalf("replay admit: %v", err)
	}
	if got := signaller.notifications.Load(); got != 1 {
		t.Fatalf("notifications after replay = %d, want 1", got)
	}
}

type faultingExecutionStore struct {
	*Store
	failStatus       domain.InvocationStatus
	failInvocationID string
}

func (s *faultingExecutionStore) AppendInvocationState(ctx context.Context, state domain.InvocationState) error {
	if state.Status == s.failStatus && (s.failInvocationID == "" || state.InvocationID == s.failInvocationID) {
		return errors.New("injected lifecycle-state failure")
	}
	return s.Store.AppendInvocationState(ctx, state)
}

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMutableClock(now time.Time) *mutableClock { return &mutableClock{now: now} }

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func completedResult() domain.InvocationExecutionResult {
	return domain.InvocationExecutionResult{Status: domain.InvocationCompleted}
}

func assertInvocationExecutionShape(
	t *testing.T,
	store *Store,
	ack services.InvocationAcknowledgement,
	status domain.InvocationStatus,
	attempt int64,
	stateCount int,
) {
	t.Helper()
	invocation, err := store.GetInvocation(context.Background(), ack.InvocationID)
	if err != nil || invocation.Status != status || invocation.LeaseAttempt != attempt {
		t.Fatalf("Invocation = %#v, error = %v; want status %s attempt %d", invocation, err, status, attempt)
	}
	states, err := store.ListInvocationStates(context.Background(), ack.SessionID)
	if err != nil || len(states) != stateCount {
		t.Fatalf("states = %#v, error = %v; want %d", states, err, stateCount)
	}
}

type countingSignaller struct{ notifications atomic.Int64 }

func (s *countingSignaller) Notify(_ context.Context, queue string) {
	if queue == ports.InvocationExecutionQueue {
		s.notifications.Add(1)
	}
}

func (*countingSignaller) Subscribe(context.Context, []string) ports.WorkSubscription {
	return noopSubscription{}
}

type noopSubscription struct{}

func (noopSubscription) Wait(context.Context, time.Duration) bool { return false }
func (noopSubscription) Close()                                   {}

type postgresSyntheticExecutor struct{}

func (postgresSyntheticExecutor) Execute(
	context.Context,
	domain.InvocationClaim,
) (domain.InvocationExecutionResult, error) {
	return completedResult(), nil
}

func waitForInvocationStatus(t *testing.T, store *Store, invocationID string, status domain.InvocationStatus) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		invocation, err := store.GetInvocation(context.Background(), invocationID)
		if err == nil && invocation.Status == status {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Invocation %s did not reach %s", invocationID, status)
}
