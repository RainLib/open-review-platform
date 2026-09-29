-- This table retains the decision made from the admitted general policy. It
-- must not be reconstructed from mutable workspace configuration or inferred
-- from a provider check receipt after the fact.
CREATE TABLE IF NOT EXISTS review_merge_gate_decisions (
    run_id UUID PRIMARY KEY REFERENCES review_runs(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL,
    threshold TEXT NOT NULL CHECK (threshold IN ('off', 'critical', 'high', 'medium', 'low')),
    conclusion TEXT NOT NULL CHECK (conclusion IN ('success', 'failure')),
    blocking_findings INTEGER NOT NULL CHECK (blocking_findings >= 0),
    finding_count INTEGER NOT NULL CHECK (finding_count >= blocking_findings),
    configuration_content_sha256 TEXT NOT NULL CHECK (length(configuration_content_sha256) = 64),
    origin_scope_kind TEXT NOT NULL CHECK (origin_scope_kind IN ('default', 'tenant', 'repository')),
    origin_scope_ref TEXT NOT NULL DEFAULT '',
    origin_revision INTEGER NOT NULL CHECK (origin_revision >= 0),
    evaluation_version TEXT NOT NULL CHECK (length(evaluation_version) BETWEEN 1 AND 64),
    decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (enabled AND threshold <> 'off' AND (conclusion = 'failure') = (blocking_findings > 0)) OR
        (NOT enabled AND threshold = 'off' AND conclusion = 'success' AND blocking_findings = 0)
    )
);
