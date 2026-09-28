-- Existing approved plans remain readable with an empty structured artifact.
-- New Console plans persist the reviewable fields alongside their canonical
-- summary; the summary hash remains the immutable adapter handoff contract.
ALTER TABLE agent_task_plans
    ADD COLUMN sections JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT agent_task_plans_sections_object CHECK (jsonb_typeof(sections) = 'object');
