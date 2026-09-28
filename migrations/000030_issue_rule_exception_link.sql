-- Link a governed, time-bounded rule exception back to the issue that
-- motivated it. Approval may suppress only the exact issue revision that was
-- reviewed by the requester; revoke and expiry restore the effective issue
-- state without deleting historical evidence.
ALTER TABLE rule_exceptions
    ADD COLUMN IF NOT EXISTS source_issue_id UUID REFERENCES review_issues(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS source_issue_revision INTEGER CHECK (source_issue_revision > 0);

DO $$ BEGIN
    ALTER TABLE rule_exceptions
        ADD CONSTRAINT rule_exceptions_source_issue_revision_check
        CHECK ((source_issue_id IS NULL AND source_issue_revision IS NULL)
            OR (source_issue_id IS NOT NULL AND source_issue_revision IS NOT NULL));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS rule_exceptions_source_issue_idx
    ON rule_exceptions (source_issue_id, created_at DESC)
    WHERE source_issue_id IS NOT NULL;

ALTER TABLE review_issues
    ADD COLUMN IF NOT EXISTS active_exception_id UUID REFERENCES rule_exceptions(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS suppression_expires_at TIMESTAMPTZ;

DO $$ BEGIN
    ALTER TABLE review_issues
        ADD CONSTRAINT review_issues_exception_suppression_check
        CHECK ((active_exception_id IS NULL AND suppression_expires_at IS NULL)
            OR (active_exception_id IS NOT NULL AND suppression_expires_at IS NOT NULL));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE review_issue_events
    DROP CONSTRAINT IF EXISTS review_issue_events_action_check;

ALTER TABLE review_issue_events
    ADD CONSTRAINT review_issue_events_action_check
    CHECK (action IN (
        'assigned','unassigned','resolved','reopened','false_positive','suppression_cleared',
        'exception_approved','exception_revoked','exception_expired'
    ));
