-- External provider CI is observed independently of the immutable Open Review
-- merge gate. One run has one exact-head observation and a recoverable lease;
-- no provider token or raw response body is persisted here.
CREATE TABLE review_provider_check_observations (
    run_id UUID PRIMARY KEY REFERENCES review_runs(id) ON DELETE CASCADE,
    head_sha TEXT NOT NULL CHECK (head_sha ~ '^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$'),
    state TEXT NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued','running','observed','failed')),
    checks JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(checks) = 'array'),
    truncated BOOLEAN NOT NULL DEFAULT FALSE,
    observed_at TIMESTAMPTZ,
    error_code TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    worker_id TEXT,
    locked_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT review_provider_checks_lease_pair
        CHECK ((worker_id IS NULL) = (locked_until IS NULL))
);

CREATE INDEX review_provider_checks_due_idx
    ON review_provider_check_observations (available_at, run_id)
    WHERE state IN ('queued','observed','running');
