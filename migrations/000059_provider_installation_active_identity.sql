-- A provider installation may route inbound webhooks for only one active
-- workspace. Once its source workspace has explicitly deactivated admission,
-- however, a different workspace may establish a fresh connection without
-- moving the source workspace's historic review or audit evidence.
ALTER TABLE provider_installations
    DROP CONSTRAINT IF EXISTS provider_installations_provider_api_base_url_external_id_key;

CREATE UNIQUE INDEX IF NOT EXISTS provider_installations_active_identity_uq
    ON provider_installations (provider, api_base_url, external_id)
    WHERE active = TRUE;
