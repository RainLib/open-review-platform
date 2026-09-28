CREATE TABLE workspace_data_residency (
    tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    primary_region TEXT NOT NULL,
    backup_region TEXT NOT NULL DEFAULT '',
    object_region TEXT NOT NULL DEFAULT '',
    queue_region TEXT NOT NULL DEFAULT '',
    model_boundary TEXT NOT NULL DEFAULT 'external/unknown',
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    effective_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    observed_at TIMESTAMPTZ,
    updated_by TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE data_retention_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('tenant', 'repository')),
    scope_ref TEXT NOT NULL DEFAULT '',
    data_class TEXT NOT NULL CHECK (data_class IN ('raw_webhook', 'findings', 'audit', 'usage', 'operational_logs')),
    retention_days INTEGER NOT NULL CHECK (retention_days BETWEEN 1 AND 3650),
    state TEXT NOT NULL CHECK (state IN ('awaiting_approval', 'active', 'rejected', 'superseded')),
    revision INTEGER NOT NULL CHECK (revision > 0),
    impact_records BIGINT NOT NULL DEFAULT 0 CHECK (impact_records >= 0),
    impact_legal_hold BOOLEAN NOT NULL DEFAULT FALSE,
    change_reason TEXT NOT NULL,
    requested_by TEXT NOT NULL,
    decided_by TEXT NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope_kind = 'tenant' AND scope_ref = '') OR (scope_kind = 'repository' AND scope_ref <> ''))
);

CREATE UNIQUE INDEX data_retention_policy_active_idx
    ON data_retention_policies (tenant_id, scope_kind, scope_ref, data_class)
    WHERE state = 'active';
CREATE UNIQUE INDEX data_retention_policy_pending_idx
    ON data_retention_policies (tenant_id, scope_kind, scope_ref, data_class)
    WHERE state = 'awaiting_approval';
CREATE INDEX data_retention_policy_tenant_idx
    ON data_retention_policies (tenant_id, created_at DESC);

CREATE TABLE data_legal_holds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('tenant', 'repository')),
    scope_ref TEXT NOT NULL DEFAULT '',
    data_class TEXT NOT NULL CHECK (data_class IN ('raw_webhook', 'findings', 'audit', 'usage', 'operational_logs', 'all')),
    reason TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'released')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_by TEXT NOT NULL DEFAULT '',
    released_at TIMESTAMPTZ,
    CHECK ((scope_kind = 'tenant' AND scope_ref = '') OR (scope_kind = 'repository' AND scope_ref <> ''))
);

CREATE INDEX data_legal_holds_active_idx
    ON data_legal_holds (tenant_id, data_class, scope_kind, scope_ref)
    WHERE state = 'active';

CREATE TABLE data_governance_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    parent_job_id UUID REFERENCES data_governance_jobs(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN ('export', 'deletion', 'region_migration')),
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('tenant', 'repository', 'review_run', 'audit_range')),
    scope_ref TEXT NOT NULL DEFAULT '',
    data_classes TEXT[] NOT NULL DEFAULT '{}',
    desired_region TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('requested', 'awaiting_approval', 'queued', 'running', 'completed', 'failed', 'cancelled', 'rejected')),
    progress INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    reason TEXT NOT NULL,
    requested_by TEXT NOT NULL,
    approved_by TEXT NOT NULL DEFAULT '',
    approved_at TIMESTAMPTZ,
    reversible_until TIMESTAMPTZ,
    artifact_ref TEXT NOT NULL DEFAULT '',
    receipt JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (tenant_id, idempotency_key),
    CHECK ((scope_kind = 'tenant' AND scope_ref = '') OR (scope_kind <> 'tenant' AND scope_ref <> '')),
    CHECK ((kind = 'region_migration' AND desired_region <> '') OR (kind <> 'region_migration' AND desired_region = ''))
);

CREATE INDEX data_governance_jobs_tenant_idx
    ON data_governance_jobs (tenant_id, created_at DESC);
CREATE INDEX data_governance_jobs_claim_idx
    ON data_governance_jobs (state, created_at)
    WHERE state = 'queued';

CREATE TABLE data_governance_job_decisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES data_governance_jobs(id) ON DELETE CASCADE,
    actor_subject TEXT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (job_id, actor_subject)
);
