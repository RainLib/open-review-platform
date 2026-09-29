CREATE TABLE platform_incidents (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK (length(title) BETWEEN 3 AND 180),
    state TEXT NOT NULL CHECK (state IN ('active', 'mitigating', 'resolved')),
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'provider', 'queue', 'worker', 'model', 'data_governance', 'identity')),
    affected_area TEXT NOT NULL CHECK (length(affected_area) BETWEEN 3 AND 160),
    started_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    resolved_by TEXT,
    resolution TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((state = 'resolved') = (resolved_at IS NOT NULL)),
    CHECK ((state = 'resolved') = (resolved_by IS NOT NULL))
);

CREATE INDEX platform_incidents_tenant_timeline_idx
    ON platform_incidents (tenant_id, state, started_at DESC, id DESC);
