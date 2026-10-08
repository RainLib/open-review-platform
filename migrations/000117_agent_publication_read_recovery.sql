-- Recover only an already validated feedback commit observed on its existing
-- owned Draft. This queue grants no coding or provider-write capability.
CREATE TABLE agent_task_publication_recoveries (
    attempt_id UUID PRIMARY KEY REFERENCES agent_task_attempts(id),
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','verified','rejected')),
    read_attempt INTEGER NOT NULL DEFAULT 0 CHECK (read_attempt BETWEEN 0 AND 3),
    original_error_code TEXT NOT NULL,
    original_error_message TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    worker_id TEXT,
    locked_until TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX agent_publication_recovery_queue ON agent_task_publication_recoveries(available_at)
    WHERE state='queued';
