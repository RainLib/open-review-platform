-- A Shadow execution is a provider-silent replay of an already admitted run.
-- It is addressable independently from an interactive Test Lab experiment so
-- rollout results can be audited and compared without changing provider state.
ALTER TABLE rule_test_runs
    ADD COLUMN IF NOT EXISTS rollout_id UUID REFERENCES rule_rollouts(id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX IF NOT EXISTS rule_test_runs_shadow_rollout_source_idx
    ON rule_test_runs (rollout_id, source_run_id)
    WHERE rollout_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS rule_test_runs_rollout_state_idx
    ON rule_test_runs (rollout_id, state, created_at DESC)
    WHERE rollout_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS rule_rollout_comparisons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rollout_id UUID NOT NULL REFERENCES rule_rollouts(id) ON DELETE RESTRICT,
    baseline_run_id UUID NOT NULL REFERENCES review_runs(id) ON DELETE RESTRICT,
    candidate_test_run_id UUID NOT NULL REFERENCES rule_test_runs(id) ON DELETE RESTRICT,
    baseline_snapshot_id UUID NOT NULL REFERENCES rule_snapshots(id) ON DELETE RESTRICT,
    candidate_snapshot_id UUID NOT NULL REFERENCES rule_snapshots(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('completed', 'failed')),
    baseline_finding_count INTEGER NOT NULL DEFAULT 0 CHECK (baseline_finding_count >= 0),
    candidate_finding_count INTEGER NOT NULL DEFAULT 0 CHECK (candidate_finding_count >= 0),
    added_finding_count INTEGER NOT NULL DEFAULT 0 CHECK (added_finding_count >= 0),
    removed_finding_count INTEGER NOT NULL DEFAULT 0 CHECK (removed_finding_count >= 0),
    matched_finding_count INTEGER NOT NULL DEFAULT 0 CHECK (matched_finding_count >= 0),
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    UNIQUE (candidate_test_run_id)
);

CREATE INDEX IF NOT EXISTS rule_rollout_comparisons_rollout_created_idx
    ON rule_rollout_comparisons (rollout_id, created_at DESC);
