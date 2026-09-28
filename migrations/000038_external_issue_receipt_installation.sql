-- Bind the receipt to the exact verified installation that admitted it. A
-- tenant may connect more than one provider account at the same API host, so
-- provider/API-base alone is not an authority for later external writes.
ALTER TABLE external_issue_receipts
    ADD COLUMN installation_id UUID REFERENCES provider_installations(id) ON DELETE RESTRICT;

ALTER TABLE external_issue_receipts
    ALTER COLUMN installation_id SET NOT NULL;

CREATE INDEX external_issue_receipts_installation_idx
    ON external_issue_receipts (installation_id, state, updated_at DESC);
