-- Usage is kept even when commercial billing is disabled. Admission reserves
-- a review before execution; terminal transitions settle or release it. The
-- ledger is append-only so reconciliation can rebuild every aggregate.

CREATE TABLE IF NOT EXISTS tenant_entitlements (
    tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    monthly_review_limit BIGINT NOT NULL DEFAULT 0 CHECK (monthly_review_limit >= 0),
    soft_warning_percent INTEGER NOT NULL DEFAULT 80 CHECK (soft_warning_percent BETWEEN 1 AND 100),
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS usage_reservations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    run_id UUID NOT NULL UNIQUE REFERENCES review_runs(id) ON DELETE CASCADE,
    repository TEXT NOT NULL,
    metric TEXT NOT NULL CHECK (metric = 'review_run'),
    reserved_quantity BIGINT NOT NULL CHECK (reserved_quantity > 0),
    settled_quantity BIGINT NOT NULL DEFAULT 0 CHECK (settled_quantity >= 0),
    state TEXT NOT NULL CHECK (state IN ('reserved', 'settled', 'released')),
    period_start DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS usage_reservations_tenant_period_idx
    ON usage_reservations (tenant_id, period_start, state);

CREATE TABLE IF NOT EXISTS usage_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    run_id UUID REFERENCES review_runs(id) ON DELETE SET NULL,
    repository TEXT NOT NULL DEFAULT '',
    metric TEXT NOT NULL,
    quantity BIGINT NOT NULL CHECK (quantity >= 0),
    unit TEXT NOT NULL,
    event_kind TEXT NOT NULL CHECK (event_kind IN ('reserve', 'settle', 'release', 'adjustment')),
    idempotency_key TEXT NOT NULL UNIQUE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS usage_ledger_tenant_occurred_idx
    ON usage_ledger (tenant_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS usage_ledger_attribution_idx
    ON usage_ledger (tenant_id, repository, metric, occurred_at DESC);

CREATE OR REPLACE FUNCTION reject_usage_ledger_mutation()
RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'usage_ledger is append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS usage_ledger_immutable ON usage_ledger;
CREATE TRIGGER usage_ledger_immutable
BEFORE UPDATE OR DELETE ON usage_ledger
FOR EACH ROW EXECUTE FUNCTION reject_usage_ledger_mutation();
