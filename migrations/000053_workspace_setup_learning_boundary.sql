-- The learning checkpoint must be an auditable decision, rather than a UI-only
-- acknowledgement. Neither value enables training on source code or historic
-- comments; that boundary remains explicit in the product contract.
ALTER TABLE workspace_setup_checkpoints
    ADD COLUMN IF NOT EXISTS learning_mode TEXT,
    ADD COLUMN IF NOT EXISTS learning_reviewer_exclusions JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS learning_recorded_at TIMESTAMPTZ;

ALTER TABLE workspace_setup_checkpoints
    DROP CONSTRAINT IF EXISTS workspace_setup_checkpoints_learning_mode_check;

ALTER TABLE workspace_setup_checkpoints
    ADD CONSTRAINT workspace_setup_checkpoints_learning_mode_check
    CHECK (learning_mode IS NULL OR learning_mode IN ('governed_policy', 'skipped'));
