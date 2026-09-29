CREATE TABLE IF NOT EXISTS provider_issue_analysis_receipts (
    delivery_id UUID PRIMARY KEY REFERENCES webhook_deliveries(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES provider_installations(id) ON DELETE CASCADE,
    job_id UUID NOT NULL REFERENCES provider_issue_analysis_jobs(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    repository TEXT NOT NULL,
    issue_number INTEGER NOT NULL CHECK (issue_number > 0),
    action TEXT NOT NULL,
    admitted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS provider_issue_analysis_receipts_installation_idx
    ON provider_issue_analysis_receipts (installation_id, admitted_at DESC, delivery_id DESC);

INSERT INTO provider_issue_analysis_receipts (
    delivery_id,
    tenant_id,
    installation_id,
    job_id,
    revision,
    repository,
    issue_number,
    action,
    admitted_at
)
SELECT
    job.last_delivery_id,
    job.tenant_id,
    job.installation_id,
    job.id,
    job.revision,
    job.repository,
    job.issue_number,
    job.action,
    job.created_at
FROM provider_issue_analysis_jobs job
ON CONFLICT (delivery_id) DO NOTHING;
