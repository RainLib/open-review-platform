-- A repository path is only unique within one provider endpoint. Rule
-- bindings and exceptions carry the same provider-qualified scope identity as
-- review configuration. Empty values retain legacy records for a deliberate,
-- observable compatibility fallback; newly-created repository records require
-- both fields at the API/store boundary.
ALTER TABLE rule_bindings
    ADD COLUMN IF NOT EXISTS scope_provider TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS scope_api_base_url TEXT NOT NULL DEFAULT '';

ALTER TABLE rule_exceptions
    ADD COLUMN IF NOT EXISTS scope_provider TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS scope_api_base_url TEXT NOT NULL DEFAULT '';

DO $$ BEGIN
    ALTER TABLE rule_bindings
        ADD CONSTRAINT rule_bindings_provider_scope_check
        CHECK (
            (scope_kind IN ('tenant', 'team') AND scope_provider = '' AND scope_api_base_url = '')
            OR (scope_kind = 'repository' AND (
                (scope_provider = '' AND scope_api_base_url = '')
                OR (scope_provider IN ('github', 'gitlab') AND scope_api_base_url <> '')
            ))
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    ALTER TABLE rule_exceptions
        ADD CONSTRAINT rule_exceptions_provider_scope_check
        CHECK (
            (scope_kind = 'tenant' AND scope_provider = '' AND scope_api_base_url = '')
            OR (scope_kind = 'repository' AND (
                (scope_provider = '' AND scope_api_base_url = '')
                OR (scope_provider IN ('github', 'gitlab') AND scope_api_base_url <> '')
            ))
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS rule_bindings_active_provider_scope_idx
    ON rule_bindings (tenant_id, scope_kind, scope_ref, scope_provider, scope_api_base_url, precedence)
    WHERE state IN ('active', 'shadow');

CREATE INDEX IF NOT EXISTS rule_exceptions_provider_admission_idx
    ON rule_exceptions (tenant_id, rule_version_id, scope_kind, scope_ref, scope_provider, scope_api_base_url, expires_at)
    WHERE state = 'approved';
