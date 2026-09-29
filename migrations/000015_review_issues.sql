-- Issues are stable, tenant-scoped aggregates over findings. Occurrences keep
-- per-review/head evidence so a new head can resolve or regress an issue
-- without mutating historical findings.
CREATE TABLE IF NOT EXISTS review_issues (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    api_base_url TEXT NOT NULL,
    repository TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    path TEXT NOT NULL DEFAULT '',
    severity TEXT NOT NULL DEFAULT 'medium',
    category TEXT NOT NULL DEFAULT 'other',
    body_preview TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'regressed', 'resolved', 'suppressed')),
    occurrence_count INTEGER NOT NULL DEFAULT 0 CHECK (occurrence_count >= 0),
    active_occurrence_count INTEGER NOT NULL DEFAULT 0 CHECK (active_occurrence_count >= 0),
    pull_request_count INTEGER NOT NULL DEFAULT 0 CHECK (pull_request_count >= 0),
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, provider, api_base_url, repository, fingerprint)
);

CREATE INDEX IF NOT EXISTS review_issues_tenant_status_seen_idx
    ON review_issues (tenant_id, status, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS review_issues_tenant_repository_idx
    ON review_issues (tenant_id, repository, last_seen_at DESC);

CREATE TABLE IF NOT EXISTS review_issue_occurrences (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id UUID NOT NULL REFERENCES review_issues(id) ON DELETE CASCADE,
    finding_id UUID NOT NULL UNIQUE REFERENCES review_findings(id) ON DELETE CASCADE,
    job_id UUID NOT NULL REFERENCES review_jobs(id) ON DELETE CASCADE,
    request_id UUID REFERENCES review_requests(id) ON DELETE SET NULL,
    run_id UUID REFERENCES review_runs(id) ON DELETE SET NULL,
    review_number INTEGER NOT NULL CHECK (review_number > 0),
    head_sha TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issue_id, job_id)
);

CREATE INDEX IF NOT EXISTS review_issue_occurrences_issue_active_idx
    ON review_issue_occurrences (issue_id, active, created_at DESC);
CREATE INDEX IF NOT EXISTS review_issue_occurrences_review_head_idx
    ON review_issue_occurrences (request_id, head_sha, active);
