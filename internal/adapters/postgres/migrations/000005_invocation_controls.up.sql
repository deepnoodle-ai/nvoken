-- Durable Invocation controls. Logical limits are admission-time facts;
-- active execution is accrued from one persisted segment at a time.

BEGIN;

ALTER TABLE invocations
    ADD COLUMN request_fingerprint_version smallint NOT NULL DEFAULT 1,
    ADD COLUMN total_timeout_ms bigint NOT NULL DEFAULT 1800000,
    ADD COLUMN active_timeout_ms bigint NOT NULL DEFAULT 1800000,
    ADD COLUMN waiting_timeout_ms bigint NOT NULL DEFAULT 1800000,
    ADD COLUMN max_output_tokens integer,
    ADD COLUMN max_estimated_cost_microusd bigint,
    ADD COLUMN max_iterations integer NOT NULL DEFAULT 1,
    ADD COLUMN active_execution_ms bigint NOT NULL DEFAULT 0,
    ADD COLUMN waiting_execution_ms bigint NOT NULL DEFAULT 0,
    ADD COLUMN deadline_at timestamptz,
    ADD COLUMN active_segment_started_at timestamptz,
    ADD COLUMN waiting_segment_started_at timestamptz,
    ADD COLUMN execution_deadline_at timestamptz,
    ADD COLUMN execution_deadline_scope text;

UPDATE invocations
SET deadline_at = created_at + interval '30 minutes'
WHERE deadline_at IS NULL;

-- A rolling upgrade may observe claims created by a pre-controls binary.
-- Seed one conservative segment without changing lifecycle or lease ownership;
-- an already-expired logical deadline is then handled by the ordinary reaper.
UPDATE invocations
SET active_segment_started_at = LEAST(
        updated_at,
        deadline_at - interval '1 millisecond'
    )
WHERE status = 'running';

UPDATE invocations
SET execution_deadline_at = LEAST(
        deadline_at,
        active_segment_started_at + interval '15 minutes'
    ),
    execution_deadline_scope = CASE
        WHEN deadline_at <= active_segment_started_at + interval '15 minutes'
            THEN 'total'
        ELSE 'execution_segment'
    END
WHERE status = 'running';

ALTER TABLE invocations
    ALTER COLUMN deadline_at SET NOT NULL,
    ADD CONSTRAINT invocations_fingerprint_version_positive CHECK (request_fingerprint_version > 0),
    ADD CONSTRAINT invocations_wall_timeout_positive CHECK (total_timeout_ms > 0),
    ADD CONSTRAINT invocations_active_timeout_positive CHECK (active_timeout_ms > 0),
    ADD CONSTRAINT invocations_waiting_timeout_positive CHECK (waiting_timeout_ms > 0),
    ADD CONSTRAINT invocations_output_limit_positive CHECK (max_output_tokens IS NULL OR max_output_tokens > 0),
    ADD CONSTRAINT invocations_cost_limit_positive CHECK (max_estimated_cost_microusd IS NULL OR max_estimated_cost_microusd > 0),
    ADD CONSTRAINT invocations_iteration_limit_positive CHECK (max_iterations > 0),
    ADD CONSTRAINT invocations_active_execution_bounded CHECK (
        active_execution_ms >= 0 AND active_execution_ms <= active_timeout_ms
    ),
    ADD CONSTRAINT invocations_waiting_execution_bounded CHECK (
        waiting_execution_ms >= 0 AND waiting_execution_ms <= waiting_timeout_ms
    ),
    ADD CONSTRAINT invocations_wall_deadline_consistent CHECK (
        deadline_at = created_at + total_timeout_ms * interval '1 millisecond'
    ),
    ADD CONSTRAINT invocations_active_segment_shape CHECK (
        (
            status = 'running'
            AND active_segment_started_at IS NOT NULL
            AND execution_deadline_at IS NOT NULL
            AND execution_deadline_at > active_segment_started_at
            AND execution_deadline_scope IN ('total', 'active_execution', 'execution_segment')
        ) OR (
            status <> 'running'
            AND active_segment_started_at IS NULL
            AND execution_deadline_at IS NULL
            AND execution_deadline_scope IS NULL
        )
    ),
    ADD CONSTRAINT invocations_waiting_segment_shape CHECK (
        (status = 'waiting' AND waiting_segment_started_at IS NOT NULL)
        OR (status <> 'waiting' AND waiting_segment_started_at IS NULL)
    );

ALTER TABLE invocations
    DROP CONSTRAINT invocations_execution_evidence_terminal,
    ADD CONSTRAINT invocations_execution_evidence_terminal CHECK (
        (usage IS NULL AND provenance IS NULL) OR status IN ('completed', 'failed')
    );

CREATE INDEX invocations_expired_logical_deadlines
    ON invocations (deadline_at, id)
    WHERE status IN ('queued', 'running', 'waiting');

CREATE INDEX invocations_expired_execution_deadlines
    ON invocations (execution_deadline_at, id)
    WHERE status = 'running';

CREATE OR REPLACE FUNCTION nvoken_default_invocation_controls()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.deadline_at IS NULL THEN
        NEW.deadline_at := NEW.created_at
            + NEW.total_timeout_ms * interval '1 millisecond';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER invocations_default_controls
    BEFORE INSERT ON invocations
    FOR EACH ROW EXECUTE FUNCTION nvoken_default_invocation_controls();

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
       OR NEW.waiting_timeout_ms <> OLD.waiting_timeout_ms
       OR NEW.max_output_tokens IS DISTINCT FROM OLD.max_output_tokens
       OR NEW.max_estimated_cost_microusd IS DISTINCT FROM OLD.max_estimated_cost_microusd
       OR NEW.max_iterations <> OLD.max_iterations THEN
        RAISE EXCEPTION 'invocation admission identity and controls are immutable'
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
    IF NEW.active_execution_ms < OLD.active_execution_ms THEN
        RAISE EXCEPTION 'invocation active execution cannot move backward'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.waiting_execution_ms < OLD.waiting_execution_ms THEN
        RAISE EXCEPTION 'invocation waiting time cannot move backward'
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

COMMIT;
