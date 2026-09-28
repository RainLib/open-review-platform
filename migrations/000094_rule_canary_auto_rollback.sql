-- Failed candidate reviews must not keep receiving traffic indefinitely. A
-- distinct PR/MR counts once within the bounded observation window; quota
-- rejection before execution is not candidate evidence.
ALTER TABLE rule_rollouts
    ADD COLUMN auto_rollback_failed_runs INTEGER NOT NULL DEFAULT 1
        CHECK (auto_rollback_failed_runs BETWEEN 1 AND 10),
    ADD COLUMN auto_rollback_window_minutes INTEGER NOT NULL DEFAULT 60
        CHECK (auto_rollback_window_minutes BETWEEN 5 AND 1440),
    ADD COLUMN auto_rollback_reason TEXT;

CREATE INDEX rule_rollout_run_selections_candidate_idx
    ON rule_rollout_run_selections (rollout_id, run_id)
    WHERE selected_candidate;
