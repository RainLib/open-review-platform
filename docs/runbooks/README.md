# Open Review recovery runbooks

These documents are versioned operator guidance for the alerts in
`deploy/observability/open-review-alerts.yml`. They do not authorize the tenant
Console to execute shell commands, purge queues, rotate credentials, or mutate
provider state. Every recovery action must retain its incident, actor, exact
target, start/end time, verification evidence, and rollback decision.

- [Review backlog recovery](review-backlog-recovery.md)
- [Broker rebuild from outbox](broker-rebuild-from-outbox.md)
- [Provider publication recovery](provider-publication-recovery.md)
