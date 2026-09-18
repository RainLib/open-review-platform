# Zeabur deployment

Deploy this project as **seven services**, not one process:

1. PostgreSQL 16 service (Zeabur managed PostgreSQL is preferred).
2. RabbitMQ 4.1 with the management image and quorum-queue support; keep it
   private to the project network.
3. `control-api`, built from `Dockerfile`, exposed on port 8080.
4. `outbox-relay`, built from `Dockerfile` with entrypoint
   `/app/outbox-relay`, with no public port.
5. `interaction-responder`, built from `Dockerfile` with entrypoint
   `/app/interaction-responder`, with no public port.
6. `acknowledger`, built from `Dockerfile` with entrypoint `/app/acknowledger`,
   with no public port.
7. `runner`, built from `Dockerfile.runner`, with no public port.

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

Set these on both the runner and interaction-responder only:

```text
OCR_BINARY=ocr
OCR_VERSION=1.12.4
GIT_BINARY=git # Git 2.41+ is required for OCR range reviews
OCR_CONCURRENCY=2 # reduce to 1 for rate-limited or serial model gateways
RISK_REVIEW_MODE=focused # standard | focused | critical
MERGE_GATE_MIN_SEVERITY=critical # off | critical | high | medium | low
CHECKOUT_TIMEOUT=2m # clone/fetch deadline; prevents stalled Git transports
RUNNER_LEASE_DURATION=2m # abandoned work becomes recoverable after this window
RUNNER_LEASE_RENEW_INTERVAL=30s # live workers renew before lease expiry
OCR_TIMEOUT=15m # whole OCR process deadline; prevents stalled model requests
RUNNER_ID=zeabur-runner-1
RUNNER_POLL_INTERVAL=5s
GITHUB_APP_ID=<GitHub App ID>
GITHUB_APP_PRIVATE_KEY_PATH=/run/secrets/github-app.pem
GITLAB_TOKEN=<development-only; replace with application-token resolver>
```

The runner also needs one OpenCodeReview model route. Set either the native OCR
configuration (`OCR_LLM_URL`, `OCR_LLM_TOKEN`, `OCR_LLM_MODEL`) or the
equivalent provider variables supported by the pinned OCR release. Do not set
model tokens on `control-api`, `outbox-relay`, or `acknowledger`.

Create provider webhooks with `https://<control-api-domain>/v1/webhooks/github`
and `https://<control-api-domain>/v1/webhooks/gitlab`. Keep the API behind
Zeabur's HTTPS domain or a custom TLS domain. Before enabling a whole
organization, register the provider installation and test one repository;
unknown installations deliberately return 404 and are never queued.

For GitHub App installations, set `credential_ref=github-app` and mount the
read-only private-key secret to the runner and interaction-responder only. The
resolver mints short-lived installation tokens and does not persist them. For a
public Cloudflare hostname, see [Cloudflare public ingress](cloudflare.md).

Grant the GitHub App **Checks: Read and write** in addition to Contents,
Issues, Pull requests, and required Metadata access. The runner creates the
native `Open Review / Analysis` Check Run directly through the GitHub API; no
GitHub Actions workflow is needed.

## GitHub merge gate

`MERGE_GATE_MIN_SEVERITY` controls the conclusion of `Open Review / Analysis`
after a review has completed: `critical` blocks only critical findings, while
`off` preserves advisory-only behavior. A review execution failure always
reports a failure after retries; findings do not make the durable job fail.

To make the policy enforceable, configure `Open Review / Analysis` as a
required status check in the target branch's GitHub protection rule. The
recommended starting policy mirrors Kodus: begin at `critical`, calibrate
false-positive rates, then optionally move to `high` or `medium` per tenant.

## Local GitHub App verification

The base Compose file deliberately contains no host-secret mount. For an
end-to-end GitHub App test, keep the PEM outside the repository and start the
explicit override:

```sh
GITHUB_APP_PRIVATE_KEY_HOST_PATH=/absolute/path/to/github-app.pem \
  docker compose -f docker-compose.yml -f docker-compose.github-app.yml up --build -d
```

The override mounts that one file read-only at `/run/secrets/github-app.pem`
only for `runner` and `interaction-responder`; it never copies the key into an
image or injects it into `control-api`, `outbox-relay`, or `acknowledger`.
