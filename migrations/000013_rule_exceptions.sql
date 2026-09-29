-- Time-bounded rule exceptions are governance records, never edits to an
-- immutable rule version. Approval and expiry are evaluated at admission and
-- the applied exception set is copied into each new rule snapshot.
CREATE TABLE IF NOT EXISTS rule_exceptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_version_id UUID NOT NULL REFERENCES rule_versions(id) ON DELETE RESTRICT,
    rule_key TEXT NOT NULL,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('tenant', 'repository')),
    scope_ref TEXT NOT NULL DEFAULT '',
    target_branch_glob TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL,
    ticket_url TEXT NOT NULL DEFAULT '',
    requested_by TEXT NOT NULL,
    approved_by TEXT,
    decision_comment TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'rejected', 'revoked')),
    expires_at TIMESTAMPTZ NOT NULL,
    decided_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope_kind = 'tenant' AND scope_ref = '') OR (scope_kind = 'repository' AND scope_ref <> '')),
    CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS rule_exceptions_admission_idx
    ON rule_exceptions(tenant_id, rule_version_id, expires_at)
    WHERE state = 'approved';
CREATE INDEX IF NOT EXISTS rule_exceptions_tenant_created_idx
    ON rule_exceptions(tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS rule_snapshot_exceptions (
    snapshot_id UUID NOT NULL REFERENCES rule_snapshots(id) ON DELETE CASCADE,
    exception_id UUID NOT NULL REFERENCES rule_exceptions(id) ON DELETE RESTRICT,
    rule_key TEXT NOT NULL,
    rule_version_id UUID NOT NULL REFERENCES rule_versions(id) ON DELETE RESTRICT,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (snapshot_id, exception_id)
);
