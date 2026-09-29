-- Issue approval is a verified, idempotent provider interaction. The existing
-- task/plan state gates still decide whether the exact digest can be admitted.
ALTER TABLE agent_task_interactions DROP CONSTRAINT agent_task_interactions_command_check;
ALTER TABLE agent_task_interactions ADD CONSTRAINT agent_task_interactions_command_check
    CHECK (command IN ('implement','status','cancel','approve','invalid','auto'));
