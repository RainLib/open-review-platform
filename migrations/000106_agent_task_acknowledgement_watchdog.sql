-- The Agent watchdog scans only tasks still waiting for their first
-- provider-visible acknowledgement. Released source jobs are rechecked under
-- the task lock before a task can be marked needs_attention.
CREATE INDEX IF NOT EXISTS idx_agent_tasks_pending_ack_created
    ON agent_tasks (created_at, id)
    WHERE state='received' AND source_state='pending';
