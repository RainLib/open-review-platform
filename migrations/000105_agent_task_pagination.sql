-- Stable tenant-local task history, including ties at the same timestamp.
CREATE INDEX IF NOT EXISTS idx_agent_tasks_tenant_created_id
    ON agent_tasks (tenant_id, created_at DESC, id DESC);
