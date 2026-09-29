-- Keyset ordering and actor-scoped dispositions for the cross-run explorer.
CREATE INDEX IF NOT EXISTS review_findings_created_id_idx
    ON review_findings (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS finding_feedback_console_actor_idx
    ON finding_feedback (finding_id, actor_external_id, created_at DESC, id DESC)
    WHERE provider = 'console' AND retracted_at IS NULL;

CREATE INDEX IF NOT EXISTS finding_feedback_active_finding_kind_idx
    ON finding_feedback (finding_id, kind)
    WHERE retracted_at IS NULL;
