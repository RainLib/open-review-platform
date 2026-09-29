-- PostgreSQL remains the authority for workflow state. RabbitMQ is notified
-- through outbox_messages and can always be rebuilt from unpublished rows.

CREATE TABLE IF NOT EXISTS review_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES provider_installations(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    api_base_url TEXT NOT NULL,
    repository TEXT NOT NULL,
    review_number INTEGER NOT NULL CHECK (review_number > 0),
    current_run_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, installation_id, repository, review_number)
);

CREATE TABLE IF NOT EXISTS review_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL REFERENCES review_requests(id) ON DELETE CASCADE,
    legacy_job_id UUID UNIQUE REFERENCES review_jobs(id) ON DELETE SET NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    state TEXT NOT NULL CHECK (state IN (
        'acknowledged', 'admitted', 'preparing', 'analyzing', 'normalizing',
        'publishing', 'completed', 'failed', 'cancelled', 'superseded', 'needs_attention'
    )),
    trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('pull_request', 'comment', 'manual', 'retry')),
    head_sha TEXT NOT NULL,
    base_sha TEXT NOT NULL DEFAULT '',
    cancel_requested_at TIMESTAMPTZ,
    superseded_by UUID,
    failure_code TEXT,
    failure_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS review_runs_request_created_idx ON review_runs (request_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS review_runs_active_request_idx ON review_runs (request_id)
    WHERE state IN ('acknowledged', 'admitted', 'preparing', 'analyzing', 'normalizing', 'publishing');

ALTER TABLE review_requests
    ADD CONSTRAINT review_requests_current_run_fk
    FOREIGN KEY (current_run_id) REFERENCES review_runs(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS review_run_stages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
    stage TEXT NOT NULL CHECK (stage IN ('ack', 'admit', 'prepare', 'analyze', 'normalize', 'publish')),
    state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'succeeded', 'failed', 'skipped')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (run_id, stage, attempt)
);

CREATE TABLE IF NOT EXISTS review_run_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    event_type TEXT NOT NULL,
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('system', 'user', 'provider', 'worker')),
    actor_subject TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, revision)
);
CREATE INDEX IF NOT EXISTS review_run_events_run_revision_idx ON review_run_events (run_id, revision);

CREATE TABLE IF NOT EXISTS outbox_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    topic TEXT NOT NULL,
    dedupe_key TEXT NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    publish_attempts INTEGER NOT NULL DEFAULT 0 CHECK (publish_attempts >= 0),
    locked_by TEXT,
    locked_until TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS outbox_messages_claim_idx ON outbox_messages (available_at, created_at)
    WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS inbox_messages (
    consumer TEXT NOT NULL,
    message_id UUID NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('claimed', 'completed', 'released')),
    claim_token UUID,
    locked_until TIMESTAMPTZ,
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    completed_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, message_id)
);

CREATE TABLE IF NOT EXISTS review_interactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    request_id UUID REFERENCES review_requests(id) ON DELETE SET NULL,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    provider_delivery_id TEXT NOT NULL,
    actor_external_id TEXT NOT NULL,
    command TEXT NOT NULL,
    normalized_input TEXT NOT NULL,
    result TEXT NOT NULL CHECK (result IN ('accepted', 'rejected', 'ignored')),
    result_run_id UUID REFERENCES review_runs(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_delivery_id)
);

CREATE TABLE IF NOT EXISTS publication_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    receipt_kind TEXT NOT NULL CHECK (receipt_kind IN ('ack', 'summary', 'inline_finding', 'status')),
    stable_marker TEXT NOT NULL,
    external_id TEXT NOT NULL DEFAULT '',
    payload_hash TEXT NOT NULL,
    published_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, receipt_kind, stable_marker)
);
