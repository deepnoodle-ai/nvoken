-- Durable ToolCall and checkpoint spine. Tool request/result content remains
-- canonical only in session_messages; these rows retain identity, evidence
-- references, fences, and lifecycle metadata.

BEGIN;

ALTER TABLE session_messages
    ADD CONSTRAINT session_messages_scoped_identity_unique UNIQUE
        (id, invocation_id, session_id, account_id, tenant_partition_id, agent_id, sequence);

ALTER TABLE invocations
    ADD COLUMN current_checkpoint_sequence bigint NOT NULL DEFAULT 0,
    ADD COLUMN current_iteration integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT invocations_checkpoint_sequence_nonnegative CHECK (current_checkpoint_sequence >= 0),
    ADD CONSTRAINT invocations_iteration_nonnegative CHECK (current_iteration >= 0);

CREATE TABLE model_usage_receipts (
    id text PRIMARY KEY,
    invocation_id text NOT NULL,
    session_id text NOT NULL,
    account_id text NOT NULL,
    tenant_partition_id text NOT NULL,
    agent_id text NOT NULL,
    iteration integer NOT NULL,
    message_id text NOT NULL,
    message_sequence bigint NOT NULL,
    usage jsonb NOT NULL,
    provenance jsonb NOT NULL,
    evidence_digest bytea NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT model_usage_receipts_id_format CHECK (id ~ '^usgr_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT model_usage_receipts_iteration_positive CHECK (iteration > 0),
    CONSTRAINT model_usage_receipts_usage_object CHECK (jsonb_typeof(usage) = 'object'),
    CONSTRAINT model_usage_receipts_provenance_object CHECK (jsonb_typeof(provenance) = 'object'),
    CONSTRAINT model_usage_receipts_digest_sha256 CHECK (octet_length(evidence_digest) = 32),
    CONSTRAINT model_usage_receipts_invocation_boundary FOREIGN KEY
        (invocation_id, session_id, account_id, tenant_partition_id, agent_id)
        REFERENCES invocations(id, session_id, account_id, tenant_partition_id, agent_id) ON DELETE RESTRICT,
    CONSTRAINT model_usage_receipts_message_boundary FOREIGN KEY
        (message_id, invocation_id, session_id, account_id, tenant_partition_id, agent_id, message_sequence)
        REFERENCES session_messages(id, invocation_id, session_id, account_id, tenant_partition_id, agent_id, sequence) ON DELETE RESTRICT,
    CONSTRAINT model_usage_receipts_iteration_unique UNIQUE (invocation_id, iteration),
    CONSTRAINT model_usage_receipts_scoped_identity_unique UNIQUE
        (id, invocation_id, session_id, account_id, tenant_partition_id, agent_id)
);

CREATE TABLE tool_calls (
    id text PRIMARY KEY,
    invocation_id text NOT NULL,
    session_id text NOT NULL,
    account_id text NOT NULL,
    tenant_partition_id text NOT NULL,
    agent_id text NOT NULL,
    iteration integer NOT NULL,
    batch_ordinal integer NOT NULL,
    provider_call_id text NOT NULL,
    name text NOT NULL,
    mode text NOT NULL,
    request_message_id text NOT NULL,
    request_message_sequence bigint NOT NULL,
    request_digest bytea NOT NULL,
    status text NOT NULL,
    deadline_at timestamptz NOT NULL,
    current_attempt integer NOT NULL DEFAULT 0,
    result_message_id text,
    result_message_sequence bigint,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    CONSTRAINT tool_calls_id_format CHECK (id ~ '^tcal_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT tool_calls_iteration_positive CHECK (iteration > 0),
    CONSTRAINT tool_calls_batch_ordinal_nonnegative CHECK (batch_ordinal >= 0),
    CONSTRAINT tool_calls_provider_id_bounded CHECK (provider_call_id <> '' AND char_length(provider_call_id) <= 255),
    CONSTRAINT tool_calls_name_bounded CHECK (name <> '' AND char_length(name) <= 255),
    CONSTRAINT tool_calls_mode CHECK (mode IN ('builtin', 'callback', 'client')),
    CONSTRAINT tool_calls_digest_sha256 CHECK (octet_length(request_digest) = 32),
    CONSTRAINT tool_calls_status CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    CONSTRAINT tool_calls_attempt_nonnegative CHECK (current_attempt >= 0),
    CONSTRAINT tool_calls_result_pair CHECK ((result_message_id IS NULL) = (result_message_sequence IS NULL)),
    CONSTRAINT tool_calls_terminal_shape CHECK (
        (status IN ('completed', 'failed', 'cancelled')) = (completed_at IS NOT NULL AND result_message_id IS NOT NULL)
    ),
    CONSTRAINT tool_calls_invocation_boundary FOREIGN KEY
        (invocation_id, session_id, account_id, tenant_partition_id, agent_id)
        REFERENCES invocations(id, session_id, account_id, tenant_partition_id, agent_id) ON DELETE RESTRICT,
    CONSTRAINT tool_calls_request_message_boundary FOREIGN KEY
        (request_message_id, invocation_id, session_id, account_id, tenant_partition_id, agent_id, request_message_sequence)
        REFERENCES session_messages(id, invocation_id, session_id, account_id, tenant_partition_id, agent_id, sequence) ON DELETE RESTRICT,
    CONSTRAINT tool_calls_result_message_boundary FOREIGN KEY
        (result_message_id, invocation_id, session_id, account_id, tenant_partition_id, agent_id, result_message_sequence)
        REFERENCES session_messages(id, invocation_id, session_id, account_id, tenant_partition_id, agent_id, sequence) ON DELETE RESTRICT,
    CONSTRAINT tool_calls_provider_iteration_unique UNIQUE (invocation_id, iteration, provider_call_id),
    CONSTRAINT tool_calls_batch_ordinal_unique UNIQUE (invocation_id, iteration, batch_ordinal),
    CONSTRAINT tool_calls_scoped_identity_unique UNIQUE
        (id, invocation_id, session_id, account_id, tenant_partition_id, agent_id)
);

CREATE INDEX tool_calls_open_by_invocation
    ON tool_calls (invocation_id, iteration, batch_ordinal)
    WHERE status IN ('pending', 'running');

CREATE TABLE tool_call_attempts (
    id text PRIMARY KEY,
    tool_call_id text NOT NULL,
    invocation_id text NOT NULL,
    session_id text NOT NULL,
    account_id text NOT NULL,
    tenant_partition_id text NOT NULL,
    agent_id text NOT NULL,
    attempt integer NOT NULL,
    invocation_lease_attempt bigint NOT NULL,
    status text NOT NULL,
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    CONSTRAINT tool_call_attempts_id_format CHECK (id ~ '^tcat_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT tool_call_attempts_attempt_positive CHECK (attempt > 0),
    CONSTRAINT tool_call_attempts_lease_attempt_positive CHECK (invocation_lease_attempt > 0),
    CONSTRAINT tool_call_attempts_status CHECK (status IN ('running', 'completed', 'failed', 'cancelled')),
    CONSTRAINT tool_call_attempts_terminal_shape CHECK ((status = 'running') = (completed_at IS NULL)),
    CONSTRAINT tool_call_attempts_tool_call_boundary FOREIGN KEY
        (tool_call_id, invocation_id, session_id, account_id, tenant_partition_id, agent_id)
        REFERENCES tool_calls(id, invocation_id, session_id, account_id, tenant_partition_id, agent_id) ON DELETE RESTRICT,
    CONSTRAINT tool_call_attempts_number_unique UNIQUE (tool_call_id, attempt)
);

CREATE TABLE invocation_checkpoints (
    id text PRIMARY KEY,
    invocation_id text NOT NULL,
    session_id text NOT NULL,
    account_id text NOT NULL,
    tenant_partition_id text NOT NULL,
    agent_id text NOT NULL,
    sequence bigint NOT NULL,
    iteration integer NOT NULL,
    kind text NOT NULL,
    lease_attempt bigint NOT NULL,
    through_message_sequence bigint NOT NULL,
    usage_receipt_id text,
    tool_call_id text,
    created_at timestamptz NOT NULL,
    CONSTRAINT invocation_checkpoints_id_format CHECK (id ~ '^icpt_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT invocation_checkpoints_sequence_positive CHECK (sequence > 0),
    CONSTRAINT invocation_checkpoints_iteration_positive CHECK (iteration > 0),
    CONSTRAINT invocation_checkpoints_kind CHECK (kind IN ('model', 'tool')),
    CONSTRAINT invocation_checkpoints_lease_attempt_positive CHECK (lease_attempt > 0),
    CONSTRAINT invocation_checkpoints_evidence_shape CHECK (
        (kind = 'model' AND usage_receipt_id IS NOT NULL AND tool_call_id IS NULL)
        OR (kind = 'tool' AND usage_receipt_id IS NULL AND tool_call_id IS NOT NULL)
    ),
    CONSTRAINT invocation_checkpoints_invocation_boundary FOREIGN KEY
        (invocation_id, session_id, account_id, tenant_partition_id, agent_id)
        REFERENCES invocations(id, session_id, account_id, tenant_partition_id, agent_id) ON DELETE RESTRICT,
    CONSTRAINT invocation_checkpoints_message_watermark FOREIGN KEY
        (session_id, through_message_sequence)
        REFERENCES session_messages(session_id, sequence) ON DELETE RESTRICT,
    CONSTRAINT invocation_checkpoints_receipt_boundary FOREIGN KEY
        (usage_receipt_id, invocation_id, session_id, account_id, tenant_partition_id, agent_id)
        REFERENCES model_usage_receipts(id, invocation_id, session_id, account_id, tenant_partition_id, agent_id) ON DELETE RESTRICT,
    CONSTRAINT invocation_checkpoints_tool_call_boundary FOREIGN KEY
        (tool_call_id, invocation_id, session_id, account_id, tenant_partition_id, agent_id)
        REFERENCES tool_calls(id, invocation_id, session_id, account_id, tenant_partition_id, agent_id) ON DELETE RESTRICT,
    CONSTRAINT invocation_checkpoints_sequence_unique UNIQUE (invocation_id, sequence)
);

CREATE UNIQUE INDEX invocation_checkpoints_model_iteration_unique
    ON invocation_checkpoints (invocation_id, iteration)
    WHERE kind = 'model';

CREATE UNIQUE INDEX invocation_checkpoints_tool_call_unique
    ON invocation_checkpoints (tool_call_id)
    WHERE kind = 'tool';

CREATE TRIGGER model_usage_receipts_append_only
    BEFORE UPDATE OR DELETE ON model_usage_receipts
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

CREATE TRIGGER invocation_checkpoints_append_only
    BEFORE UPDATE OR DELETE ON invocation_checkpoints
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

CREATE OR REPLACE FUNCTION nvoken_preserve_tool_call()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.invocation_id <> OLD.invocation_id
       OR NEW.session_id <> OLD.session_id
       OR NEW.account_id <> OLD.account_id
       OR NEW.tenant_partition_id <> OLD.tenant_partition_id
       OR NEW.agent_id <> OLD.agent_id
       OR NEW.iteration <> OLD.iteration
       OR NEW.batch_ordinal <> OLD.batch_ordinal
       OR NEW.provider_call_id <> OLD.provider_call_id
       OR NEW.name <> OLD.name
       OR NEW.mode <> OLD.mode
       OR NEW.request_message_id <> OLD.request_message_id
       OR NEW.request_message_sequence <> OLD.request_message_sequence
       OR NEW.request_digest <> OLD.request_digest
       OR NEW.deadline_at <> OLD.deadline_at
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'tool call identity and request are immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.status IN ('completed', 'failed', 'cancelled') AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'terminal tool call is immutable' USING ERRCODE = '23514';
    END IF;
    IF NEW.current_attempt < OLD.current_attempt THEN
        RAISE EXCEPTION 'tool call attempt cannot move backward' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tool_calls_preserve_identity_and_terminal
    BEFORE UPDATE ON tool_calls
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_tool_call();

CREATE TRIGGER tool_calls_no_delete
    BEFORE DELETE ON tool_calls
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

CREATE OR REPLACE FUNCTION nvoken_preserve_tool_call_attempt()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.tool_call_id <> OLD.tool_call_id
       OR NEW.invocation_id <> OLD.invocation_id
       OR NEW.session_id <> OLD.session_id
       OR NEW.account_id <> OLD.account_id
       OR NEW.tenant_partition_id <> OLD.tenant_partition_id
       OR NEW.agent_id <> OLD.agent_id
       OR NEW.attempt <> OLD.attempt
       OR NEW.invocation_lease_attempt <> OLD.invocation_lease_attempt
       OR NEW.started_at <> OLD.started_at THEN
        RAISE EXCEPTION 'tool call attempt identity is immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.status <> 'running' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'terminal tool call attempt is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tool_call_attempts_preserve_identity_and_terminal
    BEFORE UPDATE ON tool_call_attempts
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_tool_call_attempt();

CREATE TRIGGER tool_call_attempts_no_delete
    BEFORE DELETE ON tool_call_attempts
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

CREATE OR REPLACE FUNCTION nvoken_preserve_invocation_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.session_id <> OLD.session_id
       OR NEW.account_id <> OLD.account_id
       OR NEW.tenant_partition_id <> OLD.tenant_partition_id
       OR NEW.agent_id <> OLD.agent_id
       OR NEW.spec_snapshot_id <> OLD.spec_snapshot_id
       OR NEW.idempotency_key <> OLD.idempotency_key
       OR NEW.request_fingerprint <> OLD.request_fingerprint
       OR NEW.request_fingerprint_version <> OLD.request_fingerprint_version
       OR NEW.total_timeout_ms <> OLD.total_timeout_ms
       OR NEW.active_timeout_ms <> OLD.active_timeout_ms
       OR NEW.max_output_tokens IS DISTINCT FROM OLD.max_output_tokens
       OR NEW.max_estimated_cost_microusd IS DISTINCT FROM OLD.max_estimated_cost_microusd
       OR NEW.max_iterations <> OLD.max_iterations THEN
        RAISE EXCEPTION 'invocation admission identity and controls are immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.current_state_revision < OLD.current_state_revision THEN
        RAISE EXCEPTION 'invocation state revision cannot move backward' USING ERRCODE = '23514';
    END IF;
    IF NEW.lease_attempt < OLD.lease_attempt THEN
        RAISE EXCEPTION 'invocation lease attempt cannot move backward' USING ERRCODE = '23514';
    END IF;
    IF NEW.active_execution_ms < OLD.active_execution_ms THEN
        RAISE EXCEPTION 'invocation active execution cannot move backward' USING ERRCODE = '23514';
    END IF;
    IF NEW.current_checkpoint_sequence < OLD.current_checkpoint_sequence
       OR NEW.current_iteration < OLD.current_iteration THEN
        RAISE EXCEPTION 'invocation checkpoint cursors cannot move backward' USING ERRCODE = '23514';
    END IF;
    IF NEW.lease_owner IS DISTINCT FROM OLD.lease_owner
       AND NEW.lease_owner IS NOT NULL
       AND NEW.lease_attempt <= OLD.lease_attempt THEN
        RAISE EXCEPTION 'new invocation lease owner requires a fresh attempt' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

COMMIT;
