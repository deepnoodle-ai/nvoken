-- Validated structured output. The transcript remains canonical for replay;
-- Invocation.output is a terminal, equality-proven host API projection.

BEGIN;

ALTER TABLE invocations
    ADD COLUMN output_schema_digest bytea,
    ADD COLUMN output jsonb,
    ADD COLUMN output_provenance jsonb,
    ADD CONSTRAINT invocations_output_schema_digest_sha256 CHECK (
        output_schema_digest IS NULL OR octet_length(output_schema_digest) = 32
    ),
    ADD CONSTRAINT invocations_output_object CHECK (
        output IS NULL OR jsonb_typeof(output) = 'object'
    ),
    ADD CONSTRAINT invocations_output_pair CHECK (
        (output IS NULL) = (output_provenance IS NULL)
    ),
    ADD CONSTRAINT invocations_output_terminal_shape CHECK (
        (output_schema_digest IS NULL AND output IS NULL)
        OR (output_schema_digest IS NOT NULL AND status <> 'completed' AND output IS NULL)
        OR (output_schema_digest IS NOT NULL AND status = 'completed' AND output IS NOT NULL)
    ),
    ADD CONSTRAINT invocations_output_provenance_shape CHECK (
        output_provenance IS NULL OR (
            jsonb_typeof(output_provenance) = 'object'
            AND output_provenance ?& ARRAY['source', 'tool_call_id', 'schema_sha256']
            AND output_provenance->>'source' = 'tool_call'
            AND output_provenance->>'tool_call_id' ~ '^tcal_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
            AND output_provenance->>'schema_sha256' = encode(output_schema_digest, 'hex')
            AND output_provenance - ARRAY['source', 'tool_call_id', 'schema_sha256'] = '{}'::jsonb
        )
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
       OR NEW.request_fingerprint <> OLD.request_fingerprint
       OR NEW.request_fingerprint_version <> OLD.request_fingerprint_version
       OR NEW.wall_clock_timeout_ms <> OLD.wall_clock_timeout_ms
       OR NEW.active_execution_timeout_ms <> OLD.active_execution_timeout_ms
       OR NEW.max_output_tokens IS DISTINCT FROM OLD.max_output_tokens
       OR NEW.max_estimated_cost_microusd IS DISTINCT FROM OLD.max_estimated_cost_microusd
       OR NEW.max_iterations <> OLD.max_iterations
       OR NEW.output_schema_digest IS DISTINCT FROM OLD.output_schema_digest THEN
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
    IF OLD.output IS NOT NULL
       AND (NEW.output IS DISTINCT FROM OLD.output
            OR NEW.output_provenance IS DISTINCT FROM OLD.output_provenance) THEN
        RAISE EXCEPTION 'terminal structured output is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

COMMIT;
