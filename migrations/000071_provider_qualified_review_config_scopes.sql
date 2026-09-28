-- A repository name alone is not a safe policy identity in a workspace that
-- connects GitHub, GitLab.com, and one or more self-managed GitLab instances.
-- Keep legacy rows unqualified for backwards-compatible reads, while new
-- repository overrides use provider plus normalized API base URL.
ALTER TABLE review_configurations
    ADD COLUMN IF NOT EXISTS scope_provider TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS scope_api_base_url TEXT NOT NULL DEFAULT '';

ALTER TABLE review_configurations
    DROP CONSTRAINT IF EXISTS review_configurations_tenant_id_section_scope_kind_scope_re_key;
ALTER TABLE review_configurations
    ADD CONSTRAINT review_configurations_scope_identity_key
    UNIQUE (tenant_id, section, scope_kind, scope_ref, scope_provider, scope_api_base_url);

ALTER TABLE review_configurations
    DROP CONSTRAINT IF EXISTS review_configurations_scope_identity_check;
ALTER TABLE review_configurations
    ADD CONSTRAINT review_configurations_scope_identity_check
    CHECK (
        (scope_kind = 'tenant' AND scope_ref = '' AND scope_provider = '' AND scope_api_base_url = '')
        OR
        (scope_kind = 'repository' AND scope_ref <> '' AND ((scope_provider = '' AND scope_api_base_url = '') OR (scope_provider <> '' AND scope_api_base_url <> '')))
    );

ALTER TABLE review_configuration_snapshots
    ADD COLUMN IF NOT EXISTS origin_scope_provider TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS origin_scope_api_base_url TEXT NOT NULL DEFAULT '';

ALTER TABLE review_config_change_requests
    ADD COLUMN IF NOT EXISTS scope_provider TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS scope_api_base_url TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS review_config_change_requests_pending_scope_idx;
CREATE UNIQUE INDEX review_config_change_requests_pending_scope_idx
    ON review_config_change_requests (tenant_id, section, scope_kind, scope_ref, scope_provider, scope_api_base_url)
    WHERE state = 'pending';

ALTER TABLE model_probe_receipts
    ADD COLUMN IF NOT EXISTS scope_provider TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS scope_api_base_url TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS model_probe_receipt_scope_identity_idx
    ON model_probe_receipts (tenant_id, scope_kind, scope_ref, scope_provider, scope_api_base_url, created_at DESC);
