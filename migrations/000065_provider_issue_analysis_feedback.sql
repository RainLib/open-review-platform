CREATE TABLE IF NOT EXISTS provider_issue_analysis_feedback (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    job_id UUID NOT NULL REFERENCES provider_issue_analysis_jobs(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    delivery_id TEXT NOT NULL,
    reaction_external_id TEXT NOT NULL,
    actor_external_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('useful', 'not_useful')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retracted_at TIMESTAMPTZ,
    UNIQUE (provider, reaction_external_id)
);

CREATE INDEX IF NOT EXISTS provider_issue_analysis_feedback_job_idx
    ON provider_issue_analysis_feedback (job_id, created_at DESC)
    WHERE retracted_at IS NULL;
