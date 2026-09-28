-- Provider Issue commands are intentionally not review_interactions: review
-- interaction rows require a pull-request review_request. Keeping a separate
-- receipt makes an Issue command idempotent without fabricating a PR.
CREATE TABLE agent_task_interactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    task_id UUID REFERENCES agent_tasks(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (provider IN ('github','gitlab')),
    provider_delivery_id TEXT NOT NULL,
    actor_external_id TEXT NOT NULL,
    repository TEXT NOT NULL,
    issue_number INTEGER NOT NULL CHECK (issue_number > 0),
    issue_revision TEXT NOT NULL,
    comment_external_id TEXT NOT NULL,
    command TEXT NOT NULL CHECK (command IN ('implement','invalid')),
    normalized_input TEXT NOT NULL,
    result TEXT NOT NULL CHECK (result IN ('ignored','accepted','rejected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_delivery_id)
);

CREATE INDEX agent_task_interactions_task_idx ON agent_task_interactions (task_id, created_at DESC);
CREATE INDEX agent_task_interactions_tenant_issue_idx ON agent_task_interactions (tenant_id, provider, repository, issue_number, created_at DESC);
