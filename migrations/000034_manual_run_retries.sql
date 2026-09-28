-- A console retry is a new immutable review run, never a mutation of the
-- terminal source run. The idempotency fence is scoped to that source run.
CREATE TABLE IF NOT EXISTS review_run_retry_requests (
    source_run_id UUID NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    retry_run_id UUID NOT NULL UNIQUE REFERENCES review_runs(id) ON DELETE CASCADE,
    requested_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_run_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS review_run_retry_requests_retry_idx
    ON review_run_retry_requests (retry_run_id);
