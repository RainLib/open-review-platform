-- Provider reactions become tenant-scoped, auditable rule-quality evidence.
-- The comment marker is deterministic and contains no repository content.
ALTER TABLE review_findings
    ADD COLUMN IF NOT EXISTS provider_marker TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS review_findings_provider_marker_idx
    ON review_findings (provider_marker)
    WHERE provider_marker IS NOT NULL;

CREATE TABLE IF NOT EXISTS finding_feedback (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    finding_id UUID NOT NULL REFERENCES review_findings(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab', 'console')),
    delivery_id TEXT NOT NULL,
    reaction_external_id TEXT NOT NULL,
    actor_external_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('useful', 'false_positive', 'resolved', 'wont_fix')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retracted_at TIMESTAMPTZ,
    UNIQUE (provider, reaction_external_id)
);

CREATE INDEX IF NOT EXISTS finding_feedback_tenant_created_idx
    ON finding_feedback (tenant_id, created_at DESC);
