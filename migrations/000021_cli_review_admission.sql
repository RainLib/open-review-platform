ALTER TABLE review_runs
    DROP CONSTRAINT IF EXISTS review_runs_trigger_kind_check;

ALTER TABLE review_runs
    ADD CONSTRAINT review_runs_trigger_kind_check
    CHECK (trigger_kind IN ('pull_request', 'comment', 'manual', 'retry', 'cli'));

CREATE INDEX IF NOT EXISTS review_runs_cli_created_idx
    ON review_runs (created_at DESC)
    WHERE trigger_kind = 'cli';
