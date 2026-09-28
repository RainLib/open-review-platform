# Broker rebuild from PostgreSQL outbox — v1

## Invariant

PostgreSQL outbox rows are the recovery authority. RabbitMQ accelerates
delivery; it is not the only copy of accepted work. Never purge the DLQ or mark
outbox rows published to make an alert disappear.

## Recover

1. Open an incident and confirm PostgreSQL health, backup status, outbox count,
   oldest age, RabbitMQ alarms and DLQ depth.
2. Stop the outbox relay if RabbitMQ is flapping. Leave unpublished rows
   untouched.
3. Restore RabbitMQ with quorum queues and the declared topology, including the
   dead-letter exchange and delivery limit.
4. Start one relay and verify publisher confirms plus mandatory-return handling.
5. Start consumers gradually. Completed inbox receipts must suppress physical
   duplicates; released/expired claims may execute again only through their
   idempotent business boundary.
6. Scale relays/workers only while outbox age decreases and DLQ depth does not
   grow.

## Stop and rollback

Stop recovery if PostgreSQL becomes unhealthy, mandatory returns appear, DLQ
depth increases for two samples, or a duplicate provider effect is observed.
Return to one relay, preserve the failing messages and collect their immutable
IDs for diagnosis.
