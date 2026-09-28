-- The admitted model scope is immutable per review run. Publication retries
-- can reuse this plan plus persisted findings without re-running OCR/LLM.
CREATE TABLE IF NOT EXISTS review_execution_plans (
    run_id UUID PRIMARY KEY REFERENCES review_runs(id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('standard', 'focused', 'critical')),
    selected_paths JSONB NOT NULL DEFAULT '[]'::jsonb,
    deferred_files INTEGER NOT NULL DEFAULT 0 CHECK (deferred_files >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(selected_paths) = 'array')
);
