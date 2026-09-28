-- A task receives a bounded execution envelope when it is admitted.  This
-- avoids a later policy edit changing the number of recoveries or the wall
-- clock budget for a plan which an operator has already approved.
ALTER TABLE agent_task_policies
    ADD COLUMN max_attempts SMALLINT NOT NULL DEFAULT 1 CHECK (max_attempts BETWEEN 1 AND 3),
    ADD COLUMN max_execution_seconds INTEGER NOT NULL DEFAULT 1800 CHECK (max_execution_seconds BETWEEN 60 AND 7200);

ALTER TABLE agent_tasks
    ADD COLUMN policy_revision INTEGER NOT NULL DEFAULT 1 CHECK (policy_revision > 0),
    ADD COLUMN max_attempts SMALLINT NOT NULL DEFAULT 1 CHECK (max_attempts BETWEEN 1 AND 3),
    ADD COLUMN max_execution_seconds INTEGER NOT NULL DEFAULT 1800 CHECK (max_execution_seconds BETWEEN 60 AND 7200);

-- `deadline_at` is deliberately separate from the renewable worker lease.
-- An adapter heartbeat can keep a worker lease alive, but can never extend a
-- task beyond its admitted execution envelope.
ALTER TABLE agent_task_attempts
    ADD COLUMN deadline_at TIMESTAMPTZ;

CREATE INDEX agent_task_attempts_running_deadline_idx
    ON agent_task_attempts (deadline_at)
    WHERE state = 'running';
