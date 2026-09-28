-- Test Lab executions are deliberately separate from provider review runs.
-- They may reuse an immutable historical Git range, but they can never create
-- provider publication receipts, checks, comments, or merge-gate status.
CREATE TABLE IF NOT EXISTS rule_test_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_version_id UUID NOT NULL REFERENCES rule_versions(id) ON DELETE RESTRICT,
    source_run_id UUID NOT NULL REFERENCES review_runs(id) ON DELETE RESTRICT,
    source_job_id UUID NOT NULL REFERENCES review_jobs(id) ON DELETE RESTRICT,
    snapshot_id UUID NOT NULL REFERENCES rule_snapshots(id) ON DELETE RESTRICT,
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'completed', 'failed', 'cancelled')),
    requested_by TEXT NOT NULL,
    engine_version TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_by TEXT,
    locked_until TIMESTAMPTZ,
    selected_path_count INTEGER NOT NULL DEFAULT 0 CHECK (selected_path_count >= 0),
    deferred_path_count INTEGER NOT NULL DEFAULT 0 CHECK (deferred_path_count >= 0),
    finding_count INTEGER NOT NULL DEFAULT 0 CHECK (finding_count >= 0),
    duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS rule_test_runs_claim_idx
    ON rule_test_runs(state, available_at, created_at)
    WHERE state = 'queued';
CREATE INDEX IF NOT EXISTS rule_test_runs_tenant_created_idx
    ON rule_test_runs(tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS rule_test_findings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    test_run_id UUID NOT NULL REFERENCES rule_test_runs(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    start_line INTEGER NOT NULL DEFAULT 0,
    end_line INTEGER NOT NULL DEFAULT 0,
    severity TEXT NOT NULL DEFAULT 'medium',
    category TEXT NOT NULL DEFAULT 'other',
    body TEXT NOT NULL,
    suggestion TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (test_run_id, fingerprint)
);
