ALTER TABLE agent_task_policies ADD COLUMN workflow JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE agent_tasks ADD COLUMN workflow JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE agent_task_feedback_cycles
  ADD COLUMN source_review_run_id UUID UNIQUE REFERENCES review_runs(id),
  ADD COLUMN system_instruction TEXT NOT NULL DEFAULT '';

CREATE FUNCTION freeze_agent_workflow() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_policy JSONB;
BEGIN
  SELECT p.workflow INTO NEW.workflow FROM agent_task_policies p
    WHERE p.tenant_id=NEW.tenant_id AND p.provider=NEW.provider AND p.api_base_url=NEW.api_base_url AND p.repository=NEW.repository;
  NEW.workflow:=COALESCE(NEW.workflow,'{}'::jsonb);
  IF NEW.parent_task_id IS NOT NULL THEN
    SELECT workflow INTO parent_policy FROM agent_tasks WHERE id=NEW.parent_task_id;
    -- Descendants cannot enlarge an existing task's approved workflow budget.
    NEW.workflow:=COALESCE(parent_policy,'{}'::jsonb);
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER agent_task_workflow_admission BEFORE INSERT ON agent_tasks FOR EACH ROW EXECUTE FUNCTION freeze_agent_workflow();

CREATE FUNCTION protect_agent_workflow() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.workflow IS DISTINCT FROM OLD.workflow THEN RAISE EXCEPTION 'admitted Agent workflow is immutable'; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER agent_task_workflow_immutable BEFORE UPDATE ON agent_tasks FOR EACH ROW EXECUTE FUNCTION protect_agent_workflow();

CREATE TABLE agent_task_requirements (
  task_id UUID PRIMARY KEY REFERENCES agent_tasks(id) ON DELETE CASCADE,
  source_sha TEXT NOT NULL,
  criteria JSONB NOT NULL CHECK(jsonb_typeof(criteria)='array' AND jsonb_array_length(criteria)>0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE FUNCTION protect_agent_requirements() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'verified Agent acceptance criteria are immutable';
END $$;
CREATE TRIGGER agent_task_requirements_immutable BEFORE UPDATE ON agent_task_requirements FOR EACH ROW EXECUTE FUNCTION protect_agent_requirements();

CREATE TABLE agent_task_acceptances (
  task_id UUID PRIMARY KEY REFERENCES agent_tasks(id) ON DELETE CASCADE,
  attempt_id UUID NOT NULL REFERENCES agent_task_attempts(id),
  head_sha TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
  state TEXT NOT NULL DEFAULT 'reviewing' CHECK(state IN ('reviewing','checks_failed','awaiting_acceptance','accepted','changes_requested','superseded','needs_attention')),
  criteria JSONB NOT NULL CHECK(jsonb_typeof(criteria)='array'),
  review_run_id UUID REFERENCES review_runs(id),
  reason TEXT NOT NULL DEFAULT '',
  decided_by TEXT NOT NULL DEFAULT '',
  decided_at TIMESTAMPTZ,
  evidence JSONB NOT NULL DEFAULT '[]',
  provider_head_sha TEXT NOT NULL DEFAULT '',
  provider_observed_at TIMESTAMPTZ,
  poll_after TIMESTAMPTZ NOT NULL DEFAULT now(),
  locked_until TIMESTAMPTZ,
  worker_id TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX agent_acceptance_poll ON agent_task_acceptances(poll_after) WHERE state<>'superseded';
