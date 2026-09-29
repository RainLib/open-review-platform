ALTER TABLE data_governance_jobs
    ADD COLUMN available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN external_operation_id TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS data_governance_jobs_claim_idx;
CREATE INDEX data_governance_jobs_claim_idx
    ON data_governance_jobs (state, available_at, reversible_until, created_at)
    WHERE state IN ('queued', 'running');
