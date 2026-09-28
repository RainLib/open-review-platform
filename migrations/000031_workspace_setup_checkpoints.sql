-- A checkpoint is product setup state, not an installation substitute. It lets
-- an owner resume the staged onboarding flow without treating a provider
-- installation as proof that every governance decision was made.
CREATE TABLE IF NOT EXISTS workspace_setup_checkpoints (
    tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    current_step TEXT NOT NULL CHECK (current_step IN ('connect', 'review_scope', 'learning', 'severity', 'rules', 'complete')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by TEXT NOT NULL,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
