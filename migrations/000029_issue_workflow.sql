ALTER TABLE review_issues
    ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    ADD COLUMN assignee_subject TEXT NOT NULL DEFAULT '',
    ADD COLUMN disposition_kind TEXT NOT NULL DEFAULT '',
    ADD COLUMN disposition_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE review_issue_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES review_issues(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    actor_subject TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('assigned','unassigned','resolved','reopened','false_positive','suppression_cleared')),
    previous_status TEXT NOT NULL,
    next_status TEXT NOT NULL,
    previous_assignee TEXT NOT NULL DEFAULT '',
    next_assignee TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issue_id, revision)
);

CREATE INDEX review_issue_events_issue_created_idx
    ON review_issue_events (issue_id, created_at DESC);
