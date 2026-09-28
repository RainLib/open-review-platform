-- Existing model routes influence credential resolution, provider selection and
-- execution capacity. Keep a proposed change outside review_configuration_versions
-- until an independent workspace administrator accepts the exact content hash.
CREATE TABLE IF NOT EXISTS review_config_change_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    section TEXT NOT NULL CHECK (section IN ('general', 'categories', 'filters', 'prompts', 'summary', 'messages', 'models')),
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('tenant', 'repository')),
    scope_ref TEXT NOT NULL DEFAULT '',
    base_revision INTEGER NOT NULL CHECK (base_revision > 0),
    base_content_sha256 TEXT NOT NULL,
    proposed_content JSONB NOT NULL,
    proposed_content_sha256 TEXT NOT NULL,
    requested_by TEXT NOT NULL,
    reason TEXT NOT NULL,
    operation TEXT NOT NULL DEFAULT 'upsert' CHECK (operation IN ('upsert', 'restore_inheritance')),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'rejected', 'superseded')),
    applied_revision INTEGER CHECK (applied_revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at TIMESTAMPTZ,
    CHECK ((scope_kind = 'tenant' AND scope_ref = '') OR (scope_kind = 'repository' AND scope_ref <> '')),
    CHECK (length(reason) BETWEEN 3 AND 2000)
);

CREATE UNIQUE INDEX IF NOT EXISTS review_config_change_requests_pending_scope_idx
    ON review_config_change_requests (tenant_id, section, scope_kind, scope_ref)
    WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS review_config_change_requests_tenant_state_idx
    ON review_config_change_requests (tenant_id, state, created_at DESC);

CREATE TABLE IF NOT EXISTS review_config_change_approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL REFERENCES review_config_change_requests(id) ON DELETE CASCADE,
    approver_subject TEXT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
    content_sha256 TEXT NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (request_id, approver_subject)
);
