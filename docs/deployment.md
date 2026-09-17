# Deployment and security

## Services

Run `control-api`, `outbox-relay`, `acknowledger`, `interaction-responder`, and
`runner` as separate workloads with distinct service accounts. All need the
private PostgreSQL/RabbitMQ network. Only `control-api` receives public
webhooks; only `runner` needs Git and the OCR executable.

Use managed PostgreSQL or a PostgreSQL 16 StatefulSet with backups, point both
workloads at `CONTROL_DATABASE_URL`, then run `go run ./cmd/migrate` exactly
once for each release. The migration lock prevents concurrent application.

## Required boundaries

1. Terminate TLS at an ingress and expose only `/healthz` and the webhook/API
   endpoints required by the dashboard and Git providers.
2. Store `GITHUB_WEBHOOK_SECRET` and `GITLAB_WEBHOOK_SECRET` in a secret
   manager. The service verifies signatures before JSON parsing or queueing.
3. Use a dedicated Casdoor OIDC application. Configure its issuer and this
   service's audience; production will not start in development-auth mode.
4. Run workers without host mounts, privileged mode, Docker sockets, or
   persistent repository workspaces. Each job gets a new temporary checkout.
5. Use the built-in GitHub App resolver (`credential_ref=github-app`) to mint
   short-lived installation tokens. Never put a token in a webhook, job
   payload, log, review comment, or database column. GitLab remains a
   separately configured provider credential.
6. Set a retention policy for raw webhook payloads and audit logs according to
   the tenant's data-residency and privacy requirements.

## Bootstrap order

1. Create a tenant and a least-privilege GitHub App or GitLab application.
2. Register its external installation ID in `provider_installations` and keep
   its provider credentials in the chosen secret manager.
3. Configure the webhook URL and a unique provider secret.
4. Send a provider test delivery. It must return `202` only for a known active
   installation; repeats return `202` with `duplicate: true` and create no
   second job.
5. Confirm the acknowledger is consuming `review.run.acknowledged`, the runner
   is consuming `review.run.admitted`, and comments are published using an
   installation identity, not a personal access token. The runner's database
   poller is only the recovery path for a lost broker notification.

The GitHub App JWT exchange is implemented. The GitLab OAuth/application-token
broker, dashboard CRUD, and encrypted secret-reference management remain
required before a broad multi-tenant public launch.

For a Cloudflare-fronted installation, use the public ingress layout in
[Cloudflare public ingress](cloudflare.md). Cloudflare terminates HTTPS and
forwards only to the private `control-api`; it is not a replacement for the
stateful database, broker, relay, or runner.
