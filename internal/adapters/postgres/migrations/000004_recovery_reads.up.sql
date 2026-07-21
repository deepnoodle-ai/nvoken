-- Bounded keyset indexes for the public recovery collections. Transcript
-- ranges already use the unique (session_id, sequence/revision) indexes from
-- the runtime spine; no second content record is introduced.

BEGIN;

CREATE INDEX sessions_account_created_keyset
    ON sessions (account_id, created_at DESC, id DESC);

CREATE INDEX sessions_account_partition_created_keyset
    ON sessions (account_id, tenant_partition_id, created_at DESC, id DESC);

CREATE INDEX sessions_account_agent_created_keyset
    ON sessions (account_id, agent_id, created_at DESC, id DESC);

CREATE INDEX sessions_account_key_created_keyset
    ON sessions (account_id, session_key, created_at DESC, id DESC)
    WHERE session_key IS NOT NULL;

CREATE INDEX invocations_account_created_keyset
    ON invocations (account_id, created_at DESC, id DESC);

CREATE INDEX invocations_account_partition_created_keyset
    ON invocations (account_id, tenant_partition_id, created_at DESC, id DESC);

CREATE INDEX invocations_account_agent_created_keyset
    ON invocations (account_id, agent_id, created_at DESC, id DESC);

CREATE INDEX invocations_account_status_created_keyset
    ON invocations (account_id, status, created_at DESC, id DESC);

CREATE INDEX invocations_session_created_keyset
    ON invocations (session_id, created_at DESC, id DESC);

COMMIT;
