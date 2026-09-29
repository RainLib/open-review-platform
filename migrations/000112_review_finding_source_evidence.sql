-- Only bounded excerpts and structurally checked candidate patches from the
-- exact reviewed head are retained. They share the findings data class and
-- are subject to the same tenant-scoped export, retention and erasure rules.
ALTER TABLE review_findings
    ADD COLUMN IF NOT EXISTS code_excerpt TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS code_excerpt_start_line INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS proposed_patch TEXT NOT NULL DEFAULT '';

ALTER TABLE review_findings
    ADD CONSTRAINT review_findings_source_evidence_bounds CHECK (
        octet_length(code_excerpt) <= 8192 AND
        octet_length(proposed_patch) <= 16384 AND
        code_excerpt_start_line >= 0 AND
        (code_excerpt <> '' OR code_excerpt_start_line = 0)
    );
