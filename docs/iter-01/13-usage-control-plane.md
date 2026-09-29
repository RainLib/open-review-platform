# 13 — Usage control plane

## 1. Boundary

Usage governance is available in both SaaS and self-hosted deployments. The
commercial billing adapter is optional; metering, hard quotas, attribution and
audit remain active without it. A monthly review limit of `0` means unlimited.

## 2. Admission contract

```text
verified event
  -> create immutable review run
  -> lock tenant admission boundary
  -> check settled + reserved current-month reviews
  -> reserve one review or terminally reject with quota_exceeded
  -> commit run, reservation, ledger and outbox atomically
```

Concurrent webhook deliveries cannot over-admit a tenant because admission is
serialized on the tenant row inside the workflow transaction. A quota-rejected
run remains visible and auditable, but its job is terminal before a worker can
claim it.

## 3. Settlement and recovery

- `completed` settles the reservation.
- `failed`, `cancelled`, `superseded` and `needs_attention` release it.
- reserve, settle and release use stable per-run idempotency keys.
- reservation state is mutable workflow state; `usage_ledger` is append-only.
- aggregate views are rebuildable from reservations and the ledger.

An owner/admin can reconcile one explicit UTC month. The durable review-run
state is the authority: non-terminal runs must be reserved, completed runs
settled, and every other terminal run released. Reconciliation detects a
missing reservation, a mismatched reservation state/period/quantity, or a
missing ledger proof. It repairs only `usage_reservations` and appends one
`adjustment` event per affected run; it never updates or deletes an existing
ledger row. Quota-rejected runs are excluded because they intentionally never
received a reservation.

Every request includes a bounded operational reason and an idempotency key.
Replaying the same key and parameters returns the original database-timestamped
report without a second adjustment. Reusing the key for a different request is
rejected. The report and audit event retain scanned, drifted, repaired and
adjustment counts.

## 4. Attribution

The first implemented metric is `review_run` with tenant, repository and run
attribution. The schema deliberately supports additional immutable metrics
such as model input/output tokens, OCR duration, provider API calls and storage
bytes once their authoritative producers expose structured measurements.

No estimated token or cost value is presented as actual usage.

## 5. Access and audit

- owner/admin: view usage and update the entitlement;
- owner/admin: inspect and run month-scoped reconciliation;
- billing viewer: read-only usage and reconciliation status;
- other roles: no usage dashboard access;

## Tenant-safe CSV export

The Usage page can download one non-future UTC calendar month as CSV. Owner,
admin and billing-viewer roles may export. The control plane performs tenant
authorization before reading any row and returns only repository aggregates,
the review-run metric, settled/reserved/released quantities, the configured
limit and the generation timestamp. Tenant IDs, run IDs, actor identities,
ledger metadata and provider credentials are intentionally absent.

The response is `private, no-store`, uses `nosniff`, and supplies an attachment
filename. Repository values are neutralized when they begin with a spreadsheet
formula control character, including after leading whitespace. Export does not
mutate reservations, append ledger events, create an invoice or contact a
commercial billing service.
- every entitlement update and reconciliation writes an audit event;
- ledger rows reject UPDATE and DELETE in PostgreSQL.

## 6. Remaining production evidence

Before a production-complete claim, validate high-concurrency admission,
month-boundary behavior, a controlled reconciliation drill in the target
environment, export isolation, and billing-adapter integration. The isolated
PostgreSQL test proves all three drift repairs, append-only evidence, roles,
idempotent replay and conflict handling; the deployed Console read proves the
current month is clean, but intentionally did not mutate the retained workspace.
