-- Workspace-owned Issue formatting templates are reusable authoring aids. They
-- never become execution authority by themselves: applying one copies its
-- bounded content into a review-configuration draft, and the normal immutable
-- review-configuration save remains the admission boundary.
CREATE TABLE IF NOT EXISTS issue_format_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name_key TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (char_length(name_key) BETWEEN 2 AND 80)
);

CREATE UNIQUE INDEX IF NOT EXISTS issue_format_templates_active_name_idx
    ON issue_format_templates (tenant_id, name_key)
    WHERE active = TRUE;

CREATE TABLE IF NOT EXISTS issue_format_template_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id UUID NOT NULL REFERENCES issue_format_templates(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 2 AND 80),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 240),
    content JSONB NOT NULL,
    content_sha256 TEXT NOT NULL CHECK (char_length(content_sha256) = 64),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (template_id, revision)
);

CREATE INDEX IF NOT EXISTS issue_format_template_versions_latest_idx
    ON issue_format_template_versions (template_id, revision DESC);
