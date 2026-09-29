-- The Issue revision alone is not a safe checkout target. Resolve the
-- provider's default branch to an exact commit before an approved plan can be
-- handed to a coding adapter. Existing rows remain pending until a worker
-- resolves them; they cannot enter a new plan without a source base.
ALTER TABLE agent_tasks
    ADD COLUMN source_state TEXT NOT NULL DEFAULT 'pending' CHECK (source_state IN ('pending','ready','failed')),
    ADD COLUMN source_base_ref TEXT NOT NULL DEFAULT '',
    ADD COLUMN source_base_sha TEXT NOT NULL DEFAULT '',
    ADD COLUMN source_captured_at TIMESTAMPTZ;

CREATE INDEX agent_tasks_source_pending_idx
    ON agent_tasks (created_at, id)
    WHERE source_state = 'pending';
