-- The executor choice is policy-governed and frozen with every task. A later
-- repository setting must never make an already approved plan switch between
-- Codex and Claude semantics.
ALTER TABLE agent_task_policies
    ADD COLUMN executor_profile TEXT NOT NULL DEFAULT 'codex'
    CHECK (executor_profile IN ('codex','claude'));

ALTER TABLE agent_tasks
    ADD COLUMN executor_profile TEXT NOT NULL DEFAULT 'codex'
    CHECK (executor_profile IN ('codex','claude'));
