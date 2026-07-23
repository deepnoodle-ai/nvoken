-- nvoken's minimum durable runtime schema.
-- Runtime history is retained by default: every foreign key is RESTRICT and
-- this migration defines no cascade, pruning, or destructive retention path.

BEGIN;

CREATE TABLE accounts (
    id text PRIMARY KEY,
    created_at timestamptz NOT NULL,
    CONSTRAINT accounts_id_format CHECK (id ~ '^acct_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$')
);

CREATE TABLE tenant_partitions (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    tenant_key text,
    created_at timestamptz NOT NULL,
    CONSTRAINT tenant_partitions_id_format CHECK (id ~ '^tprt_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT tenant_partitions_ref_nonempty CHECK (tenant_key IS NULL OR tenant_key <> ''),
    CONSTRAINT tenant_partitions_id_account_unique UNIQUE (id, account_id)
);

CREATE UNIQUE INDEX tenant_partitions_one_default_per_account
    ON tenant_partitions (account_id)
    WHERE tenant_key IS NULL;

CREATE UNIQUE INDEX tenant_partitions_ref_per_account
    ON tenant_partitions (account_id, tenant_key)
    WHERE tenant_key IS NOT NULL;

CREATE OR REPLACE FUNCTION nvoken_account_requires_default_partition()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM tenant_partitions
        WHERE account_id = NEW.id AND tenant_key IS NULL
    ) THEN
        RAISE EXCEPTION 'account % requires one default tenant partition', NEW.id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER accounts_require_default_partition
    AFTER INSERT ON accounts
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION nvoken_account_requires_default_partition();

CREATE OR REPLACE FUNCTION nvoken_preserve_default_partition()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM accounts WHERE id = OLD.account_id)
       AND NOT EXISTS (
           SELECT 1 FROM tenant_partitions
           WHERE account_id = OLD.account_id AND tenant_key IS NULL
       ) THEN
        RAISE EXCEPTION 'account % requires one default tenant partition', OLD.account_id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER tenant_partitions_preserve_default
    AFTER DELETE ON tenant_partitions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_default_partition();

CREATE CONSTRAINT TRIGGER tenant_partitions_preserve_default_after_update
    AFTER UPDATE ON tenant_partitions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_default_partition();

CREATE TABLE agents (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    agent_key text NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT agents_id_format CHECK (id ~ '^agnt_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT agents_ref_nonempty CHECK (agent_key <> ''),
    CONSTRAINT agents_account_ref_unique UNIQUE (account_id, agent_key),
    CONSTRAINT agents_id_account_unique UNIQUE (id, account_id)
);

CREATE TABLE sessions (
    id text PRIMARY KEY,
    account_id text NOT NULL,
    tenant_partition_id text NOT NULL,
    agent_id text NOT NULL,
    session_key text,
    next_message_sequence bigint NOT NULL DEFAULT 1,
    next_lifecycle_revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT sessions_id_format CHECK (id ~ '^sesn_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT sessions_key_nonempty CHECK (session_key IS NULL OR session_key <> ''),
    CONSTRAINT sessions_next_message_positive CHECK (next_message_sequence > 0),
    CONSTRAINT sessions_next_revision_positive CHECK (next_lifecycle_revision > 0),
    CONSTRAINT sessions_partition_boundary FOREIGN KEY (tenant_partition_id, account_id)
        REFERENCES tenant_partitions(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT sessions_agent_boundary FOREIGN KEY (agent_id, account_id)
        REFERENCES agents(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT sessions_identity_unique UNIQUE (id, account_id, tenant_partition_id, agent_id)
);

CREATE UNIQUE INDEX sessions_key_per_partition_agent
    ON sessions (account_id, tenant_partition_id, agent_id, session_key)
    WHERE session_key IS NOT NULL;

CREATE TABLE execution_spec_snapshots (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    spec jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT execution_spec_snapshots_id_format CHECK (id ~ '^spec_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT execution_spec_snapshots_object CHECK (jsonb_typeof(spec) = 'object'),
    CONSTRAINT execution_spec_snapshots_id_account_unique UNIQUE (id, account_id)
);

CREATE TABLE invocations (
    id text PRIMARY KEY,
    session_id text NOT NULL,
    account_id text NOT NULL,
    tenant_partition_id text NOT NULL,
    agent_id text NOT NULL,
    spec_snapshot_id text NOT NULL,
    idempotency_key text NOT NULL,
    request_fingerprint bytea NOT NULL,
    status text NOT NULL,
    current_state_revision bigint NOT NULL,
    error jsonb,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    CONSTRAINT invocations_id_format CHECK (id ~ '^invk_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT invocations_idempotency_key_nonempty CHECK (idempotency_key <> ''),
    CONSTRAINT invocations_fingerprint_sha256 CHECK (octet_length(request_fingerprint) = 32),
    CONSTRAINT invocations_status CHECK (status IN ('queued', 'running', 'waiting', 'completed', 'failed', 'cancelled')),
    CONSTRAINT invocations_state_revision_positive CHECK (current_state_revision > 0),
    CONSTRAINT invocations_terminal_timestamp CHECK (
        (status IN ('completed', 'failed', 'cancelled')) = (completed_at IS NOT NULL)
    ),
    CONSTRAINT invocations_session_boundary FOREIGN KEY (session_id, account_id, tenant_partition_id, agent_id)
        REFERENCES sessions(id, account_id, tenant_partition_id, agent_id) ON DELETE RESTRICT,
    CONSTRAINT invocations_snapshot_boundary FOREIGN KEY (spec_snapshot_id, account_id)
        REFERENCES execution_spec_snapshots(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT invocations_identity_unique UNIQUE (id, session_id, account_id, tenant_partition_id, agent_id)
);

CREATE UNIQUE INDEX invocations_one_nonterminal_per_session
    ON invocations (session_id)
    WHERE status IN ('queued', 'running', 'waiting');

CREATE UNIQUE INDEX invocations_idempotency_scope
    ON invocations (account_id, tenant_partition_id, agent_id, idempotency_key);

CREATE TABLE session_messages (
    id text PRIMARY KEY,
    session_id text NOT NULL,
    account_id text NOT NULL,
    tenant_partition_id text NOT NULL,
    agent_id text NOT NULL,
    invocation_id text NOT NULL,
    sequence bigint NOT NULL,
    role text NOT NULL,
    content jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT session_messages_id_format CHECK (id ~ '^smsg_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT session_messages_sequence_positive CHECK (sequence > 0),
    CONSTRAINT session_messages_role CHECK (role IN ('user', 'assistant', 'tool')),
    CONSTRAINT session_messages_content_nonempty CHECK (
        jsonb_typeof(content) = 'array' AND jsonb_array_length(content) > 0
    ),
    CONSTRAINT session_messages_invocation_boundary FOREIGN KEY
        (invocation_id, session_id, account_id, tenant_partition_id, agent_id)
        REFERENCES invocations(id, session_id, account_id, tenant_partition_id, agent_id) ON DELETE RESTRICT,
    CONSTRAINT session_messages_session_sequence_unique UNIQUE (session_id, sequence)
);

CREATE TABLE invocation_states (
    id text PRIMARY KEY,
    invocation_id text NOT NULL,
    session_id text NOT NULL,
    account_id text NOT NULL,
    tenant_partition_id text NOT NULL,
    agent_id text NOT NULL,
    revision bigint NOT NULL,
    status text NOT NULL,
    through_message_sequence bigint,
    created_at timestamptz NOT NULL,
    CONSTRAINT invocation_states_id_format CHECK (id ~ '^ivst_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT invocation_states_revision_positive CHECK (revision > 0),
    CONSTRAINT invocation_states_status CHECK (status IN ('queued', 'running', 'waiting', 'completed', 'failed', 'cancelled')),
    CONSTRAINT invocation_states_invocation_boundary FOREIGN KEY
        (invocation_id, session_id, account_id, tenant_partition_id, agent_id)
        REFERENCES invocations(id, session_id, account_id, tenant_partition_id, agent_id) ON DELETE RESTRICT,
    CONSTRAINT invocation_states_message_watermark FOREIGN KEY (session_id, through_message_sequence)
        REFERENCES session_messages(session_id, sequence) ON DELETE RESTRICT,
    CONSTRAINT invocation_states_session_revision_unique UNIQUE (session_id, revision)
);

CREATE OR REPLACE FUNCTION nvoken_reject_immutable_row_change()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER execution_spec_snapshots_immutable
    BEFORE UPDATE ON execution_spec_snapshots
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

CREATE TRIGGER accounts_immutable
    BEFORE UPDATE ON accounts
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

CREATE TRIGGER tenant_partitions_immutable
    BEFORE UPDATE ON tenant_partitions
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

CREATE TRIGGER agents_immutable
    BEFORE UPDATE ON agents
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

CREATE TRIGGER session_messages_append_only
    BEFORE UPDATE OR DELETE ON session_messages
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

CREATE TRIGGER invocation_states_append_only
    BEFORE UPDATE OR DELETE ON invocation_states
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

CREATE OR REPLACE FUNCTION nvoken_preserve_session_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.account_id <> OLD.account_id
       OR NEW.tenant_partition_id <> OLD.tenant_partition_id
       OR NEW.agent_id <> OLD.agent_id
       OR NEW.session_key IS DISTINCT FROM OLD.session_key THEN
        RAISE EXCEPTION 'session identity and key are immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.next_message_sequence < OLD.next_message_sequence
       OR NEW.next_lifecycle_revision < OLD.next_lifecycle_revision THEN
        RAISE EXCEPTION 'session sequence counters cannot move backward'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER sessions_preserve_identity_and_counters
    BEFORE UPDATE ON sessions
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_session_identity();

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
       OR NEW.request_fingerprint <> OLD.request_fingerprint THEN
        RAISE EXCEPTION 'invocation admission identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.current_state_revision < OLD.current_state_revision THEN
        RAISE EXCEPTION 'invocation state revision cannot move backward'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER invocations_preserve_identity_and_revision
    BEFORE UPDATE ON invocations
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_invocation_identity();

CREATE OR REPLACE FUNCTION nvoken_preserve_terminal_invocation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IN ('completed', 'failed', 'cancelled') AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'terminal invocation is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER invocations_terminal_immutable
    BEFORE UPDATE ON invocations
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_terminal_invocation();

COMMIT;
