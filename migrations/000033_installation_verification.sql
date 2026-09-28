ALTER TABLE provider_installations
    ADD COLUMN verification_state TEXT NOT NULL DEFAULT 'legacy'
        CHECK (verification_state IN ('legacy','pending','checking','verified','failed')),
    ADD COLUMN verification_updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX provider_installations_verification_idx
    ON provider_installations (tenant_id, active, verification_state, created_at DESC);
