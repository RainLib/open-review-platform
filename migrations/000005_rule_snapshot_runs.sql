-- A review run is an immutable audit record. Persist the resolved rule
-- snapshot chosen at admission so later binding or version changes cannot
-- rewrite the rules that an already-created run used.
ALTER TABLE review_runs
    ADD COLUMN IF NOT EXISTS rule_snapshot_id UUID REFERENCES rule_snapshots(id) ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS review_runs_rule_snapshot_idx
    ON review_runs(rule_snapshot_id)
    WHERE rule_snapshot_id IS NOT NULL;
