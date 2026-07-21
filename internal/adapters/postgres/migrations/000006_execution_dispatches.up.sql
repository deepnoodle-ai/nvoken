BEGIN;

CREATE TABLE synthetic_dispatch_works (
    id text PRIMARY KEY,
    status text NOT NULL,
    settlement_count integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    settled_at timestamptz,
    CONSTRAINT synthetic_dispatch_works_id_format CHECK (
        id ~ '^synw_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT synthetic_dispatch_works_status CHECK (status IN ('pending', 'settled')),
    CONSTRAINT synthetic_dispatch_works_settlement_once CHECK (settlement_count IN (0, 1)),
    CONSTRAINT synthetic_dispatch_works_terminal_shape CHECK (
        (status = 'pending' AND settlement_count = 0 AND settled_at IS NULL)
        OR (status = 'settled' AND settlement_count = 1 AND settled_at IS NOT NULL)
    )
);

CREATE TABLE execution_dispatches (
    id text PRIMARY KEY,
    kind text NOT NULL,
    work_id text NOT NULL,
    account_id text REFERENCES accounts(id),
    tenant_partition_id text REFERENCES tenant_partitions(id),
    queue text NOT NULL,
    status text NOT NULL,
    available_at timestamptz NOT NULL,
    task_name text,
    publish_attempts integer NOT NULL DEFAULT 0,
    last_error text,
    publisher_owner text,
    publisher_lease_expires_at timestamptz,
    publisher_attempt bigint NOT NULL DEFAULT 0,
    published_at timestamptz,
    settled_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT execution_dispatches_id_format CHECK (
        id ~ '^dsp_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT execution_dispatches_kind CHECK (kind IN ('synthetic')),
    CONSTRAINT execution_dispatches_queue_nonblank CHECK (length(btrim(queue)) > 0 AND length(queue) <= 512),
    CONSTRAINT execution_dispatches_work_nonblank CHECK (length(btrim(work_id)) > 0 AND length(work_id) <= 255),
    CONSTRAINT execution_dispatches_status CHECK (status IN ('pending', 'publishing', 'published', 'settled', 'abandoned')),
    CONSTRAINT execution_dispatches_attempts_nonnegative CHECK (publish_attempts >= 0 AND publisher_attempt >= 0),
    CONSTRAINT execution_dispatches_error_bounded CHECK (last_error IS NULL OR length(last_error) <= 1024),
    CONSTRAINT execution_dispatches_publication_shape CHECK (
        (status = 'pending' AND publisher_owner IS NULL AND publisher_lease_expires_at IS NULL AND settled_at IS NULL)
        OR (status = 'publishing' AND publisher_owner IS NOT NULL AND publisher_lease_expires_at IS NOT NULL AND settled_at IS NULL)
        OR (status = 'published' AND publisher_owner IS NULL AND publisher_lease_expires_at IS NULL AND task_name IS NOT NULL AND published_at IS NOT NULL AND settled_at IS NULL)
        OR (status IN ('settled', 'abandoned') AND publisher_owner IS NULL AND publisher_lease_expires_at IS NULL AND settled_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX execution_dispatches_one_active_work
    ON execution_dispatches (kind, work_id)
    WHERE status IN ('pending', 'publishing', 'published');

CREATE INDEX execution_dispatches_due_publication
    ON execution_dispatches (available_at, created_at, id)
    WHERE status = 'pending';

CREATE INDEX execution_dispatches_expired_publication_lease
    ON execution_dispatches (publisher_lease_expires_at, id)
    WHERE status = 'publishing';

CREATE INDEX execution_dispatches_stale_published
    ON execution_dispatches (updated_at, id)
    WHERE status = 'published';

CREATE INDEX execution_dispatches_terminal_retention
    ON execution_dispatches (settled_at, id)
    WHERE status IN ('settled', 'abandoned');

CREATE OR REPLACE FUNCTION nvoken_preserve_execution_dispatch_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.kind <> OLD.kind
       OR NEW.work_id <> OLD.work_id
       OR NEW.account_id IS DISTINCT FROM OLD.account_id
       OR NEW.tenant_partition_id IS DISTINCT FROM OLD.tenant_partition_id
       OR NEW.queue <> OLD.queue
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'execution dispatch identity is immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.status IN ('settled', 'abandoned') AND NEW.status <> OLD.status THEN
        RAISE EXCEPTION 'terminal execution dispatch cannot transition' USING ERRCODE = '23514';
    END IF;
    IF NEW.publisher_attempt < OLD.publisher_attempt
       OR NEW.publish_attempts < OLD.publish_attempts THEN
        RAISE EXCEPTION 'execution dispatch attempts cannot move backward' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_dispatches_preserve_identity
    BEFORE UPDATE ON execution_dispatches
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_execution_dispatch_identity();

COMMIT;
