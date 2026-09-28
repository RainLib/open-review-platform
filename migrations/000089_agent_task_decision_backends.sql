-- The decision model is part of the immutable task admission envelope.
-- Existing policies/tasks keep their actual deterministic behavior. Newly
-- created policies choose Jev unless an owner explicitly selects otherwise.
ALTER TABLE agent_task_policies
    ADD COLUMN decision_backend TEXT NOT NULL DEFAULT 'deterministic'
        CHECK (decision_backend IN ('jev','laya','agentjev','llm','deterministic'));
ALTER TABLE agent_task_policies ALTER COLUMN decision_backend SET DEFAULT 'jev';

ALTER TABLE agent_tasks
    ADD COLUMN decision_backend TEXT NOT NULL DEFAULT 'deterministic'
        CHECK (decision_backend IN ('jev','laya','agentjev','llm','deterministic'));
