# Zeabur deployment

Deploy this project as a **control plane plus workers**, not one process. The
minimum full-feature deployment has these private services:

1. PostgreSQL 16 service (Zeabur managed PostgreSQL is preferred).
2. RabbitMQ 4.1 with the management image and quorum-queue support; keep it
   private to the project network.
3. `console`, built from `apps/web/Dockerfile`, exposed as the public browser
   origin on port 3000.
4. `control-api`, built from `Dockerfile`, exposed only for provider webhooks
   and the Console's private service-to-service API path on port 8080.
5. `outbox-relay`, built from `Dockerfile` with entrypoint
   `/app/outbox-relay`, with no public port.
6. `interaction-responder`, built from `Dockerfile` with entrypoint
   `/app/interaction-responder`, with no public port.
7. `interaction-admitter`, built from `Dockerfile` with entrypoint
   `/app/interaction-admitter`, with no public port. It resolves the current
   pull-request or merge-request revision only after the visible command
   acknowledgement has been accepted by the provider.
8. `acknowledger`, built from `Dockerfile` with entrypoint `/app/acknowledger`,
   with no public port.
9. `terminal-reporter`, built from `Dockerfile` with entrypoint
   `/app/terminal-reporter`, with no public port.
10. `runner`, built from `Dockerfile.runner`, with no public port.
11. `agent-task-source-admitter`, built from
    `Dockerfile.agent-task-source-admitter`, with no public port. It receives
    Jev and provider-read credentials, but no coding-adapter secret or CLI.
12. `agent-task-runner`, built from `Dockerfile.agent-task-runner`, with no
    public port. Its signed adapter connection is configured separately; Jev
    alone does not enable coding.
13. `issue-publisher`, built from `Dockerfile` with entrypoint
    `/app/issue-publisher`, with no public port.
14. `notifier`, `provider-prober`, `sso-prober`, `model-prober`,
    `data-governance-worker`, `review-scheduler`, and `rule-exception-expirer`, each built from
    `Dockerfile` with its matching `/app/<worker>` entrypoint and no public
    port.

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

Set these on the public `console` service. Its `CONTROL_API_URL` must use the
private Zeabur service address, never the public webhook origin:

```text
CONTROL_API_URL=http://control-api:8080
OPEN_REVIEW_APP_URL=https://review.example.com
CASDOOR_ISSUER=https://casdoor.example.com
CASDOOR_CLIENT_ID=open-review-platform
CASDOOR_CLIENT_SECRET=<when-confidential-client>
OPEN_REVIEW_DEPLOYMENT_MODE=self_hosted
OPEN_REVIEW_DEPLOYMENT_REGION=<operator-visible-region-label>
GITHUB_APP_INSTALL_URL=https://github.com/apps/<app-slug>/installations/new
OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET=<unique-32-plus-character-secret>
GITHUB_OAUTH_CLIENT_ID=<GitHub-App-client-id>
GITHUB_OAUTH_CLIENT_SECRET=<GitHub-App-client-secret>
GITHUB_OAUTH_BASE_URL=https://github.com
GITHUB_API_URL=https://api.github.com
OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET=<unique-32-plus-character-secret>
OPEN_REVIEW_GITLAB_INSTALLATION_IDENTITY_SECRET=<stable-32-plus-character-secret>
GITLAB_API_URL=https://gitlab.com/api/v4
GITLAB_OAUTH_CLIENT_ID=<GitLab OAuth application id>
GITLAB_OAUTH_CLIENT_SECRET=<GitLab OAuth application secret>
GITLAB_OAUTH_SCOPES=api read_user
```

Set `OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET` and
`OPEN_REVIEW_GITLAB_INSTALLATION_IDENTITY_SECRET` to the same values on the
private `control-api` service. Set the base64 32-byte
`PROVIDER_CREDENTIAL_ENCRYPTION_KEY` on that service and the provider-call
workers only, never on Console. Copy `GITLAB_OAUTH_CLIENT_ID`,
`GITLAB_OAUTH_CLIENT_SECRET`, and optional `GITLAB_OAUTH_BASE_URL` to those
provider-call workers as well; they are required only to refresh expiring
GitLab OAuth access tokens server-side. If their private network endpoint differs
from the browser-facing GitLab origin, copy the trusted optional
`GITLAB_OAUTH_INTERNAL_BASE_URL` too; browser authorization continues to use the
public base. The control API verifies the signed callback
receipt before recording an installation, so an authenticated browser cannot
bypass the GitHub/GitLab redirect by posting an installation ID directly.

### Control-plane telemetry

The control API exposes `GET /metrics` in Prometheus text format and returns an
opaque `X-Request-ID` on every response. Route labels use registered templates,
not tenant slugs or concrete request paths; payloads, query strings, provider
credentials, and repository source are never exported. Keep `control-api:8080`
and `/metrics` private to the Zeabur service network or a dedicated monitoring
collector. Do not route this endpoint through the public Console or webhook
service.

The endpoint also reads one PostgreSQL-authoritative operational snapshot with
queue/outbox age, acknowledgement SLA breaches, expired leases, stale worker
heartbeats, provider publication failures, terminal-run failures and pending
notification age. These are fleet aggregates with no tenant or repository
labels. Deploy a private Prometheus-compatible collector, enable RabbitMQ's
Prometheus endpoint on port `15692`, and load
`deploy/observability/open-review-alerts.yml`. The matching versioned recovery
procedures live in `docs/runbooks/`.

Zeabur must keep the collector private. Configure its own Alertmanager or
notification integration for paging; this repository does not embed a paging
credential. Before accepting the deployment, verify the `control-api` and
RabbitMQ targets both report `up=1`, all Open Review alert rules report healthy,
and a test alert reaches the deployment-owned receiver. The checked-in rules do
not by themselves prove that last delivery step.

Configure the GitHub App **Setup URL** as
`https://review.example.com/api/setup/github/complete`, and register its exact
**Redirect URI** as
`https://review.example.com/api/setup/github/authorize/complete`. The Console
adds a signed, ten-minute state before it opens the install URL. After GitHub
returns the installation, the Console performs a second server-side GitHub user
authorization and verifies that user can enumerate that installation before it
creates the provider receipt. A stale, direct, or unauthorized callback returns
to setup instead of attaching to a workspace. Keep the client secret in Zeabur
only; it is never sent to the browser or control API.

For GitLab, register the GitLab OAuth application's redirect URI as
`https://review.example.com/api/setup/gitlab/complete`. The Console uses the
deployment-selected GitLab origin, authorization code + PKCE, and a signed
HTTP-only receipt; users never paste a GitLab user ID or token. After setup,
choose exact `group/project` repositories or a real `group/*` scope. The
the callback sends OAuth material only server-to-server to the control plane,
which encrypts it using `PROVIDER_CREDENTIAL_ENCRYPTION_KEY`; provider-call
workers decrypt only the opaque credential reference at the execution boundary
and refresh an expired token with an encrypted compare-and-set write.
`GITLAB_TOKEN` remains an explicit self-hosted fallback. If an administrator
should be able to select it through the Console, set the non-secret
`GITLAB_DEPLOYMENT_TOKEN_CONFIGURED=true` on the **control-api** service only;
keep the actual token on provider-call workers. The resulting browser handoff
contains neither the token nor an endpoint, and provider verification remains
required before review admission.

Set these on the runner only:

```text
OCR_BINARY=ocr
OCR_VERSION=1.12.5
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
```

Set provider credentials only on `runner`, `interaction-responder`,
`interaction-admitter`, `terminal-reporter`, `issue-publisher`, and
`provider-prober`:

```text
GITHUB_APP_ID=<GitHub App ID>
GITHUB_APP_PRIVATE_KEY_PATH=/run/secrets/github-app.pem
PROVIDER_CREDENTIAL_ENCRYPTION_KEY=<same-base64-key-as-control-api>
GITLAB_OAUTH_BASE_URL=<optional-self-managed-public-base>
GITLAB_OAUTH_INTERNAL_BASE_URL=<optional-private-base-for-server-side-exchange>
GITLAB_OAUTH_CLIENT_ID=<same-GitLab-OAuth-client-id-as-console>
GITLAB_OAUTH_CLIENT_SECRET=<same-GitLab-OAuth-client-secret-as-console>
# Optional self-hosted fallback only; OAuth-backed installations do not use it.
GITLAB_TOKEN=<deployment-owned GitLab credential>

# control-api only: true only when the provider-call workers above receive a
# valid GITLAB_TOKEN. This is a capability flag, never the credential itself.
GITLAB_DEPLOYMENT_TOKEN_CONFIGURED=true
```

The runner also needs one OpenCodeReview model route. Set either the native OCR
configuration (`OCR_LLM_URL`, `OCR_LLM_TOKEN`, `OCR_LLM_MODEL`) or the
equivalent provider variables supported by the pinned OCR release. For an
OpenAI-compatible gateway, explicitly set `OCR_USE_ANTHROPIC=false`; otherwise
the CLI can select the Anthropic `/v1/messages` protocol. Do not set model
tokens on `control-api`, `outbox-relay`, `acknowledger`,
`interaction-responder`, `interaction-admitter`, or `terminal-reporter`.

`OCR_MAX_TOKENS_BUDGET` is a guardrail, not an output-length cap: OCR checks it
before each LLM round, so a selected group can finish its current round above
the cap. Start with at least the CLI's printed estimate for every selected
group. Use `RISK_REVIEW_MODE=critical` only for incident-style fast paths; it
reviews the strict high-risk scope and, when no strict path exists, the single
highest-signal path rather than reporting an empty success. This fallback keeps
Flash-class model requests inside an interactive review budget.

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
read-only private-key secret only to `runner`, `interaction-responder`,
`interaction-admitter`, `terminal-reporter`, `issue-publisher`, and
`provider-prober`. The resolver
mints short-lived installation tokens and does not persist them. For a public
Cloudflare hostname, see [Cloudflare public ingress](cloudflare.md).

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
only for the six provider-call workloads: `runner`, `interaction-responder`,
`interaction-admitter`, `terminal-reporter`, `issue-publisher`, and
`provider-prober`. It never copies
the key into an image or injects it into `control-api`, `outbox-relay`, or
`acknowledger`.
