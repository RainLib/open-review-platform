CREATE TABLE IF NOT EXISTS provider_repository_inventory (
    installation_id UUID NOT NULL REFERENCES provider_installations(id) ON DELETE CASCADE,
    external_id TEXT NOT NULL,
    name TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT '',
    visibility TEXT NOT NULL DEFAULT '',
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (installation_id, external_id),
    UNIQUE (installation_id, name)
);

CREATE INDEX IF NOT EXISTS provider_repository_inventory_list_idx
    ON provider_repository_inventory (installation_id, archived, name);
