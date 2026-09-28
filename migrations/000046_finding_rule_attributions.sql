-- A finding can cite one or more rules only when the exact rule version was
-- retained in the immutable snapshot used by that review run. The application
-- additionally validates the key/version pair against the canonical payload.
CREATE TABLE IF NOT EXISTS review_finding_rule_attributions (
    finding_id UUID NOT NULL REFERENCES review_findings(id) ON DELETE CASCADE,
    rule_key TEXT NOT NULL,
    rule_version_id UUID NOT NULL REFERENCES rule_versions(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (finding_id, rule_key, rule_version_id)
);

CREATE INDEX IF NOT EXISTS review_finding_rule_attributions_version_idx
    ON review_finding_rule_attributions (rule_version_id, created_at DESC);
