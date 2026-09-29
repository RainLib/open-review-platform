CREATE TABLE IF NOT EXISTS provider_issue_feedback_polls (
    job_id UUID PRIMARY KEY REFERENCES provider_issue_analysis_jobs(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','completed')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    worker_id TEXT,
    locked_until TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    observed_at TIMESTAMPTZ,
    reaction_count INTEGER NOT NULL DEFAULT 0 CHECK (reaction_count >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO provider_issue_feedback_polls (job_id)
SELECT job.id
FROM provider_issue_analysis_jobs job
JOIN provider_installations installation ON installation.id=job.installation_id
WHERE job.provider='github'
  AND job.state='completed'
  AND installation.active=TRUE
  AND installation.verification_state IN ('legacy','verified')
  AND COALESCE((job.issue_triage_config->>'reaction_feedback')::boolean,FALSE)=TRUE
  AND job.completed_at >= now()-interval '30 days'
ON CONFLICT (job_id) DO NOTHING;

CREATE INDEX IF NOT EXISTS provider_issue_feedback_polls_claim_idx
    ON provider_issue_feedback_polls (state,available_at,job_id)
    WHERE state IN ('queued','running','completed');

COMMENT ON TABLE provider_issue_feedback_polls IS
    'Durable recurring GitHub comment-reaction polling. GitHub Apps expose no Reaction webhook event.';
