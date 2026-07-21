-- Durable operator identity, API credentials, and RFC 8628 device grants.

BEGIN;

CREATE TABLE operator_subjects (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    issuer text NOT NULL,
    subject text NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT operator_subjects_id_format CHECK (id ~ '^osub_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT operator_subjects_issuer_nonempty CHECK (issuer <> ''),
    CONSTRAINT operator_subjects_subject_nonempty CHECK (subject <> ''),
    CONSTRAINT operator_subjects_account_identity_unique UNIQUE (account_id, issuer, subject),
    CONSTRAINT operator_subjects_id_account_unique UNIQUE (id, account_id)
);

CREATE TABLE account_memberships (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    subject_id text NOT NULL,
    role text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT account_memberships_id_format CHECK (id ~ '^mbrs_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT account_memberships_role CHECK (role IN ('Owner', 'Operator', 'Viewer')),
    CONSTRAINT account_memberships_subject_boundary FOREIGN KEY (subject_id, account_id)
        REFERENCES operator_subjects(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT account_memberships_subject_unique UNIQUE (account_id, subject_id),
    CONSTRAINT account_memberships_id_account_unique UNIQUE (id, account_id)
);

CREATE TABLE api_credentials (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    kind text NOT NULL,
    name text NOT NULL,
    prefix text NOT NULL UNIQUE,
    verifier bytea NOT NULL,
    status text NOT NULL,
    profile text,
    role_cap text,
    owner_subject_id text,
    creator_subject_id text,
    creator_credential_id text,
    tenant_constraint text,
    session_constraint text,
    operation_constraints text[] NOT NULL DEFAULT '{}',
    expires_at timestamptz,
    rotated_from_id text REFERENCES api_credentials(id) ON DELETE RESTRICT,
    rotation_overlap_ends_at timestamptz,
    revoked_at timestamptz,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT api_credentials_id_format CHECK (id ~ '^cred_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT api_credentials_kind CHECK (kind IN ('machine', 'user')),
    CONSTRAINT api_credentials_name_bounded CHECK (btrim(name) <> '' AND char_length(name) <= 100),
    CONSTRAINT api_credentials_prefix_format CHECK (prefix ~ '^nvk_[A-Za-z0-9_-]{10,32}$'),
    CONSTRAINT api_credentials_verifier_sha256 CHECK (octet_length(verifier) = 32),
    CONSTRAINT api_credentials_status CHECK (status IN ('active', 'revoked')),
    CONSTRAINT api_credentials_profile CHECK (profile IS NULL OR profile IN ('Runtime', 'Viewer', 'Operator')),
    CONSTRAINT api_credentials_role_cap CHECK (role_cap IS NULL OR role_cap IN ('Viewer', 'Operator')),
    CONSTRAINT api_credentials_kind_fields CHECK (
        (kind = 'machine' AND profile IS NOT NULL AND role_cap IS NULL AND owner_subject_id IS NULL)
        OR
        (kind = 'user' AND profile IS NULL AND role_cap IS NOT NULL AND owner_subject_id IS NOT NULL)
    ),
    CONSTRAINT api_credentials_revocation_state CHECK ((status = 'revoked') = (revoked_at IS NOT NULL)),
    CONSTRAINT api_credentials_tenant_constraint CHECK (
        tenant_constraint IS NULL OR (btrim(tenant_constraint) <> '' AND char_length(tenant_constraint) <= 255)
    ),
    CONSTRAINT api_credentials_session_constraint CHECK (
        session_constraint IS NULL OR session_constraint ~ '^sesn_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT api_credentials_operation_constraints CHECK (
        operation_constraints <@ ARRAY[
            'create_invocation', 'get_invocation', 'submit_tool_results', 'cancel_invocation',
            'list_invocations', 'get_session', 'list_sessions', 'list_session_messages',
            'get_session_transcript', 'get_account', 'list_credentials', 'create_credential',
            'get_credential', 'rotate_credential', 'revoke_credential'
        ]::text[]
    ),
    CONSTRAINT api_credentials_expiry_after_creation CHECK (expires_at IS NULL OR expires_at > created_at),
    CONSTRAINT api_credentials_overlap_after_creation CHECK (rotation_overlap_ends_at IS NULL OR rotation_overlap_ends_at > created_at),
    CONSTRAINT api_credentials_owner_boundary FOREIGN KEY (owner_subject_id, account_id)
        REFERENCES operator_subjects(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT api_credentials_creator_boundary FOREIGN KEY (creator_subject_id, account_id)
        REFERENCES operator_subjects(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT api_credentials_creator_credential_boundary FOREIGN KEY (creator_credential_id, account_id)
        REFERENCES api_credentials(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT api_credentials_rotation_account_boundary FOREIGN KEY (rotated_from_id, account_id)
        REFERENCES api_credentials(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT api_credentials_id_account_unique UNIQUE (id, account_id)
);

CREATE INDEX api_credentials_account_created ON api_credentials (account_id, created_at DESC, id DESC);
CREATE INDEX api_credentials_owner ON api_credentials (account_id, owner_subject_id) WHERE owner_subject_id IS NOT NULL;

CREATE TABLE credential_issuances (
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    scope text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash bytea NOT NULL,
    credential_id text NOT NULL,
    ciphertext bytea,
    nonce bytea,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, scope, idempotency_key),
    CONSTRAINT credential_issuances_scope_nonempty CHECK (scope <> ''),
    CONSTRAINT credential_issuances_key_nonempty CHECK (idempotency_key <> ''),
    CONSTRAINT credential_issuances_request_sha256 CHECK (octet_length(request_hash) = 32),
    CONSTRAINT credential_issuances_secret_pair CHECK ((ciphertext IS NULL) = (nonce IS NULL)),
    CONSTRAINT credential_issuances_nonce_length CHECK (nonce IS NULL OR octet_length(nonce) = 12),
    CONSTRAINT credential_issuances_credential_boundary FOREIGN KEY (credential_id, account_id)
        REFERENCES api_credentials(id, account_id) ON DELETE RESTRICT
);

CREATE TABLE static_credential_imports (
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    import_key text NOT NULL,
    credential_id text NOT NULL,
    imported_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, import_key),
    CONSTRAINT static_credential_imports_credential_boundary FOREIGN KEY (credential_id, account_id)
        REFERENCES api_credentials(id, account_id) ON DELETE RESTRICT
);

CREATE TABLE device_authorizations (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    device_code_hash bytea NOT NULL UNIQUE,
    user_code_hash bytea NOT NULL UNIQUE,
    user_code_display text NOT NULL,
    device_label text NOT NULL,
    role_cap text NOT NULL,
    tenant_constraint text,
    session_constraint text,
    status text NOT NULL,
    poll_interval_seconds integer NOT NULL,
    next_poll_at timestamptz NOT NULL,
    confirmation_attempts integer NOT NULL DEFAULT 0,
    approved_by_subject_id text,
    credential_id text,
    expires_at timestamptz NOT NULL,
    delivery_expires_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT device_authorizations_id_format CHECK (id ~ '^dvaa_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT device_authorizations_hashes CHECK (octet_length(device_code_hash) = 32 AND octet_length(user_code_hash) = 32),
    CONSTRAINT device_authorizations_user_code_format CHECK (user_code_display ~ '^[0-9A-F]{5}-[0-9A-F]{5}$'),
    CONSTRAINT device_authorizations_label_bounded CHECK (btrim(device_label) <> '' AND char_length(device_label) <= 100),
    CONSTRAINT device_authorizations_role_cap CHECK (role_cap IN ('Viewer', 'Operator')),
    CONSTRAINT device_authorizations_status CHECK (status IN ('pending', 'approved', 'denied', 'exchanged')),
    CONSTRAINT device_authorizations_poll_interval CHECK (poll_interval_seconds BETWEEN 1 AND 60),
    CONSTRAINT device_authorizations_attempts CHECK (confirmation_attempts BETWEEN 0 AND 20),
    CONSTRAINT device_authorizations_tenant_constraint CHECK (
        tenant_constraint IS NULL OR (btrim(tenant_constraint) <> '' AND char_length(tenant_constraint) <= 255)
    ),
    CONSTRAINT device_authorizations_session_constraint CHECK (
        session_constraint IS NULL OR session_constraint ~ '^sesn_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT device_authorizations_state_fields CHECK (
        (status IN ('pending', 'denied') AND approved_by_subject_id IS NULL AND credential_id IS NULL AND delivery_expires_at IS NULL)
        OR
        (status IN ('approved', 'exchanged') AND approved_by_subject_id IS NOT NULL AND credential_id IS NOT NULL AND delivery_expires_at IS NOT NULL)
    ),
    CONSTRAINT device_authorizations_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT device_authorizations_approver_boundary FOREIGN KEY (approved_by_subject_id, account_id)
        REFERENCES operator_subjects(id, account_id) ON DELETE RESTRICT,
    CONSTRAINT device_authorizations_credential_boundary FOREIGN KEY (credential_id, account_id)
        REFERENCES api_credentials(id, account_id) ON DELETE RESTRICT
);

CREATE INDEX device_authorizations_expiry ON device_authorizations (expires_at);

CREATE TABLE browser_sessions (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    subject_id text NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    csrf_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT browser_sessions_id_format CHECK (id ~ '^brws_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT browser_sessions_hashes CHECK (octet_length(token_hash) = 32 AND octet_length(csrf_hash) = 32),
    CONSTRAINT browser_sessions_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT browser_sessions_subject_boundary FOREIGN KEY (subject_id, account_id)
        REFERENCES operator_subjects(id, account_id) ON DELETE RESTRICT
);

CREATE INDEX browser_sessions_expiry ON browser_sessions (expires_at);

CREATE OR REPLACE FUNCTION nvoken_preserve_credential_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.account_id <> OLD.account_id
       OR NEW.kind <> OLD.kind
       OR NEW.prefix <> OLD.prefix
       OR NEW.verifier <> OLD.verifier
       OR NEW.profile IS DISTINCT FROM OLD.profile
       OR NEW.role_cap IS DISTINCT FROM OLD.role_cap
       OR NEW.owner_subject_id IS DISTINCT FROM OLD.owner_subject_id
       OR NEW.creator_subject_id IS DISTINCT FROM OLD.creator_subject_id
       OR NEW.creator_credential_id IS DISTINCT FROM OLD.creator_credential_id
       OR NEW.rotated_from_id IS DISTINCT FROM OLD.rotated_from_id
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'API credential identity and verifier are immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.status = 'revoked' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'revoked API credentials are immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER api_credentials_preserve_identity
    BEFORE UPDATE ON api_credentials
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_credential_identity();

CREATE TRIGGER operator_subjects_immutable
    BEFORE UPDATE OR DELETE ON operator_subjects
    FOR EACH ROW EXECUTE FUNCTION nvoken_reject_immutable_row_change();

COMMIT;
