-- Review configuration is stored only in the trusted control plane. Immutable
-- versions make inheritance, rollback, run snapshots, and audit reproducible;
-- repository files never become an implicit enterprise configuration source.
CREATE TABLE IF NOT EXISTS review_configurations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    section TEXT NOT NULL CHECK (section IN ('general', 'categories', 'filters', 'prompts', 'summary', 'messages')),
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('tenant', 'repository')),
    scope_ref TEXT NOT NULL DEFAULT '',
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, section, scope_kind, scope_ref),
    CHECK ((scope_kind = 'tenant' AND scope_ref = '') OR (scope_kind = 'repository' AND scope_ref <> ''))
);

CREATE TABLE IF NOT EXISTS review_configuration_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    configuration_id UUID NOT NULL REFERENCES review_configurations(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    content JSONB NOT NULL,
    content_sha256 TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (configuration_id, revision)
);

CREATE INDEX IF NOT EXISTS review_configuration_versions_latest_idx
    ON review_configuration_versions (configuration_id, revision DESC);
