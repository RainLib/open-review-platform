-- One durable start claim per leased adapter attempt. The adapter may not
-- launch a coding CLI merely because a signed runner request reached it:
-- the control plane must first have attached the opaque job ID, and this
-- gate must be consumed exactly once across adapter restarts or replicas.
ALTER TABLE agent_task_attempts
    ADD COLUMN adapter_started_at TIMESTAMPTZ;

CREATE INDEX agent_task_attempts_adapter_start_pending_idx
    ON agent_task_attempts (id, adapter_job_id)
    WHERE state = 'running' AND adapter_job_id IS NOT NULL AND adapter_started_at IS NULL;
