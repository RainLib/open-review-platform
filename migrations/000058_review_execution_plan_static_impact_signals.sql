-- Static impact signals explain why an immutable risk plan selected its paths.
-- They are path-level evidence only, never a claimed runtime dependency graph.
ALTER TABLE review_execution_plans
    ADD COLUMN IF NOT EXISTS static_impact_signals JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE review_execution_plans
    DROP CONSTRAINT IF EXISTS review_execution_plans_static_impact_signals_array;

ALTER TABLE review_execution_plans
    ADD CONSTRAINT review_execution_plans_static_impact_signals_array
    CHECK (jsonb_typeof(static_impact_signals) = 'array');
