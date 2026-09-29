-- PostgreSQL truncates long generated constraint names. 000071 removes this
-- name for fresh databases; keep this compatibility migration so databases
-- which already applied an earlier draft of 000071 also release the legacy
-- repository-name-only uniqueness constraint.
ALTER TABLE review_configurations
    DROP CONSTRAINT IF EXISTS review_configurations_tenant_id_section_scope_kind_scope_re_key;
