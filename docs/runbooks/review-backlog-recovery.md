# Review backlog recovery — v1

## Trigger

Use this runbook for an old review queue, expired leases, stale worker
heartbeats, acknowledgement SLA breaches, or a high terminal failure ratio.

## Diagnose

1. Open an incident and record the alert start, affected capability, deployed
   version, queue age and current outbox age.
2. Confirm PostgreSQL is authoritative and writable before changing workers.
3. Separate queued work from running work with expired leases. Do not mark a
   run successful from queue state alone.
4. Compare the latest heartbeat per worker kind and its version. Historical
   replica identities are retained as evidence but do not make a healthy newer
   replica look stale. A lease is not proof that its process is alive.
5. Inspect model/provider dependency health without logging credentials,
   prompts, repository source, or webhook payloads.

## Recover

1. Stop new admissions only if queue age continues to grow or PostgreSQL is
   unhealthy; record the exact stop threshold in the incident.
2. Restore healthy workers at the same version. Let expired leases be reclaimed
   by the durable claim path; never edit a lease owner manually.
3. Confirm high-priority security work leads while normal work continues to
   receive service.
4. Keep tenant concurrency limits active. Do not bypass capacity by changing a
   repository override.

## Verify and close

- ACK SLA breaches return to zero.
- Oldest queue and outbox ages decrease for two consecutive samples.
- No completed inbox message executes its handler again.
- Terminal receipts match the exact admitted revision.
- Record the last recovered run and a rollback decision before resolving the
  incident.
