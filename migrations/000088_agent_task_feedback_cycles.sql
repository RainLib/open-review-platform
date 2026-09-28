-- Feedback is a new bounded admission, not an instruction to resume a prior
-- sandbox. It is tied to one completed Draft PR/MR attempt and re-enters the
-- ordinary source-capture, plan and approval gates.
ALTER TABLE agent_task_policies
    ADD COLUMN max_feedback_cycles INTEGER NOT NULL DEFAULT 0
        CHECK (max_feedback_cycles >= 0 AND max_feedback_cycles <= 3);

ALTER TABLE agent_tasks
    ADD COLUMN max_feedback_cycles INTEGER NOT NULL DEFAULT 0
        CHECK (max_feedback_cycles >= 0 AND max_feedback_cycles <= 3),
    ADD COLUMN feedback_cycle INTEGER NOT NULL DEFAULT 0
        CHECK (feedback_cycle >= 0 AND feedback_cycle <= 3),
    ADD COLUMN execution_branch TEXT NOT NULL DEFAULT '',
    ADD COLUMN parent_task_id UUID REFERENCES agent_tasks(id) ON DELETE RESTRICT,
    ADD COLUMN parent_attempt_id UUID;

UPDATE agent_tasks
SET execution_branch = 'agent/' || id::text
WHERE execution_branch = '';

ALTER TABLE agent_tasks
    ADD CONSTRAINT agent_tasks_execution_branch_check
        CHECK (execution_branch ~ '^agent/[0-9a-f-]{36}$');

ALTER TABLE agent_task_attempts
    ADD COLUMN pull_request_number INTEGER NOT NULL DEFAULT 0
        CHECK (pull_request_number >= 0);

ALTER TABLE agent_tasks
    ADD CONSTRAINT agent_tasks_parent_attempt_fk
        FOREIGN KEY (parent_attempt_id) REFERENCES agent_task_attempts(id) ON DELETE RESTRICT;

CREATE TABLE agent_task_feedback_cycles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    parent_task_id UUID NOT NULL REFERENCES agent_tasks(id) ON DELETE RESTRICT,
    parent_attempt_id UUID NOT NULL REFERENCES agent_task_attempts(id) ON DELETE RESTRICT,
    child_task_id UUID REFERENCES agent_tasks(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (provider IN ('github','gitlab')),
    provider_delivery_id TEXT NOT NULL,
    repository TEXT NOT NULL,
    pull_request_number INTEGER NOT NULL CHECK (pull_request_number > 0),
    comment_external_id TEXT NOT NULL,
    actor_external_id TEXT NOT NULL,
    instruction_sha256 TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'received' CHECK (state IN ('received','rejected','superseded')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_delivery_id),
    UNIQUE (parent_attempt_id, comment_external_id)
);

CREATE INDEX agent_task_feedback_cycles_parent_idx
    ON agent_task_feedback_cycles (parent_task_id, created_at DESC);
