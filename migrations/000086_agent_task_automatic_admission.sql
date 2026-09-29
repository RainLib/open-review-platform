-- Automatic admission is deliberately candidate-only. It requires an exact
-- repository label and never bypasses the existing source, plan or approval
-- gates before a coding agent can receive work.
ALTER TABLE agent_task_policies
    ADD COLUMN auto_admission_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN auto_admission_label TEXT NOT NULL DEFAULT 'openreview:implement'
        CHECK (char_length(auto_admission_label) BETWEEN 1 AND 128);

ALTER TABLE agent_task_interactions DROP CONSTRAINT agent_task_interactions_command_check;
ALTER TABLE agent_task_interactions ADD CONSTRAINT agent_task_interactions_command_check
    CHECK (command IN ('implement','status','cancel','invalid','auto'));
