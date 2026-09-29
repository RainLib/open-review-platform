-- A rollout is an auditable promotion decision, not a mutable binding label.
-- Candidate bindings remain shadow while a rollout is active; only the
-- admission compiler may select them for a deterministic canary cohort.
CREATE TABLE rule_rollouts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    baseline_binding_id UUID NOT NULL REFERENCES rule_bindings(id) ON DELETE RESTRICT,
    candidate_binding_id UUID NOT NULL REFERENCES rule_bindings(id) ON DELETE RESTRICT,
    mode TEXT NOT NULL CHECK (mode IN ('shadow', 'canary')),
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'paused', 'promoted', 'rolled_back')),
    canary_basis_points INTEGER NOT NULL DEFAULT 0 CHECK (canary_basis_points BETWEEN 0 AND 10000),
    cohort_salt UUID NOT NULL DEFAULT gen_random_uuid(),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (baseline_binding_id <> candidate_binding_id),
    CHECK ((mode = 'shadow' AND canary_basis_points = 0) OR (mode = 'canary' AND canary_basis_points BETWEEN 1 AND 10000))
);

CREATE UNIQUE INDEX rule_rollouts_active_candidate_idx
    ON rule_rollouts (tenant_id, candidate_binding_id)
    WHERE state IN ('active', 'paused');
CREATE INDEX rule_rollouts_admission_idx
    ON rule_rollouts (tenant_id, state, mode, created_at DESC);
