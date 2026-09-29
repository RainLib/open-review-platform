-- P1 execution bookkeeping. An approved plan is still not a provider-write
-- grant: the attempt only leases the immutable handoff to a separately
-- deployed sandbox adapter. A missing adapter becomes needs_attention rather
-- than an invisible queue or an implicit local CLI invocation.
ALTER TABLE agent_tasks DROP CONSTRAINT agent_tasks_state_check;
ALTER TABLE agent_tasks ADD CONSTRAINT agent_tasks_state_check CHECK (
    state IN (
        'received','plan_ready','awaiting_approval','execution_queued','executing',
        'needs_attention','completed','failed','cancelled','rejected','superseded'
    )
);

CREATE TABLE agent_task_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id UUID NOT NULL REFERENCES agent_tasks(id) ON DELETE RESTRICT,
    plan_id UUID NOT NULL REFERENCES agent_task_plans(id) ON DELETE RESTRICT,
    task_revision INTEGER NOT NULL CHECK (task_revision > 0),
    plan_revision INTEGER NOT NULL CHECK (plan_revision > 0),
    attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt > 0),
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','succeeded','needs_attention','failed','superseded')),
    worker_id TEXT,
    locked_until TIMESTAMPTZ,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, task_revision, plan_id, plan_revision),
    CHECK ((state = 'running' AND worker_id IS NOT NULL AND locked_until IS NOT NULL AND started_at IS NOT NULL) OR state <> 'running'),
    CHECK ((state IN ('succeeded','needs_attention','failed','superseded') AND finished_at IS NOT NULL AND locked_until IS NULL) OR state NOT IN ('succeeded','needs_attention','failed','superseded'))
);

CREATE INDEX agent_task_attempts_task_created_idx ON agent_task_attempts (task_id, created_at DESC);
CREATE INDEX agent_task_attempts_running_lease_idx ON agent_task_attempts (state, locked_until) WHERE state = 'running';
