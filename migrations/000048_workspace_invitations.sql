CREATE TABLE IF NOT EXISTS workspace_invitations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    subject TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'rule_admin', 'reviewer', 'viewer', 'billing_viewer')),
    token_sha256 TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'revoked', 'expired')),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    accepted_by TEXT NOT NULL DEFAULT '',
    revoked_at TIMESTAMPTZ,
    revoked_by TEXT NOT NULL DEFAULT '',
    CHECK (expires_at > created_at),
    CHECK ((status <> 'accepted') OR (accepted_at IS NOT NULL AND accepted_by <> '')),
    CHECK ((status <> 'revoked') OR (revoked_at IS NOT NULL AND revoked_by <> ''))
);

CREATE UNIQUE INDEX IF NOT EXISTS workspace_invitations_pending_subject_idx
    ON workspace_invitations (tenant_id, subject)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS workspace_invitations_tenant_status_idx
    ON workspace_invitations (tenant_id, status, created_at DESC);
