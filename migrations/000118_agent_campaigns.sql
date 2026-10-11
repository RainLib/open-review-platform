ALTER TABLE agent_tasks ADD COLUMN execution_dispatch_after TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE agent_tasks DROP CONSTRAINT agent_tasks_origin_kind_check;
ALTER TABLE agent_tasks DROP CONSTRAINT agent_tasks_origin_number_check;
ALTER TABLE agent_tasks ADD CONSTRAINT agent_tasks_origin_kind_check CHECK (origin_kind IN ('issue','pull_request','campaign'));
ALTER TABLE agent_tasks ADD CONSTRAINT agent_tasks_origin_number_check CHECK ((origin_kind='campaign' AND origin_number=0) OR (origin_kind IN ('issue','pull_request') AND origin_number>0));
CREATE TABLE agent_campaigns (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL REFERENCES tenants(id),
 idempotency_key TEXT NOT NULL, input JSONB NOT NULL, request_sha256 TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','paused','cancelled')),
 revision INTEGER NOT NULL DEFAULT 1, requested_by TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,idempotency_key)
);
CREATE TABLE agent_campaign_targets (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), campaign_id UUID NOT NULL REFERENCES agent_campaigns(id),
 installation_id UUID NOT NULL REFERENCES provider_installations(id), provider TEXT NOT NULL,api_base_url TEXT NOT NULL,repository TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'scan_queued' CHECK(state IN ('scan_queued','scanning','scan_complete','no_match','plan_ready','needs_attention','cancelled')),
 scan JSONB NOT NULL DEFAULT '{}', policy JSONB NOT NULL DEFAULT '{}', task_id UUID UNIQUE REFERENCES agent_tasks(id),
 error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '',
 scan_attempts INTEGER NOT NULL DEFAULT 0, worker_id TEXT NOT NULL DEFAULT '',locked_until TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(campaign_id,installation_id,repository)
);
CREATE INDEX agent_campaign_scan_idx ON agent_campaign_targets(state,locked_until);
CREATE INDEX agent_campaigns_tenant_idx ON agent_campaigns(tenant_id,created_at DESC,id);
CREATE OR REPLACE FUNCTION freeze_agent_workflow() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_policy JSONB;
BEGIN
 IF NEW.origin_kind='campaign' THEN
  SELECT policy->'workflow' INTO NEW.workflow FROM agent_campaign_targets WHERE policy->>'id'=NEW.id::text;
 ELSE
  SELECT p.workflow INTO NEW.workflow FROM agent_task_policies p WHERE p.tenant_id=NEW.tenant_id AND p.provider=NEW.provider AND p.api_base_url=NEW.api_base_url AND p.repository=NEW.repository;
 END IF;
 NEW.workflow:=COALESCE(NEW.workflow,'{}'::jsonb);
 IF NEW.parent_task_id IS NOT NULL THEN SELECT workflow INTO parent_policy FROM agent_tasks WHERE id=NEW.parent_task_id;NEW.workflow:=COALESCE(parent_policy,'{}'::jsonb);END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION protect_agent_campaign_request() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.input IS DISTINCT FROM OLD.input OR NEW.request_sha256 IS DISTINCT FROM OLD.request_sha256 OR NEW.tenant_id<>OLD.tenant_id OR NEW.requested_by<>OLD.requested_by OR NEW.idempotency_key<>OLD.idempotency_key THEN RAISE EXCEPTION 'campaign request is immutable';END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER agent_campaign_request_immutable BEFORE UPDATE ON agent_campaigns FOR EACH ROW EXECUTE FUNCTION protect_agent_campaign_request();
CREATE FUNCTION protect_agent_campaign_target() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.policy IS DISTINCT FROM OLD.policy OR NEW.campaign_id<>OLD.campaign_id OR NEW.installation_id<>OLD.installation_id OR NEW.provider<>OLD.provider OR NEW.api_base_url<>OLD.api_base_url OR NEW.repository<>OLD.repository THEN RAISE EXCEPTION 'campaign target scope is immutable';END IF;
 IF OLD.task_id IS NOT NULL AND (NEW.scan IS DISTINCT FROM OLD.scan OR NEW.task_id IS DISTINCT FROM OLD.task_id) THEN RAISE EXCEPTION 'approved campaign source is immutable';END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER agent_campaign_target_immutable BEFORE UPDATE ON agent_campaign_targets FOR EACH ROW EXECUTE FUNCTION protect_agent_campaign_target();
