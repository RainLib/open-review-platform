# Open Review Platform

Open Review Platform is a self-hostable, multi-tenant control plane for AI code
review. It accepts authenticated GitHub and GitLab webhooks, creates an
idempotent review job, runs a pinned [OpenCodeReview](https://github.com/alibaba/open-code-review)
CLI in an isolated checkout, and publishes review findings through provider
adapters.

It intentionally keeps product concerns out of OpenCodeReview itself:

- **OpenCodeReview** remains the deterministic/agent review engine.
- **This repository** owns tenants, Casdoor identity, installations, policy,
  auditability, delivery idempotency, runner lifecycle, and Git-provider writes.
- **Kodus compatibility** is an optional future ingress adapter that emits the
  same normalized review event; it is not coupled to a commercial Kodus build.

## Current foundation

The first implementation includes:

- Kratos control API with health and verified GitHub/GitLab webhook routes;
- PostgreSQL schema for tenants, memberships, provider installations, webhook
  deliveries, durable review jobs, findings, and audit events;
- delivery-level idempotency and transactional enqueueing;
- a separate runner process which claims jobs with `FOR UPDATE SKIP LOCKED`;
- an OCR adapter that executes `ocr review --from … --to … --format json` in a
  temporary repository checkout; and
- provider publisher contracts. GitHub/GitLab installation-token exchange and
  dashboard CRUD are deliberately separate next increments, not credentials
  hard-coded into webhook payloads.

## Architecture

```text
GitHub App / GitLab App
        | verified webhook
        v
control-api (Kratos) -> PostgreSQL event ledger -> review_jobs
                                                   |
                                                   v
                                   runner -> isolated git checkout -> OCR 1.12.4
                                                   |
                                                   v
                                  GitHub PR review / GitLab MR discussion

Dashboard -> Casdoor OIDC -> control-api -> tenancy, roles, policies, audit log
```

See [the deployment and security guide](docs/deployment.md) and
[the provider integration design](docs/providers.md) before connecting a real
organization.

## Local development

Requires Go 1.25+, PostgreSQL 16+, Git, Node.js, and the pinned OCR CLI:

```bash
cp .env.example .env
npm install --global @alibaba-group/open-code-review@1.12.4
go run ./cmd/migrate
go run ./cmd/control-api
go run ./cmd/runner
```

`AUTH_MODE=development` is accepted only when `ENVIRONMENT=development`; it
exists solely for local bootstrapping. Production requires a Casdoor OIDC
issuer and audience.

Run the unit suite with `go test ./...`.
