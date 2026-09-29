CREATE TABLE provider_health_probes (
    installation_id UUID PRIMARY KEY REFERENCES provider_installations(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','completed')),
    health_state TEXT NOT NULL DEFAULT 'configured_only' CHECK (health_state IN ('live','degraded','critical','stale','configured_only')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    worker_id TEXT,
    locked_until TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    observed_at TIMESTAMPTZ,
    credential_expires_at TIMESTAMPTZ,
    permissions JSONB NOT NULL DEFAULT '[]'::jsonb,
    rate_limit_remaining BIGINT,
    rate_limit_limit BIGINT,
    rate_limit_reset_at TIMESTAMPTZ,
    latency_ms BIGINT NOT NULL DEFAULT 0 CHECK (latency_ms >= 0),
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    receipt JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO provider_health_probes (installation_id)
SELECT id FROM provider_installations
ON CONFLICT (installation_id) DO NOTHING;

CREATE INDEX provider_health_probes_claim_idx
    ON provider_health_probes (state, available_at, locked_until)
    WHERE state IN ('queued','running','completed');
