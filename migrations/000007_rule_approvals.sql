-- A rule approval is tied to the immutable version content hash. Publishing
-- only accepts a version that has enough independent approvals for that hash.
CREATE TABLE IF NOT EXISTS rule_approval_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_version_id UUID NOT NULL REFERENCES rule_versions(id) ON DELETE RESTRICT,
    content_sha256 TEXT NOT NULL,
    requested_by TEXT NOT NULL,
    required_approvals INTEGER NOT NULL DEFAULT 1 CHECK (required_approvals BETWEEN 1 AND 5),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'rejected', 'cancelled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS rule_approval_requests_tenant_state_idx
    ON rule_approval_requests (tenant_id, state, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS rule_approval_requests_active_content_idx
    ON rule_approval_requests (rule_version_id, content_sha256)
    WHERE state IN ('pending', 'approved');

CREATE TABLE IF NOT EXISTS rule_approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL REFERENCES rule_approval_requests(id) ON DELETE CASCADE,
    approver_subject TEXT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
    content_sha256 TEXT NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (request_id, approver_subject)
);
