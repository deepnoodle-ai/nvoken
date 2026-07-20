-- name: CreateAccount :exec
INSERT INTO accounts (id, created_at)
VALUES ($1, $2);

-- name: GetAccount :one
SELECT id, created_at
FROM accounts
WHERE id = $1;

-- name: CreateTenantPartition :exec
INSERT INTO tenant_partitions (id, account_id, tenant_ref, created_at)
VALUES ($1, $2, $3, $4);

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

-- name: GetAgentByRef :one
SELECT id, account_id, agent_ref, created_at
FROM agents
WHERE account_id = $1 AND agent_ref = $2;

-- name: CreateSession :exec
INSERT INTO sessions (
    id, account_id, tenant_partition_id, agent_id, session_key,
    next_message_sequence, next_lifecycle_revision, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetSession :one
SELECT id, account_id, tenant_partition_id, agent_id, session_key,
       next_message_sequence, next_lifecycle_revision, created_at, updated_at
FROM sessions
WHERE id = $1;

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
SELECT id, session_id, account_id, tenant_partition_id, agent_id,
       spec_snapshot_id, idempotency_key, request_fingerprint, status,
       current_state_revision, error, created_at, updated_at, completed_at
FROM invocations
WHERE id = $1;

-- name: GetInvocationByIdempotencyKey :one
SELECT id, session_id, account_id, tenant_partition_id, agent_id,
       spec_snapshot_id, idempotency_key, request_fingerprint, status,
       current_state_revision, error, created_at, updated_at, completed_at
FROM invocations
WHERE account_id = $1
  AND tenant_partition_id = $2
  AND agent_id = $3
  AND idempotency_key = $4;

-- name: UpdateInvocationStatus :exec
UPDATE invocations
SET status = sqlc.arg(status),
    current_state_revision = sqlc.arg(state_revision),
    error = sqlc.narg(error_payload),
    completed_at = sqlc.narg(completed_at),
    updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id);

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
    agent_id, revision, status, through_message_sequence, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListInvocationStates :many
SELECT id, invocation_id, session_id, account_id, tenant_partition_id,
       agent_id, revision, status, through_message_sequence, created_at
FROM invocation_states
WHERE session_id = $1
ORDER BY revision;
