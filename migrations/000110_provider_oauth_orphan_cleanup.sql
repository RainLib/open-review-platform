-- The provider prober reaps OAuth attempts that were encrypted but never
-- connected to an active installation after the setup receipt expires.
CREATE INDEX IF NOT EXISTS provider_oauth_credentials_orphan_age_idx
  ON provider_oauth_credentials (created_at, credential_ref)
  WHERE provider = 'gitlab' AND revoked_at IS NULL;
