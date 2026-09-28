-- A review run must retain the exact effective configuration resolved at
-- admission. Later workspace/repository edits cannot rewrite historical
-- review behavior or its trust boundary.
CREATE TABLE IF NOT EXISTS review_configuration_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
    section TEXT NOT NULL CHECK (section IN ('general', 'categories', 'filters', 'prompts', 'summary', 'messages')),
    origin_scope_kind TEXT NOT NULL CHECK (origin_scope_kind IN ('default', 'tenant', 'repository')),
    origin_scope_ref TEXT NOT NULL DEFAULT '',
    origin_revision INTEGER NOT NULL CHECK (origin_revision >= 0),
    content JSONB NOT NULL,
    content_sha256 TEXT NOT NULL CHECK (length(content_sha256) = 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, section)
);

CREATE INDEX IF NOT EXISTS review_configuration_snapshots_run_idx
    ON review_configuration_snapshots(run_id, section);
