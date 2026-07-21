BEGIN;

ALTER TABLE tool_calls
    DROP CONSTRAINT tool_calls_result_origin,
    ADD CONSTRAINT tool_calls_result_origin CHECK (
        result_origin IS NULL OR result_origin IN ('builtin', 'callback', 'client', 'system')
    );

CREATE TABLE callback_deliveries (
    id text PRIMARY KEY,
    tool_call_id text NOT NULL UNIQUE,
    invocation_id text NOT NULL,
    session_id text NOT NULL,
    account_id text NOT NULL,
    tenant_partition_id text NOT NULL,
    agent_id text NOT NULL,
    endpoint_url text NOT NULL,
    status text NOT NULL,
    available_at timestamptz,
    owner text,
    lease_expires_at timestamptz,
    attempt bigint NOT NULL DEFAULT 0,
    last_error_code text,
    response_status integer,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    terminal_at timestamptz,
    CONSTRAINT callback_deliveries_id_format CHECK (
        id ~ '^cbdy_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT callback_deliveries_endpoint_bounded CHECK (
        length(endpoint_url) > 0 AND octet_length(endpoint_url) <= 2048
    ),
    CONSTRAINT callback_deliveries_status CHECK (
        status IN ('blocked', 'pending', 'delivering', 'succeeded', 'failed', 'abandoned')
    ),
    CONSTRAINT callback_deliveries_attempt_nonnegative CHECK (attempt >= 0),
    CONSTRAINT callback_deliveries_error_bounded CHECK (
        last_error_code IS NULL OR (length(last_error_code) > 0 AND length(last_error_code) <= 64)
    ),
    CONSTRAINT callback_deliveries_response_status CHECK (
        response_status IS NULL OR response_status BETWEEN 100 AND 599
    ),
    CONSTRAINT callback_deliveries_state_shape CHECK (
        (status = 'blocked' AND available_at IS NULL AND owner IS NULL AND lease_expires_at IS NULL AND terminal_at IS NULL)
        OR (status = 'pending' AND available_at IS NOT NULL AND owner IS NULL AND lease_expires_at IS NULL AND terminal_at IS NULL)
        OR (status = 'delivering' AND available_at IS NOT NULL AND owner IS NOT NULL AND lease_expires_at IS NOT NULL AND terminal_at IS NULL)
        OR (status IN ('succeeded', 'failed', 'abandoned') AND owner IS NULL AND lease_expires_at IS NULL AND terminal_at IS NOT NULL)
    ),
    CONSTRAINT callback_deliveries_tool_call_boundary FOREIGN KEY
        (tool_call_id, invocation_id, session_id, account_id, tenant_partition_id, agent_id)
        REFERENCES tool_calls(id, invocation_id, session_id, account_id, tenant_partition_id, agent_id) ON DELETE RESTRICT
);

CREATE INDEX callback_deliveries_due
    ON callback_deliveries (available_at, created_at, id)
    WHERE status = 'pending';

CREATE INDEX callback_deliveries_expired_lease
    ON callback_deliveries (lease_expires_at, id)
    WHERE status = 'delivering';

CREATE INDEX callback_deliveries_terminal_retention
    ON callback_deliveries (terminal_at, id)
    WHERE status IN ('succeeded', 'failed', 'abandoned');

CREATE OR REPLACE FUNCTION nvoken_preserve_callback_delivery()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.tool_call_id <> OLD.tool_call_id
       OR NEW.invocation_id <> OLD.invocation_id
       OR NEW.session_id <> OLD.session_id
       OR NEW.account_id <> OLD.account_id
       OR NEW.tenant_partition_id <> OLD.tenant_partition_id
       OR NEW.agent_id <> OLD.agent_id
       OR NEW.endpoint_url <> OLD.endpoint_url
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'callback delivery identity is immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.status IN ('succeeded', 'failed', 'abandoned') AND NEW.status <> OLD.status THEN
        RAISE EXCEPTION 'terminal callback delivery cannot transition' USING ERRCODE = '23514';
    END IF;
    IF NEW.attempt < OLD.attempt THEN
        RAISE EXCEPTION 'callback delivery attempt cannot move backward' USING ERRCODE = '23514';
    END IF;
    IF NEW.status = 'delivering'
       AND (OLD.status <> 'delivering' OR NEW.owner IS DISTINCT FROM OLD.owner)
       AND NEW.attempt <= OLD.attempt THEN
        RAISE EXCEPTION 'callback delivery ownership requires a fresh attempt' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER callback_deliveries_preserve_identity_and_terminal
    BEFORE UPDATE ON callback_deliveries
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_callback_delivery();

COMMIT;
