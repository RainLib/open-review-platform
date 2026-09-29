-- OAuth bearer material is encrypted by the control-plane before it reaches
-- this table. credential_ref is an opaque capability, never a provider token.
CREATE TABLE IF NOT EXISTS provider_oauth_credentials (
  credential_ref TEXT PRIMARY KEY,
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  provider TEXT NOT NULL CHECK (provider IN ('gitlab')),
  access_token_ciphertext BYTEA NOT NULL,
  refresh_token_ciphertext BYTEA,
  expires_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS provider_oauth_credentials_tenant_provider_idx
  ON provider_oauth_credentials (tenant_id, provider)
  WHERE revoked_at IS NULL;
