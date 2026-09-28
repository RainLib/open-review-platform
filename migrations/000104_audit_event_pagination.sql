-- Stable tenant-local keyset ordering for the Console audit trail. The
-- event ID is the tie-breaker when multiple writes share a timestamp.
CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_created_id
    ON audit_events (tenant_id, created_at DESC, id DESC);
