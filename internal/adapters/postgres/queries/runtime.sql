-- name: CreateAccount :exec
INSERT INTO accounts (id, created_at)
VALUES ($1, $2);

-- name: GetAccount :one
SELECT id, created_at
FROM accounts
WHERE id = $1;

-- name: ListAccounts :many
SELECT id, created_at
FROM accounts
ORDER BY created_at, id
LIMIT 2;

-- name: LockInstallationBootstrap :one
SELECT pg_advisory_xact_lock(hashtextextended('nvoken:installation-bootstrap', 0));

-- name: CreateTenantPartition :exec
INSERT INTO tenant_partitions (id, account_id, tenant_ref, created_at)
VALUES ($1, $2, $3, $4);

-- name: CreateDefaultTenantPartitionIfAbsent :exec
INSERT INTO tenant_partitions (id, account_id, tenant_ref, created_at)
VALUES ($1, $2, NULL, $3)
ON CONFLICT (account_id) WHERE tenant_ref IS NULL
DO NOTHING;

-- name: CreateTenantPartitionByRefIfAbsent :exec
INSERT INTO tenant_partitions (id, account_id, tenant_ref, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (account_id, tenant_ref) WHERE tenant_ref IS NOT NULL
DO NOTHING;

-- name: GetTenantPartition :one
SELECT id, account_id, tenant_ref, created_at
FROM tenant_partitions
WHERE id = $1;

-- name: GetDefaultTenantPartition :one
SELECT id, account_id, tenant_ref, created_at
FROM tenant_partitions
WHERE account_id = $1 AND tenant_ref IS NULL;

-- name: GetTenantPartitionByRef :one
SELECT id, account_id, tenant_ref, created_at
FROM tenant_partitions
WHERE account_id = sqlc.arg(account_id)
  AND tenant_ref = sqlc.arg(tenant_ref)::text;

-- name: CreateAgent :exec
INSERT INTO agents (id, account_id, agent_ref, created_at)
VALUES ($1, $2, $3, $4);

-- name: CreateAgentIfAbsent :exec
INSERT INTO agents (id, account_id, agent_ref, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (account_id, agent_ref) DO NOTHING;

-- name: GetAgentByRef :one
SELECT id, account_id, agent_ref, created_at
FROM agents
WHERE account_id = $1 AND agent_ref = $2;

-- name: CreateSession :exec
INSERT INTO sessions (
    id, account_id, tenant_partition_id, agent_id, session_key,
    next_message_sequence, next_lifecycle_revision, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: CreateSessionIfAbsent :exec
INSERT INTO sessions (
    id, account_id, tenant_partition_id, agent_id, session_key,
    next_message_sequence, next_lifecycle_revision, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (account_id, tenant_partition_id, agent_id, session_key)
    WHERE session_key IS NOT NULL
DO NOTHING;

-- name: GetSession :one
SELECT id, account_id, tenant_partition_id, agent_id, session_key,
       next_message_sequence, next_lifecycle_revision, created_at, updated_at
FROM sessions
WHERE id = $1;

-- name: GetSessionForUpdate :one
SELECT id, account_id, tenant_partition_id, agent_id, session_key,
       next_message_sequence, next_lifecycle_revision, created_at, updated_at
FROM sessions
WHERE id = $1
FOR UPDATE;

-- name: GetSessionByKey :one
SELECT id, account_id, tenant_partition_id, agent_id, session_key,
       next_message_sequence, next_lifecycle_revision, created_at, updated_at
FROM sessions
WHERE account_id = $1
  AND tenant_partition_id = $2
  AND agent_id = $3
  AND session_key = sqlc.arg(session_key)::text;

-- name: ReserveMessageSequence :one
UPDATE sessions
SET next_message_sequence = next_message_sequence + 1,
    updated_at = CURRENT_TIMESTAMP
WHERE id = $1
RETURNING (next_message_sequence - 1)::bigint;

-- name: ReserveLifecycleRevision :one
UPDATE sessions
SET next_lifecycle_revision = next_lifecycle_revision + 1,
    updated_at = CURRENT_TIMESTAMP
WHERE id = $1
RETURNING (next_lifecycle_revision - 1)::bigint;

-- name: CreateExecutionSpecSnapshot :exec
INSERT INTO execution_spec_snapshots (id, account_id, spec, created_at)
VALUES ($1, $2, $3, $4);

-- name: GetExecutionSpecSnapshot :one
SELECT id, account_id, spec, created_at
FROM execution_spec_snapshots
WHERE id = $1;

-- name: CreateInvocation :exec
INSERT INTO invocations (
    id, session_id, account_id, tenant_partition_id, agent_id,
    spec_snapshot_id, idempotency_key, request_fingerprint, status,
    request_fingerprint_version, current_state_revision, error,
    wall_clock_timeout_ms, active_execution_timeout_ms, max_output_tokens,
    max_estimated_cost_microusd, max_iterations, active_execution_ms,
    wall_clock_deadline_at, output_schema_digest,
    created_at, updated_at, completed_at
) VALUES (
    sqlc.arg(id), sqlc.arg(session_id), sqlc.arg(account_id),
    sqlc.arg(tenant_partition_id), sqlc.arg(agent_id),
    sqlc.arg(spec_snapshot_id), sqlc.arg(idempotency_key),
    sqlc.arg(request_fingerprint), sqlc.arg(status), sqlc.arg(request_fingerprint_version),
    sqlc.arg(current_state_revision), sqlc.narg(error_payload),
    sqlc.arg(wall_clock_timeout_ms), sqlc.arg(active_execution_timeout_ms),
    sqlc.narg(max_output_tokens), sqlc.narg(max_estimated_cost_microusd),
    sqlc.arg(max_iterations), sqlc.arg(active_execution_ms),
    sqlc.arg(wall_clock_deadline_at), sqlc.narg(output_schema_digest),
    sqlc.arg(created_at), sqlc.arg(updated_at), sqlc.narg(completed_at)
);

-- name: GetInvocation :one
SELECT *
FROM invocations
WHERE id = $1;

-- name: GetInvocationForUpdate :one
SELECT *
FROM invocations
WHERE id = $1
FOR UPDATE;

-- name: FindNextQueuedInvocationForUpdate :one
SELECT i.*
FROM invocations AS i
JOIN sessions AS s ON s.id = i.session_id
WHERE i.status = 'queued'
  AND i.wall_clock_deadline_at > sqlc.arg(observed_at)
  AND i.active_execution_ms < i.active_execution_timeout_ms
ORDER BY i.created_at, i.id
FOR UPDATE OF s SKIP LOCKED
LIMIT 1;

-- name: ListExpiredInvocationLeases :many
SELECT *
FROM invocations
WHERE status = 'running'
  AND lease_expires_at <= sqlc.arg(observed_at)
ORDER BY lease_expires_at, id
LIMIT sqlc.arg(batch_limit);

-- name: ListExpiredInvocationDeadlines :many
SELECT *
FROM invocations
WHERE status IN ('queued', 'running', 'waiting')
  AND (
      wall_clock_deadline_at <= sqlc.arg(observed_at)
      OR active_execution_ms >= active_execution_timeout_ms
      OR (
          status = 'running'
          AND active_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
              (LEAST(
                  lease_expires_at,
                  execution_deadline_at,
                  sqlc.arg(observed_at)::timestamptz
              ) - active_segment_started_at)) * 1000)::bigint) >= active_execution_timeout_ms
      )
      OR (
          status = 'running'
          AND execution_deadline_at <= sqlc.arg(observed_at)
          AND (
              execution_deadline_scope <> 'execution_segment'
              OR lease_expires_at > sqlc.arg(observed_at)
          )
      )
  )
ORDER BY LEAST(wall_clock_deadline_at, COALESCE(execution_deadline_at, wall_clock_deadline_at)), id
LIMIT sqlc.arg(batch_limit);

-- name: GetInvocationByIdempotencyKey :one
SELECT *
FROM invocations
WHERE account_id = $1
  AND tenant_partition_id = $2
  AND agent_id = $3
  AND idempotency_key = $4;

-- name: GetNonterminalInvocationBySession :one
SELECT *
FROM invocations
WHERE session_id = $1
  AND status IN ('queued', 'running', 'waiting')
LIMIT 1;

-- name: LockInvocationAdmissionKey :one
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key)::text, 0));

-- name: ClaimInvocation :one
UPDATE invocations
SET status = 'running',
    current_state_revision = sqlc.arg(state_revision),
    lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = sqlc.arg(lease_expires_at),
    lease_attempt = lease_attempt + 1,
    active_segment_started_at = sqlc.arg(observed_at),
    execution_deadline_at = sqlc.arg(execution_deadline_at),
    execution_deadline_scope = sqlc.arg(execution_deadline_scope),
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'queued'
  AND wall_clock_deadline_at > sqlc.arg(observed_at)
RETURNING *;

-- name: RenewInvocationLease :one
UPDATE invocations
SET lease_expires_at = sqlc.arg(lease_expires_at),
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND lease_owner = sqlc.arg(lease_owner)
  AND lease_attempt = sqlc.arg(lease_attempt)
  AND lease_expires_at > sqlc.arg(observed_at)
  AND execution_deadline_at > sqlc.arg(observed_at)
RETURNING *;

-- name: SettleInvocation :one
UPDATE invocations
SET status = sqlc.arg(status),
    current_state_revision = sqlc.arg(state_revision),
    lease_owner = NULL,
    lease_expires_at = NULL,
    active_execution_ms = LEAST(
        active_execution_timeout_ms,
        active_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
            (sqlc.arg(observed_at)::timestamptz - active_segment_started_at)) * 1000)::bigint)
    ),
    active_segment_started_at = NULL,
    execution_deadline_at = NULL,
    execution_deadline_scope = NULL,
    error = sqlc.narg(error_payload),
    usage = sqlc.narg(usage_payload),
    provenance = sqlc.narg(provenance_payload),
    output = sqlc.narg(output_payload),
    output_provenance = sqlc.narg(output_provenance_payload),
    completed_at = sqlc.arg(observed_at),
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND lease_owner = sqlc.arg(lease_owner)
  AND lease_attempt = sqlc.arg(lease_attempt)
  AND lease_expires_at > sqlc.arg(observed_at)
  AND execution_deadline_at > sqlc.arg(observed_at)
RETURNING *;

-- name: RecoverInvocationLease :one
UPDATE invocations
SET status = 'queued',
    current_state_revision = sqlc.arg(state_revision),
    lease_owner = NULL,
    lease_expires_at = NULL,
    active_execution_ms = LEAST(
        active_execution_timeout_ms,
        active_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
            (LEAST(
                lease_expires_at,
                execution_deadline_at,
                sqlc.arg(observed_at)::timestamptz
            ) - active_segment_started_at)) * 1000)::bigint)
    ),
    active_segment_started_at = NULL,
    execution_deadline_at = NULL,
    execution_deadline_scope = NULL,
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND lease_attempt = sqlc.arg(lease_attempt)
  AND lease_expires_at <= sqlc.arg(observed_at)
RETURNING *;

-- name: CancelInvocation :one
UPDATE invocations
SET status = 'cancelled',
    current_state_revision = sqlc.arg(state_revision),
    lease_owner = NULL,
    lease_expires_at = NULL,
    active_execution_ms = LEAST(
        active_execution_timeout_ms,
        active_execution_ms + CASE WHEN active_segment_started_at IS NULL THEN 0 ELSE
            GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
                (sqlc.arg(observed_at)::timestamptz - active_segment_started_at)) * 1000)::bigint)
        END
    ),
    active_segment_started_at = NULL,
    execution_deadline_at = NULL,
    execution_deadline_scope = NULL,
    error = NULL,
    usage = NULL,
    provenance = NULL,
    completed_at = sqlc.arg(observed_at),
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status IN ('queued', 'running', 'waiting')
RETURNING *;

-- name: ReapInvocationDeadline :one
UPDATE invocations
SET status = 'failed',
    current_state_revision = sqlc.arg(state_revision),
    lease_owner = NULL,
    lease_expires_at = NULL,
    active_execution_ms = LEAST(
        active_execution_timeout_ms,
        active_execution_ms + CASE WHEN active_segment_started_at IS NULL THEN 0 ELSE
            GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
                (sqlc.arg(observed_at)::timestamptz - active_segment_started_at)) * 1000)::bigint)
        END
    ),
    active_segment_started_at = NULL,
    execution_deadline_at = NULL,
    execution_deadline_scope = NULL,
    error = sqlc.arg(error_payload),
    usage = NULL,
    provenance = NULL,
    completed_at = sqlc.arg(observed_at),
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status IN ('queued', 'running', 'waiting')
  AND (
      wall_clock_deadline_at <= sqlc.arg(observed_at)
      OR active_execution_ms >= active_execution_timeout_ms
      OR (status = 'running' AND execution_deadline_at <= sqlc.arg(observed_at))
  )
RETURNING *;

-- name: AppendSessionMessage :exec
INSERT INTO session_messages (
    id, session_id, account_id, tenant_partition_id, agent_id,
    invocation_id, sequence, role, content, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListSessionMessages :many
SELECT id, session_id, account_id, tenant_partition_id, agent_id,
       invocation_id, sequence, role, content, created_at
FROM session_messages
WHERE session_id = $1
ORDER BY sequence;

-- name: AppendInvocationState :exec
INSERT INTO invocation_states (
    id, invocation_id, session_id, account_id, tenant_partition_id,
    agent_id, revision, status, lease_attempt, through_message_sequence, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: CreateSyntheticDispatchWork :exec
INSERT INTO synthetic_dispatch_works (
    id, status, settlement_count, created_at, updated_at, settled_at
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSyntheticDispatchWork :one
SELECT * FROM synthetic_dispatch_works WHERE id = $1;

-- name: GetSyntheticDispatchWorkForUpdate :one
SELECT * FROM synthetic_dispatch_works WHERE id = $1 FOR UPDATE;

-- name: SettleSyntheticDispatchWork :one
UPDATE synthetic_dispatch_works
SET status = 'settled', settlement_count = settlement_count + 1,
    settled_at = sqlc.arg(observed_at), updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;

-- name: CreateExecutionDispatch :exec
INSERT INTO execution_dispatches (
    id, kind, work_id, account_id, tenant_partition_id, queue, status,
    available_at, task_name, publish_attempts, last_error, publisher_owner,
    publisher_lease_expires_at, publisher_attempt, published_at, settled_at,
    created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(kind), sqlc.arg(work_id), sqlc.narg(account_id),
    sqlc.narg(tenant_partition_id), sqlc.arg(queue), sqlc.arg(status),
    sqlc.arg(available_at), sqlc.narg(task_name), sqlc.arg(publish_attempts),
    sqlc.narg(last_error), sqlc.narg(publisher_owner),
    sqlc.narg(publisher_lease_expires_at), sqlc.arg(publisher_attempt),
    sqlc.narg(published_at), sqlc.narg(settled_at), sqlc.arg(created_at),
    sqlc.arg(updated_at)
);

-- name: GetExecutionDispatch :one
SELECT * FROM execution_dispatches WHERE id = $1;

-- name: GetExecutionDispatchForUpdate :one
SELECT * FROM execution_dispatches WHERE id = $1 FOR UPDATE;

-- name: ClaimNextExecutionDispatch :one
WITH candidate AS (
    SELECT source.id
    FROM execution_dispatches AS source
    WHERE source.queue = sqlc.arg(queue_name)
      AND (
        (source.status = 'pending' AND source.available_at <= sqlc.arg(observed_at))
        OR (source.status = 'publishing' AND source.publisher_lease_expires_at <= sqlc.arg(observed_at))
      )
    ORDER BY source.available_at, source.created_at, source.id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE execution_dispatches AS d
SET status = 'publishing',
    publisher_owner = sqlc.arg(publisher_owner),
    publisher_lease_expires_at = sqlc.arg(publisher_lease_expires_at),
    publisher_attempt = d.publisher_attempt + 1,
    publish_attempts = d.publish_attempts + 1,
    updated_at = sqlc.arg(observed_at)
FROM candidate
WHERE d.id = candidate.id
RETURNING d.*;

-- name: RenewExecutionDispatchPublication :one
UPDATE execution_dispatches
SET publisher_lease_expires_at = sqlc.arg(publisher_lease_expires_at),
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id) AND status = 'publishing'
  AND publisher_owner = sqlc.arg(publisher_owner)
  AND publisher_attempt = sqlc.arg(publisher_attempt)
  AND publisher_lease_expires_at > sqlc.arg(observed_at)
RETURNING *;

-- name: MarkExecutionDispatchPublished :one
UPDATE execution_dispatches
SET status = 'published', task_name = sqlc.arg(task_name),
    publisher_owner = NULL, publisher_lease_expires_at = NULL,
    published_at = sqlc.arg(observed_at), updated_at = sqlc.arg(observed_at),
    last_error = NULL
WHERE id = sqlc.arg(id) AND status = 'publishing'
  AND publisher_owner = sqlc.arg(publisher_owner)
  AND publisher_attempt = sqlc.arg(publisher_attempt)
  AND publisher_lease_expires_at > sqlc.arg(observed_at)
RETURNING *;

-- name: ReturnExecutionDispatchPending :one
UPDATE execution_dispatches
SET status = 'pending', available_at = sqlc.arg(available_at),
    publisher_owner = NULL, publisher_lease_expires_at = NULL,
    last_error = sqlc.arg(last_error), updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id) AND status = 'publishing'
  AND publisher_owner = sqlc.arg(publisher_owner)
  AND publisher_attempt = sqlc.arg(publisher_attempt)
RETURNING *;

-- name: SettleExecutionDispatch :one
UPDATE execution_dispatches
SET status = 'settled', publisher_owner = NULL,
    publisher_lease_expires_at = NULL, settled_at = sqlc.arg(observed_at),
    updated_at = sqlc.arg(observed_at), last_error = NULL
WHERE id = sqlc.arg(id) AND status IN ('pending', 'publishing', 'published')
RETURNING *;

-- name: SettleActiveExecutionDispatchForWork :execrows
UPDATE execution_dispatches
SET status = 'settled', publisher_owner = NULL,
    publisher_lease_expires_at = NULL, settled_at = sqlc.arg(observed_at),
    updated_at = sqlc.arg(observed_at), last_error = NULL
WHERE kind = sqlc.arg(kind) AND work_id = sqlc.arg(work_id)
  AND status IN ('pending', 'publishing', 'published');

-- name: AbandonExecutionDispatch :one
UPDATE execution_dispatches
SET status = 'abandoned', publisher_owner = NULL,
    publisher_lease_expires_at = NULL, settled_at = sqlc.arg(observed_at),
    updated_at = sqlc.arg(observed_at), last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id) AND status IN ('pending', 'publishing', 'published')
RETURNING *;

-- name: ListAgedExecutionDispatches :many
SELECT * FROM execution_dispatches
WHERE status IN ('pending', 'publishing', 'published')
  AND updated_at <= sqlc.arg(stale_before)
ORDER BY updated_at, id
LIMIT sqlc.arg(batch_limit);

-- name: ListAlertableAgedExecutionDispatches :many
SELECT d.* FROM execution_dispatches AS d
WHERE d.status IN ('pending', 'publishing', 'published')
  AND d.updated_at <= sqlc.arg(stale_before)
  AND NOT (
      d.kind = 'invocation'
      AND d.status = 'published'
      AND EXISTS (
          SELECT 1 FROM invocations AS i
          WHERE i.id = d.work_id
            AND i.status = 'running'
            AND i.lease_expires_at > sqlc.arg(observed_at)
      )
  )
ORDER BY d.updated_at, d.id
LIMIT sqlc.arg(batch_limit);

-- name: ListStalePublishedExecutionDispatches :many
SELECT * FROM execution_dispatches
WHERE status = 'published' AND updated_at <= sqlc.arg(stale_before)
ORDER BY updated_at, id
LIMIT sqlc.arg(batch_limit);

-- name: PruneTerminalExecutionDispatches :execrows
DELETE FROM execution_dispatches
WHERE id IN (
    SELECT prune.id FROM execution_dispatches AS prune
    WHERE prune.status IN ('settled', 'abandoned') AND prune.settled_at < sqlc.arg(prune_before)
    ORDER BY prune.settled_at, prune.id
    LIMIT sqlc.arg(batch_limit)
);

-- name: FindQueuedInvocationWithoutActiveDispatchForUpdate :one
-- Lock the Session serialization root, not the Invocation row. Lifecycle
-- transitions lock Session then Invocation; repair only needs queued state to
-- remain stable while it inserts the missing dispatch.
SELECT i.*
FROM invocations AS i
JOIN sessions AS s ON s.id = i.session_id
WHERE i.status = 'queued'
  AND i.wall_clock_deadline_at > sqlc.arg(observed_at)
  AND NOT EXISTS (
      SELECT 1
      FROM execution_dispatches AS d
      WHERE d.kind = 'invocation'
        AND d.work_id = i.id
        AND d.status IN ('pending', 'publishing', 'published')
  )
ORDER BY i.created_at, i.id
FOR UPDATE OF s SKIP LOCKED
LIMIT 1;

-- name: GetCurrentInvocationState :one
SELECT *
FROM invocation_states
WHERE invocation_id = $1
ORDER BY revision DESC
LIMIT 1;

-- name: ListInvocationStates :many
SELECT *
FROM invocation_states
WHERE session_id = $1
ORDER BY revision;

-- name: ListInvocationsForRecovery :many
SELECT i.*
FROM invocations AS i
WHERE i.account_id = sqlc.arg(account_id)
  AND (sqlc.narg(tenant_partition_id)::text IS NULL OR i.tenant_partition_id = sqlc.narg(tenant_partition_id)::text)
  AND (sqlc.narg(session_id)::text IS NULL OR i.session_id = sqlc.narg(session_id)::text)
  AND (sqlc.narg(agent_id)::text IS NULL OR i.agent_id = sqlc.narg(agent_id)::text)
  AND (sqlc.narg(status)::text IS NULL OR i.status = sqlc.narg(status)::text)
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (i.created_at, i.id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::text)
  )
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(batch_limit);

-- name: ListSessionsForRecovery :many
SELECT s.id, s.account_id, s.tenant_partition_id, s.agent_id, s.session_key,
       s.next_message_sequence, s.next_lifecycle_revision, s.created_at, s.updated_at,
       tp.tenant_ref,
       COALESCE(active.id, '') AS active_invocation_id,
       COALESCE(active.status, '') AS active_invocation_status
FROM sessions AS s
JOIN tenant_partitions AS tp ON tp.id = s.tenant_partition_id
LEFT JOIN LATERAL (
    SELECT i.id, i.status
    FROM invocations AS i
    WHERE i.session_id = s.id
      AND i.status IN ('queued', 'running', 'waiting')
    ORDER BY i.created_at, i.id
    LIMIT 1
) AS active ON true
WHERE s.account_id = sqlc.arg(account_id)
  AND (sqlc.narg(tenant_partition_id)::text IS NULL OR s.tenant_partition_id = sqlc.narg(tenant_partition_id)::text)
  AND (sqlc.narg(agent_id)::text IS NULL OR s.agent_id = sqlc.narg(agent_id)::text)
  AND (sqlc.narg(session_key)::text IS NULL OR s.session_key = sqlc.narg(session_key)::text)
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (s.created_at, s.id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::text)
  )
ORDER BY s.created_at DESC, s.id DESC
LIMIT sqlc.arg(batch_limit);

-- name: ListSessionMessagesRange :many
SELECT id, session_id, account_id, tenant_partition_id, agent_id,
       invocation_id, sequence, role, content, created_at
FROM session_messages
WHERE session_id = sqlc.arg(session_id)
  AND sequence > sqlc.arg(after_sequence)
  AND sequence <= sqlc.arg(through_sequence)
ORDER BY sequence
LIMIT sqlc.arg(batch_limit);

-- name: ListInvocationLifecycleChanges :many
SELECT st.id, st.invocation_id, st.session_id, st.account_id,
       st.tenant_partition_id, st.agent_id, st.revision, st.status,
       st.lease_attempt, st.through_message_sequence, st.created_at,
       i.error, i.usage, i.provenance, i.output, i.output_provenance
FROM invocation_states AS st
JOIN invocations AS i ON i.id = st.invocation_id
WHERE st.session_id = sqlc.arg(session_id)
  AND st.revision > sqlc.arg(after_revision)
  AND st.revision <= sqlc.arg(through_revision)
ORDER BY st.revision
LIMIT sqlc.arg(batch_limit);

-- name: CreateToolCall :exec
INSERT INTO tool_calls (
    id, invocation_id, session_id, account_id, tenant_partition_id, agent_id,
    iteration, batch_ordinal, provider_call_id, name, mode,
    request_message_id, request_message_sequence, request_digest, status,
    deadline_at, current_attempt, result_message_id, result_message_sequence,
    created_at, updated_at, completed_at
) VALUES (
    sqlc.arg(id), sqlc.arg(invocation_id), sqlc.arg(session_id),
    sqlc.arg(account_id), sqlc.arg(tenant_partition_id), sqlc.arg(agent_id),
    sqlc.arg(iteration), sqlc.arg(batch_ordinal), sqlc.arg(provider_call_id),
    sqlc.arg(name), sqlc.arg(mode), sqlc.arg(request_message_id),
    sqlc.arg(request_message_sequence), sqlc.arg(request_digest), sqlc.arg(status),
    sqlc.arg(deadline_at), sqlc.arg(current_attempt), sqlc.narg(result_message_id),
    sqlc.narg(result_message_sequence), sqlc.arg(created_at), sqlc.arg(updated_at),
    sqlc.narg(completed_at)
);

-- name: GetToolCall :one
SELECT * FROM tool_calls WHERE id = $1;

-- name: GetToolCallForUpdate :one
SELECT * FROM tool_calls WHERE id = $1 FOR UPDATE;

-- name: GetToolCallByProviderIdentityForUpdate :one
SELECT * FROM tool_calls
WHERE invocation_id = $1 AND iteration = $2 AND provider_call_id = $3
FOR UPDATE;

-- name: ListOpenToolCallsForUpdate :many
SELECT * FROM tool_calls
WHERE invocation_id = $1 AND status IN ('pending', 'running')
ORDER BY iteration, batch_ordinal, id
FOR UPDATE;

-- name: ListToolCallsByInvocation :many
SELECT * FROM tool_calls
WHERE invocation_id = $1
ORDER BY iteration, batch_ordinal, id;

-- name: ListToolCallsByIteration :many
SELECT * FROM tool_calls
WHERE invocation_id = $1 AND iteration = $2
ORDER BY batch_ordinal, id;

-- name: StartToolCallAttempt :one
UPDATE tool_calls
SET status = 'running', current_attempt = current_attempt + 1,
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;

-- name: RestartToolCallAttempt :one
UPDATE tool_calls
SET current_attempt = current_attempt + 1,
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id) AND status = 'running'
RETURNING *;

-- name: GetCurrentToolCallAttemptForUpdate :one
SELECT * FROM tool_call_attempts
WHERE tool_call_id = sqlc.arg(tool_call_id)
  AND attempt = sqlc.arg(attempt)
FOR UPDATE;

-- name: CreateToolCallAttempt :exec
INSERT INTO tool_call_attempts (
    id, tool_call_id, invocation_id, session_id, account_id,
    tenant_partition_id, agent_id, attempt, invocation_lease_attempt,
    status, started_at, completed_at
) VALUES (
    sqlc.arg(id), sqlc.arg(tool_call_id), sqlc.arg(invocation_id),
    sqlc.arg(session_id), sqlc.arg(account_id), sqlc.arg(tenant_partition_id),
    sqlc.arg(agent_id), sqlc.arg(attempt), sqlc.arg(invocation_lease_attempt),
    sqlc.arg(status), sqlc.arg(started_at), sqlc.narg(completed_at)
);

-- name: SettleToolCall :one
UPDATE tool_calls
SET status = sqlc.arg(status), result_message_id = sqlc.arg(result_message_id),
    result_message_sequence = sqlc.arg(result_message_sequence),
    completed_at = sqlc.arg(observed_at), updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id) AND status IN ('pending', 'running')
RETURNING *;

-- name: SettleToolCallAttempt :one
UPDATE tool_call_attempts
SET status = sqlc.arg(status), completed_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id) AND status = 'running'
RETURNING *;

-- name: SettleRunningToolCallAttempts :execrows
UPDATE tool_call_attempts
SET status = sqlc.arg(status), completed_at = sqlc.arg(observed_at)
WHERE tool_call_id = sqlc.arg(tool_call_id) AND status = 'running';

-- name: CreateModelUsageReceipt :exec
INSERT INTO model_usage_receipts (
    id, invocation_id, session_id, account_id, tenant_partition_id, agent_id,
    iteration, message_id, message_sequence, usage, provenance,
    evidence_digest, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(invocation_id), sqlc.arg(session_id),
    sqlc.arg(account_id), sqlc.arg(tenant_partition_id), sqlc.arg(agent_id),
    sqlc.arg(iteration), sqlc.arg(message_id), sqlc.arg(message_sequence),
    sqlc.arg(usage), sqlc.arg(provenance), sqlc.arg(evidence_digest),
    sqlc.arg(created_at)
);

-- name: GetModelUsageReceiptByIteration :one
SELECT * FROM model_usage_receipts
WHERE invocation_id = $1 AND iteration = $2;

-- name: ListModelUsageReceipts :many
SELECT * FROM model_usage_receipts
WHERE invocation_id = $1 ORDER BY iteration;

-- name: CreateInvocationCheckpoint :exec
INSERT INTO invocation_checkpoints (
    id, invocation_id, session_id, account_id, tenant_partition_id, agent_id,
    sequence, iteration, kind, lease_attempt, through_message_sequence,
    usage_receipt_id, tool_call_id, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(invocation_id), sqlc.arg(session_id),
    sqlc.arg(account_id), sqlc.arg(tenant_partition_id), sqlc.arg(agent_id),
    sqlc.arg(sequence), sqlc.arg(iteration), sqlc.arg(kind),
    sqlc.arg(lease_attempt), sqlc.arg(through_message_sequence),
    sqlc.narg(usage_receipt_id), sqlc.narg(tool_call_id), sqlc.arg(created_at)
);

-- name: GetLatestInvocationCheckpoint :one
SELECT * FROM invocation_checkpoints
WHERE invocation_id = $1 ORDER BY sequence DESC LIMIT 1;

-- name: ListInvocationCheckpoints :many
SELECT * FROM invocation_checkpoints
WHERE invocation_id = $1 ORDER BY sequence;

-- name: AdvanceInvocationCheckpoint :one
UPDATE invocations
SET current_checkpoint_sequence = sqlc.arg(checkpoint_sequence),
    current_iteration = sqlc.arg(iteration), updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id) AND status = 'running'
  AND lease_owner = sqlc.arg(lease_owner)
  AND lease_attempt = sqlc.arg(lease_attempt)
  AND lease_expires_at > sqlc.arg(observed_at)
  AND execution_deadline_at > sqlc.arg(observed_at)
  AND current_checkpoint_sequence < sqlc.arg(checkpoint_sequence)
  AND current_iteration <= sqlc.arg(iteration)
RETURNING *;

-- name: AdvanceInvocationCheckpointForTerminal :one
UPDATE invocations
SET current_checkpoint_sequence = sqlc.arg(checkpoint_sequence),
    current_iteration = sqlc.arg(iteration)
WHERE id = sqlc.arg(id) AND status IN ('queued', 'running', 'waiting')
  AND current_checkpoint_sequence < sqlc.arg(checkpoint_sequence)
  AND current_iteration <= sqlc.arg(iteration)
RETURNING *;
