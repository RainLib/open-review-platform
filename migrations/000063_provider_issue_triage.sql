CREATE TABLE IF NOT EXISTS provider_issue_analysis_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES provider_installations(id) ON DELETE RESTRICT,
    last_delivery_id UUID NOT NULL REFERENCES webhook_deliveries(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    api_base_url TEXT NOT NULL,
    repository TEXT NOT NULL,
    issue_number INTEGER NOT NULL CHECK (issue_number > 0),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    action TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    labels TEXT[] NOT NULL DEFAULT '{}',
    state TEXT NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'acknowledged', 'completed', 'failed')),
    stable_marker TEXT NOT NULL UNIQUE,
    model_route JSONB NOT NULL,
    model_route_sha256 TEXT NOT NULL,
    prompt_config JSONB NOT NULL,
    prompt_config_sha256 TEXT NOT NULL,
    analysis TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    UNIQUE (tenant_id, provider, api_base_url, repository, issue_number)
);

CREATE INDEX IF NOT EXISTS provider_issue_analysis_jobs_state_idx
    ON provider_issue_analysis_jobs (state, updated_at DESC);
CREATE INDEX IF NOT EXISTS provider_issue_analysis_jobs_tenant_idx
    ON provider_issue_analysis_jobs (tenant_id, updated_at DESC);
