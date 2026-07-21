package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestSyntheticDispatchCommitsAtomicallyAndSettlesOnce(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := newMutableClock(time.Now().UTC())
	service := newDispatchTestService(t, store, txm, clock)

	work, dispatch, err := service.CreateSynthetic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if work.Status != domain.SyntheticDispatchWorkPending || dispatch.Status != domain.ExecutionDispatchPending {
		t.Fatalf("created work/dispatch = %#v / %#v", work, dispatch)
	}

	const contenders = 20
	start := make(chan struct{})
	results := make(chan error, contenders)
	var group sync.WaitGroup
	for range contenders {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := service.Attempt(context.Background(), dispatch.ID)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent attempt: %v", err)
		}
	}
	settledWork, err := store.GetSyntheticDispatchWork(context.Background(), work.ID)
	if err != nil {
		t.Fatal(err)
	}
	settledDispatch, err := store.GetExecutionDispatch(context.Background(), dispatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settledWork.Status != domain.SyntheticDispatchWorkSettled || settledWork.SettlementCount != 1 || settledDispatch.Status != domain.ExecutionDispatchSettled {
		t.Fatalf("settled work/dispatch = %#v / %#v", settledWork, settledDispatch)
	}

	sharedWork := domain.SyntheticDispatchWork{
		ID: testID(t, domain.PrefixSyntheticDispatchWork), Status: domain.SyntheticDispatchWorkPending,
		CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
	}
	if err := store.CreateSyntheticDispatchWork(context.Background(), sharedWork); err != nil {
		t.Fatal(err)
	}
	dispatchIDs := make([]string, contenders)
	for index := range contenders {
		dispatchIDs[index] = testID(t, domain.PrefixExecutionDispatch)
	}
	createResults := make(chan error, contenders)
	for index := range contenders {
		go func() {
			createResults <- store.CreateExecutionDispatch(context.Background(), domain.ExecutionDispatch{
				ID: dispatchIDs[index], Kind: domain.ExecutionDispatchSynthetic, WorkID: sharedWork.ID,
				Queue: services.DefaultExecutionDispatchQueue, Status: domain.ExecutionDispatchPending,
				AvailableAt: clock.Now(), CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
			})
		}()
	}
	created := 0
	for range contenders {
		if err := <-createResults; err == nil {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("active dispatches created = %d, want 1", created)
	}

	rollbackWork := domain.SyntheticDispatchWork{
		ID: testID(t, domain.PrefixSyntheticDispatchWork), Status: domain.SyntheticDispatchWorkPending,
		CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
	}
	rollbackDispatch := domain.ExecutionDispatch{
		ID: testID(t, domain.PrefixExecutionDispatch), Kind: domain.ExecutionDispatchSynthetic,
		WorkID: rollbackWork.ID, Queue: services.DefaultExecutionDispatchQueue,
		Status: domain.ExecutionDispatchPending, AvailableAt: clock.Now(),
		CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
	}
	injected := errors.New("injected rollback")
	err = txm.WithTransaction(context.Background(), func(ctx context.Context) error {
		if err := store.CreateSyntheticDispatchWork(ctx, rollbackWork); err != nil {
			return err
		}
		if err := store.CreateExecutionDispatch(ctx, rollbackDispatch); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("rollback error = %v", err)
	}
	if _, err := store.GetSyntheticDispatchWork(context.Background(), rollbackWork.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("rolled back work error = %v", err)
	}
	if _, err := store.GetExecutionDispatch(context.Background(), rollbackDispatch.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("rolled back dispatch error = %v", err)
	}
}

func TestCancelledSyntheticAttemptLeavesDurableWorkRetryable(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	clock := newMutableClock(time.Now().UTC())
	cfg := services.DefaultDispatchConfig()
	cfg.SyntheticAttemptDelay = time.Second
	service, err := services.NewDispatchService(
		store, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock), cfg,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	work, dispatch, err := service.CreateSynthetic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Attempt(ctx, dispatch.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled attempt error = %v", err)
	}
	currentWork, err := store.GetSyntheticDispatchWork(context.Background(), work.ID)
	if err != nil || currentWork.Status != domain.SyntheticDispatchWorkPending || currentWork.SettlementCount != 0 {
		t.Fatalf("work after cancellation = %#v, %v", currentWork, err)
	}
	currentDispatch, err := store.GetExecutionDispatch(context.Background(), dispatch.ID)
	if err != nil || currentDispatch.Status != domain.ExecutionDispatchPending {
		t.Fatalf("dispatch after cancellation = %#v, %v", currentDispatch, err)
	}
}

func TestDispatchPublicationFencesCrashWindowsAndEarlyDelivery(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	clock := newMutableClock(time.Now().UTC())
	service := newDispatchTestService(t, store, NewTransactionManager(pool), clock)
	_, dispatch, err := service.CreateSynthetic(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	const publishers = 20
	claimResults := make(chan struct {
		claim domain.ExecutionDispatchClaim
		ok    bool
		err   error
	}, publishers)
	start := make(chan struct{})
	for index := range publishers {
		go func() {
			<-start
			claim, ok, err := service.ClaimNext(context.Background(), fmt.Sprintf("publisher-a-%d", index))
			claimResults <- struct {
				claim domain.ExecutionDispatchClaim
				ok    bool
				err   error
			}{claim: claim, ok: ok, err: err}
		}()
	}
	close(start)
	var claim domain.ExecutionDispatchClaim
	claimed := 0
	for range publishers {
		result := <-claimResults
		if result.err != nil {
			t.Fatalf("concurrent claim: %v", result.err)
		}
		if result.ok {
			claimed++
			claim = result.claim
		}
	}
	if claimed != 1 {
		t.Fatalf("publication claims = %d, want 1", claimed)
	}
	// Crash before CreateTask: an expired fenced claim is recoverable.
	clock.Advance(2 * time.Second)
	reclaimed, ok, err := service.ClaimNext(context.Background(), "publisher-b")
	if err != nil || !ok || reclaimed.Attempt <= claim.Attempt {
		t.Fatalf("reclaimed = %#v, %v, %v", reclaimed, ok, err)
	}

	tasks := newDispatchTestQueue()
	tasks.beforeReturn = func(id string) error {
		_, err := service.Attempt(context.Background(), id)
		return err
	}
	if err := service.PublishClaim(context.Background(), tasks, reclaimed); err != nil {
		t.Fatalf("publish with early delivery: %v", err)
	}
	current, err := store.GetExecutionDispatch(context.Background(), dispatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.ExecutionDispatchSettled {
		t.Fatalf("early-delivered status = %s, want settled", current.Status)
	}
	if _, err := store.MarkExecutionDispatchPublished(context.Background(), dispatch.ID, reclaimed.Owner, reclaimed.Attempt, tasks.name(dispatch.ID), clock.Now()); !errors.Is(err, ports.ErrDispatchLeaseLost) {
		t.Fatalf("late publisher error = %v, want lease lost", err)
	}

	// CreateTask succeeded but its response was lost. Deterministic naming makes
	// AlreadyExists converge on the next publication attempt.
	_, second, err := service.CreateSynthetic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondClaim, _, err := service.ClaimNext(context.Background(), "publisher-c")
	if err != nil {
		t.Fatal(err)
	}
	tasks.beforeReturn = nil
	if _, err := tasks.CreateTask(context.Background(), ports.ExecutionTask{DispatchID: second.ID, AvailableAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := service.PublishClaim(context.Background(), tasks, secondClaim); err != nil {
		t.Fatalf("AlreadyExists convergence: %v", err)
	}
	published, err := store.GetExecutionDispatch(context.Background(), second.ID)
	if err != nil || published.Status != domain.ExecutionDispatchPublished {
		t.Fatalf("published = %#v, error = %v", published, err)
	}
}

func TestDispatchReconciliationAndRetention(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	clock := newMutableClock(time.Now().UTC())
	service := newDispatchTestService(t, store, NewTransactionManager(pool), clock)
	tasks := newDispatchTestQueue()

	work, dispatch, err := service.CreateSynthetic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	claim, _, _ := service.ClaimNext(context.Background(), "publisher")
	if err := service.PublishClaim(context.Background(), tasks, claim); err != nil {
		t.Fatal(err)
	}
	clock.Advance(3 * time.Second)
	result, err := service.Reconcile(context.Background(), tasks)
	if err != nil || result.Existing != 1 {
		t.Fatalf("existing reconcile = %#v, %v", result, err)
	}
	tasks.delete(dispatch.ID)
	result, err = service.Reconcile(context.Background(), tasks)
	if err != nil || result.Succeeded != 1 {
		t.Fatalf("missing reconcile = %#v, %v", result, err)
	}
	old, err := store.GetExecutionDispatch(context.Background(), dispatch.ID)
	if err != nil || old.Status != domain.ExecutionDispatchSettled {
		t.Fatalf("old dispatch = %#v, %v", old, err)
	}
	active, err := store.ListAgedExecutionDispatches(context.Background(), clock.Now().Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	var successor domain.ExecutionDispatch
	for _, item := range active {
		if item.WorkID == work.ID {
			successor = item
		}
	}
	if successor.ID == "" || successor.Status != domain.ExecutionDispatchPending {
		t.Fatalf("successor = %#v", successor)
	}
	if _, err := service.Attempt(context.Background(), successor.ID); err != nil {
		t.Fatal(err)
	}

	// A missing task for already-settled authoritative work settles without a
	// successor.
	terminalWork, terminalDispatch, err := service.CreateSynthetic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	terminalClaim, _, _ := service.ClaimNext(context.Background(), "terminal-publisher")
	if err := service.PublishClaim(context.Background(), tasks, terminalClaim); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SettleSyntheticDispatchWork(context.Background(), terminalWork.ID, clock.Now()); err != nil {
		t.Fatal(err)
	}
	tasks.delete(terminalDispatch.ID)
	clock.Advance(3 * time.Second)
	result, err = service.Reconcile(context.Background(), tasks)
	if err != nil || result.Settled != 1 || result.Succeeded != 0 {
		t.Fatalf("terminal reconcile = %#v, %v", result, err)
	}
	terminalCurrent, err := store.GetExecutionDispatch(context.Background(), terminalDispatch.ID)
	if err != nil || terminalCurrent.Status != domain.ExecutionDispatchSettled {
		t.Fatalf("terminal dispatch = %#v, %v", terminalCurrent, err)
	}

	clock.Advance(5 * time.Second)
	pruned, err := service.Prune(context.Background())
	if err != nil || pruned != 2 {
		t.Fatalf("prune = %d, %v", pruned, err)
	}
	pruned, err = service.Prune(context.Background())
	if err != nil || pruned != 1 {
		t.Fatalf("second prune = %d, %v", pruned, err)
	}
	if _, err := store.GetSyntheticDispatchWork(context.Background(), work.ID); err != nil {
		t.Fatalf("retention deleted authoritative work: %v", err)
	}
}

func newDispatchTestService(t *testing.T, store *Store, txm *TransactionManager, clock *mutableClock) *services.DispatchService {
	t.Helper()
	cfg := services.DefaultDispatchConfig()
	cfg.PublicationLease = time.Second
	cfg.PublishRetryBase = time.Second
	cfg.PublishRetryMax = 2 * time.Second
	cfg.StaleAfter = 2 * time.Second
	cfg.Retention = 4 * time.Second
	cfg.BatchLimit = 2
	service, err := services.NewDispatchService(
		store, txm, clock, identity.NewUUIDv7Generator(clock), cfg,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type dispatchTestQueue struct {
	mu           sync.Mutex
	tasks        map[string]bool
	beforeReturn func(string) error
}

func newDispatchTestQueue() *dispatchTestQueue {
	return &dispatchTestQueue{tasks: make(map[string]bool)}
}

func (q *dispatchTestQueue) name(id string) string {
	return "projects/test/locations/test/queues/execution/tasks/" + id
}

func (q *dispatchTestQueue) CreateTask(_ context.Context, task ports.ExecutionTask) (string, error) {
	q.mu.Lock()
	name := q.name(task.DispatchID)
	already := q.tasks[name]
	q.tasks[name] = true
	callback := q.beforeReturn
	q.mu.Unlock()
	if callback != nil {
		if err := callback(task.DispatchID); err != nil {
			return "", err
		}
	}
	if already {
		return name, fmt.Errorf("%w: %s", ports.ErrTaskAlreadyExists, name)
	}
	return name, nil
}

func (q *dispatchTestQueue) TaskExists(_ context.Context, name string) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.tasks[name], nil
}

func (q *dispatchTestQueue) delete(id string) {
	q.mu.Lock()
	delete(q.tasks, q.name(id))
	q.mu.Unlock()
}

func (q *dispatchTestQueue) Close() error { return nil }
