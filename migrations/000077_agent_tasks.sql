-- Agent tasks are a control-plane record only. A task cannot create a
-- workspace, invoke a coding CLI, push a branch, or open a PR. Those future
-- execution stages must consume an approved immutable plan.
CREATE TABLE agent_task_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('github','gitlab')),
    api_base_url TEXT NOT NULL,
    repository TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'disabled' CHECK (mode IN ('disabled','suggest','manual')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,provider,api_base_url,repository)
);

CREATE INDEX agent_task_policies_tenant_idx ON agent_task_policies (tenant_id, provider, api_base_url, repository);

CREATE TABLE agent_tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES provider_installations(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (provider IN ('github','gitlab')),
    api_base_url TEXT NOT NULL,
    repository TEXT NOT NULL,
    origin_kind TEXT NOT NULL CHECK (origin_kind IN ('issue','pull_request')),
    origin_number INTEGER NOT NULL CHECK (origin_number > 0),
    origin_revision TEXT NOT NULL,
    intent TEXT NOT NULL CHECK (intent = 'implement'),
    state TEXT NOT NULL DEFAULT 'received' CHECK (state IN ('received','plan_ready','awaiting_approval','admitted','cancelled','rejected','superseded')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    requested_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, installation_id, repository, origin_kind, origin_number, origin_revision, intent)
);

CREATE INDEX agent_tasks_tenant_state_created_idx ON agent_tasks (tenant_id, state, created_at DESC);
CREATE INDEX agent_tasks_origin_idx ON agent_tasks (tenant_id, provider, api_base_url, repository, origin_kind, origin_number, created_at DESC);

CREATE TABLE agent_task_plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id UUID NOT NULL REFERENCES agent_tasks(id) ON DELETE RESTRICT,
    revision INTEGER NOT NULL CHECK (revision > 0),
    state TEXT NOT NULL DEFAULT 'awaiting_approval' CHECK (state IN ('awaiting_approval','approved','rejected','superseded')),
    summary TEXT NOT NULL,
    plan_sha256 TEXT NOT NULL,
    created_by TEXT NOT NULL,
    approved_by TEXT,
    approved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, revision),
    CHECK ((state = 'approved' AND approved_by IS NOT NULL AND approved_at IS NOT NULL) OR state <> 'approved')
);

CREATE INDEX agent_task_plans_task_created_idx ON agent_task_plans (task_id, created_at DESC);
