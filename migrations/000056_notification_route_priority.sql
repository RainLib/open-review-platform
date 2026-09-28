-- A route's order is policy, not an accident of insertion timing.  The first
-- matching route selects a destination, so retain one explicit, tenant-local
-- priority for both the preview and the publisher.
ALTER TABLE notification_routes
    ADD COLUMN IF NOT EXISTS priority INTEGER;

WITH ordered AS (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY tenant_id ORDER BY created_at, id)::INTEGER AS priority
    FROM notification_routes
    WHERE priority IS NULL
)
UPDATE notification_routes route
SET priority = ordered.priority
FROM ordered
WHERE route.id = ordered.id;

ALTER TABLE notification_routes
    ALTER COLUMN priority SET NOT NULL;

ALTER TABLE notification_routes
    ADD CONSTRAINT notification_routes_tenant_priority_unique UNIQUE (tenant_id, priority);

CREATE INDEX IF NOT EXISTS notification_routes_tenant_priority_idx
    ON notification_routes (tenant_id, priority, id);
