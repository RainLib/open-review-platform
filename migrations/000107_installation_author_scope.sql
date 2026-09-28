-- Keep existing connections at their previous all-authors behavior. A
-- newly selected owner-only scope must carry one OAuth-bound provider user ID.
ALTER TABLE provider_installations
    ADD COLUMN author_scope TEXT NOT NULL DEFAULT 'all',
    ADD COLUMN author_external_id TEXT NOT NULL DEFAULT '';

ALTER TABLE provider_installations
    ADD CONSTRAINT provider_installations_author_scope_check
    CHECK (
        (author_scope = 'all' AND author_external_id = '') OR
        (author_scope = 'mine' AND author_external_id ~ '^[1-9][0-9]{0,18}$')
    );
