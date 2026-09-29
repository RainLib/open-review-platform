-- A classification is evidence about one exact task revision, not an
-- execution grant. Keeping it append-only makes classifier upgrades and
-- later model-assisted signals auditable.
CREATE TABLE agent_task_classifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id UUID NOT NULL REFERENCES agent_tasks(id) ON DELETE RESTRICT,
    task_revision INTEGER NOT NULL CHECK (task_revision > 0),
    source_revision TEXT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('needs_context','requires_human','rejected')),
    risk_level TEXT NOT NULL CHECK (risk_level IN ('unknown','low','medium','high','critical')),
    confidence INTEGER NOT NULL CHECK (confidence >= 0 AND confidence <= 100),
    reasons JSONB NOT NULL CHECK (jsonb_typeof(reasons) = 'array'),
    snapshot_sha256 TEXT NOT NULL,
    classifier_version TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, task_revision, classifier_version)
);

CREATE INDEX agent_task_classifications_task_created_idx ON agent_task_classifications (task_id, created_at DESC);
