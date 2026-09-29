CREATE TABLE workspace_sso_configurations (
    tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    protocol TEXT NOT NULL CHECK (protocol IN ('oidc', 'saml')),
    display_name TEXT NOT NULL,
    issuer_url TEXT NOT NULL DEFAULT '',
    metadata_url TEXT NOT NULL DEFAULT '',
    client_id TEXT NOT NULL DEFAULT '',
    secret_ref TEXT NOT NULL DEFAULT '',
    group_claim TEXT NOT NULL DEFAULT 'groups',
    state TEXT NOT NULL CHECK (state IN ('draft_saved', 'testing', 'verified', 'enforcement_ready', 'enforced', 'suspended')),
    revision INTEGER NOT NULL CHECK (revision > 0),
    tested_revision INTEGER CHECK (tested_revision > 0),
    break_glass_subject TEXT NOT NULL DEFAULT '',
    enforced_at TIMESTAMPTZ,
    updated_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workspace_sso_probe_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    config_revision INTEGER NOT NULL CHECK (config_revision > 0),
    protocol TEXT NOT NULL CHECK (protocol IN ('oidc', 'saml')),
    target_url TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    worker_id TEXT,
    locked_until TIMESTAMPTZ,
    metadata_sha256 TEXT,
    error_code TEXT,
    error_message TEXT,
    requested_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);

CREATE INDEX workspace_sso_probe_claim_idx
    ON workspace_sso_probe_receipts (state, created_at)
    WHERE state IN ('queued', 'running');

CREATE INDEX workspace_sso_probe_tenant_idx
    ON workspace_sso_probe_receipts (tenant_id, created_at DESC);

CREATE TABLE workspace_sso_domains (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    challenge_token TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'verified')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    verified_at TIMESTAMPTZ,
    UNIQUE (tenant_id, domain),
    UNIQUE (domain)
);

CREATE TABLE workspace_sso_role_mappings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    group_value TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'rule_admin', 'reviewer', 'viewer', 'billing_viewer')),
    repository_scope TEXT NOT NULL DEFAULT '*',
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, group_value, repository_scope)
);

CREATE INDEX workspace_sso_role_mapping_tenant_idx
    ON workspace_sso_role_mappings (tenant_id, lower(group_value), repository_scope);
