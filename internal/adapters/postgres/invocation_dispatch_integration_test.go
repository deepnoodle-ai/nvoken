package postgres

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

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	dispatchruntime "github.com/deepnoodle-ai/nvoken/internal/dispatch"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestInvocationAdmissionCreatesDispatchOnlyInCloudTasksMode(t *testing.T) {
	for _, test := range []struct {
		name string
		mode services.InvocationExecutionMode
		want int
	}{
		{name: "embedded", mode: services.InvocationExecutionEmbedded, want: 0},
		{name: "cloud_tasks", mode: services.InvocationExecutionCloudTasks, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool, _ := testDatabase(t, true)
			store := NewStore(pool)
			clock := identity.SystemClock{}
			ids := identity.NewUUIDv7Generator(clock)
			txm := NewTransactionManager(pool)
			account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
			if err != nil {
				t.Fatal(err)
			}
			runtime := services.NewRuntimeService(store, txm, clock, ids,
				services.WithInvocationExecutionMode(test.mode, services.DefaultExecutionDispatchQueue))
			ack, err := runtime.Admit(context.Background(), runtimeAuth(account.ID), runtimeInput())
			if err != nil {
				t.Fatal(err)
			}
			items, err := store.ListAgedExecutionDispatches(context.Background(), time.Now().Add(time.Hour), 10)
			if err != nil || len(items) != test.want {
				t.Fatalf("dispatches = %#v, error = %v", items, err)
			}
			if test.want == 1 {
				dispatch := items[0]
				if dispatch.Kind != domain.ExecutionDispatchInvocation || dispatch.WorkID != ack.InvocationID ||
					dispatch.AccountID == nil || *dispatch.AccountID != account.ID || dispatch.TenantPartitionID == nil {
					t.Fatalf("Invocation dispatch = %#v", dispatch)
				}
				if _, err := runtime.Admit(context.Background(), runtimeAuth(account.ID), runtimeInput()); err != nil {
					t.Fatal(err)
				}
				items, _ = store.ListAgedExecutionDispatches(context.Background(), time.Now().Add(time.Hour), 10)
				if len(items) != 1 {
					t.Fatalf("dispatches after idempotent replay = %d", len(items))
				}
			}
		})
	}
}

func TestInvocationDispatchAttemptIsRequestBoundAndDuplicateSafe(t *testing.T) {
	generator := newBlockingDispatchGenerator()
	fixture := newInvocationDispatchFixture(t, generator)

	first := make(chan error, 1)
	go func() {
		_, err := fixture.attempts.Attempt(context.Background(), fixture.dispatch.ID)
		first <- err
	}()
	<-generator.started

	const duplicates = 19
	results := make(chan error, duplicates)
	for range duplicates {
		go func() {
			_, err := fixture.attempts.Attempt(context.Background(), fixture.dispatch.ID)
			results <- err
		}()
	}
	for range duplicates {
		if err := <-results; !errors.Is(err, ports.ErrDispatchAttemptActive) {
			t.Fatalf("live duplicate error = %v", err)
		}
	}
	close(generator.release)
	if err := <-first; err != nil {
		t.Fatalf("winning attempt: %v", err)
	}
	if got := generator.calls.Load(); got != 1 {
		t.Fatalf("generation calls = %d, want 1", got)
	}
	if _, err := fixture.attempts.Attempt(context.Background(), fixture.dispatch.ID); err != nil {
		t.Fatalf("terminal redelivery: %v", err)
	}
	assertInvocationDispatchTerminal(t, fixture.store, fixture.ack, fixture.dispatch.ID, domain.InvocationCompleted)
}

func TestInvocationDispatchSemanticFailureAcknowledgesDurably(t *testing.T) {
	generator := &postgresModelGenerator{err: errors.New("provider unavailable")}
	fixture := newInvocationDispatchFixture(t, generator)
	outcome, err := fixture.attempts.Attempt(context.Background(), fixture.dispatch.ID)
	if err != nil || outcome != services.DispatchAttemptSettled {
		t.Fatalf("semantic failure outcome = %q, error = %v", outcome, err)
	}
	assertInvocationDispatchTerminal(t, fixture.store, fixture.ack, fixture.dispatch.ID, domain.InvocationFailed)
}

func TestInvocationDispatchCancellationAndLostOwnerDoNotReplay(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		generator := newBlockingDispatchGenerator()
		fixture := newInvocationDispatchFixture(t, generator)
		done := make(chan error, 1)
		go func() {
			_, err := fixture.attempts.Attempt(context.Background(), fixture.dispatch.ID)
			done <- err
		}()
		<-generator.started
		if _, err := fixture.runtime.CancelInvocation(context.Background(), fixture.auth, fixture.ack.InvocationID); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatalf("cancelled attempt: %v", err)
		}
		if got := generator.calls.Load(); got != 1 {
			t.Fatalf("generation calls = %d, want 1", got)
		}
		assertInvocationDispatchTerminal(t, fixture.store, fixture.ack, fixture.dispatch.ID, domain.InvocationCancelled)
	})

	t.Run("expired owner", func(t *testing.T) {
		generator := &postgresModelGenerator{}
		fixture := newInvocationDispatchFixture(t, generator)
		ownership := services.NewInvocationExecutionService(fixture.store, fixture.txm, fixture.clock, fixture.ids,
			services.WithExecutionSegmentCeiling(time.Second))
		claim, disposition, err := ownership.ClaimExact(context.Background(), fixture.ack.InvocationID, "lost-owner", 30*time.Millisecond)
		if err != nil || disposition != services.Claimed {
			t.Fatalf("claim = %#v, disposition = %q, error = %v", claim, disposition, err)
		}
		time.Sleep(40 * time.Millisecond)
		if _, err := fixture.attempts.Attempt(context.Background(), fixture.dispatch.ID); !errors.Is(err, ports.ErrDispatchAttemptActive) {
			t.Fatalf("expired-but-unreaped delivery error = %v", err)
		}
		reaped, err := ownership.ReapExpired(context.Background(), 10)
		if err != nil || len(reaped) != 1 {
			t.Fatalf("reaped = %#v, error = %v", reaped, err)
		}
		if _, err := fixture.attempts.Attempt(context.Background(), fixture.dispatch.ID); err != nil {
			t.Fatalf("delivery after reaper: %v", err)
		}
		if len(generator.Requests()) != 1 {
			t.Fatalf("replacement generation calls = %d, want 1", len(generator.Requests()))
		}
		assertInvocationDispatchTerminal(t, fixture.store, fixture.ack, fixture.dispatch.ID, domain.InvocationCompleted)
	})
}

func TestInvocationDispatchSettlementFailureStaysRetryable(t *testing.T) {
	fixture := newInvocationDispatchFixture(t, &postgresModelGenerator{})
	faults := &faultingExecutionStore{Store: fixture.store, failSettlement: true}
	ownership := services.NewInvocationExecutionService(faults, fixture.txm, fixture.clock, fixture.ids,
		services.WithExecutionSegmentCeiling(time.Second))
	cfg := dispatchEngineConfig()
	cfg.LeaseDuration = 80 * time.Millisecond
	cfg.HeartbeatInterval = 20 * time.Millisecond
	attempts, err := dispatchruntime.NewAttemptService(
		fixture.dispatches, ownership,
		services.NewGenerationExecutor(fixture.store, &postgresModelGenerator{}, nil),
		fixture.store, fixture.txm, fixture.clock, "faulted-executor", cfg, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attempts.Attempt(context.Background(), fixture.dispatch.ID); !errors.Is(err, ports.ErrDispatchAttemptPending) {
		t.Fatalf("faulted settlement error = %v", err)
	}
	invocation, _ := fixture.store.GetInvocation(context.Background(), fixture.ack.InvocationID)
	dispatch, _ := fixture.store.GetExecutionDispatch(context.Background(), fixture.dispatch.ID)
	if invocation.Status != domain.InvocationRunning || dispatch.Status.Terminal() {
		t.Fatalf("faulted durable state = %#v / %#v", invocation, dispatch)
	}
}

func TestInvocationSettlementToleratesMatchingTerminalDispatch(t *testing.T) {
	for _, terminal := range []domain.ExecutionDispatchStatus{
		domain.ExecutionDispatchSettled,
		domain.ExecutionDispatchAbandoned,
	} {
		t.Run(string(terminal), func(t *testing.T) {
			fixture := newInvocationDispatchFixture(t, &postgresModelGenerator{})
			ownership := services.NewInvocationExecutionService(fixture.store, fixture.txm, fixture.clock, fixture.ids,
				services.WithExecutionSegmentCeiling(time.Second))
			claim, disposition, err := ownership.ClaimExact(context.Background(), fixture.ack.InvocationID, "terminal-dispatch-owner", time.Minute)
			if err != nil || disposition != services.Claimed {
				t.Fatalf("claim disposition/error = %q/%v", disposition, err)
			}
			switch terminal {
			case domain.ExecutionDispatchSettled:
				_, err = fixture.store.SettleExecutionDispatch(context.Background(), fixture.dispatch.ID, fixture.clock.Now())
			case domain.ExecutionDispatchAbandoned:
				_, err = fixture.store.AbandonExecutionDispatch(context.Background(), fixture.dispatch.ID, "test terminal dispatch", fixture.clock.Now())
			}
			if err != nil {
				t.Fatal(err)
			}

			if err := ownership.SettleDispatch(context.Background(), claim, completedResult(), fixture.dispatch.ID); err != nil {
				t.Fatalf("settle with %s dispatch: %v", terminal, err)
			}
			invocation, _ := fixture.store.GetInvocation(context.Background(), fixture.ack.InvocationID)
			dispatch, _ := fixture.store.GetExecutionDispatch(context.Background(), fixture.dispatch.ID)
			messages, _ := fixture.store.ListSessionMessages(context.Background(), fixture.ack.SessionID)
			if invocation.Status != domain.InvocationCompleted || dispatch.Status != terminal || len(messages) != 2 {
				t.Fatalf("terminal settlement state = %#v / %#v / %d messages", invocation, dispatch, len(messages))
			}
		})
	}
}

func TestInvocationSettlementRejectsMismatchedDispatch(t *testing.T) {
	fixture := newInvocationDispatchFixture(t, &postgresModelGenerator{})
	ownership := services.NewInvocationExecutionService(fixture.store, fixture.txm, fixture.clock, fixture.ids,
		services.WithExecutionSegmentCeiling(time.Second))
	claim, disposition, err := ownership.ClaimExact(context.Background(), fixture.ack.InvocationID, "mismatched-dispatch-owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim disposition/error = %q/%v", disposition, err)
	}
	_, mismatched, err := fixture.dispatches.CreateSynthetic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := ownership.SettleDispatch(context.Background(), claim, completedResult(), mismatched.ID); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched dispatch settlement error = %v", err)
	}
	invocation, _ := fixture.store.GetInvocation(context.Background(), fixture.ack.InvocationID)
	messages, _ := fixture.store.ListSessionMessages(context.Background(), fixture.ack.SessionID)
	if invocation.Status != domain.InvocationRunning || len(messages) != 1 {
		t.Fatalf("mismatched settlement changed Invocation: %#v / %d messages", invocation, len(messages))
	}
}

func TestInvocationDispatchModeRaceExecutesOnceAndConverges(t *testing.T) {
	generator := newBlockingDispatchGenerator()
	fixture := newInvocationDispatchFixture(t, generator)
	ownership := services.NewInvocationExecutionService(fixture.store, fixture.txm, fixture.clock, fixture.ids,
		services.WithExecutionSegmentCeiling(time.Second))
	cfg := dispatchEngineConfig()
	cfg.PollInterval = 5 * time.Millisecond
	runner, err := engine.NewRunner(
		"embedded-race", ownership,
		services.NewGenerationExecutor(fixture.store, generator, slog.New(slog.NewTextHandler(io.Discard, nil))),
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runner.Run(ctx) }()
	attemptDone := make(chan error, 1)
	go func() {
		_, err := fixture.attempts.Attempt(context.Background(), fixture.dispatch.ID)
		attemptDone <- err
	}()
	<-generator.started
	close(generator.release)
	if err := <-attemptDone; err != nil && !errors.Is(err, ports.ErrDispatchAttemptActive) {
		t.Fatalf("request-bound racer: %v", err)
	}
	waitForInvocationStatus(t, fixture.store, fixture.ack.InvocationID, domain.InvocationCompleted)
	if _, err := fixture.attempts.Attempt(context.Background(), fixture.dispatch.ID); err != nil {
		t.Fatalf("converging redelivery: %v", err)
	}
	cancel()
	if err := <-runnerDone; err != nil {
		t.Fatalf("embedded racer: %v", err)
	}
	if got := generator.calls.Load(); got != 1 {
		t.Fatalf("generation calls = %d, want 1", got)
	}
	assertInvocationDispatchTerminal(t, fixture.store, fixture.ack, fixture.dispatch.ID, domain.InvocationCompleted)
}

func TestInvocationDispatchRepairCoversModeEnablement(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		t.Fatal(err)
	}
	runtime := services.NewRuntimeService(store, txm, clock, ids)
	ack, err := runtime.Admit(context.Background(), runtimeAuth(account.ID), runtimeInput())
	if err != nil {
		t.Fatal(err)
	}
	dispatches := newDispatchServiceForInvocationTest(t, store, txm, clock, ids)

	const contenders = 20
	var repaired atomic.Int64
	var group sync.WaitGroup
	for range contenders {
		group.Add(1)
		go func() {
			defer group.Done()
			count, err := dispatches.RepairQueuedInvocations(context.Background(), 1)
			if err != nil {
				t.Errorf("repair: %v", err)
			}
			repaired.Add(int64(count))
		}()
	}
	group.Wait()
	if repaired.Load() != 1 {
		t.Fatalf("repairs = %d, want 1", repaired.Load())
	}
	items, _ := store.ListAgedExecutionDispatches(context.Background(), time.Now().Add(time.Hour), 10)
	if len(items) != 1 || items[0].WorkID != ack.InvocationID {
		t.Fatalf("repaired dispatches = %#v", items)
	}
}

func TestInvocationDispatchReconcilesMissingTasksFromAuthoritativeState(t *testing.T) {
	t.Run("queued gets one successor", func(t *testing.T) {
		fixture := newInvocationReconcileFixture(t)
		fixture.publishAndLoseTask(t)

		result, err := fixture.dispatches.Reconcile(context.Background(), fixture.tasks)
		if err != nil || result.Succeeded != 1 || result.Settled != 0 || result.Retained != 0 {
			t.Fatalf("queued reconcile = %#v, error = %v", result, err)
		}
		old, _ := fixture.store.GetExecutionDispatch(context.Background(), fixture.dispatch.ID)
		active := activeInvocationDispatch(t, fixture.store, fixture.ack.InvocationID)
		if old.Status != domain.ExecutionDispatchSettled || active.ID == old.ID || active.Status != domain.ExecutionDispatchPending {
			t.Fatalf("old/successor dispatches = %#v / %#v", old, active)
		}
	})

	t.Run("running is retained for the reaper", func(t *testing.T) {
		fixture := newInvocationReconcileFixture(t)
		fixture.publishAndLoseTask(t)
		ownership := services.NewInvocationExecutionService(fixture.store, fixture.txm, fixture.clock, fixture.ids,
			services.WithExecutionSegmentCeiling(time.Second))
		if _, disposition, err := ownership.ClaimExact(context.Background(), fixture.ack.InvocationID, "lost-executor", time.Minute); err != nil || disposition != services.Claimed {
			t.Fatalf("claim disposition/error = %q/%v", disposition, err)
		}
		var logs bytes.Buffer
		cfg := services.DefaultDispatchConfig()
		cfg.PublicationLease = time.Second
		cfg.StaleAfter = 2 * time.Second
		cfg.Retention = 4 * time.Second
		observability, err := services.NewDispatchService(
			fixture.store, fixture.txm, fixture.clock, fixture.ids, cfg,
			slog.New(slog.NewTextHandler(&logs, nil)),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := observability.LogAged(context.Background()); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(logs.String(), "dispatch_stale_published") {
			t.Fatalf("live Invocation produced stale-dispatch warning: %s", logs.String())
		}
		fixture.clock.Advance(2 * time.Minute)
		if err := observability.LogAged(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(logs.String(), "dispatch_stale_published") {
			t.Fatalf("expired Invocation did not produce stale-dispatch warning: %s", logs.String())
		}

		result, err := fixture.dispatches.Reconcile(context.Background(), fixture.tasks)
		if err != nil || result.Retained != 1 || result.Succeeded != 0 || result.Settled != 0 {
			t.Fatalf("running reconcile = %#v, error = %v", result, err)
		}
		current, _ := fixture.store.GetExecutionDispatch(context.Background(), fixture.dispatch.ID)
		if current.Status != domain.ExecutionDispatchPublished {
			t.Fatalf("running dispatch status = %s, want published", current.Status)
		}
	})

	t.Run("terminal settles without successor", func(t *testing.T) {
		fixture := newInvocationReconcileFixture(t)
		fixture.publishAndLoseTask(t)
		if err := fixture.txm.WithTransaction(context.Background(), func(ctx context.Context) error {
			if _, err := fixture.store.GetSessionForUpdate(ctx, fixture.ack.SessionID); err != nil {
				return err
			}
			if _, err := fixture.store.GetInvocationForUpdate(ctx, fixture.ack.InvocationID); err != nil {
				return err
			}
			revision, err := fixture.store.ReserveLifecycleRevision(ctx, fixture.ack.SessionID)
			if err != nil {
				return err
			}
			_, err = fixture.store.CancelInvocation(ctx, fixture.ack.InvocationID, revision, fixture.clock.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}

		result, err := fixture.dispatches.Reconcile(context.Background(), fixture.tasks)
		if err != nil || result.Settled != 1 || result.Succeeded != 0 || result.Retained != 0 {
			t.Fatalf("terminal reconcile = %#v, error = %v", result, err)
		}
		current, _ := fixture.store.GetExecutionDispatch(context.Background(), fixture.dispatch.ID)
		if current.Status != domain.ExecutionDispatchSettled {
			t.Fatalf("terminal dispatch status = %s, want settled", current.Status)
		}
	})
}

func TestRecoveredInvocationConvergesAcrossOriginalAndSuccessorDeliveries(t *testing.T) {
	fixture := newInvocationReconcileFixture(t)
	fixture.publishAndLoseTask(t)
	ctx := context.Background()
	ownership := services.NewInvocationExecutionService(
		fixture.store,
		fixture.txm,
		fixture.clock,
		fixture.ids,
		services.WithExecutionSegmentCeiling(10*time.Second),
	)
	if _, disposition, err := ownership.ClaimExact(
		ctx,
		fixture.ack.InvocationID,
		"lost-dispatch-owner",
		time.Second,
	); err != nil || disposition != services.Claimed {
		t.Fatalf("lost claim disposition = %q, error = %v", disposition, err)
	}
	fixture.clock.Advance(2 * time.Second)
	recovered, err := ownership.ReapExpired(ctx, 10)
	if err != nil || len(recovered) != 1 || recovered[0].Status != domain.InvocationQueued {
		t.Fatalf("recovered = %#v, error = %v", recovered, err)
	}
	result, err := fixture.dispatches.Reconcile(ctx, fixture.tasks)
	if err != nil || result.Succeeded != 1 {
		t.Fatalf("reconcile = %#v, error = %v", result, err)
	}
	successor := activeInvocationDispatch(t, fixture.store, fixture.ack.InvocationID)
	if successor.ID == fixture.dispatch.ID {
		t.Fatal("reconcile did not create a successor dispatch")
	}
	generator := &postgresModelGenerator{}
	config := dispatchEngineConfig()
	config.LeaseDuration = time.Second
	config.HeartbeatInterval = 100 * time.Millisecond
	attempts, err := dispatchruntime.NewAttemptService(
		fixture.dispatches,
		ownership,
		services.NewGenerationExecutor(
			fixture.store,
			generator,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		),
		fixture.store,
		fixture.txm,
		fixture.clock,
		"replacement-dispatch-owner",
		config,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("configure attempts: %v", err)
	}
	type deliveryResult struct {
		dispatchID string
		outcome    services.DispatchAttemptOutcome
		err        error
	}
	results := make(chan deliveryResult, 2)
	for _, dispatchID := range []string{fixture.dispatch.ID, successor.ID} {
		go func() {
			outcome, err := attempts.Attempt(ctx, dispatchID)
			results <- deliveryResult{
				dispatchID: dispatchID,
				outcome:    outcome,
				err:        err,
			}
		}()
	}
	got := make(map[string]deliveryResult, 2)
	for range 2 {
		result := <-results
		got[result.dispatchID] = result
	}
	if got[fixture.dispatch.ID].err != nil ||
		got[fixture.dispatch.ID].outcome != services.DispatchAttemptNoop {
		t.Fatalf("original delivery = %#v", got[fixture.dispatch.ID])
	}
	if got[successor.ID].err != nil ||
		got[successor.ID].outcome != services.DispatchAttemptSettled {
		t.Fatalf("successor delivery = %#v", got[successor.ID])
	}
	if len(generator.Requests()) != 1 {
		t.Fatalf("generation calls = %d, want 1", len(generator.Requests()))
	}
	assertInvocationDispatchTerminal(
		t,
		fixture.store,
		fixture.ack,
		successor.ID,
		domain.InvocationCompleted,
	)
	old, err := fixture.store.GetExecutionDispatch(ctx, fixture.dispatch.ID)
	if err != nil || old.Status != domain.ExecutionDispatchSettled {
		t.Fatalf("original dispatch = %#v, error = %v", old, err)
	}
}

type invocationReconcileFixture struct {
	store      *Store
	txm        *TransactionManager
	clock      *mutableClock
	ids        *identity.UUIDv7Generator
	ack        services.InvocationAcknowledgement
	dispatch   domain.ExecutionDispatch
	dispatches *services.DispatchService
	tasks      *dispatchTestQueue
}

func newInvocationReconcileFixture(t *testing.T) invocationReconcileFixture {
	t.Helper()
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := newMutableClock(time.Now().UTC())
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		t.Fatal(err)
	}
	runtime := services.NewRuntimeService(store, txm, clock, ids,
		services.WithInvocationExecutionMode(services.InvocationExecutionCloudTasks, services.DefaultExecutionDispatchQueue))
	ack, err := runtime.Admit(context.Background(), runtimeAuth(account.ID), runtimeInput())
	if err != nil {
		t.Fatal(err)
	}
	dispatches := newDispatchTestService(t, store, txm, clock)
	return invocationReconcileFixture{
		store: store, txm: txm, clock: clock, ids: ids, ack: ack,
		dispatch:   activeInvocationDispatch(t, store, ack.InvocationID),
		dispatches: dispatches, tasks: newDispatchTestQueue(),
	}
}

func (f invocationReconcileFixture) publishAndLoseTask(t *testing.T) {
	t.Helper()
	claim, ok, err := f.dispatches.ClaimNext(context.Background(), "publisher")
	if err != nil || !ok || claim.Dispatch.ID != f.dispatch.ID {
		t.Fatalf("publication claim = %#v/%v, error = %v", claim, ok, err)
	}
	if err := f.dispatches.PublishClaim(context.Background(), f.tasks, claim); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(3 * time.Second)
	f.tasks.delete(f.dispatch.ID)
}

type invocationDispatchFixture struct {
	store      *Store
	txm        *TransactionManager
	clock      identity.SystemClock
	ids        *identity.UUIDv7Generator
	runtime    *services.RuntimeService
	auth       domain.RuntimeAuthContext
	ack        services.InvocationAcknowledgement
	dispatch   domain.ExecutionDispatch
	dispatches *services.DispatchService
	attempts   *dispatchruntime.AttemptService
}

func newInvocationDispatchFixture(t *testing.T, generator ports.ModelGenerator) invocationDispatchFixture {
	t.Helper()
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		t.Fatal(err)
	}
	auth := runtimeAuth(account.ID)
	runtime := services.NewRuntimeService(store, txm, clock, ids,
		services.WithInvocationExecutionMode(services.InvocationExecutionCloudTasks, services.DefaultExecutionDispatchQueue))
	ack, err := runtime.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatal(err)
	}
	dispatches := newDispatchServiceForInvocationTest(t, store, txm, clock, ids)
	dispatch := activeInvocationDispatch(t, store, ack.InvocationID)
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids,
		services.WithExecutionSegmentCeiling(time.Second))
	attempts, err := dispatchruntime.NewAttemptService(
		dispatches, ownership, services.NewGenerationExecutor(store, generator, slog.New(slog.NewTextHandler(io.Discard, nil))),
		store, txm, clock, "request-executor", dispatchEngineConfig(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return invocationDispatchFixture{
		store: store, txm: txm, clock: clock, ids: ids, runtime: runtime, auth: auth,
		ack: ack, dispatch: dispatch, dispatches: dispatches, attempts: attempts,
	}
}

func newDispatchServiceForInvocationTest(t *testing.T, store *Store, txm *TransactionManager, clock ports.Clock, ids ports.IDGenerator) *services.DispatchService {
	t.Helper()
	cfg := services.DefaultDispatchConfig()
	cfg.PublicationLease = 100 * time.Millisecond
	cfg.StaleAfter = time.Second
	service, err := services.NewDispatchService(store, txm, clock, ids, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func activeInvocationDispatch(t *testing.T, store *Store, invocationID string) domain.ExecutionDispatch {
	t.Helper()
	items, err := store.ListAgedExecutionDispatches(context.Background(), time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Kind == domain.ExecutionDispatchInvocation && item.WorkID == invocationID {
			return item
		}
	}
	t.Fatalf("active dispatch for %s not found", invocationID)
	return domain.ExecutionDispatch{}
}

func dispatchEngineConfig() engine.Config {
	return engine.Config{
		Concurrency: 1, PollInterval: 10 * time.Millisecond,
		LeaseDuration: 500 * time.Millisecond, HeartbeatInterval: 50 * time.Millisecond,
		ReaperInterval: 50 * time.Millisecond, ReaperBatchLimit: 10,
		DrainGrace: time.Second, ExecutionSegmentCeiling: time.Second,
		SettlementReserve: 100 * time.Millisecond,
	}
}

func assertInvocationDispatchTerminal(t *testing.T, store *Store, ack services.InvocationAcknowledgement, dispatchID string, status domain.InvocationStatus) {
	t.Helper()
	invocation, err := store.GetInvocation(context.Background(), ack.InvocationID)
	if err != nil || invocation.Status != status {
		t.Fatalf("Invocation = %#v, error = %v", invocation, err)
	}
	dispatch, err := store.GetExecutionDispatch(context.Background(), dispatchID)
	if err != nil || dispatch.Status != domain.ExecutionDispatchSettled {
		t.Fatalf("dispatch = %#v, error = %v", dispatch, err)
	}
	messages, err := store.ListSessionMessages(context.Background(), ack.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantMessages := 1
	if status == domain.InvocationCompleted {
		wantMessages = 2
	}
	if len(messages) != wantMessages {
		t.Fatalf("messages = %d, want %d", len(messages), wantMessages)
	}
}

type blockingDispatchGenerator struct {
	calls   atomic.Int64
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingDispatchGenerator() *blockingDispatchGenerator {
	return &blockingDispatchGenerator{started: make(chan struct{}), release: make(chan struct{})}
}

func (g *blockingDispatchGenerator) Generate(ctx context.Context, _ domain.GenerationRequest) (domain.GenerationResponse, error) {
	g.calls.Add(1)
	g.once.Do(func() { close(g.started) })
	select {
	case <-ctx.Done():
		return domain.GenerationResponse{}, ctx.Err()
	case <-g.release:
	}
	return domain.GenerationResponse{
		Messages: []domain.GenerationMessage{{Role: domain.MessageRoleAssistant, Content: []byte(`[{"type":"text","text":"generated"}]`)}},
		Usage:    domain.ModelUsage{InputTokens: 2, OutputTokens: 1, Iterations: 1}, ServedModel: "test-model-served",
	}, nil
}
