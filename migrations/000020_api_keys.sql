CREATE TABLE IF NOT EXISTS api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    key_prefix TEXT NOT NULL UNIQUE CHECK (char_length(key_prefix) BETWEEN 12 AND 32),
    secret_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(secret_hash) = 32),
    caller_subject TEXT NOT NULL UNIQUE,
    scopes TEXT[] NOT NULL CHECK (cardinality(scopes) BETWEEN 1 AND 3),
    repositories TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    revoked_by TEXT NOT NULL DEFAULT '',
    CHECK (expires_at IS NULL OR expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS api_keys_tenant_created_idx
    ON api_keys (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS api_keys_active_hash_idx
    ON api_keys (secret_hash)
    WHERE revoked_at IS NULL;
