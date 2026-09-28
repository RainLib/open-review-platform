CREATE TABLE issue_saved_views (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    owner_subject TEXT NOT NULL,
    visibility TEXT NOT NULL CHECK (visibility IN ('personal','workspace')),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    definition JSONB NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX issue_saved_views_name_idx
    ON issue_saved_views (tenant_id, visibility, (CASE WHEN visibility = 'personal' THEN owner_subject ELSE '' END), lower(name));
CREATE INDEX issue_saved_views_directory_idx ON issue_saved_views (tenant_id, visibility, owner_subject, updated_at DESC);
