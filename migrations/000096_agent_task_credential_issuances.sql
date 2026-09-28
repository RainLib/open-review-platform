-- A broker request reserves one of a bounded number of provider token
-- issuances for an exact adapter job. Tokens and private keys are never stored.
CREATE TABLE agent_task_credential_issuances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id UUID NOT NULL REFERENCES agent_task_attempts(id) ON DELETE CASCADE,
    adapter_job_id TEXT NOT NULL,
    ordinal SMALLINT NOT NULL CHECK (ordinal BETWEEN 1 AND 6),
    tenant_id UUID NOT NULL,
    review_installation_id UUID NOT NULL,
    review_installation_external_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    api_base_url TEXT NOT NULL,
    repository TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','issued','failed','withheld')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    UNIQUE (attempt_id,adapter_job_id,ordinal),
    CHECK ((state='reserved' AND finished_at IS NULL) OR (state<>'reserved' AND finished_at IS NOT NULL))
);

CREATE INDEX agent_task_credential_issuances_attempt_idx
    ON agent_task_credential_issuances (attempt_id,created_at DESC);
