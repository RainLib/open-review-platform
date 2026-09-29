-- JEV is immutable classification evidence. The controlled next action is
-- explanatory only; all execution admission remains enforced by task state,
-- source capture, plan approval, the runner and the adapter.
ALTER TABLE agent_task_classifications
    ADD COLUMN evaluation JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(evaluation) = 'array'),
    ADD COLUMN next_action TEXT NOT NULL DEFAULT 'await_plan_approval'
        CHECK (next_action IN ('reject','request_context','capture_source','await_plan_approval'));
