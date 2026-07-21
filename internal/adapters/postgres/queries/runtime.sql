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
    current_state_revision, error, created_at, updated_at, completed_at
) VALUES (
    sqlc.arg(id), sqlc.arg(session_id), sqlc.arg(account_id),
    sqlc.arg(tenant_partition_id), sqlc.arg(agent_id),
    sqlc.arg(spec_snapshot_id), sqlc.arg(idempotency_key),
    sqlc.arg(request_fingerprint), sqlc.arg(status),
    sqlc.arg(current_state_revision), sqlc.narg(error_payload),
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
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'queued'
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
RETURNING *;

-- name: SettleInvocation :one
UPDATE invocations
SET status = sqlc.arg(status),
    current_state_revision = sqlc.arg(state_revision),
    lease_owner = NULL,
    lease_expires_at = NULL,
    error = sqlc.narg(error_payload),
    usage = sqlc.narg(usage_payload),
    provenance = sqlc.narg(provenance_payload),
    completed_at = sqlc.arg(observed_at),
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND lease_owner = sqlc.arg(lease_owner)
  AND lease_attempt = sqlc.arg(lease_attempt)
  AND lease_expires_at > sqlc.arg(observed_at)
RETURNING *;

-- name: ReapInvocationLease :one
UPDATE invocations
SET status = 'failed',
    current_state_revision = sqlc.arg(state_revision),
    lease_owner = NULL,
    lease_expires_at = NULL,
    error = sqlc.arg(error_payload),
    completed_at = sqlc.arg(observed_at),
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND lease_attempt = sqlc.arg(lease_attempt)
  AND lease_expires_at <= sqlc.arg(observed_at)
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
       i.error, i.usage, i.provenance
FROM invocation_states AS st
JOIN invocations AS i ON i.id = st.invocation_id
WHERE st.session_id = sqlc.arg(session_id)
  AND st.revision > sqlc.arg(after_revision)
  AND st.revision <= sqlc.arg(through_revision)
ORDER BY st.revision
LIMIT sqlc.arg(batch_limit);
