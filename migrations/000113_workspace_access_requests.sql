CREATE TABLE IF NOT EXISTS workspace_access_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    subject TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '' CHECK (octet_length(note) <= 1000),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at TIMESTAMPTZ,
    decided_by TEXT NOT NULL DEFAULT '',
    CHECK ((status = 'pending' AND decided_at IS NULL AND decided_by = '') OR
           (status <> 'pending' AND decided_at IS NOT NULL AND decided_by <> ''))
);

CREATE UNIQUE INDEX IF NOT EXISTS workspace_access_requests_pending_subject_idx
    ON workspace_access_requests (tenant_id, subject)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS workspace_access_requests_tenant_status_idx
    ON workspace_access_requests (tenant_id, status, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS workspace_access_requests_subject_created_idx
    ON workspace_access_requests (subject, created_at DESC);
