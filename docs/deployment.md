# Deployment and security

## Services

Run `control-api` and `runner` as separate workloads with distinct service
accounts. Both need PostgreSQL access. Only `control-api` receives public
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
5. Replace development `GITHUB_TOKEN`/`GITLAB_TOKEN` with short-lived
   installation tokens obtained by the provider-credential service. Never put
   a token in a webhook, job payload, log, review comment, or database column.
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
5. Confirm the runner is consuming jobs and that comments are published using
   an installation identity, not a personal access token.

The first code increment contains the schema, verification, and runner seams.
The credential vault, GitHub App JWT exchange, GitLab OAuth/application token
exchange, dashboard CRUD, and encrypted secret reference implementation should
be completed before a public SaaS launch.
