-- Durable Invocation execution ownership. The Invocation row remains the
-- authoritative queue; lease_attempt is the monotonic fencing token.

BEGIN;

ALTER TABLE invocations
    ADD COLUMN lease_owner text,
    ADD COLUMN lease_expires_at timestamptz,
    ADD COLUMN lease_attempt bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT invocations_lease_attempt_nonnegative CHECK (lease_attempt >= 0),
    ADD CONSTRAINT invocations_running_lease_shape CHECK (
        (
            status = 'running'
            AND lease_owner IS NOT NULL
            AND lease_owner <> ''
            AND octet_length(lease_owner) <= 255
            AND lease_expires_at IS NOT NULL
            AND lease_expires_at > updated_at
            AND lease_attempt > 0
        ) OR (
            status <> 'running'
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
        )
    );

ALTER TABLE invocation_states
    ADD COLUMN lease_attempt bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT invocation_states_lease_attempt_nonnegative CHECK (lease_attempt >= 0),
    ADD CONSTRAINT invocation_states_running_attempt_positive CHECK (
        status <> 'running' OR lease_attempt > 0
    );

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
    IF NEW.lease_attempt < OLD.lease_attempt THEN
        RAISE EXCEPTION 'invocation lease attempt cannot move backward'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.lease_owner IS DISTINCT FROM OLD.lease_owner
       AND NEW.lease_owner IS NOT NULL
       AND NEW.lease_attempt <= OLD.lease_attempt THEN
        RAISE EXCEPTION 'new invocation lease owner requires a fresh attempt'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE INDEX invocations_claim_queue
    ON invocations (created_at, id)
    WHERE status = 'queued';

CREATE INDEX invocations_expired_running_leases
    ON invocations (lease_expires_at, id)
    WHERE status = 'running';

COMMIT;
