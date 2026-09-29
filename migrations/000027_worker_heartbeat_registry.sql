CREATE TABLE worker_heartbeats (
    worker_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    version TEXT NOT NULL DEFAULT '',
    capacity INTEGER NOT NULL DEFAULT 1 CHECK (capacity > 0),
    busy INTEGER NOT NULL DEFAULT 0 CHECK (busy >= 0 AND busy <= capacity),
    started_at TIMESTAMPTZ NOT NULL,
    heartbeat_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (length(worker_id) BETWEEN 1 AND 200),
    CHECK (length(kind) BETWEEN 1 AND 80),
    CHECK (expires_at > heartbeat_at)
);

CREATE INDEX worker_heartbeats_freshness_idx
    ON worker_heartbeats (heartbeat_at DESC, kind);
