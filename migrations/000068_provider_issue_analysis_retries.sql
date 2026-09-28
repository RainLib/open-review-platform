-- A failed provider-Issue analysis can be retried from the Console without
-- editing the provider Issue. The admitted Issue body and all configuration
-- snapshots remain unchanged; only the execution attempt advances.
ALTER TABLE provider_issue_analysis_jobs
    ADD COLUMN IF NOT EXISTS analysis_attempt INTEGER NOT NULL DEFAULT 1
        CHECK (analysis_attempt > 0 AND analysis_attempt <= 100);

CREATE TABLE IF NOT EXISTS provider_issue_analysis_retry_requests (
    job_id UUID NOT NULL REFERENCES provider_issue_analysis_jobs(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    analysis_attempt INTEGER NOT NULL CHECK (analysis_attempt > 1 AND analysis_attempt <= 100),
    requested_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, idempotency_key),
    UNIQUE (job_id, revision, analysis_attempt)
);

CREATE INDEX IF NOT EXISTS provider_issue_analysis_retry_attempt_idx
    ON provider_issue_analysis_retry_requests (job_id, revision, analysis_attempt DESC);
