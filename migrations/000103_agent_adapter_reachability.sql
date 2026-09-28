ALTER TABLE worker_heartbeats
    ADD COLUMN adapter_probe_at TIMESTAMPTZ,
    ADD COLUMN adapter_reachable BOOLEAN;

ALTER TABLE worker_heartbeats
    ADD CONSTRAINT worker_heartbeats_adapter_probe_pair
    CHECK ((adapter_probe_at IS NULL) = (adapter_reachable IS NULL));
