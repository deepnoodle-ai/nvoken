package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// restoreManifestSchemaVersion intentionally pins the verifier's catalog
// expectations to one embedded schema. A new migration must update the
// manifest and its invariant queries before a newer binary can verify a
// restore.
const restoreManifestSchemaVersion uint = 17

var restoreRequiredTables = []string{
	"account_memberships",
	"accounts",
	"agents",
	"api_credentials",
	"browser_sessions",
	"callback_deliveries",
	"credential_issuances",
	"device_authorizations",
	"execution_dispatches",
	"execution_spec_snapshots",
	"invocation_checkpoints",
	"invocation_mcp_discoveries",
	"invocation_mcp_server_bindings",
	"invocation_provider_credentials",
	"invocation_states",
	"invocations",
	"model_usage_receipts",
	"nvoken_schema_compatibility",
	"nvoken_schema_migrations",
	"operator_subjects",
	"provider_credential_versions",
	"provider_credentials",
	"session_messages",
	"sessions",
	"static_credential_imports",
	"synthetic_dispatch_works",
	"tenant_partitions",
	"tool_call_attempts",
	"tool_calls",
}

var restoreRequiredConstraints = []string{
	"nvoken_schema_compatibility_singleton",
	"nvoken_schema_compatibility_schema_positive",
	"nvoken_schema_compatibility_minimum_positive",
	"nvoken_schema_compatibility_minimum_bounded",
	"sessions_partition_boundary",
	"sessions_agent_boundary",
	"sessions_identity_unique",
	"invocations_terminal_timestamp",
	"invocations_session_boundary",
	"invocations_snapshot_boundary",
	"invocations_identity_unique",
	"invocations_running_lease_shape",
	"invocations_active_segment_shape",
	"session_messages_invocation_boundary",
	"session_messages_session_sequence_unique",
	"session_messages_scoped_identity_unique",
	"invocation_states_invocation_boundary",
	"invocation_states_message_watermark",
	"invocation_states_session_revision_unique",
	"tool_calls_invocation_boundary",
	"tool_calls_request_message_boundary",
	"tool_calls_result_message_boundary",
	"tool_calls_terminal_shape",
	"tool_calls_provider_iteration_unique",
	"invocation_checkpoints_invocation_boundary",
	"invocation_checkpoints_message_watermark",
	"invocation_checkpoints_receipt_boundary",
	"invocation_checkpoints_tool_call_boundary",
	"invocation_checkpoints_sequence_unique",
	"invocation_mcp_server_bindings_invocation_boundary",
	"invocation_mcp_server_bindings_secret_shape",
	"invocation_mcp_server_bindings_server_unique",
	"invocation_mcp_discoveries_invocation_boundary",
	"invocation_mcp_discoveries_invocation_unique",
}

const restoreRequiredNonterminalIndex = "invocations_one_nonterminal_per_session"

// RestoreCheck is one bounded, content-free restore verification result.
type RestoreCheck struct {
	Component       string
	Passed          bool
	ErrorClass      string
	RecordsExamined int64
}

// RestoreVerification is the complete verifier result for one consistent
// database snapshot.
type RestoreVerification struct {
	Schema SchemaStatus
	Checks []RestoreCheck
}

func (v RestoreVerification) Passed() bool {
	if len(v.Checks) == 0 {
		return false
	}
	for _, check := range v.Checks {
		if !check.Passed {
			return false
		}
	}
	return true
}

// VerifyRestore checks a restored database without changing it. Catalog and
// durable-state checks run in one repeatable-read, read-only transaction. It
// deliberately reads metadata only and never starts execution components.
func VerifyRestore(ctx context.Context, pool *pgxpool.Pool) (RestoreVerification, error) {
	if pool == nil {
		return RestoreVerification{}, fmt.Errorf("restore verification pool is required")
	}
	verification := RestoreVerification{}
	status, err := InspectSchema(ctx, pool)
	if err != nil {
		return verification, fmt.Errorf("inspect restored schema: %w", err)
	}
	verification.Schema = status
	verification.Checks = append(verification.Checks, RestoreCheck{
		Component:  "database_schema",
		Passed:     status.Compatible(),
		ErrorClass: string(status.State),
	})
	if !status.Compatible() {
		return verification, fmt.Errorf("restored schema is %s", status.State)
	}
	if status.Expected != restoreManifestSchemaVersion {
		verification.Checks = append(verification.Checks, RestoreCheck{
			Component:  "verifier_manifest",
			ErrorClass: "outdated",
		})
		return verification, fmt.Errorf(
			"restore verifier manifest covers schema %06d, binary expects %06d",
			restoreManifestSchemaVersion,
			status.Expected,
		)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return verification, fmt.Errorf("begin read-only restore verification: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	readOnly, err := restoreTransactionReadOnly(ctx, tx)
	if err != nil {
		return verification, fmt.Errorf("confirm read-only restore verification: %w", err)
	}
	verification.Checks = append(verification.Checks, RestoreCheck{
		Component:  "read_only_transaction",
		Passed:     readOnly,
		ErrorClass: restoreCheckClass(readOnly, "not_read_only"),
	})
	if !readOnly {
		return verification, fmt.Errorf("restore verification transaction is not read only")
	}

	missingTables, err := restoreMissingTables(ctx, tx)
	if err != nil {
		return verification, fmt.Errorf("inspect restored tables: %w", err)
	}
	tablesPassed := missingTables == 0
	verification.Checks = append(verification.Checks, RestoreCheck{
		Component:       "required_tables",
		Passed:          tablesPassed,
		ErrorClass:      restoreCheckClass(tablesPassed, "missing"),
		RecordsExamined: int64(len(restoreRequiredTables)),
	})
	if !tablesPassed {
		return verification, fmt.Errorf("restored schema is missing %d required tables", missingTables)
	}

	missingConstraints, err := restoreMissingConstraints(ctx, tx)
	if err != nil {
		return verification, fmt.Errorf("inspect restored constraints: %w", err)
	}
	constraintsPassed := missingConstraints == 0
	verification.Checks = append(verification.Checks, RestoreCheck{
		Component:       "required_constraints",
		Passed:          constraintsPassed,
		ErrorClass:      restoreCheckClass(constraintsPassed, "missing_or_unvalidated"),
		RecordsExamined: int64(len(restoreRequiredConstraints)),
	})

	indexValid, err := restoreNonterminalIndexValid(ctx, tx)
	if err != nil {
		return verification, fmt.Errorf("inspect restored nonterminal index: %w", err)
	}
	verification.Checks = append(verification.Checks, RestoreCheck{
		Component:       "nonterminal_unique_index",
		Passed:          indexValid,
		ErrorClass:      restoreCheckClass(indexValid, "missing_or_invalid"),
		RecordsExamined: 1,
	})

	invariantQueries := []struct {
		component  string
		errorClass string
		query      string
	}{
		{
			component:  "churn_table_autovacuum_parameters",
			errorClass: "missing_storage_parameters",
			query: `SELECT EXISTS (
				SELECT 1
				FROM unnest(ARRAY[
					'sessions', 'invocations', 'tool_calls',
					'execution_dispatches', 'callback_deliveries'
				]) AS churn(relname)
				LEFT JOIN pg_catalog.pg_class c
				  ON c.relname = churn.relname
				 AND c.relnamespace = (
					SELECT oid FROM pg_catalog.pg_namespace
					WHERE nspname = current_schema()
				 )
				WHERE c.oid IS NULL
				   OR NOT COALESCE(c.reloptions @> ARRAY['autovacuum_vacuum_scale_factor=0.05'], false)
				   OR NOT COALESCE(c.reloptions @> ARRAY['autovacuum_analyze_scale_factor=0.02'], false)
			)`,
		},
		{
			component:  "one_nonterminal_invocation_per_session",
			errorClass: "conflict",
			query: `SELECT EXISTS (
				SELECT 1
				FROM invocations
				WHERE status IN ('queued', 'running', 'waiting')
				GROUP BY session_id
				HAVING count(*) > 1
			)`,
		},
		{
			component:  "terminal_state_consistency",
			errorClass: "inconsistent",
			query: `SELECT EXISTS (
				SELECT 1
				FROM invocations i
				LEFT JOIN invocation_states current_state
				  ON current_state.invocation_id = i.id
				 AND current_state.revision = i.current_state_revision
				WHERE ((i.status IN ('completed', 'failed', 'cancelled')) <> (i.completed_at IS NOT NULL))
				   OR (i.status <> 'running' AND (
					   i.lease_owner IS NOT NULL OR i.lease_expires_at IS NOT NULL
					   OR i.active_segment_started_at IS NOT NULL OR i.execution_deadline_at IS NOT NULL
					   OR i.execution_deadline_scope IS NOT NULL
				   ))
				   OR current_state.id IS NULL
				   OR current_state.status <> i.status
				   OR EXISTS (
					   SELECT 1 FROM invocation_states later
					   WHERE later.invocation_id = i.id
					     AND later.revision > i.current_state_revision
				   )
			)`,
		},
		{
			component:  "transcript_cursor_bounds",
			errorClass: "out_of_bounds",
			query: `SELECT EXISTS (
				SELECT 1
				FROM sessions s
				LEFT JOIN LATERAL (
					SELECT COALESCE(max(m.sequence), 0) AS max_message_sequence
					FROM session_messages m WHERE m.session_id = s.id
				) messages ON true
				LEFT JOIN LATERAL (
					SELECT COALESCE(max(st.revision), 0) AS max_state_revision
					FROM invocation_states st WHERE st.session_id = s.id
				) states ON true
				WHERE s.next_message_sequence <> messages.max_message_sequence + 1
				   OR s.next_lifecycle_revision <> states.max_state_revision + 1
				   OR EXISTS (
					   SELECT 1 FROM invocation_states st
					   WHERE st.session_id = s.id
					     AND st.through_message_sequence > messages.max_message_sequence
				   )
			)`,
		},
		{
			component:  "checkpoint_cursor_bounds",
			errorClass: "out_of_bounds",
			query: `SELECT EXISTS (
				SELECT 1
				FROM invocations i
				LEFT JOIN LATERAL (
					SELECT COALESCE(max(c.sequence), 0) AS max_sequence,
					       COALESCE(max(c.iteration), 0) AS max_iteration,
					       count(*) AS checkpoint_count
					FROM invocation_checkpoints c WHERE c.invocation_id = i.id
				) checkpoints ON true
				LEFT JOIN LATERAL (
					SELECT COALESCE(max(m.sequence), 0) AS max_message_sequence
					FROM session_messages m WHERE m.session_id = i.session_id
				) messages ON true
				WHERE i.current_checkpoint_sequence <> checkpoints.max_sequence
				   OR i.current_iteration <> checkpoints.max_iteration
				   OR checkpoints.max_sequence <> checkpoints.checkpoint_count
				   OR EXISTS (
					   SELECT 1 FROM invocation_checkpoints c
					   WHERE c.invocation_id = i.id
					     AND c.through_message_sequence > messages.max_message_sequence
				   )
			)`,
		},
	}
	for _, invariant := range invariantQueries {
		violated, queryErr := restoreInvariantViolated(ctx, tx, invariant.query)
		if queryErr != nil {
			return verification, fmt.Errorf("check %s: %w", invariant.component, queryErr)
		}
		verification.Checks = append(verification.Checks, RestoreCheck{
			Component:  invariant.component,
			Passed:     !violated,
			ErrorClass: restoreCheckClass(!violated, invariant.errorClass),
		})
	}

	representativeQueries := []struct {
		component string
		query     string
	}{
		{
			component: "representative_session",
			query: `SELECT count(*) FROM (
				SELECT id, account_id, next_message_sequence, next_lifecycle_revision
				FROM sessions ORDER BY created_at, id LIMIT 1
			) sample`,
		},
		{
			component: "representative_invocation",
			query: `SELECT count(*) FROM (
				SELECT id, session_id, status, current_state_revision, current_checkpoint_sequence
				FROM invocations ORDER BY created_at, id LIMIT 1
			) sample`,
		},
		{
			component: "representative_transcript",
			query: `SELECT count(*) FROM (
				SELECT id, session_id, invocation_id, sequence, role
				FROM session_messages ORDER BY session_id, sequence LIMIT 1
			) sample`,
		},
		{
			component: "representative_tool_call",
			query: `SELECT count(*) FROM (
				SELECT id, invocation_id, iteration, mode, status
				FROM tool_calls ORDER BY created_at, id LIMIT 1
			) sample`,
		},
		{
			component: "representative_checkpoint",
			query: `SELECT count(*) FROM (
				SELECT id, invocation_id, sequence, iteration, kind, through_message_sequence
				FROM invocation_checkpoints ORDER BY created_at, id LIMIT 1
			) sample`,
		},
	}
	for _, representative := range representativeQueries {
		var count int64
		if err := tx.QueryRow(ctx, representative.query).Scan(&count); err != nil {
			return verification, fmt.Errorf("read %s: %w", representative.component, err)
		}
		passed := count == 1
		verification.Checks = append(verification.Checks, RestoreCheck{
			Component:       representative.component,
			Passed:          passed,
			ErrorClass:      restoreCheckClass(passed, "missing"),
			RecordsExamined: count,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return verification, fmt.Errorf("finish read-only restore verification: %w", err)
	}
	if !verification.Passed() {
		return verification, fmt.Errorf("one or more restore verification checks failed")
	}
	return verification, nil
}

func restoreTransactionReadOnly(ctx context.Context, tx pgx.Tx) (bool, error) {
	var value string
	if err := tx.QueryRow(ctx, "SHOW transaction_read_only").Scan(&value); err != nil {
		return false, err
	}
	return value == "on", nil
}

func restoreMissingTables(ctx context.Context, tx pgx.Tx) (int, error) {
	var missing int
	err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM unnest($1::text[]) AS required(name)
		LEFT JOIN pg_catalog.pg_namespace n
		  ON n.nspname = current_schema()
		LEFT JOIN pg_catalog.pg_class c
		  ON c.relnamespace = n.oid
		 AND c.relname = required.name
		 AND c.relkind IN ('r', 'p')
		WHERE c.oid IS NULL
	`, restoreRequiredTables).Scan(&missing)
	return missing, err
}

func restoreMissingConstraints(ctx context.Context, tx pgx.Tx) (int, error) {
	var missing int
	err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM unnest($1::text[]) AS required(name)
		LEFT JOIN pg_catalog.pg_namespace n
		  ON n.nspname = current_schema()
		LEFT JOIN pg_catalog.pg_constraint c
		  ON c.connamespace = n.oid
		 AND c.conname = required.name
		 AND c.convalidated
		WHERE c.oid IS NULL
	`, restoreRequiredConstraints).Scan(&missing)
	return missing, err
}

func restoreNonterminalIndexValid(ctx context.Context, tx pgx.Tx) (bool, error) {
	var valid bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_catalog.pg_class c
			JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_catalog.pg_index i ON i.indexrelid = c.oid
			WHERE n.nspname = current_schema()
			  AND c.relname = $1
			  AND i.indisunique
			  AND i.indisvalid
			  AND i.indisready
			  AND i.indpred IS NOT NULL
		)
	`, restoreRequiredNonterminalIndex).Scan(&valid)
	return valid, err
}

func restoreInvariantViolated(ctx context.Context, tx pgx.Tx, query string) (bool, error) {
	var violated bool
	err := tx.QueryRow(ctx, query).Scan(&violated)
	return violated, err
}

func restoreCheckClass(passed bool, failedClass string) string {
	if passed {
		return "none"
	}
	return failedClass
}
