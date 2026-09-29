-- Shadow evidence and a distinct owner/admin decision may authorize one
-- visible Canary. Shadow replay can continue while Canary observes real runs;
-- neither mode mutates its frozen baseline/candidate binding pair.
ALTER TABLE rule_rollouts
    ADD COLUMN approved_shadow_comparison_id UUID
        REFERENCES rule_rollout_comparisons(id) ON DELETE RESTRICT;

-- Older releases could record Canary rows without ever selecting them at
-- admission. Retire those unevidenced rows before enabling visible selection;
-- operators can create a new rollout after an independent Shadow comparison.
WITH retired AS (
    UPDATE rule_rollouts
    SET state = 'rolled_back', revision = revision + 1, updated_at = now()
    WHERE mode = 'canary' AND state IN ('active', 'paused')
    RETURNING id, tenant_id, revision
)
INSERT INTO audit_events(tenant_id, actor_subject, action, target, metadata)
SELECT tenant_id, 'system:migration:000093', 'rule_rollout.legacy_canary_retired', id::text,
       jsonb_build_object('reason', 'missing shadow comparison evidence', 'revision', revision)
FROM retired;

DROP INDEX IF EXISTS rule_rollouts_active_candidate_idx;

CREATE UNIQUE INDEX rule_rollouts_active_shadow_pair_idx
    ON rule_rollouts (tenant_id, baseline_binding_id, candidate_binding_id)
    WHERE mode = 'shadow' AND state IN ('active', 'paused');

CREATE UNIQUE INDEX rule_rollouts_active_canary_candidate_idx
    ON rule_rollouts (tenant_id, candidate_binding_id)
    WHERE mode = 'canary' AND state IN ('active', 'paused');

CREATE UNIQUE INDEX rule_rollouts_active_canary_baseline_idx
    ON rule_rollouts (tenant_id, baseline_binding_id)
    WHERE mode = 'canary' AND state IN ('active', 'paused');

ALTER TABLE rule_rollouts
    ADD CONSTRAINT rule_rollouts_canary_evidence_check
        CHECK ((mode = 'shadow' AND approved_shadow_comparison_id IS NULL)
            OR (mode = 'canary' AND (approved_shadow_comparison_id IS NOT NULL
                OR state IN ('rolled_back', 'promoted'))));

-- This is the immutable admission decision for the exact run. A later pause or
-- rollback changes only future admissions, not an already compiled snapshot.
CREATE TABLE rule_rollout_run_selections (
    run_id UUID NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
    rollout_id UUID NOT NULL REFERENCES rule_rollouts(id) ON DELETE RESTRICT,
    baseline_binding_id UUID NOT NULL REFERENCES rule_bindings(id) ON DELETE RESTRICT,
    candidate_binding_id UUID NOT NULL REFERENCES rule_bindings(id) ON DELETE RESTRICT,
    selected_binding_id UUID NOT NULL REFERENCES rule_bindings(id) ON DELETE RESTRICT,
    selected_rule_version_id UUID NOT NULL REFERENCES rule_versions(id) ON DELETE RESTRICT,
    cohort_bucket INTEGER NOT NULL CHECK (cohort_bucket BETWEEN 0 AND 9999),
    selected_candidate BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, rollout_id),
    CHECK ((selected_candidate AND selected_binding_id = candidate_binding_id)
        OR (NOT selected_candidate AND selected_binding_id = baseline_binding_id))
);

CREATE INDEX rule_rollout_run_selections_rollout_idx
    ON rule_rollout_run_selections (rollout_id, created_at DESC);
