-- Keep GitLab MR admission asynchronous when the webhook actor is not its
-- author and a username exclusion policy is configured. The raw verified
-- payload remains in webhook_deliveries; no provider credential is stored.
CREATE TABLE gitlab_author_admissions (
    delivery_id UUID PRIMARY KEY REFERENCES webhook_deliveries(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES provider_installations(id) ON DELETE RESTRICT,
    repository TEXT NOT NULL,
    review_number INTEGER NOT NULL CHECK (review_number > 0),
    head_sha TEXT NOT NULL CHECK (head_sha ~ '^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$'),
    author_id TEXT NOT NULL CHECK (author_id ~ '^[1-9][0-9]*$'),
    state TEXT NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued','running','admitted','skipped','failed')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    worker_id TEXT,
    locked_until TIMESTAMPTZ,
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT gitlab_author_admissions_lease_pair
        CHECK ((worker_id IS NULL) = (locked_until IS NULL))
);

CREATE INDEX gitlab_author_admissions_due_idx
    ON gitlab_author_admissions (available_at, delivery_id)
    WHERE state IN ('queued','running');
