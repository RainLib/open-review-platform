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

The current implementation includes:

- Kratos control API with health and verified GitHub/GitLab webhook routes;
- PostgreSQL schema for tenants, memberships, provider installations, webhook
  deliveries, durable review jobs, findings, and audit events;
- delivery-level idempotency, a transactional outbox, and a consumer inbox;
- revisioned review runs with durable stage events and SSE task updates;
- separate relay, acknowledger, interaction-responder, terminal-reporter, and runner processes;
- an OCR adapter that executes `ocr review --from … --to … --format json` in a
  temporary repository checkout, materializing focused/critical plans as an
  exact selected-file range rather than a long model-side exclusion list; and
- GitHub App installation-token exchange immediately before GitHub reads or
  writes, never in webhook payloads or database rows; and
- a native GitHub `Open Review / Analysis` Check Run lifecycle, separate from
  future enterprise governance merge gates; and
- published enterprise rule versions, repository/branch bindings, immutable
  admission snapshots, and runner-owned OCR `--rule` files; and
- explicit `@openreview` commands that receive an idempotent GitHub response
  and source-comment acknowledgement reaction before their review runs
  asynchronously; and
- a Next.js management console that reads tenant runs, policies, and
  credential-safe installation summaries, and can create installations and
  validated policy drafts through a local-development control-plane bridge.

The browser never receives a provider credential. Production console writes
remain disabled until a Casdoor session bridge is configured. GitLab
OAuth/application-token brokering and a general secret-manager resolver remain
future increments; a deployment-configured GitLab token is supported only for
controlled deployment.

## Architecture

```text
GitHub App / GitLab App
        | verified webhook
        v
control-api -> PostgreSQL -> outbox-relay -> RabbitMQ quorum queues -> interaction-responder -> GitHub reply
     |             |
     |             +--> polling runner -> isolated checkout -> OCR -> provider review
     v
Casdoor OIDC + tenant/RBAC + task/SSE API

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
go run ./cmd/outbox-relay
go run ./cmd/acknowledger
go run ./cmd/interaction-responder
go run ./cmd/terminal-reporter
go run ./cmd/runner
```

`AUTH_MODE=development` is accepted only when `ENVIRONMENT=development`; it
exists solely for local bootstrapping. Production requires a Casdoor OIDC
issuer and audience.

Run the unit suite with `go test ./...`. For the Cloudflare-hosted webhook
layout, see [Cloudflare public ingress](docs/cloudflare.md).
