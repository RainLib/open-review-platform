-- Persist command-selected review intensity with each run. A retry must use
-- the same scope, even if a deployment-wide default changes later.
ALTER TABLE review_runs
    ADD COLUMN review_mode TEXT NOT NULL DEFAULT 'configured'
    CHECK (review_mode IN ('configured', 'standard', 'deep', 'security'));
