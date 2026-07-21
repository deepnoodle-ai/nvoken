package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestInstallationBootstrapConvergesAcrossReplicas(t *testing.T) {
	pool, databaseURL := testDatabase(t, true)
	secondPool, err := OpenPool(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open second replica pool: %v", err)
	}
	t.Cleanup(secondPool.Close)
	type bootstrapReplica struct {
		store *Store
		txm   *TransactionManager
		clock identity.SystemClock
		ids   *identity.UUIDv7Generator
	}
	replicas := make([]bootstrapReplica, 0, 2)
	for _, replicaPool := range []*pgxpool.Pool{pool, secondPool} {
		clock := identity.SystemClock{}
		replicas = append(replicas, bootstrapReplica{
			store: NewStore(replicaPool), txm: NewTransactionManager(replicaPool),
			clock: clock, ids: identity.NewUUIDv7Generator(clock),
		})
	}

	start := make(chan struct{})
	results := make(chan struct {
		account domain.Account
		err     error
	}, 2)
	for _, replica := range replicas {
		go func(replica bootstrapReplica) {
			<-start
			account, err := services.BootstrapInstallation(context.Background(), replica.store, replica.txm, replica.clock, replica.ids)
			results <- struct {
				account domain.Account
				err     error
			}{account: account, err: err}
		}(replica)
	}
	close(start)
	var accountID string
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("bootstrap: %v", result.err)
		}
		if accountID == "" {
			accountID = result.account.ID
		}
		if result.account.ID != accountID {
			t.Fatalf("Account IDs differ: %q and %q", accountID, result.account.ID)
		}
	}
	assertTableCount(t, pool, "accounts", 1)
	assertTableCount(t, pool, "tenant_partitions", 1)

	secondAccount := domain.Account{ID: testID(t, domain.PrefixAccount), CreatedAt: time.Now().UTC()}
	secondPartition := domain.TenantPartition{
		ID: testID(t, domain.PrefixTenantPartition), AccountID: secondAccount.ID, CreatedAt: secondAccount.CreatedAt,
	}
	store := replicas[0].store
	txm := replicas[0].txm
	if err := txm.WithTransaction(context.Background(), func(ctx context.Context) error {
		if err := store.CreateAccount(ctx, secondAccount); err != nil {
			return err
		}
		return store.CreateTenantPartition(ctx, secondPartition)
	}); err != nil {
		t.Fatalf("create second Account: %v", err)
	}
	if _, err := services.BootstrapInstallation(context.Background(), store, txm, replicas[0].clock, replicas[0].ids); err == nil {
		t.Fatal("bootstrap accepted multiple Accounts")
	}
}

func TestResolveTenantPartitionConvergesWithinExplicitConflictScope(t *testing.T) {
	pool, _, store, auth := newRuntimeFixture(t)
	now := time.Now().UTC()
	defaultCandidate := domain.TenantPartition{
		ID: testID(t, domain.PrefixTenantPartition), AccountID: auth.AccountID, CreatedAt: now,
	}
	resolvedDefault, err := store.ResolveTenantPartition(context.Background(), defaultCandidate)
	if err != nil {
		t.Fatalf("resolve default partition: %v", err)
	}
	if resolvedDefault.ID == defaultCandidate.ID || resolvedDefault.TenantRef != nil {
		t.Fatalf("resolved default partition = %#v", resolvedDefault)
	}

	tenantRef := "tenant-a"
	first, err := store.ResolveTenantPartition(context.Background(), domain.TenantPartition{
		ID: testID(t, domain.PrefixTenantPartition), AccountID: auth.AccountID,
		TenantRef: &tenantRef, CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("resolve tenant partition: %v", err)
	}
	second, err := store.ResolveTenantPartition(context.Background(), domain.TenantPartition{
		ID: testID(t, domain.PrefixTenantPartition), AccountID: auth.AccountID,
		TenantRef: &tenantRef, CreatedAt: now,
	})
	if err != nil || second.ID != first.ID {
		t.Fatalf("second tenant resolution = %#v, error = %v; first = %#v", second, err, first)
	}
	assertTableCount(t, pool, "tenant_partitions", 2)
}

func TestRuntimeAdmissionRetryReadsAndTenantIsolation(t *testing.T) {
	pool, service, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	input := runtimeInput()

	first, err := service.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if first.Status != domain.InvocationQueued || first.Deduplicated {
		t.Fatalf("first acknowledgement = %#v", first)
	}
	replay, err := service.Admit(ctx, auth, input)
	if err != nil || replay.InvocationID != first.InvocationID || !replay.Deduplicated {
		t.Fatalf("replay = %#v, error = %v", replay, err)
	}
	assertAdmissionCounts(t, pool, 1)
	assertAdmissionReadback(t, store, first, input)

	invocation, err := service.GetInvocation(ctx, auth, first.InvocationID)
	if err != nil || invocation.Status != domain.InvocationQueued || invocation.SessionID != first.SessionID {
		t.Fatalf("Invocation read = %#v, error = %v", invocation, err)
	}
	session, err := service.GetSession(ctx, auth, first.SessionID)
	if err != nil || session.ActiveInvocationID == nil || *session.ActiveInvocationID != first.InvocationID {
		t.Fatalf("Session read = %#v, error = %v", session, err)
	}
	_, err = service.GetInvocation(ctx, auth, "not-an-invocation-id")
	assertPublicCode(t, err, services.CodeInvalidRequest)
	_, err = service.GetSession(ctx, auth, "not-a-session-id")
	assertPublicCode(t, err, services.CodeInvalidRequest)

	conflicts := map[string]func(*services.CreateInvocationInput){
		"input": func(changed *services.CreateInvocationInput) {
			changed.Input.Content = []services.TextInputBlock{{Type: "text", Text: "changed"}}
		},
		"spec": func(changed *services.CreateInvocationInput) {
			changed.Spec.Instructions = "changed"
		},
		"selector": func(changed *services.CreateInvocationInput) {
			changed.SessionKey = nil
			changed.SessionID = &first.SessionID
		},
	}
	for name, mutate := range conflicts {
		t.Run("changed "+name, func(t *testing.T) {
			changed := input
			mutate(&changed)
			_, conflictErr := service.Admit(ctx, auth, changed)
			assertPublicCode(t, conflictErr, services.CodeIdempotencyConflict)
			assertAdmissionCounts(t, pool, 1)
		})
	}

	wrongAgent := runtimeInput()
	wrongAgent.AgentRef = "other-agent"
	wrongAgent.SessionKey = nil
	wrongAgent.SessionID = &first.SessionID
	wrongAgent.IdempotencyKey = "wrong-agent"
	_, err = service.Admit(ctx, auth, wrongAgent)
	assertPublicCode(t, err, services.CodeNotFound)
	assertTableCount(t, pool, "agents", 1)

	wrongTenant := runtimeInput()
	wrongTenant.SessionKey = nil
	wrongTenant.SessionID = &first.SessionID
	wrongTenant.TenantRef = pointerString("other-tenant")
	wrongTenant.IdempotencyKey = "wrong-tenant"
	_, err = service.Admit(ctx, auth, wrongTenant)
	assertPublicCode(t, err, services.CodeNotFound)

	completedAt := time.Now().UTC()
	if err := updateInvocationStatusForTest(ctx, pool, first.InvocationID, domain.InvocationCompleted, 2, nil, &completedAt); err != nil {
		t.Fatalf("complete Invocation: %v", err)
	}
	replay, err = service.Admit(ctx, auth, input)
	if err != nil || replay.Status != domain.InvocationCompleted || !replay.Deduplicated {
		t.Fatalf("terminal replay = %#v, error = %v", replay, err)
	}

	next := runtimeInput()
	next.SessionKey = nil
	next.SessionID = &first.SessionID
	next.IdempotencyKey = "request-2"
	next.Input.Content[0].Text = "follow up"
	second, err := service.Admit(ctx, auth, next)
	if err != nil || second.SessionID != first.SessionID || second.Deduplicated {
		t.Fatalf("second Invocation = %#v, error = %v", second, err)
	}

	tenantInput := runtimeInput()
	tenantInput.TenantRef = pointerString("tenant-a")
	tenantInput.IdempotencyKey = input.IdempotencyKey
	tenantInput.SessionKey = pointerString("ticket-1")
	tenantAck, err := service.Admit(ctx, auth, tenantInput)
	if err != nil || tenantAck.SessionID == first.SessionID {
		t.Fatalf("tenant admission = %#v, error = %v", tenantAck, err)
	}
	assertTableCount(t, pool, "agents", 1)
	tenantAAuth := runtimeAuth(auth.AccountID)
	tenantAAuth.TenantConstraint = pointerString("tenant-a")
	if _, err := service.GetSession(ctx, tenantAAuth, tenantAck.SessionID); err != nil {
		t.Fatalf("tenant-constrained read: %v", err)
	}
	tenantBAuth := runtimeAuth(auth.AccountID)
	tenantBAuth.TenantConstraint = pointerString("tenant-b")
	_, err = service.GetSession(ctx, tenantBAuth, tenantAck.SessionID)
	assertPublicCode(t, err, services.CodeNotFound)

	constrained := runtimeAuth(auth.AccountID)
	constrained.TenantConstraint = pointerString("tenant-a")
	mismatch := runtimeInput()
	mismatch.TenantRef = pointerString("tenant-b")
	_, err = service.Admit(ctx, constrained, mismatch)
	assertPublicCode(t, err, services.CodeForbidden)
	var tenantB int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM tenant_partitions WHERE tenant_ref = 'tenant-b'").Scan(&tenantB); err != nil || tenantB != 0 {
		t.Fatalf("tenant-b count = %d, error = %v", tenantB, err)
	}

	denied := runtimeAuth(auth.AccountID)
	denied.Operations = map[domain.RuntimeOperation]struct{}{}
	_, err = service.GetSession(ctx, denied, first.SessionID)
	assertPublicCode(t, err, services.CodeForbidden)

	otherAccount := domain.Account{ID: testID(t, domain.PrefixAccount), CreatedAt: time.Now().UTC()}
	otherDefault := domain.TenantPartition{
		ID: testID(t, domain.PrefixTenantPartition), AccountID: otherAccount.ID, CreatedAt: otherAccount.CreatedAt,
	}
	if err := NewTransactionManager(pool).WithTransaction(ctx, func(txCtx context.Context) error {
		if err := store.CreateAccount(txCtx, otherAccount); err != nil {
			return err
		}
		return store.CreateTenantPartition(txCtx, otherDefault)
	}); err != nil {
		t.Fatalf("create other Account: %v", err)
	}
	_, err = service.GetInvocation(ctx, runtimeAuth(otherAccount.ID), first.InvocationID)
	assertPublicCode(t, err, services.CodeNotFound)
}

func TestRuntimeAdmissionConcurrentEqualAndDistinctRequests(t *testing.T) {
	t.Run("equal requests without selector", func(t *testing.T) {
		pool, service, _, auth := newRuntimeFixture(t)
		input := runtimeInput()
		input.SessionKey = nil

		const concurrency = 20
		start := make(chan struct{})
		results := make(chan struct {
			ack services.InvocationAcknowledgement
			err error
		}, concurrency)
		for range concurrency {
			go func() {
				<-start
				ack, err := service.Admit(context.Background(), auth, input)
				results <- struct {
					ack services.InvocationAcknowledgement
					err error
				}{ack: ack, err: err}
			}()
		}
		close(start)
		var invocationID, sessionID string
		fresh := 0
		for range concurrency {
			result := <-results
			if result.err != nil {
				t.Fatalf("concurrent admission: %v", result.err)
			}
			if invocationID == "" {
				invocationID, sessionID = result.ack.InvocationID, result.ack.SessionID
			}
			if result.ack.InvocationID != invocationID || result.ack.SessionID != sessionID {
				t.Fatalf("IDs diverged: %#v", result.ack)
			}
			if !result.ack.Deduplicated {
				fresh++
			}
		}
		if fresh != 1 {
			t.Fatalf("fresh acknowledgements = %d, want 1", fresh)
		}
		assertAdmissionCounts(t, pool, 1)
	})

	t.Run("distinct requests for a first-use Session key", func(t *testing.T) {
		pool, service, _, auth := newRuntimeFixture(t)
		inputs := []services.CreateInvocationInput{runtimeInput(), runtimeInput()}
		inputs[1].IdempotencyKey = "request-2"
		inputs[1].Input.Content[0].Text = "different"
		start := make(chan struct{})
		errorsChannel := make(chan error, 2)
		for _, input := range inputs {
			go func(input services.CreateInvocationInput) {
				<-start
				_, err := service.Admit(context.Background(), auth, input)
				errorsChannel <- err
			}(input)
		}
		close(start)
		accepted, active := 0, 0
		for range 2 {
			err := <-errorsChannel
			if err == nil {
				accepted++
				continue
			}
			var public *services.PublicError
			if errors.As(err, &public) && public.Code == services.CodeSessionInvocationActive {
				active++
			}
		}
		if accepted != 1 || active != 1 {
			t.Fatalf("accepted = %d, active = %d", accepted, active)
		}
		assertAdmissionCounts(t, pool, 1)
	})

	t.Run("distinct requests for an existing idle Session", func(t *testing.T) {
		pool, service, _, auth := newRuntimeFixture(t)
		first, err := service.Admit(context.Background(), auth, runtimeInput())
		if err != nil {
			t.Fatalf("initial admission: %v", err)
		}
		completedAt := time.Now().UTC()
		if err := updateInvocationStatusForTest(context.Background(), pool, first.InvocationID, domain.InvocationCompleted, 2, nil, &completedAt); err != nil {
			t.Fatalf("complete initial Invocation: %v", err)
		}

		inputs := []services.CreateInvocationInput{runtimeInput(), runtimeInput()}
		for index := range inputs {
			inputs[index].SessionKey = nil
			inputs[index].SessionID = &first.SessionID
			inputs[index].IdempotencyKey = fmt.Sprintf("next-request-%d", index)
			inputs[index].Input.Content[0].Text = fmt.Sprintf("next input %d", index)
		}
		start := make(chan struct{})
		errorsChannel := make(chan error, 2)
		for _, input := range inputs {
			go func(input services.CreateInvocationInput) {
				<-start
				_, err := service.Admit(context.Background(), auth, input)
				errorsChannel <- err
			}(input)
		}
		close(start)
		accepted, active := 0, 0
		for range 2 {
			err := <-errorsChannel
			if err == nil {
				accepted++
				continue
			}
			var public *services.PublicError
			if errors.As(err, &public) && public.Code == services.CodeSessionInvocationActive {
				active++
			}
		}
		if accepted != 1 || active != 1 {
			t.Fatalf("accepted = %d, active = %d", accepted, active)
		}
		assertAdmissionCounts(t, pool, 2)
		assertTableCount(t, pool, "sessions", 1)
	})
}

func TestRuntimeAdmissionRollsBackEveryWriteStage(t *testing.T) {
	for _, failAt := range []string{"agent", "partition", "session", "message sequence", "lifecycle revision", "snapshot", "invocation", "message", "state"} {
		t.Run(failAt, func(t *testing.T) {
			pool, _, store, auth := newRuntimeFixture(t)
			clock := identity.SystemClock{}
			faults := &faultingAdmissionStore{Store: store, failAt: failAt}
			service := services.NewRuntimeService(faults, NewTransactionManager(pool), clock, identity.NewUUIDv7Generator(clock))
			input := runtimeInput()
			input.TenantRef = pointerString("tenant-a")
			_, err := service.Admit(context.Background(), auth, input)
			if err == nil {
				t.Fatal("faulted admission succeeded")
			}
			assertTableCount(t, pool, "accounts", 1)
			assertTableCount(t, pool, "tenant_partitions", 1)
			assertTableCount(t, pool, "agents", 0)
			assertTableCount(t, pool, "sessions", 0)
			assertAdmissionCounts(t, pool, 0)
		})
	}
}

func TestRuntimeAdmissionRollsBackOnCommitFailure(t *testing.T) {
	pool, _, store, auth := newRuntimeFixture(t)
	clock := identity.SystemClock{}
	base := NewTransactionManager(pool)
	txm := &commitFailureTransactionManager{
		base: base, store: store,
		invalidAccount: domain.Account{ID: testID(t, domain.PrefixAccount), CreatedAt: clock.Now().UTC()},
	}
	service := services.NewRuntimeService(store, txm, clock, identity.NewUUIDv7Generator(clock))
	_, err := service.Admit(context.Background(), auth, runtimeInput())
	if err == nil {
		t.Fatal("commit-faulted admission succeeded")
	}
	assertTableCount(t, pool, "accounts", 1)
	assertTableCount(t, pool, "agents", 0)
	assertTableCount(t, pool, "sessions", 0)
	assertAdmissionCounts(t, pool, 0)
}

func TestRuntimeAdmissionMapsRetryableDatabaseConflictToUnavailable(t *testing.T) {
	pool, _, store, auth := newRuntimeFixture(t)
	clock := identity.SystemClock{}
	service := services.NewRuntimeService(store, retryableTransactionManager{}, clock, identity.NewUUIDv7Generator(clock))

	_, err := service.Admit(context.Background(), auth, runtimeInput())
	assertPublicCode(t, err, services.CodeUnavailable)
	assertTableCount(t, pool, "agents", 0)
	assertTableCount(t, pool, "sessions", 0)
	assertAdmissionCounts(t, pool, 0)
}

func TestRuntimeAdmissionReevaluatesConcurrentBackstopConflict(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*services.CreateInvocationInput, services.InvocationAcknowledgement)
		wantCode  services.ErrorCode
		wantDedup bool
	}{
		{name: "equal replay", wantDedup: true},
		{
			name: "changed replay", wantCode: services.CodeIdempotencyConflict,
			mutate: func(input *services.CreateInvocationInput, _ services.InvocationAcknowledgement) {
				input.Input.Content[0].Text = "changed"
			},
		},
		{
			name: "active Session", wantCode: services.CodeSessionInvocationActive,
			mutate: func(input *services.CreateInvocationInput, first services.InvocationAcknowledgement) {
				input.SessionKey = nil
				input.SessionID = &first.SessionID
				input.IdempotencyKey = "request-2"
				input.Input.Content[0].Text = "next input"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, initialService, store, auth := newRuntimeFixture(t)
			first, err := initialService.Admit(context.Background(), auth, runtimeInput())
			if err != nil {
				t.Fatalf("initial admission: %v", err)
			}
			input := runtimeInput()
			if test.mutate != nil {
				test.mutate(&input, first)
			}
			clock := identity.SystemClock{}
			txm := &concurrentAdmissionOnceTransactionManager{base: NewTransactionManager(pool)}
			service := services.NewRuntimeService(store, txm, clock, identity.NewUUIDv7Generator(clock))

			acknowledgement, err := service.Admit(context.Background(), auth, input)
			if txm.calls != 2 {
				t.Fatalf("transaction attempts = %d, want 2", txm.calls)
			}
			if test.wantCode != "" {
				assertPublicCode(t, err, test.wantCode)
				return
			}
			if err != nil || acknowledgement.InvocationID != first.InvocationID || acknowledgement.Deduplicated != test.wantDedup {
				t.Fatalf("acknowledgement = %#v, error = %v", acknowledgement, err)
			}
		})
	}
}

func TestRuntimeAdmissionBoundsRepeatedBackstopConflict(t *testing.T) {
	pool, _, store, auth := newRuntimeFixture(t)
	clock := identity.SystemClock{}
	txm := &concurrentAdmissionAlwaysTransactionManager{}
	service := services.NewRuntimeService(store, txm, clock, identity.NewUUIDv7Generator(clock))

	_, err := service.Admit(context.Background(), auth, runtimeInput())
	assertPublicCode(t, err, services.CodeUnavailable)
	if txm.calls != 2 {
		t.Fatalf("transaction attempts = %d, want 2", txm.calls)
	}
	assertTableCount(t, pool, "agents", 0)
	assertTableCount(t, pool, "sessions", 0)
	assertAdmissionCounts(t, pool, 0)
}

func TestRuntimeAdmissionCancellationWhileWaitingForSessionLockRollsBack(t *testing.T) {
	pool, service, store, auth := newRuntimeFixture(t)
	first, err := service.Admit(context.Background(), auth, runtimeInput())
	if err != nil {
		t.Fatalf("first admission: %v", err)
	}
	completedAt := time.Now().UTC()
	if err := updateInvocationStatusForTest(context.Background(), pool, first.InvocationID, domain.InvocationCompleted, 2, nil, &completedAt); err != nil {
		t.Fatalf("complete first: %v", err)
	}

	txm := NewTransactionManager(pool)
	locked := make(chan struct{})
	release := make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- txm.WithTransaction(context.Background(), func(ctx context.Context) error {
			if _, err := store.GetSessionForUpdate(ctx, first.SessionID); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	next := runtimeInput()
	next.SessionKey = nil
	next.SessionID = &first.SessionID
	next.IdempotencyKey = "blocked-request"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = service.Admit(ctx, auth, next)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked admission error = %v", err)
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatalf("lock transaction: %v", err)
	}
	assertAdmissionCounts(t, pool, 1)
}

func TestCheckSchemaNeverMigratesServeDatabase(t *testing.T) {
	pool, _ := testDatabase(t, false)
	if err := CheckSchema(context.Background(), pool); err == nil {
		t.Fatal("empty schema passed compatibility check")
	}
	var relation *string
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass('nvoken_schema_migrations')::text").Scan(&relation); err != nil {
		t.Fatalf("inspect empty schema: %v", err)
	}
	if relation != nil {
		t.Fatalf("schema check created migration table %q", *relation)
	}

	migratedPool, _ := testDatabase(t, true)
	if err := CheckSchema(context.Background(), migratedPool); err != nil {
		t.Fatalf("clean schema check: %v", err)
	}
	if _, err := migratedPool.Exec(context.Background(), "UPDATE nvoken_schema_migrations SET dirty = true"); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}
	if err := CheckSchema(context.Background(), migratedPool); err == nil {
		t.Fatal("dirty schema passed compatibility check")
	}

	newerPool, _ := testDatabase(t, true)
	if _, err := newerPool.Exec(context.Background(), "UPDATE nvoken_schema_migrations SET version = 999"); err != nil {
		t.Fatalf("mark newer: %v", err)
	}
	if err := CheckSchema(context.Background(), newerPool); err == nil {
		t.Fatal("newer schema passed compatibility check")
	}
}

type faultingAdmissionStore struct {
	*Store
	failAt string
}

type commitFailureTransactionManager struct {
	base           *TransactionManager
	store          *Store
	invalidAccount domain.Account
}

type retryableTransactionManager struct{}

func (retryableTransactionManager) WithTransaction(context.Context, func(context.Context) error) error {
	return ports.ErrRetryable
}

type concurrentAdmissionOnceTransactionManager struct {
	base  *TransactionManager
	calls int
}

type concurrentAdmissionAlwaysTransactionManager struct{ calls int }

func (m *concurrentAdmissionAlwaysTransactionManager) WithTransaction(context.Context, func(context.Context) error) error {
	m.calls++
	return ports.ErrConcurrentAdmission
}

func (m *concurrentAdmissionOnceTransactionManager) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	m.calls++
	if m.calls == 1 {
		return ports.ErrConcurrentAdmission
	}
	return m.base.WithTransaction(ctx, fn)
}

func (m *commitFailureTransactionManager) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	return m.base.WithTransaction(ctx, func(txCtx context.Context) error {
		if err := fn(txCtx); err != nil {
			return err
		}
		// The Account's deferred constraint fails at COMMIT because no default
		// partition is inserted, exercising a genuine commit-time rollback.
		return m.store.CreateAccount(txCtx, m.invalidAccount)
	})
}

func (s *faultingAdmissionStore) ResolveAgent(ctx context.Context, agent domain.Agent) (domain.Agent, error) {
	if s.failAt == "agent" {
		return domain.Agent{}, errors.New("injected agent failure")
	}
	return s.Store.ResolveAgent(ctx, agent)
}

func (s *faultingAdmissionStore) ResolveTenantPartition(ctx context.Context, partition domain.TenantPartition) (domain.TenantPartition, error) {
	if s.failAt == "partition" {
		return domain.TenantPartition{}, errors.New("injected partition failure")
	}
	return s.Store.ResolveTenantPartition(ctx, partition)
}

func (s *faultingAdmissionStore) ResolveSessionByKey(ctx context.Context, session domain.Session) (domain.Session, error) {
	if s.failAt == "session" {
		return domain.Session{}, errors.New("injected session failure")
	}
	return s.Store.ResolveSessionByKey(ctx, session)
}

func (s *faultingAdmissionStore) ReserveMessageSequence(ctx context.Context, sessionID string) (int64, error) {
	if s.failAt == "message sequence" {
		return 0, errors.New("injected message sequence failure")
	}
	return s.Store.ReserveMessageSequence(ctx, sessionID)
}

func (s *faultingAdmissionStore) ReserveLifecycleRevision(ctx context.Context, sessionID string) (int64, error) {
	if s.failAt == "lifecycle revision" {
		return 0, errors.New("injected lifecycle revision failure")
	}
	return s.Store.ReserveLifecycleRevision(ctx, sessionID)
}

func (s *faultingAdmissionStore) CreateExecutionSpecSnapshot(ctx context.Context, snapshot domain.ExecutionSpecSnapshot) error {
	if s.failAt == "snapshot" {
		return errors.New("injected snapshot failure")
	}
	return s.Store.CreateExecutionSpecSnapshot(ctx, snapshot)
}

func (s *faultingAdmissionStore) CreateInvocation(ctx context.Context, invocation domain.Invocation) error {
	if s.failAt == "invocation" {
		return errors.New("injected invocation failure")
	}
	return s.Store.CreateInvocation(ctx, invocation)
}

func (s *faultingAdmissionStore) AppendSessionMessage(ctx context.Context, message domain.SessionMessage) error {
	if s.failAt == "message" {
		return errors.New("injected message failure")
	}
	return s.Store.AppendSessionMessage(ctx, message)
}

func (s *faultingAdmissionStore) AppendInvocationState(ctx context.Context, state domain.InvocationState) error {
	if s.failAt == "state" {
		return errors.New("injected state failure")
	}
	return s.Store.AppendInvocationState(ctx, state)
}

func newRuntimeFixture(t *testing.T) (*pgxpool.Pool, *services.RuntimeService, *Store, domain.RuntimeAuthContext) {
	t.Helper()
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap installation: %v", err)
	}
	return pool, services.NewRuntimeService(store, txm, clock, ids), store, runtimeAuth(account.ID)
}

func runtimeAuth(accountID string) domain.RuntimeAuthContext {
	return domain.RuntimeAuthContext{
		AccountID: accountID,
		Operations: map[domain.RuntimeOperation]struct{}{
			domain.OperationCreateInvocation: {},
			domain.OperationGetInvocation:    {},
			domain.OperationCancelInvocation: {},
			domain.OperationListInvocations:  {},
			domain.OperationGetSession:       {},
			domain.OperationListSessions:     {},
			domain.OperationListMessages:     {},
			domain.OperationGetTranscript:    {},
		},
	}
}

func runtimeInput() services.CreateInvocationInput {
	return services.CreateInvocationInput{
		AgentRef: "support", SessionKey: pointerString("ticket-1"), IdempotencyKey: "request-1",
		Input: services.InvocationInput{Content: []services.TextInputBlock{{Type: "text", Text: "hello"}}},
		Spec: services.InlineExecutionSpec{
			Instructions: "help", Model: services.ModelSelection{Provider: "anthropic", Name: "test-model"},
		},
	}
}

func assertAdmissionReadback(
	t *testing.T,
	store *Store,
	ack services.InvocationAcknowledgement,
	input services.CreateInvocationInput,
) {
	t.Helper()
	ctx := context.Background()
	invocation, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil {
		t.Fatalf("read admitted Invocation: %v", err)
	}
	if invocation.SessionID != ack.SessionID || invocation.AgentID != ack.AgentID || invocation.Status != domain.InvocationQueued {
		t.Fatalf("admitted Invocation = %#v", invocation)
	}
	snapshot, err := store.GetExecutionSpecSnapshot(ctx, invocation.SpecSnapshotID)
	if err != nil {
		t.Fatalf("read admitted spec snapshot: %v", err)
	}
	wantSpec, err := json.Marshal(input.Spec)
	if err != nil {
		t.Fatalf("encode expected spec: %v", err)
	}
	if snapshot.AccountID != invocation.AccountID || !sameJSON(snapshot.Spec, wantSpec) {
		t.Fatalf("spec snapshot = %#v, want %s", snapshot, wantSpec)
	}
	messages, err := store.ListSessionMessages(ctx, ack.SessionID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("admitted messages = %#v, error = %v", messages, err)
	}
	wantContent, err := json.Marshal(input.Input.Content)
	if err != nil {
		t.Fatalf("encode expected input: %v", err)
	}
	if messages[0].InvocationID != ack.InvocationID || messages[0].Role != domain.MessageRoleUser || !sameJSON(messages[0].Content, wantContent) {
		t.Fatalf("admitted message = %#v, want content %s", messages[0], wantContent)
	}
	states, err := store.ListInvocationStates(ctx, ack.SessionID)
	if err != nil || len(states) != 1 {
		t.Fatalf("admitted states = %#v, error = %v", states, err)
	}
	state := states[0]
	if state.InvocationID != ack.InvocationID || state.Status != domain.InvocationQueued || state.Revision != invocation.CurrentStateRevision || state.ThroughMessageSequence == nil || *state.ThroughMessageSequence != messages[0].Sequence {
		t.Fatalf("admitted state = %#v, message sequence = %d", state, messages[0].Sequence)
	}
}

func sameJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func assertAdmissionCounts(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	for _, table := range []string{"execution_spec_snapshots", "invocations", "session_messages", "invocation_states"} {
		assertTableCount(t, pool, table, want)
	}
}

func assertTableCount(t *testing.T, pool *pgxpool.Pool, table string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}

func assertPublicCode(t *testing.T, err error, code services.ErrorCode) {
	t.Helper()
	var public *services.PublicError
	if !errors.As(err, &public) || public.Code != code {
		t.Fatalf("error = %T %v, want public code %s", err, err, code)
	}
}

func pointerString(value string) *string { return &value }
