-- Completion is an auditable baseline, not merely a mutable checkpoint value.
-- Keep the accepted provider scope and governance configuration as they were
-- when the workspace became eligible to admit reviews.
CREATE TABLE IF NOT EXISTS workspace_setup_readiness_snapshots (
    tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    checkpoint_revision INTEGER NOT NULL CHECK (checkpoint_revision > 0),
    installation_id UUID NOT NULL,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    repository_scope TEXT NOT NULL,
    general_config_revision INTEGER NOT NULL CHECK (general_config_revision > 0),
    general_config_content_sha256 TEXT NOT NULL,
    learning_mode TEXT,
    rule_set_count INTEGER NOT NULL CHECK (rule_set_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
