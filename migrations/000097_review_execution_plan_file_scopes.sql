-- Retain source-free, per-file admission evidence for the exact review run.
-- This contains path classifications only, never patch or repository content.
ALTER TABLE review_execution_plans
    ADD COLUMN IF NOT EXISTS file_scopes JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE review_execution_plans
    DROP CONSTRAINT IF EXISTS review_execution_plans_file_scopes_array;

ALTER TABLE review_execution_plans
    ADD CONSTRAINT review_execution_plans_file_scopes_array
    CHECK (jsonb_typeof(file_scopes) = 'array');
