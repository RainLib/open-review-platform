-- A sandbox adapter may be restarted or redeliver its callback. Persist one
-- receipt per adapter delivery before changing task state so a valid callback
-- can never duplicate a draft-PR result or overwrite a newer lease holder.
ALTER TABLE agent_task_attempts
    ADD COLUMN adapter_job_id TEXT,
    ADD COLUMN result_summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN branch_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN head_sha TEXT NOT NULL DEFAULT '',
    ADD COLUMN pull_request_url TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX agent_task_attempts_adapter_job_idx
    ON agent_task_attempts (adapter_job_id) WHERE adapter_job_id IS NOT NULL;

CREATE TABLE agent_task_adapter_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id UUID NOT NULL REFERENCES agent_task_attempts(id) ON DELETE RESTRICT,
    delivery_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('heartbeat','completed','failed','needs_attention')),
    payload_sha256 TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (attempt_id, delivery_id)
);

CREATE INDEX agent_task_adapter_events_attempt_received_idx ON agent_task_adapter_events (attempt_id, received_at DESC);
