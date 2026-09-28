-- The expiry reconciler finds suppressed Issues whose approved exception has
-- elapsed. These partial indexes keep the background maintenance pass bounded
-- without changing the immutable exception/version record itself.
CREATE INDEX IF NOT EXISTS rule_exceptions_expiry_reconcile_idx
    ON rule_exceptions (expires_at, tenant_id)
    WHERE state = 'approved';

CREATE INDEX IF NOT EXISTS review_issues_active_exception_reconcile_idx
    ON review_issues (tenant_id, active_exception_id)
    WHERE status = 'suppressed'
      AND disposition_kind = 'exception'
      AND active_exception_id IS NOT NULL;
