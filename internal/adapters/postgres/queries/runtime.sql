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

-- name: CreateOperatorSubject :exec
INSERT INTO operator_subjects (id, account_id, issuer, subject, created_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetOperatorSubjectByIdentity :one
SELECT * FROM operator_subjects
WHERE account_id = $1 AND issuer = $2 AND subject = $3;

-- name: GetOperatorSubject :one
SELECT * FROM operator_subjects WHERE id = $1;

-- name: CreateMembership :exec
INSERT INTO account_memberships (id, account_id, subject_id, role, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: UpsertMembership :one
INSERT INTO account_memberships (id, account_id, subject_id, role, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (account_id, subject_id) DO UPDATE
SET role = EXCLUDED.role, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: GetMembershipBySubject :one
SELECT * FROM account_memberships
WHERE account_id = $1 AND subject_id = $2;

-- name: DeleteMembershipBySubject :execrows
DELETE FROM account_memberships
WHERE account_id = $1 AND subject_id = $2;

-- name: CreateAPICredential :exec
INSERT INTO api_credentials (
    id, account_id, kind, name, prefix, verifier, status, profile, role_cap,
    owner_subject_id, creator_subject_id, creator_credential_id, tenant_constraint, session_constraint,
    operation_constraints, expires_at, rotated_from_id, rotation_overlap_ends_at,
    revoked_at, last_used_at, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, sqlc.narg(profile), sqlc.narg(role_cap),
    sqlc.narg(owner_subject_id), sqlc.narg(creator_subject_id), sqlc.narg(creator_credential_id), sqlc.narg(tenant_constraint),
    sqlc.narg(session_constraint), $8, sqlc.narg(expires_at), sqlc.narg(rotated_from_id),
    sqlc.narg(rotation_overlap_ends_at), sqlc.narg(revoked_at), sqlc.narg(last_used_at), $9, $10
);

-- name: GetAPICredential :one
SELECT * FROM api_credentials WHERE id = $1;

-- name: GetAPICredentialByPrefix :one
SELECT * FROM api_credentials WHERE prefix = $1;

-- name: GetAPICredentialForUpdate :one
SELECT * FROM api_credentials WHERE id = $1 FOR UPDATE;

-- name: ListAPICredentials :many
SELECT * FROM api_credentials
WHERE account_id = $1
ORDER BY created_at DESC, id DESC;

-- name: TouchAPICredential :exec
UPDATE api_credentials
SET last_used_at = GREATEST(COALESCE(last_used_at, sqlc.arg(used_at)::timestamptz), sqlc.arg(used_at)),
    updated_at = GREATEST(updated_at, sqlc.arg(used_at))
WHERE id = sqlc.arg(id) AND status = 'active'
  AND (last_used_at IS NULL OR last_used_at <= sqlc.arg(used_at)::timestamptz - INTERVAL '5 minutes');

-- name: RevokeAPICredential :one
UPDATE api_credentials
SET status = 'revoked', revoked_at = sqlc.arg(revoked_at), updated_at = sqlc.arg(revoked_at)
WHERE id = sqlc.arg(id) AND account_id = sqlc.arg(account_id) AND status = 'active'
RETURNING *;

-- name: SetCredentialRotationOverlap :one
UPDATE api_credentials
SET rotation_overlap_ends_at = sqlc.arg(overlap_ends_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND account_id = sqlc.arg(account_id) AND status = 'active'
RETURNING *;

-- name: CreateCredentialIssuance :exec
INSERT INTO credential_issuances (
    account_id, scope, idempotency_key, request_hash, credential_id,
    ciphertext, nonce, expires_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetCredentialIssuance :one
SELECT * FROM credential_issuances
WHERE account_id = $1 AND scope = $2 AND idempotency_key = $3;

-- name: ClearExpiredCredentialIssuance :exec
UPDATE credential_issuances SET ciphertext = NULL, nonce = NULL
WHERE account_id = $1 AND scope = $2 AND idempotency_key = $3
  AND expires_at <= sqlc.arg(observed_at);

-- name: GetStaticCredentialImport :one
SELECT * FROM static_credential_imports
WHERE account_id = $1 AND import_key = $2;

-- name: CreateStaticCredentialImport :exec
INSERT INTO static_credential_imports (account_id, import_key, credential_id, imported_at)
VALUES ($1, $2, $3, $4);

-- name: CreateDeviceAuthorization :exec
INSERT INTO device_authorizations (
    id, account_id, device_code_hash, user_code_hash, user_code_display,
    device_label, role_cap, tenant_constraint, session_constraint, status,
    poll_interval_seconds, next_poll_at, confirmation_attempts,
    approved_by_subject_id, credential_id, expires_at, delivery_expires_at,
    created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, sqlc.narg(tenant_constraint),
    sqlc.narg(session_constraint), $8, $9, $10, $11,
    sqlc.narg(approved_by_subject_id), sqlc.narg(credential_id), $12,
    sqlc.narg(delivery_expires_at), $13, $14
);

-- name: GetDeviceAuthorizationByDeviceCodeForUpdate :one
SELECT * FROM device_authorizations WHERE device_code_hash = $1 FOR UPDATE;

-- name: GetDeviceAuthorizationByUserCodeForUpdate :one
SELECT * FROM device_authorizations WHERE user_code_hash = $1 FOR UPDATE;

-- name: RecordDevicePoll :one
UPDATE device_authorizations
SET poll_interval_seconds = sqlc.arg(poll_interval_seconds),
    next_poll_at = sqlc.arg(next_poll_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ApproveDeviceAuthorization :one
UPDATE device_authorizations
SET status = 'approved', approved_by_subject_id = sqlc.arg(approved_by_subject_id),
    credential_id = sqlc.arg(credential_id), delivery_expires_at = sqlc.arg(delivery_expires_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;

-- name: DenyDeviceAuthorization :one
UPDATE device_authorizations
SET status = 'denied', updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;

-- name: ExchangeDeviceAuthorization :one
UPDATE device_authorizations
SET status = 'exchanged', updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status IN ('approved', 'exchanged')
RETURNING *;

-- name: IncrementDeviceConfirmationAttempts :one
UPDATE device_authorizations
SET confirmation_attempts = confirmation_attempts + 1, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND confirmation_attempts < 20
RETURNING *;

-- name: CreateBrowserSession :exec
INSERT INTO browser_sessions (
    id, account_id, subject_id, token_hash, csrf_hash, expires_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetBrowserSessionByTokenHash :one
SELECT * FROM browser_sessions WHERE token_hash = $1;

-- name: DeleteBrowserSession :execrows
DELETE FROM browser_sessions WHERE id = $1;

-- name: CreateTenantPartition :exec
INSERT INTO tenant_partitions (id, account_id, tenant_key, created_at)
VALUES ($1, $2, $3, $4);

-- name: CreateDefaultTenantPartitionIfAbsent :exec
INSERT INTO tenant_partitions (id, account_id, tenant_key, created_at)
VALUES ($1, $2, NULL, $3)
ON CONFLICT (account_id) WHERE tenant_key IS NULL
DO NOTHING;

-- name: CreateTenantPartitionByRefIfAbsent :exec
INSERT INTO tenant_partitions (id, account_id, tenant_key, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (account_id, tenant_key) WHERE tenant_key IS NOT NULL
DO NOTHING;

-- name: GetTenantPartition :one
SELECT id, account_id, tenant_key, created_at
FROM tenant_partitions
WHERE id = $1;

-- name: GetDefaultTenantPartition :one
SELECT id, account_id, tenant_key, created_at
FROM tenant_partitions
WHERE account_id = $1 AND tenant_key IS NULL;

-- name: GetTenantPartitionByRef :one
SELECT id, account_id, tenant_key, created_at
FROM tenant_partitions
WHERE account_id = sqlc.arg(account_id)
  AND tenant_key = sqlc.arg(tenant_key)::text;

-- name: CreateAgent :exec
INSERT INTO agents (id, account_id, agent_key, created_at)
VALUES ($1, $2, $3, $4);

-- name: CreateAgentIfAbsent :exec
INSERT INTO agents (id, account_id, agent_key, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (account_id, agent_key) DO NOTHING;

-- name: GetAgentByRef :one
SELECT id, account_id, agent_key, created_at
FROM agents
WHERE account_id = $1 AND agent_key = $2;

-- name: GetAgentByID :one
SELECT id, account_id, agent_key, created_at
FROM agents
WHERE id = $1;

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
    total_timeout_ms, active_timeout_ms, waiting_timeout_ms, max_output_tokens,
    max_estimated_cost_microusd, max_iterations, active_execution_ms,
    waiting_execution_ms, deadline_at, output_schema_digest,
    created_at, updated_at, completed_at
) VALUES (
    sqlc.arg(id), sqlc.arg(session_id), sqlc.arg(account_id),
    sqlc.arg(tenant_partition_id), sqlc.arg(agent_id),
    sqlc.arg(spec_snapshot_id), sqlc.arg(idempotency_key),
    sqlc.arg(request_fingerprint), sqlc.arg(status), sqlc.arg(request_fingerprint_version),
    sqlc.arg(current_state_revision), sqlc.narg(error_payload),
    sqlc.arg(total_timeout_ms), sqlc.arg(active_timeout_ms), sqlc.arg(waiting_timeout_ms),
    sqlc.narg(max_output_tokens), sqlc.narg(max_estimated_cost_microusd),
    sqlc.arg(max_iterations), sqlc.arg(active_execution_ms),
    sqlc.arg(waiting_execution_ms),
    sqlc.arg(deadline_at), sqlc.narg(output_schema_digest),
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
  AND i.deadline_at > sqlc.arg(observed_at)
  AND i.active_execution_ms < i.active_timeout_ms
  AND i.waiting_execution_ms < i.waiting_timeout_ms
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
      deadline_at <= sqlc.arg(observed_at)
      OR active_execution_ms >= active_timeout_ms
      OR waiting_execution_ms >= waiting_timeout_ms
      OR (
          status = 'waiting'
          AND waiting_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
              (sqlc.arg(observed_at)::timestamptz - waiting_segment_started_at)) * 1000)::bigint) >= waiting_timeout_ms
      )
      OR (
          status = 'running'
          AND active_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
              (LEAST(
                  lease_expires_at,
                  execution_deadline_at,
                  sqlc.arg(observed_at)::timestamptz
              ) - active_segment_started_at)) * 1000)::bigint) >= active_timeout_ms
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
ORDER BY LEAST(deadline_at, COALESCE(execution_deadline_at, deadline_at)), id
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
    waiting_segment_started_at = NULL,
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'queued'
  AND deadline_at > sqlc.arg(observed_at)
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
        active_timeout_ms,
        active_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
            (sqlc.arg(observed_at)::timestamptz - active_segment_started_at)) * 1000)::bigint)
    ),
    active_segment_started_at = NULL,
    waiting_segment_started_at = NULL,
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

-- name: ParkInvocationForHostTools :one
UPDATE invocations
SET status = 'waiting',
    current_state_revision = sqlc.arg(state_revision),
    lease_owner = NULL,
    lease_expires_at = NULL,
    active_execution_ms = LEAST(
        active_timeout_ms,
        active_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
            (sqlc.arg(observed_at)::timestamptz - active_segment_started_at)) * 1000)::bigint)
    ),
    active_segment_started_at = NULL,
    waiting_segment_started_at = sqlc.arg(observed_at),
    execution_deadline_at = NULL,
    execution_deadline_scope = NULL,
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND lease_owner = sqlc.arg(lease_owner)
  AND lease_attempt = sqlc.arg(lease_attempt)
  AND lease_expires_at > sqlc.arg(observed_at)
  AND execution_deadline_at > sqlc.arg(observed_at)
  AND deadline_at > sqlc.arg(observed_at)
  AND waiting_execution_ms < waiting_timeout_ms
RETURNING *;

-- name: QueueWaitingInvocation :one
UPDATE invocations
SET status = 'queued',
    current_state_revision = sqlc.arg(state_revision),
    waiting_execution_ms = LEAST(
        waiting_timeout_ms,
        waiting_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
            (sqlc.arg(observed_at)::timestamptz - waiting_segment_started_at)) * 1000)::bigint)
    ),
    waiting_segment_started_at = NULL,
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'waiting'
  AND deadline_at > sqlc.arg(observed_at)
  AND waiting_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
      (sqlc.arg(observed_at)::timestamptz - waiting_segment_started_at)) * 1000)::bigint) < waiting_timeout_ms
RETURNING *;

-- name: RecoverInvocationLease :one
UPDATE invocations
SET status = 'queued',
    current_state_revision = sqlc.arg(state_revision),
    lease_owner = NULL,
    lease_expires_at = NULL,
    active_execution_ms = LEAST(
        active_timeout_ms,
        active_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
            (LEAST(
                lease_expires_at,
                execution_deadline_at,
                sqlc.arg(observed_at)::timestamptz
            ) - active_segment_started_at)) * 1000)::bigint)
    ),
    active_segment_started_at = NULL,
    waiting_segment_started_at = NULL,
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
        active_timeout_ms,
        active_execution_ms + CASE WHEN active_segment_started_at IS NULL THEN 0 ELSE
            GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
                (sqlc.arg(observed_at)::timestamptz - active_segment_started_at)) * 1000)::bigint)
        END
    ),
    active_segment_started_at = NULL,
    waiting_execution_ms = LEAST(
        waiting_timeout_ms,
        waiting_execution_ms + CASE WHEN waiting_segment_started_at IS NULL THEN 0 ELSE
            GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
                (sqlc.arg(observed_at)::timestamptz - waiting_segment_started_at)) * 1000)::bigint)
        END
    ),
    waiting_segment_started_at = NULL,
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
        active_timeout_ms,
        active_execution_ms + CASE WHEN active_segment_started_at IS NULL THEN 0 ELSE
            GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
                (sqlc.arg(observed_at)::timestamptz - active_segment_started_at)) * 1000)::bigint)
        END
    ),
    active_segment_started_at = NULL,
    waiting_execution_ms = LEAST(
        waiting_timeout_ms,
        waiting_execution_ms + CASE WHEN waiting_segment_started_at IS NULL THEN 0 ELSE
            GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
                (sqlc.arg(observed_at)::timestamptz - waiting_segment_started_at)) * 1000)::bigint)
        END
    ),
    waiting_segment_started_at = NULL,
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
      deadline_at <= sqlc.arg(observed_at)
      OR active_execution_ms >= active_timeout_ms
      OR waiting_execution_ms >= waiting_timeout_ms
      OR (
          status = 'waiting'
          AND waiting_execution_ms + GREATEST(0, FLOOR(EXTRACT(EPOCH FROM
              (sqlc.arg(observed_at)::timestamptz - waiting_segment_started_at)) * 1000)::bigint) >= waiting_timeout_ms
      )
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

-- name: ListSessionMessagesByInvocation :many
SELECT id, session_id, account_id, tenant_partition_id, agent_id,
       invocation_id, sequence, role, content, created_at
FROM session_messages
WHERE invocation_id = $1
ORDER BY sequence;

-- name: ListSessionMessagesForGeneration :many
SELECT message.id, message.session_id, message.account_id,
       message.tenant_partition_id, message.agent_id, message.invocation_id,
       message.sequence, message.role, message.content, message.created_at
FROM session_messages AS message
JOIN invocations AS invocation ON invocation.id = message.invocation_id
WHERE message.session_id = $1
  AND (
      message.role = 'user'
      OR invocation.status NOT IN ('failed', 'cancelled')
  )
ORDER BY message.sequence;

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
  AND i.deadline_at > sqlc.arg(observed_at)
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
       tp.tenant_key,
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
-- The service first verifies a live Invocation owner and execution deadline.
-- ToolCall.deadline_at is the logical wall deadline, not the abandoned
-- owner's segment cutoff, so recovery can start the same pending call.
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
    result_origin = sqlc.arg(result_origin),
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

-- name: AdvanceWaitingInvocationCheckpoint :one
UPDATE invocations
SET current_checkpoint_sequence = sqlc.arg(checkpoint_sequence),
    updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)
  AND status = 'waiting'
  AND current_checkpoint_sequence = sqlc.arg(expected_checkpoint_sequence)
  AND current_iteration = sqlc.arg(iteration)
RETURNING *;

-- name: AdvanceInvocationCheckpointForTerminal :one
UPDATE invocations
SET current_checkpoint_sequence = sqlc.arg(checkpoint_sequence),
    current_iteration = sqlc.arg(iteration)
WHERE id = sqlc.arg(id) AND status IN ('queued', 'running', 'waiting')
  AND current_checkpoint_sequence < sqlc.arg(checkpoint_sequence)
  AND current_iteration <= sqlc.arg(iteration)
RETURNING *;

-- name: CreateCallbackDelivery :exec
INSERT INTO callback_deliveries (
    id, tool_call_id, invocation_id, session_id, account_id,
    tenant_partition_id, agent_id, endpoint_url, status, available_at,
    owner, lease_expires_at, attempt, last_error_code, response_status,
    created_at, updated_at, terminal_at
) VALUES (
    sqlc.arg(id), sqlc.arg(tool_call_id), sqlc.arg(invocation_id),
    sqlc.arg(session_id), sqlc.arg(account_id), sqlc.arg(tenant_partition_id),
    sqlc.arg(agent_id), sqlc.arg(endpoint_url), sqlc.arg(status),
    sqlc.narg(available_at), sqlc.narg(owner), sqlc.narg(lease_expires_at),
    sqlc.arg(attempt), sqlc.narg(last_error_code), sqlc.narg(response_status),
    sqlc.arg(created_at), sqlc.arg(updated_at), sqlc.narg(terminal_at)
);

-- name: GetCallbackDelivery :one
SELECT * FROM callback_deliveries WHERE id = $1;

-- name: GetCallbackDeliveryForUpdate :one
SELECT * FROM callback_deliveries WHERE id = $1 FOR UPDATE;

-- name: ActivateCallbackDeliveries :execrows
UPDATE callback_deliveries
SET status = 'pending', available_at = sqlc.arg(observed_at),
    updated_at = sqlc.arg(observed_at)
WHERE invocation_id = sqlc.arg(invocation_id) AND status = 'blocked';

-- name: ClaimNextCallbackDelivery :one
WITH candidate AS (
    SELECT delivery.id
    FROM callback_deliveries AS delivery
    JOIN tool_calls AS call ON call.id = delivery.tool_call_id
    JOIN invocations AS invocation ON invocation.id = delivery.invocation_id
    WHERE delivery.status = 'pending'
      AND delivery.available_at <= sqlc.arg(observed_at)
      AND call.status = 'pending'
      AND call.mode = 'callback'
      AND call.deadline_at > sqlc.arg(observed_at)
      AND invocation.status = 'waiting'
      AND invocation.current_iteration = call.iteration
      AND invocation.deadline_at > sqlc.arg(observed_at)
    ORDER BY delivery.available_at, delivery.created_at, delivery.id
    FOR UPDATE OF delivery SKIP LOCKED
    LIMIT 1
)
UPDATE callback_deliveries AS delivery
SET status = 'delivering', owner = sqlc.arg(owner),
    lease_expires_at = sqlc.arg(lease_expires_at), attempt = attempt + 1,
    updated_at = sqlc.arg(observed_at)
FROM candidate
WHERE delivery.id = candidate.id
RETURNING delivery.*;

-- name: ReturnCallbackDeliveryPending :one
UPDATE callback_deliveries
SET status = 'pending', available_at = sqlc.arg(available_at),
    owner = NULL, lease_expires_at = NULL,
    last_error_code = sqlc.arg(last_error_code),
    response_status = NULL, updated_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id) AND status = 'delivering'
  AND owner = sqlc.arg(owner) AND attempt = sqlc.arg(attempt)
  AND lease_expires_at > sqlc.arg(observed_at)
RETURNING *;

-- name: SettleCallbackDelivery :one
UPDATE callback_deliveries
SET status = sqlc.arg(status), owner = NULL, lease_expires_at = NULL,
    last_error_code = sqlc.narg(last_error_code),
    response_status = sqlc.narg(response_status),
    updated_at = sqlc.arg(observed_at), terminal_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id) AND status = 'delivering'
  AND owner = sqlc.arg(owner) AND attempt = sqlc.arg(attempt)
  AND lease_expires_at > sqlc.arg(observed_at)
RETURNING *;

-- name: AbandonActiveCallbackDeliveries :execrows
UPDATE callback_deliveries
SET status = 'abandoned', owner = NULL, lease_expires_at = NULL,
    last_error_code = sqlc.arg(last_error_code), updated_at = sqlc.arg(observed_at),
    terminal_at = sqlc.arg(observed_at)
WHERE invocation_id = sqlc.arg(invocation_id)
  AND status IN ('blocked', 'pending', 'delivering');

-- name: RecoverExpiredCallbackDeliveries :execrows
WITH candidates AS (
    SELECT id
    FROM callback_deliveries
    WHERE status = 'delivering' AND lease_expires_at <= sqlc.arg(observed_at)
    ORDER BY lease_expires_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(batch_limit)
)
UPDATE callback_deliveries AS delivery
SET status = 'pending', available_at = sqlc.arg(observed_at), owner = NULL,
    lease_expires_at = NULL, last_error_code = 'lease_expired',
    updated_at = sqlc.arg(observed_at)
FROM candidates
WHERE delivery.id = candidates.id;

-- name: PruneTerminalCallbackDeliveries :execrows
WITH candidates AS (
    SELECT delivery.id
    FROM callback_deliveries AS delivery
    WHERE delivery.status IN ('succeeded', 'failed', 'abandoned')
      AND delivery.terminal_at < sqlc.arg(prune_before)
    ORDER BY delivery.terminal_at, delivery.id
    LIMIT sqlc.arg(batch_limit)
)
DELETE FROM callback_deliveries
WHERE id IN (SELECT id FROM candidates);

-- name: CreateProviderCredential :exec
INSERT INTO provider_credentials (
    id, account_id, tenant_partition_id, provider, scope, status,
    current_version_id, current_version, create_idempotency_key,
    create_fingerprint, created_by, created_at, updated_at, revoked_at
) VALUES (
    sqlc.arg(id), sqlc.arg(account_id), sqlc.narg(tenant_partition_id),
    sqlc.arg(provider), sqlc.arg(scope), sqlc.arg(status),
    sqlc.arg(current_version_id), sqlc.arg(current_version),
    sqlc.arg(create_idempotency_key), sqlc.arg(create_fingerprint),
    sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(updated_at),
    sqlc.narg(revoked_at)
);

-- name: CreateProviderCredentialVersion :exec
INSERT INTO provider_credential_versions (
    id, provider_credential_id, account_id, tenant_partition_id, provider,
    version, status, previous_version_id, encryption_key_id, nonce,
    ciphertext, expires_at, overlap_expires_at, rotation_idempotency_key,
    rotation_fingerprint, created_by, created_at, destroyed_at
) VALUES (
    sqlc.arg(id), sqlc.arg(provider_credential_id), sqlc.arg(account_id),
    sqlc.narg(tenant_partition_id), sqlc.arg(provider), sqlc.arg(version),
    sqlc.arg(status), sqlc.narg(previous_version_id),
    sqlc.narg(encryption_key_id), sqlc.narg(nonce), sqlc.narg(ciphertext),
    sqlc.narg(expires_at), sqlc.narg(overlap_expires_at),
    sqlc.narg(rotation_idempotency_key), sqlc.narg(rotation_fingerprint),
    sqlc.arg(created_by), sqlc.arg(created_at), sqlc.narg(destroyed_at)
);

-- name: GetProviderCredential :one
SELECT * FROM provider_credentials WHERE id = $1;

-- name: GetProviderCredentialForUpdate :one
SELECT * FROM provider_credentials WHERE id = $1 FOR UPDATE;

-- name: GetProviderCredentialVersion :one
SELECT * FROM provider_credential_versions WHERE id = $1;

-- name: GetProviderCredentialByCreateIdempotencyKey :one
SELECT * FROM provider_credentials
WHERE account_id = sqlc.arg(account_id)
  AND create_idempotency_key = sqlc.arg(idempotency_key);

-- name: GetProviderCredentialVersionByRotationIdempotencyKey :one
SELECT * FROM provider_credential_versions
WHERE account_id = sqlc.arg(account_id)
  AND rotation_idempotency_key = sqlc.arg(idempotency_key);

-- name: GetActiveProviderCredential :one
SELECT * FROM provider_credentials
WHERE account_id = sqlc.arg(account_id)
  AND tenant_partition_id IS NOT DISTINCT FROM sqlc.narg(tenant_partition_id)::text
  AND provider = sqlc.arg(provider)
  AND status = 'active';

-- name: ListProviderCredentials :many
SELECT * FROM provider_credentials
WHERE account_id = sqlc.arg(account_id)
  AND (
      sqlc.narg(tenant_partition_id)::text IS NULL
      OR tenant_partition_id = sqlc.narg(tenant_partition_id)::text
  )
  AND (sqlc.narg(provider)::text IS NULL OR provider = sqlc.narg(provider)::text)
  AND (sqlc.narg(scope)::text IS NULL OR scope = sqlc.narg(scope)::text)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::text)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(batch_limit);

-- name: ActivateProviderCredentialVersion :one
WITH retired AS (
    UPDATE provider_credential_versions AS version
    SET status = CASE
            WHEN sqlc.narg(overlap_expires_at)::timestamptz IS NOT NULL
              AND version.status = 'active'
              AND version.ciphertext IS NOT NULL THEN 'overlap'
            ELSE 'revoked'
        END,
        overlap_expires_at = CASE
            WHEN sqlc.narg(overlap_expires_at)::timestamptz IS NOT NULL
              AND version.status = 'active'
              AND version.ciphertext IS NOT NULL THEN sqlc.narg(overlap_expires_at)
            ELSE NULL
        END,
        encryption_key_id = CASE
            WHEN sqlc.narg(overlap_expires_at)::timestamptz IS NOT NULL
              AND version.status = 'active'
              AND version.ciphertext IS NOT NULL THEN encryption_key_id
            ELSE NULL
        END,
        nonce = CASE
            WHEN sqlc.narg(overlap_expires_at)::timestamptz IS NOT NULL
              AND version.status = 'active'
              AND version.ciphertext IS NOT NULL THEN nonce
            ELSE NULL
        END,
        ciphertext = CASE
            WHEN sqlc.narg(overlap_expires_at)::timestamptz IS NOT NULL
              AND version.status = 'active'
              AND version.ciphertext IS NOT NULL THEN ciphertext
            ELSE NULL
        END,
        destroyed_at = CASE
            WHEN sqlc.narg(overlap_expires_at)::timestamptz IS NOT NULL
              AND version.status = 'active'
              AND version.ciphertext IS NOT NULL THEN NULL
            ELSE COALESCE(destroyed_at, sqlc.arg(observed_at))
        END
    FROM provider_credentials AS credential
    WHERE credential.id = sqlc.arg(credential_id)
      AND credential.status = 'active'
      AND credential.current_version_id = version.id
    RETURNING version.id
)
UPDATE provider_credentials
SET current_version_id = sqlc.arg(current_version_id),
    current_version = sqlc.arg(current_version),
    updated_at = sqlc.arg(observed_at)
WHERE provider_credentials.id = sqlc.arg(credential_id)
  AND status = 'active'
  AND EXISTS (SELECT 1 FROM retired)
RETURNING *;

-- name: RevokeProviderCredential :one
WITH revoked_versions AS (
    UPDATE provider_credential_versions
    SET status = 'revoked', encryption_key_id = NULL, nonce = NULL,
        ciphertext = NULL, overlap_expires_at = NULL,
        destroyed_at = COALESCE(destroyed_at, sqlc.arg(observed_at))
    WHERE provider_credential_id = sqlc.arg(credential_id)
      AND status IN ('active', 'overlap')
    RETURNING id
)
UPDATE provider_credentials
SET status = 'revoked', revoked_at = sqlc.arg(observed_at),
    updated_at = sqlc.arg(observed_at)
WHERE provider_credentials.id = sqlc.arg(credential_id) AND status = 'active'
RETURNING *;

-- name: CreateInvocationProviderCredential :exec
INSERT INTO invocation_provider_credentials (
    id, invocation_id, account_id, tenant_partition_id, provider, source,
    provider_credential_id, credential_version_id, selector,
    encryption_key_id, nonce, ciphertext, expires_at, cleared_at, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(invocation_id), sqlc.arg(account_id),
    sqlc.arg(tenant_partition_id), sqlc.arg(provider), sqlc.arg(source),
    sqlc.narg(provider_credential_id), sqlc.narg(credential_version_id),
    sqlc.narg(selector), sqlc.narg(encryption_key_id), sqlc.narg(nonce),
    sqlc.narg(ciphertext), sqlc.narg(expires_at), sqlc.narg(cleared_at),
    sqlc.arg(created_at)
);

-- name: GetInvocationProviderCredential :one
SELECT * FROM invocation_provider_credentials
WHERE invocation_id = sqlc.arg(invocation_id)
  AND provider = sqlc.arg(provider);

-- name: ClearExpiredInvocationCredentialMaterial :execrows
WITH candidates AS (
    SELECT id FROM invocation_provider_credentials
    WHERE source = 'caller_ephemeral'
      AND ciphertext IS NOT NULL
      AND expires_at <= sqlc.arg(observed_at)
    ORDER BY expires_at, id
    LIMIT sqlc.arg(batch_limit)
)
UPDATE invocation_provider_credentials
SET encryption_key_id = NULL, nonce = NULL, ciphertext = NULL,
    cleared_at = sqlc.arg(observed_at)
WHERE id IN (SELECT id FROM candidates);

-- name: ExpireProviderCredentialVersions :execrows
WITH candidates AS (
    SELECT id FROM provider_credential_versions
    WHERE ciphertext IS NOT NULL
      AND (
          (expires_at IS NOT NULL AND expires_at <= sqlc.arg(observed_at))
          OR (status = 'overlap' AND overlap_expires_at <= sqlc.arg(observed_at))
      )
    ORDER BY LEAST(
        COALESCE(expires_at, 'infinity'::timestamptz),
        COALESCE(overlap_expires_at, 'infinity'::timestamptz)
    ), id
    LIMIT sqlc.arg(batch_limit)
)
UPDATE provider_credential_versions
SET status = 'expired', encryption_key_id = NULL, nonce = NULL,
    ciphertext = NULL, overlap_expires_at = NULL,
    destroyed_at = sqlc.arg(observed_at)
WHERE id IN (SELECT id FROM candidates);
