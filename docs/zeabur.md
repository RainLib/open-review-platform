# Zeabur deployment

Deploy this project as **three services**, not one process:

1. PostgreSQL 16 service (Zeabur managed PostgreSQL is preferred).
2. `control-api`, built from `Dockerfile`, exposed on port 8080.
3. `runner`, built from `Dockerfile.runner`, with no public port.

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
GITHUB_TOKEN=<development-only; replace with installation-token resolver>
GITLAB_TOKEN=<development-only; replace with application-token resolver>
```

Create provider webhooks with `https://<control-api-domain>/v1/webhooks/github`
and `https://<control-api-domain>/v1/webhooks/gitlab`. Keep the API behind
Zeabur's HTTPS domain or a custom TLS domain. Before enabling a whole
organization, register the provider installation and test one repository;
unknown installations deliberately return 404 and are never queued.

The present runner uses environment tokens only for initial controlled testing.
For a multi-tenant public SaaS deployment, deploy the planned credential
resolver backed by a KMS/secret manager before granting broad provider access.
