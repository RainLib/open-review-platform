-- Workspace switching lists only memberships for the authenticated subject.
-- This keeps the read bounded as a tenant count grows.
CREATE INDEX IF NOT EXISTS memberships_subject_tenant_idx
    ON memberships (subject, tenant_id);
