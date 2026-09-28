-- The built-in catalog is release-owned, but every installation keeps its
-- exact template identity so a historical governed draft can be explained and
-- a concurrent install cannot create duplicate policy sets.
ALTER TABLE rule_sets
    ADD COLUMN IF NOT EXISTS catalog_id TEXT,
    ADD COLUMN IF NOT EXISTS catalog_version TEXT,
    ADD COLUMN IF NOT EXISTS catalog_content_sha256 TEXT,
    ADD COLUMN IF NOT EXISTS catalog_origin TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS rule_sets_catalog_installation_idx
    ON rule_sets (tenant_id, catalog_id, catalog_version)
    WHERE catalog_id IS NOT NULL;
