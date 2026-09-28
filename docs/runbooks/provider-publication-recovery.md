# Provider publication recovery — v1

## Diagnose

1. Identify the provider, installation, exact run revision, receipt kind and
   safe error classification. Do not copy tokens or provider response bodies
   into the incident.
2. Check the read-only provider probe, permission state, rate-limit reset and
   whether the admitted head is still current.
3. Inspect the stable marker or Check/status identity before any retry.

## Recover

1. For 429 or transient 5xx, retain the durable successor and its bounded
   `Retry-After`/backoff schedule. Do not sleep inside a consumer or immediately
   NACK-loop the same delivery.
2. For an ambiguous accepted write, search by stable marker or Check identity.
   Complete the existing receipt when found; never create a replacement first.
3. For permanent permission failures, repair the installation and run the
   provider probe before an approved retry.
4. Reject a passing status when the provider head no longer matches the
   admitted revision.

## Verify and close

- Exactly one external comment/Check/status exists for each stable identity.
- The receipt points to that external identity and exact revision.
- Publication failure count stops increasing for two samples.
- The incident records the retry actor, reason, outcome and rollback decision.
