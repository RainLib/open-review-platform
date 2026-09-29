-- Explicit model probes are durable worker jobs. route_content retains the
-- already-governed route snapshot so a later configuration save cannot change
-- the target while a probe is queued. It contains only an opaque credential
-- reference, never the credential itself.
CREATE TABLE IF NOT EXISTS model_probe_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('tenant', 'repository')),
    scope_ref TEXT NOT NULL DEFAULT '',
    config_revision INTEGER NOT NULL CHECK (config_revision > 0),
    content_sha256 TEXT NOT NULL,
    provider TEXT NOT NULL,
    protocol TEXT NOT NULL,
    model TEXT NOT NULL,
    endpoint_host TEXT NOT NULL,
    route_content JSONB NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    worker_id TEXT,
    locked_until TIMESTAMPTZ,
    response_sha256 TEXT,
    latency_ms BIGINT,
    error_code TEXT,
    error_message TEXT,
    requested_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);

CREATE INDEX model_probe_receipt_claim_idx
    ON model_probe_receipts (state, created_at)
    WHERE state IN ('queued', 'running');

CREATE INDEX model_probe_receipt_tenant_idx
    ON model_probe_receipts (tenant_id, scope_kind, scope_ref, created_at DESC);
