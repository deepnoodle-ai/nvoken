-- Per-provider credential resources and durable Invocation bindings. Secret
-- material is application-layer ciphertext; platform and installation keys
-- remain deployment state outside Postgres.

BEGIN;

CREATE TABLE provider_credentials (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    tenant_partition_id text,
    provider text NOT NULL,
    scope text NOT NULL,
    status text NOT NULL,
    current_version_id text NOT NULL,
    current_version integer NOT NULL,
    create_idempotency_key text NOT NULL,
    create_fingerprint bytea NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CONSTRAINT provider_credentials_id_format CHECK (id ~ '^pcrd_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT provider_credentials_version_id_format CHECK (current_version_id ~ '^pcvr_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT provider_credentials_provider_nonempty CHECK (provider <> ''),
    CONSTRAINT provider_credentials_scope CHECK (scope IN ('account', 'tenant')),
    CONSTRAINT provider_credentials_scope_shape CHECK (
        (scope = 'account' AND tenant_partition_id IS NULL)
        OR (scope = 'tenant' AND tenant_partition_id IS NOT NULL)
    ),
    CONSTRAINT provider_credentials_status CHECK (status IN ('active', 'revoked')),
    CONSTRAINT provider_credentials_status_shape CHECK (
        (status = 'active' AND revoked_at IS NULL)
        OR (status = 'revoked' AND revoked_at IS NOT NULL)
    ),
    CONSTRAINT provider_credentials_version_positive CHECK (current_version > 0),
    CONSTRAINT provider_credentials_idempotency_nonempty CHECK (create_idempotency_key <> ''),
    CONSTRAINT provider_credentials_fingerprint_sha256 CHECK (octet_length(create_fingerprint) = 32),
    CONSTRAINT provider_credentials_creator_nonempty CHECK (created_by <> ''),
    CONSTRAINT provider_credentials_partition_boundary FOREIGN KEY (tenant_partition_id, account_id)
        REFERENCES tenant_partitions(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT provider_credentials_id_account_provider_unique UNIQUE (id, account_id, provider),
    CONSTRAINT provider_credentials_id_complete_scope_unique
        UNIQUE NULLS NOT DISTINCT (id, account_id, provider, tenant_partition_id),
    CONSTRAINT provider_credentials_create_idempotency UNIQUE (account_id, create_idempotency_key)
);

CREATE UNIQUE INDEX provider_credentials_one_active_account_provider
    ON provider_credentials (account_id, provider)
    WHERE status = 'active' AND scope = 'account';

CREATE UNIQUE INDEX provider_credentials_one_active_tenant_provider
    ON provider_credentials (account_id, tenant_partition_id, provider)
    WHERE status = 'active' AND scope = 'tenant';

CREATE INDEX provider_credentials_account_created
    ON provider_credentials (account_id, created_at DESC, id DESC);

CREATE TABLE provider_credential_versions (
    id text PRIMARY KEY,
    provider_credential_id text NOT NULL REFERENCES provider_credentials(id) ON DELETE RESTRICT,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    tenant_partition_id text,
    provider text NOT NULL,
    version integer NOT NULL,
    status text NOT NULL,
    previous_version_id text REFERENCES provider_credential_versions(id) ON DELETE RESTRICT,
    encryption_key_id text,
    nonce bytea,
    ciphertext bytea,
    expires_at timestamptz,
    overlap_expires_at timestamptz,
    rotation_idempotency_key text,
    rotation_fingerprint bytea,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL,
    destroyed_at timestamptz,
    CONSTRAINT provider_credential_versions_id_format CHECK (id ~ '^pcvr_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT provider_credential_versions_provider_nonempty CHECK (provider <> ''),
    CONSTRAINT provider_credential_versions_version_positive CHECK (version > 0),
    CONSTRAINT provider_credential_versions_status CHECK (status IN ('active', 'overlap', 'expired', 'revoked')),
    CONSTRAINT provider_credential_versions_secret_shape CHECK (
        (
            status IN ('active', 'overlap')
            AND
            encryption_key_id IS NOT NULL AND encryption_key_id <> ''
            AND nonce IS NOT NULL AND octet_length(nonce) > 0
            AND ciphertext IS NOT NULL AND octet_length(ciphertext) > 0
            AND destroyed_at IS NULL
        ) OR (
            status IN ('expired', 'revoked')
            AND
            encryption_key_id IS NULL AND nonce IS NULL AND ciphertext IS NULL
            AND destroyed_at IS NOT NULL
        )
    ),
    CONSTRAINT provider_credential_versions_overlap_shape CHECK (
        (status = 'overlap' AND overlap_expires_at IS NOT NULL)
        OR (status <> 'overlap' AND overlap_expires_at IS NULL)
    ),
    CONSTRAINT provider_credential_versions_rotation_shape CHECK (
        (rotation_idempotency_key IS NULL AND rotation_fingerprint IS NULL)
        OR (
            rotation_idempotency_key IS NOT NULL AND rotation_idempotency_key <> ''
            AND rotation_fingerprint IS NOT NULL
            AND octet_length(rotation_fingerprint) = 32
        )
    ),
    CONSTRAINT provider_credential_versions_creator_nonempty CHECK (created_by <> ''),
    CONSTRAINT provider_credential_versions_partition_boundary FOREIGN KEY (tenant_partition_id, account_id)
        REFERENCES tenant_partitions(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT provider_credential_versions_scope_boundary FOREIGN KEY
        (provider_credential_id, account_id, provider)
        REFERENCES provider_credentials(id, account_id, provider) ON DELETE RESTRICT,
    CONSTRAINT provider_credential_versions_tenant_scope_boundary FOREIGN KEY
        (provider_credential_id, account_id, provider, tenant_partition_id)
        REFERENCES provider_credentials(id, account_id, provider, tenant_partition_id) ON DELETE RESTRICT,
    CONSTRAINT provider_credential_versions_identity_scope_unique
        UNIQUE (id, provider_credential_id, account_id, provider),
    CONSTRAINT provider_credential_versions_identity_complete_scope_unique
        UNIQUE NULLS NOT DISTINCT (id, provider_credential_id, account_id, provider, tenant_partition_id),
    CONSTRAINT provider_credential_versions_number_unique UNIQUE (provider_credential_id, version),
    CONSTRAINT provider_credential_versions_rotation_idempotency UNIQUE (account_id, rotation_idempotency_key)
);

ALTER TABLE provider_credentials
    ADD CONSTRAINT provider_credentials_current_version_fk
    FOREIGN KEY (current_version_id, id, account_id, provider)
    REFERENCES provider_credential_versions(id, provider_credential_id, account_id, provider)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT provider_credentials_current_version_scope_fk
    FOREIGN KEY (current_version_id, id, account_id, provider, tenant_partition_id)
    REFERENCES provider_credential_versions(id, provider_credential_id, account_id, provider, tenant_partition_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX provider_credential_versions_credential_created
    ON provider_credential_versions (provider_credential_id, created_at DESC, id DESC);

CREATE INDEX provider_credential_versions_expiry
    ON provider_credential_versions (expires_at, id)
    WHERE ciphertext IS NOT NULL AND expires_at IS NOT NULL;

CREATE INDEX provider_credential_versions_overlap_expiry
    ON provider_credential_versions (overlap_expires_at, id)
    WHERE ciphertext IS NOT NULL AND status = 'overlap';

ALTER TABLE invocations
    ADD CONSTRAINT invocations_id_account_partition_unique
    UNIQUE (id, account_id, tenant_partition_id);

CREATE TABLE invocation_provider_credentials (
    id text PRIMARY KEY,
    invocation_id text NOT NULL REFERENCES invocations(id) ON DELETE RESTRICT,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    tenant_partition_id text NOT NULL,
    provider text NOT NULL,
    source text NOT NULL,
    provider_credential_id text REFERENCES provider_credentials(id) ON DELETE RESTRICT,
    credential_version_id text REFERENCES provider_credential_versions(id) ON DELETE RESTRICT,
    selector text,
    encryption_key_id text,
    nonce bytea,
    ciphertext bytea,
    expires_at timestamptz,
    cleared_at timestamptz,
    created_at timestamptz NOT NULL,
    CONSTRAINT invocation_provider_credentials_id_format CHECK (id ~ '^ipcb_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT invocation_provider_credentials_provider_nonempty CHECK (provider <> ''),
    CONSTRAINT invocation_provider_credentials_source CHECK (
        source IN ('caller_ephemeral', 'account_byok', 'tenant_byok', 'platform', 'installation_byok')
    ),
    CONSTRAINT invocation_provider_credentials_source_shape CHECK (
        (
            source = 'caller_ephemeral'
            AND provider_credential_id IS NULL AND credential_version_id IS NULL AND selector IS NULL
            AND expires_at IS NOT NULL
            AND (
                (
                    encryption_key_id IS NOT NULL AND encryption_key_id <> ''
                    AND nonce IS NOT NULL AND octet_length(nonce) > 0
                    AND ciphertext IS NOT NULL AND octet_length(ciphertext) > 0
                    AND cleared_at IS NULL
                ) OR (
                    encryption_key_id IS NULL AND nonce IS NULL AND ciphertext IS NULL
                    AND cleared_at IS NOT NULL
                )
            )
        ) OR (
            source IN ('account_byok', 'tenant_byok')
            AND provider_credential_id IS NOT NULL AND credential_version_id IS NOT NULL
            AND selector IS NULL AND encryption_key_id IS NULL AND nonce IS NULL
            AND ciphertext IS NULL AND expires_at IS NULL AND cleared_at IS NULL
        ) OR (
            source IN ('platform', 'installation_byok')
            AND provider_credential_id IS NULL AND credential_version_id IS NULL
            AND selector IS NOT NULL AND selector <> ''
            AND encryption_key_id IS NULL AND nonce IS NULL AND ciphertext IS NULL
            AND expires_at IS NULL AND cleared_at IS NULL
        )
    ),
    CONSTRAINT invocation_provider_credentials_invocation_boundary FOREIGN KEY
        (invocation_id, account_id, tenant_partition_id)
        REFERENCES invocations(id, account_id, tenant_partition_id) ON DELETE RESTRICT,
    CONSTRAINT invocation_provider_credentials_partition_boundary FOREIGN KEY (tenant_partition_id, account_id)
        REFERENCES tenant_partitions(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT invocation_provider_credentials_root_boundary FOREIGN KEY
        (provider_credential_id, account_id, provider)
        REFERENCES provider_credentials(id, account_id, provider) ON DELETE RESTRICT,
    CONSTRAINT invocation_provider_credentials_version_boundary FOREIGN KEY
        (credential_version_id, provider_credential_id, account_id, provider)
        REFERENCES provider_credential_versions(id, provider_credential_id, account_id, provider) ON DELETE RESTRICT,
    CONSTRAINT invocation_provider_credentials_provider_unique UNIQUE (invocation_id, provider)
);

CREATE INDEX invocation_provider_credentials_ephemeral_expiry
    ON invocation_provider_credentials (expires_at, id)
    WHERE source = 'caller_ephemeral' AND ciphertext IS NOT NULL;

-- Existing retained Invocations used installation configuration. Their
-- prefixed UUIDv7 suffix gives the migration a stable binding identity without
-- requiring a database UUID extension.
INSERT INTO invocation_provider_credentials (
    id, invocation_id, account_id, tenant_partition_id, provider, source,
    selector, created_at
)
SELECT
    'ipcb_' || substring(i.id FROM 6), i.id, i.account_id,
    i.tenant_partition_id, lower(s.spec -> 'model' ->> 'provider'),
    'installation_byok',
    'installation:' || lower(s.spec -> 'model' ->> 'provider'), i.created_at
FROM invocations AS i
JOIN execution_spec_snapshots AS s ON s.id = i.spec_snapshot_id;

CREATE OR REPLACE FUNCTION nvoken_clear_terminal_ephemeral_credential()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status NOT IN ('completed', 'failed', 'cancelled')
       AND NEW.status IN ('completed', 'failed', 'cancelled') THEN
        UPDATE invocation_provider_credentials
        SET encryption_key_id = NULL,
            nonce = NULL,
            ciphertext = NULL,
            cleared_at = COALESCE(NEW.completed_at, NEW.updated_at)
        WHERE invocation_id = NEW.id
          AND source = 'caller_ephemeral'
          AND ciphertext IS NOT NULL;
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER invocations_clear_terminal_ephemeral_credential
    AFTER UPDATE OF status ON invocations
    FOR EACH ROW EXECUTE FUNCTION nvoken_clear_terminal_ephemeral_credential();

COMMIT;
