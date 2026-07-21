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

-- name: FindNextQueuedInvocation :one
SELECT *
FROM invocations
WHERE status = 'queued'
ORDER BY created_at, id
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
