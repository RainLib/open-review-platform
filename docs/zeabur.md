# Zeabur deployment

Deploy this project as **eight services**, not one process:

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
7. `terminal-reporter`, built from `Dockerfile` with entrypoint
   `/app/terminal-reporter`, with no public port.
8. `runner`, built from `Dockerfile.runner`, with no public port.

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
GIT_BINARY=git # Git 2.41+ is required for OCR range reviews
OCR_CONCURRENCY=2 # reduce to 1 for rate-limited or serial model gateways
OCR_REVIEW_EFFORT=low # low | medium | high; low is the latency-oriented starting point
OCR_MAX_PROMPT_TOKENS=8000 # 0 preserves the OCR template default
OCR_MAX_TOKENS_BUDGET=128000 # input + output budget across the whole review
OCR_SUBTASK_TIMEOUT_MINUTES=5 # 0 preserves the OCR CLI default (15 minutes)
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
equivalent provider variables supported by the pinned OCR release. For an
OpenAI-compatible gateway, explicitly set `OCR_USE_ANTHROPIC=false`; otherwise
the CLI can select the Anthropic `/v1/messages` protocol. Do not set model
tokens on `control-api`, `outbox-relay`, `acknowledger`,
`interaction-responder`, or `terminal-reporter`.

`OCR_MAX_TOKENS_BUDGET` is a guardrail, not an output-length cap: OCR checks it
before each LLM round, so a selected group can finish its current round above
the cap. Start with at least the CLI's printed estimate for every selected
group. Use `RISK_REVIEW_MODE=critical` only for incident-style fast paths; it
reviews the strict high-risk scope and, when no strict path exists, at most two
highest-signal paths rather than reporting an empty success.

## Pull-request review commands

An explicit GitHub or GitLab comment starts a separately auditable run only
after Open Review has acknowledged the comment. A normal `@openreview review`
uses `RISK_REVIEW_MODE`; command modes never mutate that deployment setting:

```text
@openreview review --mode=standard  # balanced focused scope
@openreview review --mode=deep      # complete selected source delta
@openreview review --mode=security  # critical high-signal paths first
```

The chosen mode is stored on the review run. `@openreview retry <run-id>`
preserves it, even when an administrator changes the deployment default while
the pull request is open.

For a DeepSeek flash-class OpenAI-compatible route, a concrete starting set is:

```text
OCR_LLM_URL=https://<gateway>/v1/chat/completions
OCR_LLM_TOKEN=<secret>
OCR_LLM_MODEL=deepseek-v4-flash
OCR_USE_ANTHROPIC=false
OCR_REVIEW_EFFORT=low
OCR_MAX_PROMPT_TOKENS=8000
OCR_MAX_TOKENS_BUDGET=128000
OCR_SUBTASK_TIMEOUT_MINUTES=5
OCR_TIMEOUT=15m
```

Run `ocr llm test` inside the runner image before accepting live webhooks. It
must report the intended model and a successful connection; that test verifies
the provider route only, so follow it with one disposable pull request to
verify a complete review and Check Run.

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
