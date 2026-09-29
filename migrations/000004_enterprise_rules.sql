-- Governance-controlled rules are immutable once published. The snapshot is
-- stored separately from mutable bindings so a historical review can always
-- explain exactly which trusted inputs were used.

CREATE TABLE IF NOT EXISTS rule_sets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS rule_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_set_id UUID NOT NULL REFERENCES rule_sets(id) ON DELETE RESTRICT,
    version INTEGER NOT NULL CHECK (version > 0),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    state TEXT NOT NULL CHECK (state IN ('draft', 'in_review', 'approved', 'published', 'deprecated', 'retired')),
    rules JSONB NOT NULL DEFAULT '[]'::jsonb,
    content_sha256 TEXT NOT NULL,
    compiler_version TEXT NOT NULL DEFAULT 'rules-v1',
    created_by TEXT NOT NULL,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (rule_set_id, version),
    UNIQUE (rule_set_id, content_sha256)
);
CREATE INDEX IF NOT EXISTS rule_versions_set_state_idx ON rule_versions(rule_set_id, state, version DESC);

CREATE TABLE IF NOT EXISTS rule_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_version_id UUID NOT NULL REFERENCES rule_versions(id) ON DELETE RESTRICT,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('tenant', 'team', 'repository')),
    scope_ref TEXT NOT NULL DEFAULT '',
    precedence INTEGER NOT NULL CHECK (precedence >= 0),
    target_branch_glob TEXT NOT NULL DEFAULT '',
    path_include_glob TEXT NOT NULL DEFAULT '',
    path_exclude_glob TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'shadow', 'disabled')),
    starts_at TIMESTAMPTZ,
    ends_at TIMESTAMPTZ,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);
CREATE INDEX IF NOT EXISTS rule_bindings_active_scope_idx ON rule_bindings(tenant_id, scope_kind, scope_ref, precedence)
    WHERE state IN ('active', 'shadow');

CREATE TABLE IF NOT EXISTS rule_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sha256 TEXT NOT NULL,
    compiler_version TEXT NOT NULL,
    engine TEXT NOT NULL DEFAULT 'ocr',
    canonical_payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, sha256)
);

CREATE TABLE IF NOT EXISTS rule_snapshot_sources (
    snapshot_id UUID NOT NULL REFERENCES rule_snapshots(id) ON DELETE CASCADE,
    rule_version_id UUID NOT NULL REFERENCES rule_versions(id) ON DELETE RESTRICT,
    precedence INTEGER NOT NULL,
    PRIMARY KEY (snapshot_id, rule_version_id)
);
