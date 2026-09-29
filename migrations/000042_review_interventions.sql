-- An intervention is a human-operational task, not another run state. The
-- linked run remains immutable even after its queue item is acknowledged.
CREATE TABLE IF NOT EXISTS review_run_interventions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    run_id UUID NOT NULL UNIQUE REFERENCES review_runs(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    state TEXT NOT NULL CHECK (state IN ('open', 'claimed', 'resolved')),
    assignee_subject TEXT NOT NULL DEFAULT '',
    opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    resolved_by TEXT NOT NULL DEFAULT '',
    resolution TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS review_run_interventions_active_tenant_idx
    ON review_run_interventions (tenant_id, opened_at, run_id)
    WHERE state IN ('open', 'claimed');

-- Retain the exact pre-migration failure evidence, but make it visible in the
-- new queue model. A terminal run has only one intervention by construction.
INSERT INTO review_run_interventions (tenant_id, run_id, state, reason)
SELECT request.tenant_id, run.id, 'open', 'backfilled terminal review requiring operator attention'
FROM review_runs run
JOIN review_requests request ON request.id = run.request_id
WHERE run.state IN ('failed', 'needs_attention')
ON CONFLICT (run_id) DO NOTHING;
