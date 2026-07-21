package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/worksignal"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestInvocationCancellationAccruesSegmentAndWinsStaleSettlement(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := newMutableClock(time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC))
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	runtime := services.NewRuntimeService(store, txm, clock, ids)
	auth := runtimeAuth(account.ID)
	input := runtimeInput()
	wall, active, output, iterations, cost := int64(60), int64(30), 4096, 2, 0.25
	input.Spec.Budgets = &services.InvocationBudgetInput{
		WallClockTimeoutSeconds: &wall, ActiveExecutionTimeoutSeconds: &active,
		MaxOutputTokens: &output, MaxEstimatedCostUSD: &cost, MaxIterations: &iterations,
	}
	ack, err := runtime.Admit(context.Background(), auth, input)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	replay, err := runtime.Admit(context.Background(), auth, input)
	if err != nil || !replay.Deduplicated || replay.InvocationID != ack.InvocationID {
		t.Fatalf("budget replay = %#v, error = %v", replay, err)
	}
	changed := input
	changedOutput := output + 1
	changedBudgets := *input.Spec.Budgets
	changedBudgets.MaxOutputTokens = &changedOutput
	changed.Spec.Budgets = &changedBudgets
	_, err = runtime.Admit(context.Background(), auth, changed)
	assertPublicCode(t, err, services.CodeIdempotencyConflict)
	execution := services.NewInvocationExecutionService(store, txm, clock, ids,
		services.WithExecutionSegmentCeiling(10*time.Second))
	claim, disposition, err := execution.ClaimExact(context.Background(), ack.InvocationID, "cancel-owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim = %#v, disposition = %q, error = %v", claim, disposition, err)
	}
	clock.Advance(1250 * time.Millisecond)
	cancelled, err := runtime.CancelInvocation(context.Background(), auth, ack.InvocationID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != domain.InvocationCancelled || cancelled.ActiveExecutionMS != 1250 ||
		cancelled.Budgets.MaxOutputTokens == nil || *cancelled.Budgets.MaxOutputTokens != output ||
		cancelled.Budgets.MaxEstimatedCostUSD == nil || *cancelled.Budgets.MaxEstimatedCostUSD != cost {
		t.Fatalf("cancelled Invocation = %#v", cancelled)
	}
	if err := execution.Settle(context.Background(), claim, completedResult()); !errors.Is(err, ports.ErrLeaseLost) {
		t.Fatalf("stale settlement error = %v, want lease lost", err)
	}
	states, err := store.ListInvocationStates(context.Background(), ack.SessionID)
	if err != nil || len(states) != 3 || states[2].Status != domain.InvocationCancelled {
		t.Fatalf("lifecycle after cancel = %#v, error = %v", states, err)
	}
	again, err := runtime.CancelInvocation(context.Background(), auth, ack.InvocationID)
	if err != nil || again.Status != domain.InvocationCancelled {
		t.Fatalf("repeat cancel = %#v, error = %v", again, err)
	}
	states, _ = store.ListInvocationStates(context.Background(), ack.SessionID)
	if len(states) != 3 {
		t.Fatalf("repeat cancel appended lifecycle: %#v", states)
	}
	input.IdempotencyKey = "request-after-cancel"
	if _, err := runtime.Admit(context.Background(), auth, input); err != nil {
		t.Fatalf("admit after cancel: %v", err)
	}
}

func TestConcurrentCancellationAndSettlementHaveOneTerminalWinner(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ack, err := runtime.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := identity.SystemClock{}
	execution := services.NewInvocationExecutionService(
		store, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock),
	)
	claim, disposition, err := execution.ClaimExact(context.Background(), ack.InvocationID, "terminal-race", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim = %#v, disposition = %q, error = %v", claim, disposition, err)
	}
	start := make(chan struct{})
	settled := make(chan error, 1)
	cancelled := make(chan error, 1)
	go func() {
		<-start
		settled <- execution.Settle(context.Background(), claim, completedResult())
	}()
	go func() {
		<-start
		_, err := runtime.CancelInvocation(context.Background(), auth, ack.InvocationID)
		cancelled <- err
	}()
	close(start)
	settleErr, cancelErr := <-settled, <-cancelled
	if cancelErr != nil {
		t.Fatalf("cancel race: %v", cancelErr)
	}
	invocation, err := store.GetInvocation(context.Background(), ack.InvocationID)
	if err != nil || !invocation.Status.Terminal() {
		t.Fatalf("terminal Invocation = %#v, error = %v", invocation, err)
	}
	states, _ := store.ListInvocationStates(context.Background(), ack.SessionID)
	if len(states) != 3 || states[2].Status != invocation.Status {
		t.Fatalf("terminal lifecycle = %#v, Invocation = %#v", states, invocation)
	}
	messages, _ := store.ListSessionMessages(context.Background(), ack.SessionID)
	switch invocation.Status {
	case domain.InvocationCompleted:
		if settleErr != nil || len(messages) != 2 {
			t.Fatalf("completed winner: settlement = %v, messages = %#v", settleErr, messages)
		}
	case domain.InvocationCancelled:
		if !errors.Is(settleErr, ports.ErrLeaseLost) || len(messages) != 1 {
			t.Fatalf("cancelled winner: settlement = %v, messages = %#v", settleErr, messages)
		}
	default:
		t.Fatalf("unexpected terminal winner %q", invocation.Status)
	}
}

func TestCancellationLifecycleFaultRollsBackTerminalRow(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ack, err := runtime.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := identity.SystemClock{}
	faults := &faultingExecutionStore{
		Store: store, failStatus: domain.InvocationCancelled, failInvocationID: ack.InvocationID,
	}
	faultyRuntime := services.NewRuntimeService(
		faults, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock),
	)
	if _, err := faultyRuntime.CancelInvocation(context.Background(), auth, ack.InvocationID); err == nil {
		t.Fatal("faulted cancellation succeeded")
	}
	invocation, err := store.GetInvocation(context.Background(), ack.InvocationID)
	if err != nil || invocation.Status != domain.InvocationQueued || invocation.CompletedAt != nil {
		t.Fatalf("Invocation after rollback = %#v, error = %v", invocation, err)
	}
	states, _ := store.ListInvocationStates(context.Background(), ack.SessionID)
	if len(states) != 1 || states[0].Status != domain.InvocationQueued {
		t.Fatalf("lifecycle after rollback = %#v", states)
	}
}

func TestCancellationScopeFailureDoesNotPublishWake(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	signaller := &countingCancellationSignaller{}
	runtime := services.NewRuntimeService(store, txm, clock, ids, services.WithCancellationSignaller(signaller))
	auth := runtimeAuth(account.ID)
	ack, err := runtime.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}

	crossAccount := auth
	otherAccountID, err := ids.NewID(domain.PrefixAccount)
	if err != nil {
		t.Fatalf("other Account ID: %v", err)
	}
	crossAccount.AccountID = otherAccountID
	_, err = runtime.CancelInvocation(context.Background(), crossAccount, ack.InvocationID)
	assertPublicCode(t, err, services.CodeNotFound)

	conflictingTenant := auth
	tenantRef := "other-tenant"
	conflictingTenant.TenantConstraint = &tenantRef
	_, err = runtime.CancelInvocation(context.Background(), conflictingTenant, ack.InvocationID)
	assertPublicCode(t, err, services.CodeNotFound)
	if got := signaller.notifications.Load(); got != 0 {
		t.Fatalf("unauthorized cancellation wakes = %d, want 0", got)
	}

	if _, err := runtime.CancelInvocation(context.Background(), auth, ack.InvocationID); err != nil {
		t.Fatalf("authorized cancellation: %v", err)
	}
	if got := signaller.notifications.Load(); got != 1 {
		t.Fatalf("authorized cancellation wakes = %d, want 1", got)
	}
}

func TestInvocationDeadlineReaperDistinguishesLogicalAndSegmentExpiry(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := newMutableClock(time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC))
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	runtime := services.NewRuntimeService(store, txm, clock, ids)
	auth := runtimeAuth(account.ID)
	execution := services.NewInvocationExecutionService(store, txm, clock, ids,
		services.WithExecutionSegmentCeiling(2*time.Second))

	input := runtimeInput()
	wall := int64(2)
	input.Spec.Budgets = &services.InvocationBudgetInput{WallClockTimeoutSeconds: &wall}
	queued, err := runtime.Admit(context.Background(), auth, input)
	if err != nil {
		t.Fatalf("admit queued deadline: %v", err)
	}
	clock.Advance(3 * time.Second)
	faults := &faultingExecutionStore{
		Store: store, failStatus: domain.InvocationFailed, failInvocationID: queued.InvocationID,
	}
	faultyExecution := services.NewInvocationExecutionService(
		faults, txm, clock, ids, services.WithExecutionSegmentCeiling(2*time.Second),
	)
	if _, err := faultyExecution.ReapExpired(context.Background(), 10); err == nil {
		t.Fatal("faulted deadline reaping succeeded")
	}
	stillQueued, _ := store.GetInvocation(context.Background(), queued.InvocationID)
	if stillQueued.Status != domain.InvocationQueued {
		t.Fatalf("deadline rollback left status %q", stillQueued.Status)
	}
	if _, err := execution.ReapExpired(context.Background(), 10); err != nil {
		t.Fatalf("reap queued deadline: %v", err)
	}
	assertInvocationFailureScope(t, store, queued.InvocationID, "deadline_exceeded", "wall_clock")

	input.IdempotencyKey = "segment-deadline"
	input.Spec.Budgets = nil
	running, err := runtime.Admit(context.Background(), auth, input)
	if err != nil {
		t.Fatalf("admit segment deadline: %v", err)
	}
	claim, disposition, err := execution.ClaimExact(context.Background(), running.InvocationID, "segment-owner", time.Minute)
	if err != nil || disposition != services.Claimed || claim.Invocation.ExecutionDeadlineScope == nil || *claim.Invocation.ExecutionDeadlineScope != "execution_segment" {
		t.Fatalf("segment claim = %#v, disposition = %q, error = %v", claim, disposition, err)
	}
	clock.Advance(3 * time.Second)
	if _, err := execution.ReapExpired(context.Background(), 10); err != nil {
		t.Fatalf("reap segment deadline: %v", err)
	}
	assertInvocationFailureScope(t, store, running.InvocationID, "deadline_exceeded", "execution_segment")
	failed, _ := store.GetInvocation(context.Background(), running.InvocationID)
	if failed.ActiveExecutionMS != 3000 {
		t.Fatalf("active execution = %dms, want 3000ms", failed.ActiveExecutionMS)
	}
}

func TestBudgetFailureSettlementRollbackPreservesRunningClaim(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ack, err := runtime.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	execution := services.NewInvocationExecutionService(store, NewTransactionManager(pool), clock, ids)
	claim, disposition, err := execution.ClaimExact(context.Background(), ack.InvocationID, "budget-owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim = %#v, disposition = %q, error = %v", claim, disposition, err)
	}
	result := domain.InvocationExecutionResult{
		Status: domain.InvocationFailed,
		Error:  json.RawMessage(`{"code":"budget_exceeded","message":"The execution budget was exceeded.","details":{"kind":"output_tokens"}}`),
		Usage:  &domain.ModelUsage{InputTokens: 10, OutputTokens: 5, Iterations: 1},
		Provenance: &domain.ModelProvenance{
			Provider: "anthropic", RequestedModel: "requested", ServedModel: "served",
			CredentialSource: "installation_byok",
		},
	}
	faults := &faultingExecutionStore{Store: store, failStatus: domain.InvocationFailed, failInvocationID: ack.InvocationID}
	faulty := services.NewInvocationExecutionService(faults, NewTransactionManager(pool), clock, ids)
	if err := faulty.Settle(context.Background(), claim, result); err == nil {
		t.Fatal("faulted budget settlement succeeded")
	}
	running, _ := store.GetInvocation(context.Background(), ack.InvocationID)
	if running.Status != domain.InvocationRunning || len(running.Usage) != 0 || len(running.Provenance) != 0 {
		t.Fatalf("budget rollback row = %#v", running)
	}
	if err := execution.Settle(context.Background(), claim, result); err != nil {
		t.Fatalf("settle budget failure after rollback: %v", err)
	}
	failed, _ := store.GetInvocation(context.Background(), ack.InvocationID)
	if failed.Status != domain.InvocationFailed || len(failed.Usage) == 0 || len(failed.Provenance) == 0 {
		t.Fatalf("budget failure row = %#v", failed)
	}
}

func TestPostgresCancellationSignalCrossesPoolBoundary(t *testing.T) {
	pool, databaseURL := testDatabase(t, true)
	secondPool, err := OpenPool(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open second pool: %v", err)
	}
	t.Cleanup(secondPool.Close)
	signaller := worksignal.NewPostgresCancellation(pool)
	subscription := worksignal.NewPostgresCancellation(secondPool).SubscribeCancellations(context.Background())
	t.Cleanup(subscription.Close)
	// The first bounded wait establishes LISTEN before the notification.
	_, _ = subscription.Wait(context.Background(), 10*time.Millisecond)
	signaller.NotifyCancellation(context.Background(), "invocation-cross-process")
	got, ok := subscription.Wait(context.Background(), time.Second)
	if !ok || got != "invocation-cross-process" {
		t.Fatalf("notification = %q, %t", got, ok)
	}
}

func assertInvocationFailureScope(t *testing.T, store *Store, invocationID, code, scope string) {
	t.Helper()
	invocation, err := store.GetInvocation(context.Background(), invocationID)
	if err != nil || invocation.Status != domain.InvocationFailed {
		t.Fatalf("failed Invocation = %#v, error = %v", invocation, err)
	}
	var failure struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	}
	if err := json.Unmarshal(invocation.Error, &failure); err != nil || failure.Code != code || failure.Details["scope"] != scope {
		t.Fatalf("failure = %s, error = %v", invocation.Error, err)
	}
}

type countingCancellationSignaller struct{ notifications atomic.Int64 }

func (s *countingCancellationSignaller) NotifyCancellation(context.Context, string) {
	s.notifications.Add(1)
}

func (*countingCancellationSignaller) SubscribeCancellations(context.Context) ports.CancellationSubscription {
	return noopCancellationSubscription{}
}

type noopCancellationSubscription struct{}

func (noopCancellationSubscription) Wait(context.Context, time.Duration) (string, bool) {
	return "", false
}

func (noopCancellationSubscription) Close() {}
