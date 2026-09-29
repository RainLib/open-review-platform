-- A queued receipt can lose authorisation before the worker receives it (for
-- example an owner disables the policy or deactivates the exact installation).
-- Record that terminal non-write explicitly instead of leaving a misleading
-- queued state in the Issue detail UI.
ALTER TABLE external_issue_receipts
    DROP CONSTRAINT external_issue_receipts_state_check;

ALTER TABLE external_issue_receipts
    ADD CONSTRAINT external_issue_receipts_state_check
    CHECK (state IN ('queued', 'created', 'failed', 'cancelled'));
