CREATE TABLE data_governance_erasure_tombstones (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES data_governance_jobs(id) ON DELETE RESTRICT,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    data_class TEXT NOT NULL CHECK (data_class IN ('raw_webhook', 'findings', 'audit')),
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('tenant', 'repository')),
    scope_ref TEXT NOT NULL DEFAULT '',
    records_redacted BIGINT NOT NULL CHECK (records_redacted >= 0),
    executed_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (job_id, data_class)
);

CREATE INDEX data_governance_erasure_tenant_idx
    ON data_governance_erasure_tombstones (tenant_id, created_at DESC);
