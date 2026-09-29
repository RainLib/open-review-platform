-- Align persisted membership roles with the stage-two RBAC contract. The
-- owner and admin retain tenant-wide administration; rule_admin is deliberately
-- narrower and cannot manage installations or memberships.
ALTER TABLE memberships DROP CONSTRAINT IF EXISTS memberships_role_check;
ALTER TABLE memberships
    ADD CONSTRAINT memberships_role_check
    CHECK (role IN ('owner', 'admin', 'rule_admin', 'reviewer', 'viewer', 'billing_viewer'));
