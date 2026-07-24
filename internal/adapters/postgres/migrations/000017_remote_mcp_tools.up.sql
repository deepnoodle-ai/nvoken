-- Per-Invocation remote MCP credentials and one fenced discovery snapshot.
-- Secret headers remain application-layer ciphertext and are destroyed when
-- their Invocation reaches a terminal state.

BEGIN;

CREATE TABLE invocation_mcp_server_bindings (
    id text PRIMARY KEY,
    invocation_id text NOT NULL REFERENCES invocations(id) ON DELETE RESTRICT,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    tenant_partition_id text NOT NULL,
    server_name text NOT NULL,
    encryption_key_id text,
    nonce bytea,
    ciphertext bytea,
    expires_at timestamptz,
    cleared_at timestamptz,
    created_at timestamptz NOT NULL,
    CONSTRAINT invocation_mcp_server_bindings_id_format CHECK (
        id ~ '^imcb_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT invocation_mcp_server_bindings_name CHECK (
        server_name ~ '^[A-Za-z0-9_-]{1,24}$'
        AND lower(server_name) NOT LIKE 'nvoken%'
    ),
    CONSTRAINT invocation_mcp_server_bindings_secret_shape CHECK (
        (
            encryption_key_id IS NULL AND nonce IS NULL AND ciphertext IS NULL
            AND expires_at IS NULL AND cleared_at IS NULL
        ) OR (
            encryption_key_id IS NOT NULL AND encryption_key_id <> ''
            AND nonce IS NOT NULL AND octet_length(nonce) > 0
            AND ciphertext IS NOT NULL AND octet_length(ciphertext) > 0
            AND expires_at IS NOT NULL AND cleared_at IS NULL
        ) OR (
            encryption_key_id IS NULL AND nonce IS NULL AND ciphertext IS NULL
            AND expires_at IS NOT NULL AND cleared_at IS NOT NULL
        )
    ),
    CONSTRAINT invocation_mcp_server_bindings_invocation_boundary FOREIGN KEY
        (invocation_id, account_id, tenant_partition_id)
        REFERENCES invocations(id, account_id, tenant_partition_id) ON DELETE RESTRICT,
    CONSTRAINT invocation_mcp_server_bindings_partition_boundary FOREIGN KEY
        (tenant_partition_id, account_id)
        REFERENCES tenant_partitions(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT invocation_mcp_server_bindings_server_unique
        UNIQUE (invocation_id, server_name)
);

CREATE INDEX invocation_mcp_server_bindings_expiry
    ON invocation_mcp_server_bindings (expires_at, id)
    WHERE ciphertext IS NOT NULL AND expires_at IS NOT NULL;

CREATE TABLE invocation_mcp_discoveries (
    id text PRIMARY KEY,
    invocation_id text NOT NULL REFERENCES invocations(id) ON DELETE RESTRICT,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    tenant_partition_id text NOT NULL,
    catalog jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT invocation_mcp_discoveries_id_format CHECK (
        id ~ '^mcpd_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT invocation_mcp_discoveries_catalog_object CHECK (
        jsonb_typeof(catalog) = 'object'
    ),
    CONSTRAINT invocation_mcp_discoveries_invocation_boundary FOREIGN KEY
        (invocation_id, account_id, tenant_partition_id)
        REFERENCES invocations(id, account_id, tenant_partition_id) ON DELETE RESTRICT,
    CONSTRAINT invocation_mcp_discoveries_partition_boundary FOREIGN KEY
        (tenant_partition_id, account_id)
        REFERENCES tenant_partitions(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT invocation_mcp_discoveries_invocation_unique UNIQUE (invocation_id)
);

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

        UPDATE invocation_mcp_server_bindings
        SET encryption_key_id = NULL,
            nonce = NULL,
            ciphertext = NULL,
            cleared_at = COALESCE(NEW.completed_at, NEW.updated_at)
        WHERE invocation_id = NEW.id
          AND ciphertext IS NOT NULL;
    END IF;
    RETURN NULL;
END;
$$;

UPDATE nvoken_schema_compatibility
SET schema_version = 17,
    minimum_binary_schema_version = 14,
    updated_at = now()
WHERE singleton = true;

COMMIT;
