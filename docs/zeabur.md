# Zeabur deployment

Deploy this project as **four services**, not one process:

1. PostgreSQL 16 service (Zeabur managed PostgreSQL is preferred).
2. `control-api`, built from `Dockerfile`, exposed on port 8080.
3. `outbox-relay`, built from `Dockerfile` with entrypoint
   `/app/outbox-relay`, with no public port.
4. `runner`, built from `Dockerfile.runner`, with no public port.

Run `/app/migrate` as a release command or one-off job before rolling either
application workload. Give both workloads the same `CONTROL_DATABASE_URL` from
the private PostgreSQL connection string. Do not expose PostgreSQL publicly.

Set the following encrypted Zeabur environment variables on both relevant
services:

```text
CONTROL_DATABASE_URL=postgres://...
ENVIRONMENT=production
AUTH_MODE=oidc
CASDOOR_ISSUER=https://casdoor.example.com
CASDOOR_AUDIENCE=open-review-platform
GITHUB_WEBHOOK_SECRET=<unique-random-secret>
GITLAB_WEBHOOK_SECRET=<unique-random-secret>
```

Set these on the runner only:

```text
OCR_BINARY=ocr
OCR_VERSION=1.12.4
RUNNER_ID=zeabur-runner-1
RUNNER_POLL_INTERVAL=5s
GITHUB_APP_ID=<GitHub App ID>
GITHUB_APP_PRIVATE_KEY_PATH=/run/secrets/github-app.pem
GITLAB_TOKEN=<development-only; replace with application-token resolver>
```

Create provider webhooks with `https://<control-api-domain>/v1/webhooks/github`
and `https://<control-api-domain>/v1/webhooks/gitlab`. Keep the API behind
Zeabur's HTTPS domain or a custom TLS domain. Before enabling a whole
organization, register the provider installation and test one repository;
unknown installations deliberately return 404 and are never queued.

For GitHub App installations, set `credential_ref=github-app` and mount the
read-only private-key secret to the runner only. The resolver mints short-lived
installation tokens and does not persist them. For a public Cloudflare
hostname, see [Cloudflare public ingress](cloudflare.md).
