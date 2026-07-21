package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	migratedatabase "github.com/golang-migrate/migrate/v4/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

var testSchemaCounter atomic.Uint64

func testDatabase(t *testing.T, migrate bool) (*pgxpool.Pool, string) {
	t.Helper()
	baseURL := os.Getenv("NVOKEN_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("NVOKEN_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := OpenPool(ctx, baseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	schema := fmt.Sprintf("nvoken_test_%d_%d", time.Now().UnixNano(), testSchemaCounter.Add(1))
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatalf("create test schema: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE")
		admin.Close()
	})

	schemaURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	query := schemaURL.Query()
	query.Set("search_path", schema)
	schemaURL.RawQuery = query.Encode()
	pool, err := OpenPool(ctx, schemaURL.String())
	if err != nil {
		t.Fatalf("open schema pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if migrate {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		if err := NewMigrator(schemaURL.String(), 5*time.Second, logger).Apply(ctx); err != nil {
			t.Fatalf("migrate test schema: %v", err)
		}
	}
	return pool, schemaURL.String()
}

func TestMigratorIsIdempotentAndSerialized(t *testing.T) {
	pool, databaseURL := testDatabase(t, false)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			results <- NewMigrator(databaseURL, 5*time.Second, logger).Apply(ctx)
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}

	var stateRows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM nvoken_schema_migrations").Scan(&stateRows); err != nil {
		t.Fatalf("count migration state rows: %v", err)
	}
	if stateRows != 1 {
		t.Fatalf("migration state row count = %d, want 1", stateRows)
	}
	if err := NewMigrator(databaseURL, 5*time.Second, logger).Apply(ctx); err != nil {
		t.Fatalf("idempotent migrate: %v", err)
	}
}

func TestMigratorFailsOnDirtyAndUnknownVersion(t *testing.T) {
	t.Run("dirty version", func(t *testing.T) {
		pool, databaseURL := testDatabase(t, true)
		if _, err := pool.Exec(context.Background(),
			"UPDATE nvoken_schema_migrations SET dirty = true WHERE version = 7",
		); err != nil {
			t.Fatalf("mark migration dirty: %v", err)
		}
		err := NewMigrator(databaseURL, 2*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil))).Apply(context.Background())
		if err == nil || !strings.Contains(err.Error(), "000007 is dirty") {
			t.Fatalf("migrate error = %v", err)
		}
	})

	t.Run("unknown version", func(t *testing.T) {
		pool, databaseURL := testDatabase(t, true)
		if _, err := pool.Exec(context.Background(),
			"UPDATE nvoken_schema_migrations SET version = 999, dirty = false WHERE version = 7",
		); err != nil {
			t.Fatalf("set future migration: %v", err)
		}
		err := NewMigrator(databaseURL, 2*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil))).Apply(context.Background())
		if err == nil || !strings.Contains(err.Error(), "000999 is unknown") {
			t.Fatalf("migrate error = %v", err)
		}
	})
}

func TestMigrationLockReleasesWithConnection(t *testing.T) {
	pool, databaseURL := testDatabase(t, true)
	ctx := context.Background()
	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire lock connection: %v", err)
	}
	var databaseName, schemaName string
	if err := pool.QueryRow(ctx, "SELECT current_database(), current_schema()").Scan(&databaseName, &schemaName); err != nil {
		t.Fatalf("read migration lock scope: %v", err)
	}
	lockID, err := migratedatabase.GenerateAdvisoryLockId(databaseName, schemaName, migrationTable)
	if err != nil {
		t.Fatalf("generate migration lock ID: %v", err)
	}
	if err := lockConn.QueryRow(ctx, "SELECT pg_advisory_lock($1)", lockID).Scan(new(any)); err != nil {
		t.Fatalf("hold migration lock: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	err = NewMigrator(databaseURL, 150*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil))).Apply(waitCtx)
	cancel()
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked migrate error = %v", err)
	}

	// Releasing the dedicated session models process or connection loss:
	// Postgres drops the session-scoped lock without a cooperating unlock.
	if err := lockConn.Conn().Close(ctx); err != nil {
		t.Fatalf("close migration lock connection: %v", err)
	}
	lockConn.Release()
	proceedCtx, proceedCancel := context.WithTimeout(ctx, 2*time.Second)
	defer proceedCancel()
	if err := NewMigrator(databaseURL, 2*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil))).Apply(proceedCtx); err != nil {
		t.Fatalf("migrate after lock connection loss: %v", err)
	}
}

type runtimeFixture struct {
	account   domain.Account
	partition domain.TenantPartition
	agent     domain.Agent
	session   domain.Session
}

func createRuntimeFixture(t *testing.T, ctx context.Context, store *Store, txm *TransactionManager) runtimeFixture {
	t.Helper()
	now := time.Now().UTC()
	fixture := runtimeFixture{
		account: domain.Account{ID: testID(t, domain.PrefixAccount), CreatedAt: now},
		agent: domain.Agent{
			ID: testID(t, domain.PrefixAgent), AgentRef: "support-agent", CreatedAt: now,
		},
		session: domain.Session{
			ID: testID(t, domain.PrefixSession), SessionKey: stringPointer("ticket-1"),
			NextMessageSequence: 1, NextLifecycleRevision: 1, CreatedAt: now, UpdatedAt: now,
		},
	}
	fixture.partition = domain.TenantPartition{
		ID: testID(t, domain.PrefixTenantPartition), AccountID: fixture.account.ID, CreatedAt: now,
	}
	fixture.agent.AccountID = fixture.account.ID
	fixture.session.AccountID = fixture.account.ID
	fixture.session.TenantPartitionID = fixture.partition.ID
	fixture.session.AgentID = fixture.agent.ID
	if err := txm.WithTransaction(ctx, func(ctx context.Context) error {
		if err := store.CreateAccount(ctx, fixture.account); err != nil {
			return err
		}
		if err := store.CreateTenantPartition(ctx, fixture.partition); err != nil {
			return err
		}
		if err := store.CreateAgent(ctx, fixture.agent); err != nil {
			return err
		}
		return store.CreateSession(ctx, fixture.session)
	}); err != nil {
		t.Fatalf("create runtime fixture: %v", err)
	}
	return fixture
}

func TestRuntimeRepositoriesCommitRollbackAndReadback(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	ctx := context.Background()
	fixture := createRuntimeFixture(t, ctx, store, txm)
	now := time.Now().UTC()

	snapshot := domain.ExecutionSpecSnapshot{
		ID: testID(t, domain.PrefixExecutionSpecSnapshot), AccountID: fixture.account.ID,
		Spec: []byte(`{"instructions":"help","model":{"provider":"anthropic","name":"test"}}`), CreatedAt: now,
	}
	invocation := domain.Invocation{
		ID: testID(t, domain.PrefixInvocation), SessionID: fixture.session.ID,
		AccountID: fixture.account.ID, TenantPartitionID: fixture.partition.ID, AgentID: fixture.agent.ID,
		SpecSnapshotID: snapshot.ID, IdempotencyKey: "request-1", RequestFingerprint: make([]byte, 32),
		Status: domain.InvocationQueued, CurrentStateRevision: 1, CreatedAt: now, UpdatedAt: now,
	}
	setTestInvocationControls(&invocation, now)
	message := domain.SessionMessage{
		ID: "smsg_019b0a12-0000-7000-8000-0000000000ff", SessionID: fixture.session.ID,
		AccountID: fixture.account.ID, TenantPartitionID: fixture.partition.ID, AgentID: fixture.agent.ID,
		InvocationID: invocation.ID, Role: domain.MessageRoleUser,
		Content: []byte(`[{"type":"text","text":"hello"}]`), CreatedAt: now,
	}
	state := domain.InvocationState{
		ID: testID(t, domain.PrefixInvocationState), InvocationID: invocation.ID, SessionID: fixture.session.ID,
		AccountID: fixture.account.ID, TenantPartitionID: fixture.partition.ID, AgentID: fixture.agent.ID,
		Status: domain.InvocationQueued, CreatedAt: now,
	}
	if err := txm.WithTransaction(ctx, func(ctx context.Context) error {
		sequence, err := store.ReserveMessageSequence(ctx, fixture.session.ID)
		if err != nil {
			return err
		}
		revision, err := store.ReserveLifecycleRevision(ctx, fixture.session.ID)
		if err != nil {
			return err
		}
		message.Sequence = sequence
		state.Revision = revision
		state.ThroughMessageSequence = &sequence
		invocation.CurrentStateRevision = revision
		if err := store.CreateExecutionSpecSnapshot(ctx, snapshot); err != nil {
			return err
		}
		if err := store.CreateInvocation(ctx, invocation); err != nil {
			return err
		}
		if err := store.AppendSessionMessage(ctx, message); err != nil {
			return err
		}
		return store.AppendInvocationState(ctx, state)
	}); err != nil {
		t.Fatalf("persist invocation aggregate: %v", err)
	}
	secondMessage := message
	secondMessage.ID = "smsg_019b0a12-0000-7000-8000-000000000001"
	secondMessage.Role = domain.MessageRoleAssistant
	secondMessage.Content = []byte(`[{"type":"text","text":"response"}]`)
	secondState := state
	secondState.ID = testID(t, domain.PrefixInvocationState)
	if err := txm.WithTransaction(ctx, func(ctx context.Context) error {
		sequence, err := store.ReserveMessageSequence(ctx, fixture.session.ID)
		if err != nil {
			return err
		}
		revision, err := store.ReserveLifecycleRevision(ctx, fixture.session.ID)
		if err != nil {
			return err
		}
		secondMessage.Sequence = sequence
		secondState.Revision = revision
		secondState.ThroughMessageSequence = &sequence
		if err := store.AppendSessionMessage(ctx, secondMessage); err != nil {
			return err
		}
		if err := store.AppendInvocationState(ctx, secondState); err != nil {
			return err
		}
		return updateInvocationStatusForTest(ctx, pool, invocation.ID, domain.InvocationQueued, revision, nil, nil)
	}); err != nil {
		t.Fatalf("append second message and state: %v", err)
	}

	messages, err := store.ListSessionMessages(ctx, fixture.session.ID)
	if err != nil || len(messages) != 2 || !equalJSON(messages[0].Content, message.Content) {
		t.Fatalf("messages = %#v, error = %v", messages, err)
	}
	if messages[0].ID <= messages[1].ID || messages[0].Sequence != 1 || messages[1].Sequence != 2 {
		t.Fatalf("messages are not ordered by sequence independently of UUID text: %#v", messages)
	}
	states, err := store.ListInvocationStates(ctx, fixture.session.ID)
	if err != nil || len(states) != 2 || states[0].Revision != 1 || states[1].Revision != 2 {
		t.Fatalf("states = %#v, error = %v", states, err)
	}
	storedInvocation, err := store.GetInvocation(ctx, invocation.ID)
	if err != nil || storedInvocation.Status != domain.InvocationQueued || len(storedInvocation.Error) != 0 {
		t.Fatalf("invocation = %#v, error = %v", storedInvocation, err)
	}

	rolledBackID := testID(t, domain.PrefixExecutionSpecSnapshot)
	wantRollback := errors.New("stop")
	err = txm.WithTransaction(ctx, func(ctx context.Context) error {
		if err := store.CreateExecutionSpecSnapshot(ctx, domain.ExecutionSpecSnapshot{
			ID: rolledBackID, AccountID: fixture.account.ID, Spec: []byte(`{"rollback":true}`), CreatedAt: now,
		}); err != nil {
			return err
		}
		return txm.WithTransaction(ctx, func(context.Context) error { return wantRollback })
	})
	if !errors.Is(err, wantRollback) {
		t.Fatalf("rollback error = %v", err)
	}
	if _, err := store.GetExecutionSpecSnapshot(ctx, rolledBackID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("rolled-back snapshot read error = %v", err)
	}

	panicAgent := domain.Agent{
		ID: testID(t, domain.PrefixAgent), AccountID: fixture.account.ID, AgentRef: "panic-agent", CreatedAt: now,
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("transaction did not propagate panic")
			}
		}()
		_ = txm.WithTransaction(ctx, func(ctx context.Context) error {
			if err := store.CreateAgent(ctx, panicAgent); err != nil {
				return err
			}
			panic("test panic")
		})
	}()
	if _, err := store.GetAgentByRef(ctx, fixture.account.ID, panicAgent.AgentRef); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("panic write read error = %v", err)
	}
}

func TestSessionSequenceAndLifecycleCountersReserveIndependently(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	ctx := context.Background()
	fixture := createRuntimeFixture(t, ctx, store, txm)

	if err := txm.WithTransaction(ctx, func(ctx context.Context) error {
		revision, err := store.ReserveLifecycleRevision(ctx, fixture.session.ID)
		if err != nil {
			return err
		}
		if revision != 1 {
			return fmt.Errorf("lifecycle revision = %d, want 1", revision)
		}
		session, err := store.GetSession(ctx, fixture.session.ID)
		if err != nil {
			return err
		}
		if session.NextMessageSequence != 1 || session.NextLifecycleRevision != 2 {
			return fmt.Errorf("counters after lifecycle reserve = (%d, %d), want (1, 2)",
				session.NextMessageSequence, session.NextLifecycleRevision)
		}
		return nil
	}); err != nil {
		t.Fatalf("reserve lifecycle revision: %v", err)
	}
	if err := txm.WithTransaction(ctx, func(ctx context.Context) error {
		sequence, err := store.ReserveMessageSequence(ctx, fixture.session.ID)
		if err != nil {
			return err
		}
		if sequence != 1 {
			return fmt.Errorf("message sequence = %d, want 1", sequence)
		}
		session, err := store.GetSession(ctx, fixture.session.ID)
		if err != nil {
			return err
		}
		if session.NextMessageSequence != 2 || session.NextLifecycleRevision != 2 {
			return fmt.Errorf("counters after message reserve = (%d, %d), want (2, 2)",
				session.NextMessageSequence, session.NextLifecycleRevision)
		}
		return nil
	}); err != nil {
		t.Fatalf("reserve message sequence: %v", err)
	}
}

func TestRuntimeSchemaConstraints(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	ctx := context.Background()
	fixture := createRuntimeFixture(t, ctx, store, txm)
	now := time.Now().UTC()

	t.Run("account requires exactly one default partition", func(t *testing.T) {
		account := domain.Account{ID: testID(t, domain.PrefixAccount), CreatedAt: now}
		err := txm.WithTransaction(ctx, func(ctx context.Context) error {
			return store.CreateAccount(ctx, account)
		})
		assertPostgresCode(t, err, "23514")

		duplicate := domain.TenantPartition{
			ID: testID(t, domain.PrefixTenantPartition), AccountID: fixture.account.ID, CreatedAt: now,
		}
		assertPostgresCode(t, store.CreateTenantPartition(ctx, duplicate), "23505")
		assertPostgresCode(t, execError(ctx, pool,
			"UPDATE tenant_partitions SET tenant_ref = 'not-default' WHERE id = $1", fixture.partition.ID,
		), "23514")
	})

	t.Run("identities and boundaries", func(t *testing.T) {
		badAgent := domain.Agent{ID: "agnt_bad", AccountID: fixture.account.ID, AgentRef: "bad", CreatedAt: now}
		assertPostgresCode(t, store.CreateAgent(ctx, badAgent), "23514")

		otherAccount := domain.Account{ID: testID(t, domain.PrefixAccount), CreatedAt: now}
		otherPartition := domain.TenantPartition{
			ID: testID(t, domain.PrefixTenantPartition), AccountID: otherAccount.ID, CreatedAt: now,
		}
		if err := txm.WithTransaction(ctx, func(ctx context.Context) error {
			if err := store.CreateAccount(ctx, otherAccount); err != nil {
				return err
			}
			return store.CreateTenantPartition(ctx, otherPartition)
		}); err != nil {
			t.Fatalf("create other account: %v", err)
		}
		crossed := fixture.session
		crossed.ID = testID(t, domain.PrefixSession)
		crossed.SessionKey = stringPointer("crossed")
		crossed.TenantPartitionID = otherPartition.ID
		assertPostgresCode(t, store.CreateSession(ctx, crossed), "23503")
		assertPostgresCode(t, execError(ctx, pool,
			"UPDATE sessions SET tenant_partition_id = $2 WHERE id = $1", fixture.session.ID, otherPartition.ID,
		), "23514")
	})

	t.Run("tenant ref and session key scopes", func(t *testing.T) {
		tenantRef := "tenant-a"
		partition := domain.TenantPartition{
			ID: testID(t, domain.PrefixTenantPartition), AccountID: fixture.account.ID,
			TenantRef: &tenantRef, CreatedAt: now,
		}
		if err := store.CreateTenantPartition(ctx, partition); err != nil {
			t.Fatalf("create named partition: %v", err)
		}
		duplicateRef := partition
		duplicateRef.ID = testID(t, domain.PrefixTenantPartition)
		assertPostgresCode(t, store.CreateTenantPartition(ctx, duplicateRef), "23505")

		duplicateKey := fixture.session
		duplicateKey.ID = testID(t, domain.PrefixSession)
		assertPostgresCode(t, store.CreateSession(ctx, duplicateKey), "23505")
		otherPartitionSession := fixture.session
		otherPartitionSession.ID = testID(t, domain.PrefixSession)
		otherPartitionSession.TenantPartitionID = partition.ID
		if err := store.CreateSession(ctx, otherPartitionSession); err != nil {
			t.Fatalf("same key in another partition: %v", err)
		}
	})

	t.Run("message and lifecycle checks", func(t *testing.T) {
		snapshot, invocation := createInvocationRecords(t, fixture, now, "constraints", domain.InvocationQueued)
		if err := store.CreateExecutionSpecSnapshot(ctx, snapshot); err != nil {
			t.Fatalf("create snapshot: %v", err)
		}
		if err := store.CreateInvocation(ctx, invocation); err != nil {
			t.Fatalf("create invocation: %v", err)
		}
		empty := domain.SessionMessage{
			ID: testID(t, domain.PrefixSessionMessage), SessionID: fixture.session.ID,
			AccountID: fixture.account.ID, TenantPartitionID: fixture.partition.ID, AgentID: fixture.agent.ID,
			InvocationID: invocation.ID, Sequence: 1, Role: domain.MessageRoleUser, Content: []byte(`[]`), CreatedAt: now,
		}
		assertPostgresCode(t, store.AppendSessionMessage(ctx, empty), "23514")
		empty.Content = []byte(`[{"type":"text","text":"ok"}]`)
		if err := store.AppendSessionMessage(ctx, empty); err != nil {
			t.Fatalf("append message: %v", err)
		}
		duplicate := empty
		duplicate.ID = testID(t, domain.PrefixSessionMessage)
		assertPostgresCode(t, store.AppendSessionMessage(ctx, duplicate), "23505")

		watermark := int64(1)
		state := domain.InvocationState{
			ID: testID(t, domain.PrefixInvocationState), InvocationID: invocation.ID, SessionID: fixture.session.ID,
			AccountID: fixture.account.ID, TenantPartitionID: fixture.partition.ID, AgentID: fixture.agent.ID,
			Revision: 1, Status: domain.InvocationQueued, ThroughMessageSequence: &watermark, CreatedAt: now,
		}
		if err := store.AppendInvocationState(ctx, state); err != nil {
			t.Fatalf("append state: %v", err)
		}
		state.ID = testID(t, domain.PrefixInvocationState)
		assertPostgresCode(t, store.AppendInvocationState(ctx, state), "23505")
		invalidState := state
		invalidState.ID = testID(t, domain.PrefixInvocationState)
		invalidState.Revision = 2
		invalidState.Status = domain.InvocationStatus("unknown")
		assertPostgresCode(t, store.AppendInvocationState(ctx, invalidState), "23514")
		invalidSnapshot, invalidInvocation := createInvocationRecords(t, fixture, now, "invalid-status", domain.InvocationStatus("unknown"))
		if err := store.CreateExecutionSpecSnapshot(ctx, invalidSnapshot); err != nil {
			t.Fatalf("create invalid-status snapshot: %v", err)
		}
		assertPostgresCode(t, store.CreateInvocation(ctx, invalidInvocation), "23514")
		assertPostgresCode(t, execError(ctx, pool, "UPDATE session_messages SET role = 'assistant' WHERE id = $1", empty.ID), "23514")
		assertPostgresCode(t, execError(ctx, pool, "DELETE FROM invocation_states WHERE invocation_id = $1", invocation.ID), "23514")
	})
}

func TestRuntimeSchemaRejectsEveryMalformedID(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	ctx := context.Background()
	fixture := createRuntimeFixture(t, ctx, store, txm)
	now := time.Now().UTC()

	assertPostgresCode(t, store.CreateAccount(ctx, domain.Account{ID: "acct_bad", CreatedAt: now}), "23514")
	assertPostgresCode(t, store.CreateTenantPartition(ctx, domain.TenantPartition{
		ID: "tprt_bad", AccountID: fixture.account.ID, TenantRef: stringPointer("bad-partition"), CreatedAt: now,
	}), "23514")
	assertPostgresCode(t, store.CreateAgent(ctx, domain.Agent{
		ID: "agnt_bad", AccountID: fixture.account.ID, AgentRef: "bad-agent", CreatedAt: now,
	}), "23514")
	badSession := fixture.session
	badSession.ID = "sesn_bad"
	badSession.SessionKey = stringPointer("bad-session")
	assertPostgresCode(t, store.CreateSession(ctx, badSession), "23514")
	assertPostgresCode(t, store.CreateExecutionSpecSnapshot(ctx, domain.ExecutionSpecSnapshot{
		ID: "spec_bad", AccountID: fixture.account.ID, Spec: []byte(`{"valid":true}`), CreatedAt: now,
	}), "23514")

	snapshot, invocation := createInvocationRecords(t, fixture, now, "malformed-ids", domain.InvocationQueued)
	if err := store.CreateExecutionSpecSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("create valid snapshot: %v", err)
	}
	badInvocation := invocation
	badInvocation.ID = "invk_bad"
	assertPostgresCode(t, store.CreateInvocation(ctx, badInvocation), "23514")
	if err := store.CreateInvocation(ctx, invocation); err != nil {
		t.Fatalf("create valid invocation: %v", err)
	}
	assertPostgresCode(t, store.AppendSessionMessage(ctx, domain.SessionMessage{
		ID: "smsg_bad", SessionID: fixture.session.ID, AccountID: fixture.account.ID,
		TenantPartitionID: fixture.partition.ID, AgentID: fixture.agent.ID, InvocationID: invocation.ID,
		Sequence: 1, Role: domain.MessageRoleUser, Content: []byte(`[{"type":"text","text":"valid"}]`), CreatedAt: now,
	}), "23514")
	assertPostgresCode(t, store.AppendInvocationState(ctx, domain.InvocationState{
		ID: "ivst_bad", InvocationID: invocation.ID, SessionID: fixture.session.ID,
		AccountID: fixture.account.ID, TenantPartitionID: fixture.partition.ID, AgentID: fixture.agent.ID,
		Revision: 1, Status: domain.InvocationQueued, CreatedAt: now,
	}), "23514")
}

func TestRuntimeSchemaRejectsCompositeBoundaryCrossing(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	ctx := context.Background()
	fixture := createRuntimeFixture(t, ctx, store, txm)
	now := time.Now().UTC()

	otherAccount := domain.Account{ID: testID(t, domain.PrefixAccount), CreatedAt: now}
	otherPartition := domain.TenantPartition{
		ID: testID(t, domain.PrefixTenantPartition), AccountID: otherAccount.ID, CreatedAt: now,
	}
	otherAgent := domain.Agent{
		ID: testID(t, domain.PrefixAgent), AccountID: otherAccount.ID, AgentRef: "other-agent", CreatedAt: now,
	}
	if err := txm.WithTransaction(ctx, func(ctx context.Context) error {
		if err := store.CreateAccount(ctx, otherAccount); err != nil {
			return err
		}
		if err := store.CreateTenantPartition(ctx, otherPartition); err != nil {
			return err
		}
		return store.CreateAgent(ctx, otherAgent)
	}); err != nil {
		t.Fatalf("create other boundary: %v", err)
	}

	crossedSession := fixture.session
	crossedSession.ID = testID(t, domain.PrefixSession)
	crossedSession.SessionKey = stringPointer("crossed-partition")
	crossedSession.TenantPartitionID = otherPartition.ID
	assertPostgresCode(t, store.CreateSession(ctx, crossedSession), "23503")
	crossedSession.ID = testID(t, domain.PrefixSession)
	crossedSession.SessionKey = stringPointer("crossed-agent")
	crossedSession.TenantPartitionID = fixture.partition.ID
	crossedSession.AgentID = otherAgent.ID
	assertPostgresCode(t, store.CreateSession(ctx, crossedSession), "23503")

	crossSession := fixture.session
	crossSession.ID = testID(t, domain.PrefixSession)
	crossSession.SessionKey = stringPointer("composite-records")
	if err := store.CreateSession(ctx, crossSession); err != nil {
		t.Fatalf("create composite test session: %v", err)
	}
	crossFixture := fixture
	crossFixture.session = crossSession
	snapshot, invocation := createInvocationRecords(t, crossFixture, now, "composite-records", domain.InvocationQueued)
	if err := store.CreateExecutionSpecSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("create composite test snapshot: %v", err)
	}
	badInvocation := invocation
	badInvocation.ID = testID(t, domain.PrefixInvocation)
	badInvocation.IdempotencyKey = "crossed-invocation"
	badInvocation.TenantPartitionID = otherPartition.ID
	assertPostgresCode(t, store.CreateInvocation(ctx, badInvocation), "23503")
	if err := store.CreateInvocation(ctx, invocation); err != nil {
		t.Fatalf("create composite test invocation: %v", err)
	}

	badMessage := domain.SessionMessage{
		ID: testID(t, domain.PrefixSessionMessage), SessionID: crossSession.ID,
		AccountID: fixture.account.ID, TenantPartitionID: otherPartition.ID, AgentID: fixture.agent.ID,
		InvocationID: invocation.ID, Sequence: 1, Role: domain.MessageRoleUser,
		Content: []byte(`[{"type":"text","text":"crossed"}]`), CreatedAt: now,
	}
	assertPostgresCode(t, store.AppendSessionMessage(ctx, badMessage), "23503")
	badState := domain.InvocationState{
		ID: testID(t, domain.PrefixInvocationState), InvocationID: invocation.ID, SessionID: crossSession.ID,
		AccountID: fixture.account.ID, TenantPartitionID: otherPartition.ID, AgentID: fixture.agent.ID,
		Revision: 1, Status: domain.InvocationQueued, CreatedAt: now,
	}
	assertPostgresCode(t, store.AppendInvocationState(ctx, badState), "23503")
}

func TestInvocationUniquenessAndRetention(t *testing.T) {
	pool, _ := testDatabase(t, true)
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	ctx := context.Background()
	fixture := createRuntimeFixture(t, ctx, store, txm)
	now := time.Now().UTC()

	shortSnapshot, shortFingerprint := createInvocationRecords(t, fixture, now, "short", domain.InvocationQueued)
	if err := store.CreateExecutionSpecSnapshot(ctx, shortSnapshot); err != nil {
		t.Fatalf("create short snapshot: %v", err)
	}
	shortFingerprint.RequestFingerprint = []byte("short")
	assertPostgresCode(t, store.CreateInvocation(ctx, shortFingerprint), "23514")

	firstSnapshot, first := createInvocationRecords(t, fixture, now, "first", domain.InvocationQueued)
	secondSnapshot, second := createInvocationRecords(t, fixture, now, "second", domain.InvocationQueued)
	for _, snapshot := range []domain.ExecutionSpecSnapshot{firstSnapshot, secondSnapshot} {
		if err := store.CreateExecutionSpecSnapshot(ctx, snapshot); err != nil {
			t.Fatalf("create snapshot: %v", err)
		}
	}
	start := make(chan struct{})
	results := make(chan struct {
		id  string
		err error
	}, 2)
	for _, invocation := range []domain.Invocation{first, second} {
		go func(invocation domain.Invocation) {
			<-start
			results <- struct {
				id  string
				err error
			}{id: invocation.ID, err: store.CreateInvocation(ctx, invocation)}
		}(invocation)
	}
	close(start)
	var winner domain.Invocation
	failures := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			if result.id == first.ID {
				winner = first
			} else {
				winner = second
			}
			continue
		}
		failures++
		assertPostgresCode(t, result.err, "23505")
	}
	if winner.ID == "" || failures != 1 {
		t.Fatalf("winner = %q, failures = %d", winner.ID, failures)
	}

	completedAt := now.Add(time.Second)
	if err := updateInvocationStatusForTest(ctx, pool, winner.ID, domain.InvocationCompleted, 2, nil, &completedAt); err != nil {
		t.Fatalf("complete winner: %v", err)
	}
	replacementSnapshot, replacement := createInvocationRecords(t, fixture, now, "replacement", domain.InvocationQueued)
	if err := store.CreateExecutionSpecSnapshot(ctx, replacementSnapshot); err != nil {
		t.Fatalf("create replacement snapshot: %v", err)
	}
	if err := store.CreateInvocation(ctx, replacement); err != nil {
		t.Fatalf("create after terminal: %v", err)
	}

	duplicateSession := fixture.session
	duplicateSession.ID = testID(t, domain.PrefixSession)
	duplicateSession.SessionKey = stringPointer("duplicate-idempotency")
	if err := store.CreateSession(ctx, duplicateSession); err != nil {
		t.Fatalf("create duplicate-key session: %v", err)
	}
	duplicateFixture := fixture
	duplicateFixture.session = duplicateSession
	duplicateSnapshot, duplicateInvocation := createInvocationRecords(t, duplicateFixture, now, winner.IdempotencyKey, domain.InvocationQueued)
	if err := store.CreateExecutionSpecSnapshot(ctx, duplicateSnapshot); err != nil {
		t.Fatalf("create duplicate-key snapshot: %v", err)
	}
	assertPostgresCode(t, store.CreateInvocation(ctx, duplicateInvocation), "23505")

	otherTenantRef := "other"
	otherPartition := domain.TenantPartition{
		ID: testID(t, domain.PrefixTenantPartition), AccountID: fixture.account.ID,
		TenantRef: &otherTenantRef, CreatedAt: now,
	}
	otherSession := fixture.session
	otherSession.ID = testID(t, domain.PrefixSession)
	otherSession.TenantPartitionID = otherPartition.ID
	otherSession.SessionKey = stringPointer("other")
	if err := store.CreateTenantPartition(ctx, otherPartition); err != nil {
		t.Fatalf("create other partition: %v", err)
	}
	if err := store.CreateSession(ctx, otherSession); err != nil {
		t.Fatalf("create other session: %v", err)
	}
	otherFixture := fixture
	otherFixture.partition = otherPartition
	otherFixture.session = otherSession
	otherSnapshot, otherInvocation := createInvocationRecords(t, otherFixture, now, winner.IdempotencyKey, domain.InvocationQueued)
	if err := store.CreateExecutionSpecSnapshot(ctx, otherSnapshot); err != nil {
		t.Fatalf("create other snapshot: %v", err)
	}
	if err := store.CreateInvocation(ctx, otherInvocation); err != nil {
		t.Fatalf("same idempotency key in other tenant: %v", err)
	}

	assertPostgresCode(t, execError(ctx, pool, "DELETE FROM sessions WHERE id = $1", fixture.session.ID), "23503")
	assertPostgresCode(t, execError(ctx, pool, "UPDATE invocations SET status = 'running', completed_at = NULL WHERE id = $1", winner.ID), "23514")
	assertPostgresCode(t, execError(ctx, pool, "UPDATE invocations SET error = '{}'::jsonb WHERE id = $1", winner.ID), "23514")
}

func createInvocationRecords(
	t *testing.T,
	fixture runtimeFixture,
	now time.Time,
	idempotencyKey string,
	status domain.InvocationStatus,
) (domain.ExecutionSpecSnapshot, domain.Invocation) {
	t.Helper()
	snapshot := domain.ExecutionSpecSnapshot{
		ID: testID(t, domain.PrefixExecutionSpecSnapshot), AccountID: fixture.account.ID,
		Spec: []byte(`{"instructions":"test"}`), CreatedAt: now,
	}
	invocation := domain.Invocation{
		ID: testID(t, domain.PrefixInvocation), SessionID: fixture.session.ID,
		AccountID: fixture.account.ID, TenantPartitionID: fixture.partition.ID, AgentID: fixture.agent.ID,
		SpecSnapshotID: snapshot.ID, IdempotencyKey: idempotencyKey, RequestFingerprint: make([]byte, 32),
		Status: status, CurrentStateRevision: 1, CreatedAt: now, UpdatedAt: now,
	}
	setTestInvocationControls(&invocation, now)
	if status.Terminal() {
		invocation.CompletedAt = &now
	}
	return snapshot, invocation
}

func setTestInvocationControls(invocation *domain.Invocation, createdAt time.Time) {
	invocation.FingerprintVersion = 1
	invocation.WallClockTimeoutMS = int64((30 * time.Minute) / time.Millisecond)
	invocation.ActiveTimeoutMS = int64((30 * time.Minute) / time.Millisecond)
	invocation.MaxIterations = 1
	invocation.WallClockDeadlineAt = createdAt.Add(30 * time.Minute)
}

func testID(t *testing.T, prefix domain.StableIDPrefix) string {
	t.Helper()
	generator := identity.NewUUIDv7Generator(identity.SystemClock{})
	id, err := generator.NewID(prefix)
	if err != nil {
		t.Fatalf("generate %s ID: %v", prefix, err)
	}
	return id
}

func stringPointer(value string) *string { return &value }

func execError(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) error {
	_, err := pool.Exec(ctx, sql, args...)
	return err
}

// updateInvocationStatusForTest is intentionally test-only. Production
// execution transitions go through the fenced ownership service.
func updateInvocationStatusForTest(
	ctx context.Context,
	pool *pgxpool.Pool,
	id string,
	status domain.InvocationStatus,
	revision int64,
	errorPayload []byte,
	completedAt *time.Time,
) error {
	_, err := pool.Exec(ctx, `
		UPDATE invocations
		SET status = $2, current_state_revision = $3, error = $4,
		    completed_at = $5, updated_at = CURRENT_TIMESTAMP,
		    lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $1
	`, id, string(status), revision, errorPayload, completedAt)
	return err
}

func assertPostgresCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected Postgres error %s", code)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %T %v, want Postgres %s", err, err, code)
	}
	if pgErr.Code != code {
		t.Fatalf("Postgres code = %s (%v), want %s", pgErr.Code, err, code)
	}
}

func equalJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
