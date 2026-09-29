CREATE TABLE IF NOT EXISTS notification_destinations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    provider TEXT NOT NULL CHECK (provider IN ('dingtalk', 'feishu', 'webhook')),
    secret_ref TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS notification_routes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    destination_id UUID NOT NULL REFERENCES notification_destinations(id) ON DELETE CASCADE,
    repository_glob TEXT NOT NULL DEFAULT '*',
    branch_glob TEXT NOT NULL DEFAULT '*',
    event_types TEXT[] NOT NULL,
    min_severity TEXT NOT NULL DEFAULT 'medium' CHECK (min_severity IN ('low', 'medium', 'high', 'critical')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS notification_routes_match_idx
    ON notification_routes (tenant_id, enabled, destination_id);

CREATE TABLE IF NOT EXISTS notification_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id TEXT NOT NULL,
    run_id UUID NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
    destination_id UUID NOT NULL REFERENCES notification_destinations(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sending', 'delivered', 'failed')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    response_code INTEGER,
    last_error TEXT,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (event_id, destination_id)
);
CREATE INDEX IF NOT EXISTS notification_deliveries_retry_idx
    ON notification_deliveries (state, updated_at) WHERE state IN ('pending', 'failed');
