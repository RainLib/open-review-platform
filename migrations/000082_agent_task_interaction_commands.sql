-- Agent tasks have Issue-local status and cancellation commands in addition to
-- creation. They remain separate from pull-request review interactions.
ALTER TABLE agent_task_interactions DROP CONSTRAINT agent_task_interactions_command_check;
ALTER TABLE agent_task_interactions ADD CONSTRAINT agent_task_interactions_command_check
    CHECK (command IN ('implement','status','cancel','invalid'));
