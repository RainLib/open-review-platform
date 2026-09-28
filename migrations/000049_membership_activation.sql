ALTER TABLE memberships
    ADD COLUMN IF NOT EXISTS active BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS deactivated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deactivated_by TEXT NOT NULL DEFAULT '';

ALTER TABLE memberships
    DROP CONSTRAINT IF EXISTS memberships_deactivation_consistency;

ALTER TABLE memberships
    ADD CONSTRAINT memberships_deactivation_consistency CHECK (
        (active = TRUE AND deactivated_at IS NULL AND deactivated_by = '')
        OR (active = FALSE AND deactivated_at IS NOT NULL AND deactivated_by <> '')
    );

CREATE INDEX IF NOT EXISTS memberships_active_subject_idx
    ON memberships (subject, tenant_id)
    WHERE active = TRUE;
