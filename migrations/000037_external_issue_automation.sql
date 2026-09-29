-- Provider Issue automation is a governed, tenant-local policy. A matching
-- aggregate is enqueued transactionally and gets one durable receipt, so a
-- broker retry or worker crash cannot create duplicate external tickets.
CREATE TABLE issue_auto_create_policies (
    tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    target TEXT NOT NULL DEFAULT 'provider' CHECK (target = 'provider'),
    repository_scopes TEXT[] NOT NULL DEFAULT '{}',
    minimum_severity TEXT NOT NULL DEFAULT 'high'
        CHECK (minimum_severity IN ('low', 'medium', 'high', 'critical')),
    categories TEXT[] NOT NULL DEFAULT '{}',
    trigger_first_seen BOOLEAN NOT NULL DEFAULT TRUE,
    trigger_regressed BOOLEAN NOT NULL DEFAULT TRUE,
    repeat_occurrence_threshold INTEGER NOT NULL DEFAULT 0
        CHECK (repeat_occurrence_threshold >= 0 AND repeat_occurrence_threshold <= 10000),
    labels TEXT[] NOT NULL DEFAULT '{}',
    assignee_external_id TEXT NOT NULL DEFAULT '',
    title_template TEXT NOT NULL,
    body_template TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (cardinality(repository_scopes) <= 100),
    CHECK (cardinality(categories) <= 100),
    CHECK (cardinality(labels) <= 50),
    CHECK (length(title_template) <= 240),
    CHECK (length(body_template) <= 12000),
    CHECK (
        trigger_first_seen
        OR trigger_regressed
        OR repeat_occurrence_threshold > 0
    )
);

CREATE TABLE external_issue_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES review_issues(id) ON DELETE CASCADE,
    policy_revision INTEGER NOT NULL CHECK (policy_revision > 0),
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    api_base_url TEXT NOT NULL,
    repository TEXT NOT NULL,
    trigger TEXT NOT NULL CHECK (trigger IN ('first_seen', 'regressed', 'repeated')),
    stable_marker TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    labels TEXT[] NOT NULL DEFAULT '{}',
    assignee_external_id TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'created', 'failed')),
    external_id TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issue_id),
    UNIQUE (stable_marker)
);

CREATE INDEX external_issue_receipts_tenant_state_idx
    ON external_issue_receipts (tenant_id, state, updated_at DESC);
CREATE INDEX external_issue_receipts_issue_idx
    ON external_issue_receipts (issue_id, created_at DESC);
