package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type restoreFixtureIDs struct {
	completedInvocation string
	queuedInvocation    string
	waitingInvocation   string
	waitingSession      string
}

func TestVerifyRestoreChecksHealthyRepresentativeStateWithoutMutation(t *testing.T) {
	pool, _ := testDatabase(t, true)
	seedRestoreFixture(t, pool)
	before := restoreTableCounts(t, pool)

	verification, err := VerifyRestore(context.Background(), pool)
	if err != nil {
		t.Fatalf("verify healthy restore: %v; checks = %#v", err, verification.Checks)
	}
	if !verification.Passed() {
		t.Fatalf("healthy restore did not pass: %#v", verification.Checks)
	}
	for _, component := range []string{
		"database_schema",
		"read_only_transaction",
		"required_tables",
		"required_constraints",
		"nonterminal_unique_index",
		"churn_table_autovacuum_parameters",
		"one_nonterminal_invocation_per_session",
		"terminal_state_consistency",
		"transcript_cursor_bounds",
		"checkpoint_cursor_bounds",
		"representative_session",
		"representative_invocation",
		"representative_transcript",
		"representative_tool_call",
		"representative_checkpoint",
	} {
		check := findRestoreCheck(t, verification, component)
		wantClass := "none"
		if component == "database_schema" {
			wantClass = "compatible"
		}
		if !check.Passed || check.ErrorClass != wantClass {
			t.Fatalf("restore check %q = %#v", component, check)
		}
	}
	after := restoreTableCounts(t, pool)
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("restore verifier mutated rows: before %v, after %v", before, after)
	}
}

func TestVerifyRestoreRejectsUnsafeFixtures(t *testing.T) {
	tests := []struct {
		name          string
		prepare       func(*testing.T, *pgxpool.Pool)
		wantComponent string
		wantClass     string
	}{
		{
			name: "dirty schema",
			prepare: func(t *testing.T, pool *pgxpool.Pool) {
				if _, err := pool.Exec(context.Background(), "UPDATE nvoken_schema_migrations SET dirty = true"); err != nil {
					t.Fatal(err)
				}
			},
			wantComponent: "database_schema",
			wantClass:     "dirty",
		},
		{
			name: "incompatible schema",
			prepare: func(t *testing.T, pool *pgxpool.Pool) {
				rewindToPreviousSchema(t, pool)
			},
			wantComponent: "database_schema",
			wantClass:     "behind",
		},
		{
			name: "incomplete schema",
			prepare: func(t *testing.T, pool *pgxpool.Pool) {
				if _, err := pool.Exec(context.Background(), "DROP TABLE tool_call_attempts"); err != nil {
					t.Fatal(err)
				}
			},
			wantComponent: "required_tables",
			wantClass:     "missing",
		},
		{
			name: "missing required constraint",
			prepare: func(t *testing.T, pool *pgxpool.Pool) {
				seedRestoreFixture(t, pool)
				if _, err := pool.Exec(context.Background(),
					"ALTER TABLE sessions DROP CONSTRAINT sessions_agent_boundary",
				); err != nil {
					t.Fatal(err)
				}
			},
			wantComponent: "required_constraints",
			wantClass:     "missing_or_unvalidated",
		},
		{
			name: "missing nonterminal index",
			prepare: func(t *testing.T, pool *pgxpool.Pool) {
				seedRestoreFixture(t, pool)
				if _, err := pool.Exec(context.Background(),
					"DROP INDEX invocations_one_nonterminal_per_session",
				); err != nil {
					t.Fatal(err)
				}
			},
			wantComponent: "nonterminal_unique_index",
			wantClass:     "missing_or_invalid",
		},
		{
			name: "multiple nonterminal invocations",
			prepare: func(t *testing.T, pool *pgxpool.Pool) {
				ids := seedRestoreFixture(t, pool)
				if _, err := pool.Exec(context.Background(),
					"DROP INDEX invocations_one_nonterminal_per_session",
				); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(context.Background(), `
					INSERT INTO invocations (
						id, session_id, account_id, tenant_partition_id, agent_id,
						spec_snapshot_id, idempotency_key, request_fingerprint, status,
						request_fingerprint_version, current_state_revision, error,
						total_timeout_ms, active_timeout_ms, max_output_tokens,
						max_estimated_cost_microusd, max_iterations, active_execution_ms,
						deadline_at, output_schema_digest,
						created_at, updated_at, completed_at
					)
					SELECT
						'invk_018f0000-0000-7000-8000-000000000001',
						session_id, account_id, tenant_partition_id, agent_id,
						spec_snapshot_id, 'restore-conflict', request_fingerprint, status,
						request_fingerprint_version, current_state_revision, error,
						total_timeout_ms, active_timeout_ms, max_output_tokens,
						max_estimated_cost_microusd, max_iterations, active_execution_ms,
						deadline_at, output_schema_digest,
						created_at, updated_at, completed_at
					FROM invocations
					WHERE id = $1
				`, ids.queuedInvocation); err != nil {
					t.Fatal(err)
				}
			},
			wantComponent: "one_nonterminal_invocation_per_session",
			wantClass:     "conflict",
		},
		{
			name: "corrupt transcript cursor",
			prepare: func(t *testing.T, pool *pgxpool.Pool) {
				seedRestoreFixture(t, pool)
				if _, err := pool.Exec(context.Background(), `
					UPDATE sessions SET next_message_sequence = next_message_sequence + 1
					WHERE id = (SELECT id FROM sessions ORDER BY id LIMIT 1)
				`); err != nil {
					t.Fatal(err)
				}
			},
			wantComponent: "transcript_cursor_bounds",
			wantClass:     "out_of_bounds",
		},
		{
			name: "corrupt checkpoint cursor",
			prepare: func(t *testing.T, pool *pgxpool.Pool) {
				ids := seedRestoreFixture(t, pool)
				if _, err := pool.Exec(context.Background(), `
					UPDATE invocations
					SET current_checkpoint_sequence = current_checkpoint_sequence + 1
					WHERE id = $1
				`, ids.waitingInvocation); err != nil {
					t.Fatal(err)
				}
			},
			wantComponent: "checkpoint_cursor_bounds",
			wantClass:     "out_of_bounds",
		},
		{
			name: "corrupt terminal state",
			prepare: func(t *testing.T, pool *pgxpool.Pool) {
				ids := seedRestoreFixture(t, pool)
				if _, err := pool.Exec(context.Background(),
					"ALTER TABLE invocations DROP CONSTRAINT invocations_terminal_timestamp",
				); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(context.Background(),
					"UPDATE invocations SET completed_at = now() WHERE id = $1",
					ids.queuedInvocation,
				); err != nil {
					t.Fatal(err)
				}
			},
			wantComponent: "terminal_state_consistency",
			wantClass:     "inconsistent",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, _ := testDatabase(t, true)
			test.prepare(t, pool)
			verification, err := VerifyRestore(context.Background(), pool)
			if err == nil || verification.Passed() {
				t.Fatalf("unsafe fixture passed: checks = %#v", verification.Checks)
			}
			check := findRestoreCheck(t, verification, test.wantComponent)
			if check.Passed || check.ErrorClass != test.wantClass {
				t.Fatalf("restore check %q = %#v, want class %q", test.wantComponent, check, test.wantClass)
			}
		})
	}
}

func TestLogicalBackupRestoreDrill(t *testing.T) {
	if os.Getenv("NVOKEN_RUN_LOGICAL_RESTORE_DRILL") != "1" {
		t.Skip("run the logical restore drill through scripts/test_restore.py")
	}
	for _, command := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("%s is required for the logical restore drill", command)
		}
	}
	baseURL := os.Getenv("NVOKEN_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Fatal("NVOKEN_TEST_DATABASE_URL is required")
	}
	startedAt := time.Now().UTC()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	sourceName := "nvoken_restore_source_" + suffix
	targetName := "nvoken_restore_target_" + suffix
	admin, err := OpenPool(context.Background(), baseURL)
	if err != nil {
		t.Fatalf("open restore drill admin database: %v", err)
	}
	t.Cleanup(admin.Close)
	for _, database := range []string{sourceName, targetName} {
		if _, err := admin.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
			t.Fatalf("create drill database %s: %v", database, err)
		}
		database := database
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = admin.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+pgx.Identifier{database}.Sanitize()+" WITH (FORCE)")
		})
	}

	sourceURL := restoreDatabaseURL(t, baseURL, sourceName)
	targetURL := restoreDatabaseURL(t, baseURL, targetName)
	if err := NewMigrator(sourceURL, 30*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil))).Apply(context.Background()); err != nil {
		t.Fatalf("migrate restore drill source: %v", err)
	}
	sourcePool, err := OpenPool(context.Background(), sourceURL)
	if err != nil {
		t.Fatalf("open restore drill source: %v", err)
	}
	ids := seedRestoreFixture(t, sourcePool)
	wantCounts := restoreTableCounts(t, sourcePool)
	sourcePool.Close()

	dumpPath := filepath.Join(t.TempDir(), "nvoken.dump")
	runPostgresClient(t, sourceURL, "pg_dump",
		"--format=custom",
		"--no-owner",
		"--no-privileges",
		"--file", dumpPath,
	)
	archive, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read logical restore archive: %v", err)
	}
	archiveChecksum := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	recoveryPointCompletedAt := time.Now().UTC()
	runPostgresClient(t, targetURL, "pg_restore",
		"--exit-on-error",
		"--no-owner",
		"--no-privileges",
		"--dbname", targetName,
		dumpPath,
	)

	targetPool, err := OpenPool(context.Background(), targetURL)
	if err != nil {
		t.Fatalf("open restored drill target: %v", err)
	}
	defer targetPool.Close()
	verification, err := VerifyRestore(context.Background(), targetPool)
	if err != nil || !verification.Passed() {
		t.Fatalf("verify logical restore: %v; checks = %#v", err, verification.Checks)
	}
	if gotCounts := restoreTableCounts(t, targetPool); fmt.Sprint(gotCounts) != fmt.Sprint(wantCounts) {
		t.Fatalf("restored counts = %v, want %v", gotCounts, wantCounts)
	}
	store := NewStore(targetPool)
	for _, invocationID := range []string{ids.completedInvocation, ids.queuedInvocation, ids.waitingInvocation} {
		if _, err := store.GetInvocation(context.Background(), invocationID); err != nil {
			t.Fatalf("read restored Invocation %s: %v", invocationID, err)
		}
	}
	if messages, err := store.ListSessionMessages(context.Background(), ids.waitingSession); err != nil || len(messages) < 2 {
		t.Fatalf("read restored checkpoint transcript: messages = %d, error = %v", len(messages), err)
	}
	if checkpoints, err := store.ListInvocationCheckpoints(context.Background(), ids.waitingInvocation); err != nil || len(checkpoints) == 0 {
		t.Fatalf("read restored checkpoints: checkpoints = %d, error = %v", len(checkpoints), err)
	}
	t.Logf(
		"restore drill evidence: started=%s recovery_point_completed=%s ended=%s recovery_point=%s schema=%06d stable_ids_checked=3 counts=%v",
		startedAt.Format(time.RFC3339Nano),
		recoveryPointCompletedAt.Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano),
		archiveChecksum,
		verification.Schema.Current,
		wantCounts,
	)
}

func TestSeedRestoreDrillDatabase(t *testing.T) {
	if os.Getenv("NVOKEN_SEED_RESTORE_DRILL") != "1" {
		t.Skip("set NVOKEN_SEED_RESTORE_DRILL=1 for an explicitly selected disposable database")
	}
	databaseURL := os.Getenv("NVOKEN_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("NVOKEN_TEST_DATABASE_URL is required")
	}
	pool, err := OpenPool(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open restore drill database: %v", err)
	}
	defer pool.Close()

	var existingSessions int64
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM sessions").Scan(&existingSessions); err != nil {
		t.Fatalf("confirm migrated restore drill database: %v", err)
	}
	if existingSessions != 0 {
		t.Fatalf("refusing to seed restore drill database with %d existing Sessions", existingSessions)
	}
	seedRestoreFixture(t, pool)
	verification, err := VerifyRestore(context.Background(), pool)
	if err != nil || !verification.Passed() {
		t.Fatalf("verify seeded restore drill database: %v; checks = %#v", err, verification.Checks)
	}
	t.Logf("seeded restore drill fixture counts: %v", restoreTableCounts(t, pool))
}

func seedRestoreFixture(t *testing.T, pool *pgxpool.Pool) restoreFixtureIDs {
	t.Helper()
	ctx := context.Background()
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(ctx, store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap restore fixture: %v", err)
	}
	runtime := services.NewRuntimeService(store, txm, clock, ids)
	auth := runtimeAuth(account.ID)
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)

	completedInput := runtimeInput()
	completedInput.SessionKey = pointerString("restore-completed")
	completedInput.IdempotencyKey = "restore-completed"
	completed, err := runtime.Admit(ctx, auth, completedInput)
	if err != nil {
		t.Fatalf("admit completed restore fixture: %v", err)
	}
	completedClaim, disposition, err := ownership.ClaimExact(ctx, completed.InvocationID, "restore-completed", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim completed restore fixture: disposition = %s, error = %v", disposition, err)
	}
	usage := domain.ModelUsage{InputTokens: 1, OutputTokens: 1, Iterations: 1}
	provenance := testModelProvenance()
	if err := ownership.Settle(ctx, completedClaim, domain.InvocationExecutionResult{
		Status: domain.InvocationCompleted,
		AssistantMessages: []domain.GenerationMessage{
			{
				Role:    domain.MessageRoleAssistant,
				Content: json.RawMessage(`[{"type":"text","text":"restored"}]`),
			},
		},
		Usage:      &usage,
		Provenance: &provenance,
	}); err != nil {
		t.Fatalf("settle completed restore fixture: %v", err)
	}

	queuedInput := runtimeInput()
	queuedInput.SessionKey = pointerString("restore-queued")
	queuedInput.IdempotencyKey = "restore-queued"
	queued, err := runtime.Admit(ctx, auth, queuedInput)
	if err != nil {
		t.Fatalf("admit queued restore fixture: %v", err)
	}

	waitingInput := runtimeInputWithTwoIterations()
	waitingInput.SessionKey = pointerString("restore-waiting")
	waitingInput.IdempotencyKey = "restore-waiting"
	waitingInput.Spec.Tools = []services.HostToolSpec{
		{
			Name:        "restore_lookup",
			Description: "Read a restore fixture",
			Mode:        "host",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}
	waiting, err := runtime.Admit(ctx, auth, waitingInput)
	if err != nil {
		t.Fatalf("admit waiting restore fixture: %v", err)
	}
	waitingClaim, disposition, err := ownership.ClaimExact(ctx, waiting.InvocationID, "restore-waiting", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim waiting restore fixture: disposition = %s, error = %v", disposition, err)
	}
	coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	recorded, err := coordinator.RecordModelCheckpoint(ctx, waitingClaim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role: domain.MessageRoleAssistant,
			Content: json.RawMessage(
				`[{"type":"tool_use","id":"restore-provider-call","name":"restore_lookup","input":{}}]`,
			),
		},
		Usage:      usage,
		Provenance: provenance,
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "restore-provider-call",
				Name:           "restore_lookup",
				Mode:           domain.ToolCallModeHost,
				Input:          json.RawMessage(`{}`),
			},
		},
	})
	if err != nil || len(recorded.ToolCalls) != 1 {
		t.Fatalf("checkpoint waiting restore fixture: result = %#v, error = %v", recorded, err)
	}
	if err := ownership.Settle(ctx, waitingClaim, domain.InvocationExecutionResult{
		Status:               domain.InvocationWaiting,
		MessagesCheckpointed: true,
		Usage:                &usage,
		Provenance:           &provenance,
	}); err != nil {
		t.Fatalf("park waiting restore fixture: %v", err)
	}

	return restoreFixtureIDs{
		completedInvocation: completed.InvocationID,
		queuedInvocation:    queued.InvocationID,
		waitingInvocation:   waiting.InvocationID,
		waitingSession:      waiting.SessionID,
	}
}

func findRestoreCheck(t *testing.T, verification RestoreVerification, component string) RestoreCheck {
	t.Helper()
	for _, check := range verification.Checks {
		if check.Component == component {
			return check
		}
	}
	t.Fatalf("restore verification omitted component %q: %#v", component, verification.Checks)
	return RestoreCheck{}
}

func restoreTableCounts(t *testing.T, pool *pgxpool.Pool) map[string]int64 {
	t.Helper()
	counts := make(map[string]int64)
	for _, table := range []string{
		"sessions",
		"invocations",
		"session_messages",
		"invocation_states",
		"tool_calls",
		"invocation_checkpoints",
	} {
		var count int64
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}

func restoreDatabaseURL(t *testing.T, baseURL, database string) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse restore drill URL: %v", err)
	}
	parsed.Path = "/" + database
	return parsed.String()
}

func runPostgresClient(t *testing.T, databaseURL, command string, args ...string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse %s database URL: %v", command, err)
	}
	password, _ := parsed.User.Password()
	passFile := filepath.Join(t.TempDir(), ".pgpass")
	passLine := strings.Join([]string{
		escapePGPass(parsed.Hostname()),
		escapePGPass(parsed.Port()),
		escapePGPass(strings.TrimPrefix(parsed.Path, "/")),
		escapePGPass(parsed.User.Username()),
		escapePGPass(password),
	}, ":") + "\n"
	if err := os.WriteFile(passFile, []byte(passLine), 0o600); err != nil {
		t.Fatalf("write temporary pgpass: %v", err)
	}
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}
	sslMode := parsed.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "prefer"
	}
	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(),
		"PGHOST="+parsed.Hostname(),
		"PGPORT="+port,
		"PGUSER="+parsed.User.Username(),
		"PGDATABASE="+strings.TrimPrefix(parsed.Path, "/"),
		"PGPASSFILE="+passFile,
		"PGSSLMODE="+sslMode,
		"PGCONNECT_TIMEOUT="+strconv.Itoa(10),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", command, err, output)
	}
}

func escapePGPass(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, ":", `\:`)
}
