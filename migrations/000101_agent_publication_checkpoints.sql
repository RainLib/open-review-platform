-- A validated local commit is not proof that a provider accepted a branch or
-- Draft. Preserve the pre-push checkpoint separately from terminal results,
-- including across a later approved attempt on the same task/plan row.
ALTER TABLE agent_task_adapter_events DROP CONSTRAINT agent_task_adapter_events_kind_check;
ALTER TABLE agent_task_adapter_events ADD CONSTRAINT agent_task_adapter_events_kind_check
    CHECK (kind IN ('heartbeat','publication_checkpoint','completed','failed','needs_attention'));

CREATE TABLE agent_task_publication_checkpoints (
    attempt_id UUID NOT NULL REFERENCES agent_task_attempts(id) ON DELETE RESTRICT,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    adapter_job_id TEXT NOT NULL CHECK (adapter_job_id <> ''),
    branch_name TEXT NOT NULL CHECK (branch_name LIKE 'agent/%'),
    head_sha TEXT NOT NULL CHECK (head_sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
    patch_sha256 TEXT NOT NULL CHECK (patch_sha256 ~ '^[0-9a-f]{64}$'),
    changed_file_count INTEGER NOT NULL CHECK (changed_file_count > 0),
    diff_bytes BIGINT NOT NULL CHECK (diff_bytes > 0),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (attempt_id, attempt_number)
);
