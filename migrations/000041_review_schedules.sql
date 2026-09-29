-- One-shot admission requests are deliberately not review_jobs. Until due,
-- they must not look queued, consume usage, or create provider status/checks.
CREATE TABLE IF NOT EXISTS review_schedules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_run_id UUID NOT NULL REFERENCES review_runs(id) ON DELETE RESTRICT,
    source_job_id UUID NOT NULL REFERENCES review_jobs(id) ON DELETE RESTRICT,
    installation_id UUID NOT NULL REFERENCES provider_installations(id) ON DELETE RESTRICT,
    request_id UUID NOT NULL REFERENCES review_requests(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    api_base_url TEXT NOT NULL,
    repository TEXT NOT NULL,
    review_number INTEGER NOT NULL CHECK (review_number > 0),
    title TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    review_mode TEXT NOT NULL CHECK (review_mode IN ('configured', 'standard', 'deep', 'security')),
    base_sha TEXT NOT NULL,
    head_sha TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('scheduled', 'blocked', 'admitted', 'coalesced', 'cancelled')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    scheduled_for TIMESTAMPTZ NOT NULL,
    requested_by TEXT NOT NULL,
    blocked_reason TEXT NOT NULL DEFAULT '',
    admitted_run_id UUID REFERENCES review_runs(id) ON DELETE SET NULL,
    cancelled_by TEXT NOT NULL DEFAULT '',
    cancelled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS review_schedules_due_idx
    ON review_schedules (scheduled_for, created_at)
    WHERE state = 'scheduled';

CREATE INDEX IF NOT EXISTS review_schedules_tenant_idx
    ON review_schedules (tenant_id, scheduled_for DESC, created_at DESC);

ALTER TABLE review_runs
    DROP CONSTRAINT IF EXISTS review_runs_trigger_kind_check;

ALTER TABLE review_runs
    ADD CONSTRAINT review_runs_trigger_kind_check
    CHECK (trigger_kind IN ('pull_request', 'comment', 'manual', 'retry', 'cli', 'scheduled'));
