BEGIN;

ALTER TABLE execution_dispatches
    DROP CONSTRAINT execution_dispatches_kind;

ALTER TABLE execution_dispatches
    ADD CONSTRAINT execution_dispatches_kind CHECK (kind IN ('synthetic', 'invocation')),
    ADD CONSTRAINT execution_dispatches_kind_scope CHECK (
        (kind = 'synthetic'
            AND work_id ~ '^synw_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
            AND account_id IS NULL
            AND tenant_partition_id IS NULL)
        OR
        (kind = 'invocation'
            AND work_id ~ '^invk_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
            AND account_id IS NOT NULL
            AND tenant_partition_id IS NOT NULL)
    );

CREATE INDEX execution_dispatches_active_invocation_scope
    ON execution_dispatches (account_id, tenant_partition_id, work_id)
    WHERE kind = 'invocation' AND status IN ('pending', 'publishing', 'published');

COMMIT;
