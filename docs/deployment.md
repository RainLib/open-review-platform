# Deployment and security

## Services

`docker-compose.core.yml` is a deliberately lean local/evaluation override.
It defers optional workers behind profiles and must not replace the complete
deployment described below when notifications, feedback, Agent Work, SSO,
scheduled reviews, rule rollout, model probes, or data-governance jobs are
enabled. The base `docker-compose.yml` remains full-featured by default.
For UI/API iteration only, target `postgres migrate control-api console` with
`docker-compose.dev.yml`; the migrator exits, leaving three long-running
containers and no RabbitMQ/review consumers. This is not a provider E2E mode.
The lean review override still needs separate queue consumers for acknowledgements,
review execution, provider publication, Issue triage, and durable outbox delivery;
container count reflects isolation rather than duplicate Go builds. Switching
Compose modes does not stop already-running workers, so inspect and explicitly
pause them only after confirming no active jobs. Do not use `down -v` for this.
Its Go workloads share one built image but remain separate processes and
security boundaries. The `migrate` service builds that image once; the
Console and OCR runner use their own images. Publish the shared image before
starting dependent services and apply migrations before rolling out workers
that require the new schema.

`docker-compose.compact-review.yml` is an additional **development-only**
override for a resource-constrained local machine. It supervises the nine
essential PR/Issue review executables in one OCR-capable container, reducing
the active review footprint to PostgreSQL, RabbitMQ, API, Console and the
bundle. `scripts/local-stack.sh compact --build` builds only that bundle and
switches off its split counterparts before starting it; the process refuses
to start unless the rendered environment is development with an explicit
compact-mode opt-in. The launcher requires 3 GiB free inside Docker before
RabbitMQ starts and rechecks after any build. Use `compact --stop-unused` to
pause optional project workers after checking for active Agent Work; it does
not delete images, queues or database data. A single child failure
stops/restarts all nine, rather
than leaving an apparently healthy partial pipeline. Do **not** use this
override for production: the combined container shares credentials, failure
domain and scaling capacity across roles. The base split-worker deployment
remains the production security boundary. This compact profile does not run
Agent coding, notifications, feedback polling, scheduled reviews, or other
optional capabilities.

Run `control-api`, `outbox-relay`, `acknowledger`, `interaction-responder`,
`interaction-admitter`, `terminal-reporter`, `issue-publisher`, `notifier`,
and `runner` as separate
workloads with distinct service accounts. The queue consumers need the private
PostgreSQL/RabbitMQ network; only `control-api` receives public webhooks and
only `runner` needs Git and the OCR executable.

Run `review-scheduler`, `rule-exception-expirer`, `provider-prober`,
`provider-feedback-poller`, `model-prober`, `sso-prober`, and
`data-governance-worker` as private database
workers as well. They make durable claims from PostgreSQL rather than consuming
review messages, so they must not be silently omitted from a full-featured
deployment. `rule-exception-expirer` only restores an Issue when its approved
exception expires; it never changes a rule version or contacts a provider.

Run `notifier` on the same private PostgreSQL/RabbitMQ network. It is the only
service that needs outbound access to configured DingTalk, Feishu, or generic
HTTPS webhook endpoints. A notification failure is retried independently and
never changes the review or merge-gate result.

Use managed PostgreSQL or a PostgreSQL 16 StatefulSet with backups, point both
workloads at `CONTROL_DATABASE_URL`, then run `go run ./cmd/migrate` exactly
once for each release. The migration lock prevents concurrent application.

## Agent task adapter

Run `agent-task-runner` on the private PostgreSQL/RabbitMQ network. It may call
only a separately deployed HTTPS adapter through `AGENT_TASK_ADAPTER_URL` (the
disposable development HTTP exception is described below); the
shared `AGENT_TASK_ADAPTER_SECRET` must be at least 32 bytes and never enters
the Console. Set `AGENT_TASK_LEASE_DURATION=2m` and
`AGENT_TASK_REAPER_INTERVAL=15s` unless an adapter profile has a stricter
heartbeat SLO. Missing heartbeats become `needs_attention` and send an
idempotent stop for the exact adapter job; they never retry code execution.
Each repository Agent policy also carries a 1–3 attempt limit and a 1–120
minute execution limit. They are snapshotted into each task at admission;
changing a policy affects only future tasks. The runner caps every renewable
lease at that immutable task deadline and sends the deadline in its signed
adapter handoff. Keep the adapter's own wall-clock timer stricter or equal;
the control plane remains the authoritative stop boundary.
The adapter owns its sandbox and provider write credentials, and may create
only a Draft PR/MR. Open Review never grants it merge authority.

For a self-managed GitLab whose internal API origin differs from the browser
origin, set `AGENT_GITLAB_PUBLIC_BASE_URL` to the deployment-owned public
GitLab base on both `control-api` and `agent-task-adapter`. Set
`AGENT_GITLAB_PUBLIC_FOR_API_BASE_URL` on the adapter to the exact internal
`GITLAB_API_URL`; the control API pairs them from its own provider config.
Other GitLab installations retain their own URLs and are never remapped.
The adapter first
checks the provider response against the admitted repository and exact MR IID;
the control plane repeats that check before persisting the canonical public
link used by Console and the originating Issue comment. Without this setting,
the GitLab API origin is used as before. Production public bases must use
HTTPS; only a disposable development deployment may use loopback HTTP.

GitHub PR links are likewise checked against the exact repository and PR
number. `https://api.github.com` maps to `https://github.com` by default. A
GitHub Enterprise deployment whose API and browser origins differ may set
`AGENT_GITHUB_PUBLIC_BASE_URL` on control API and adapter, plus
`AGENT_GITHUB_PUBLIC_FOR_API_BASE_URL` on the adapter to the exact
`GITHUB_API_URL`. Unpaired or cross-instance links are rejected.

`agent-task-adapter` is an optional Compose profile, not a sidecar of the
control plane. It has no PostgreSQL/RabbitMQ connection and receives no host
workspace mount. Each repository policy selects either `codex` or `claude`; the
choice is frozen into each admitted task. For disposable local Codex acceptance,
`docker-compose.agent-codex.yml` selects the `adapter-codex` build target with
Codex CLI 0.142.1. For Claude, `docker-compose.agent-claude.yml` selects the
`adapter-claude` target with Claude Code 2.1.144; configure its separate
`AGENT_ADAPTER_CLAUDE_MODEL_API_BASE_URL` (Anthropic Messages-compatible HTTPS
base ending in `/v1`), `AGENT_ADAPTER_CLAUDE_MODEL_API_KEY`, and
`AGENT_ADAPTER_CLAUDE_MODEL`. The Claude CLI receives only the short-lived
job capability, not that upstream key, and is admitted only with the per-job
Docker sandbox. The Codex local UID-pool path is development-only. Separate
`agent-sandbox-codex` and `agent-sandbox-claude` targets contain the fixed
CLI but no adapter binary or deployment credentials. Build the selected target and record
its exact local `sha256:` image ID before setting
`AGENT_ADAPTER_SANDBOX_KIND=docker` and `AGENT_ADAPTER_SANDBOX_IMAGE_ID`. The
adapter preflights that image, its named workspace volume, and its internal-only
Docker network at startup and before every task. The child runs with a
read-only root filesystem, leased non-root UID, dropped capabilities, bounded
PID/memory/CPU/runtime, and access only to its workspace and job-scoped model
broker. It receives no Docker socket, state volume, provider token, or model
upstream key. The adapter itself needs the Docker daemon socket: run it only
on a dedicated execution host and never expose that socket to the child or
general review workers. Set a non-empty repository path
allowlist, and provide repository-scoped adapter-only GitHub/GitLab credentials. The adapter
requires a canonical (non-symlinked) workspace-root path, including its parent
directories. After removing only its own orphan workspaces on startup, it
requires at least 1 GiB free on the shared checkout volume before accepting
new coding work; the same check runs before each child. Alert separately on
Docker daemon storage if it is on another filesystem: the workspace-volume
check cannot prevent a Docker overlay from filling up. The adapter rejects a
signed task when its frozen profile does not equal its installed
executor, rather than silently falling back to another CLI. The adapter
checks the runner HMAC, requires the configured exact callback URL, checks out
the frozen SHA onto only `agent/<task-id>`, rejects disallowed paths/secrets and
oversized diffs, pushes without force, then creates only a Draft PR/MR. It
does not run provider repository hooks, install packages, merge, tag, release,
or accept a user-provided shell command.

For a dedicated-host sandbox image build, use
`docker build -f Dockerfile.agent-task-adapter --target agent-sandbox-claude -t open-review-agent-sandbox:reviewed .`
(or select `agent-sandbox-codex` for a Codex policy)
and read its exact ID with
`docker image inspect open-review-agent-sandbox:reviewed --format '{{.Id}}'`.
Set that ID in `AGENT_ADAPTER_SANDBOX_IMAGE_ID`; a mutable tag is rejected.
The Compose `agent-adapter` profile supplies an internal-only network and a
separate checkout volume. Keep `AGENT_TASK_ADAPTER_URL` unset until the adapter
and its scoped credentials are accepted in the target deployment; simply
building the image never enables automatic Issue work.

Adapter subprocess output is bounded: coding CLI stdout/stderr is counted but
not retained and is stopped after 1 MiB; Git command output is capped at 8 MiB
before it can be parsed. `AGENT_ADAPTER_MAX_DIFF_BYTES` cannot exceed that
Git cap; an oversized setting fails adapter startup. These limits stop output
flooding but do not isolate an untrusted coding CLI from the adapter process.

The signed adapter handoff now includes the provider installation ID. For a
single-node test, mount a private regular JSON file into the adapter only,
set `AGENT_ADAPTER_CREDENTIALS_FILE` to its **container path**, and omit
`AGENT_ADAPTER_GITHUB_TOKEN`/`AGENT_ADAPTER_GITLAB_TOKEN`. The file must be
readable only by the root-owned adapter process and have no group/other permissions. The adapter
opens it without following the final symlink, validates permissions and size
on that same open file at startup, then re-reads it for each execution and selects
a credential only when all four
fields match the signed task: `installation_id`, `provider`, `api_base_url`, and
`repository`. Duplicate scopes and a missing match fail closed. For example:

```json
{"version":1,"entries":[{"installation_id":"<verified-installation-uuid>","provider":"gitlab","api_base_url":"https://gitlab.example/api/v4","repository":"team/project","clone_base_url":"https://gitlab.example","token":"<project-access-token>"}]}
```

For the production broker path, a GitLab personal/group token is rejected at
issuance: the broker reads the admitted project and requires the authenticated
user to be that project's documented access-token bot. It also reads the token's
own metadata and requires an active, non-revoked token owned by that bot with
both `api` (MR API writes) and `write_repository` (Git-over-HTTP push) scopes.
If the self-managed GitLab cannot serve the read-only token-self API, issuance
fails closed; passing this check does not prove project role or branch protection.
The development-only
adapter file path shown here does not provide that broker-side check.

The default Compose profile passes the container path but does not mount a
host secret file; supply that private mount in a deployment override. Global
provider tokens remain development-only compatibility inputs. This scoped file
is adapter-only, not a browser or repository setting. The UID-pool mode still
shares PID/network namespaces and must not use production write credentials.
The Docker mode separates each coding job into a new container, but its daemon
socket grants the trusted adapter host-level authority; dedicated-host hardening
and a real provider end-to-end acceptance run remain required before rollout.

The adapter requires `AGENT_ADAPTER_RECEIPT_DIR` (Compose mounts a private
`agent-adapter-state` volume at `/state`). It fsyncs an immutable
attempt-to-job receipt before acknowledging Submit and holds an exclusive file
lock, so only one adapter replica may own that volume. An unstarted reservation
keeps its Job ID across restart; an uncertain or already started job is never
re-executed and is reported as needing attention. Pending terminal callbacks
are replayed under the original delivery ID. This local receipt is not a
cross-node durable queue or a substitute for provider-side publication
reconciliation. Protect and back up the volume as sensitive Issue/plan data;
do not mount it into the coding executor once process isolation is introduced.
In Docker mode, after taking this receipt lock and before listening or replaying
interrupted receipts, startup lists only children labeled with the configured
private workspace volume and removes them. It then removes only the adapter's
`agent-task-<decimal>` temporary checkout directories from that dedicated
volume; unrelated entries are preserved, and a matching symlink fails startup.
A failed list/removal blocks startup.
Give each adapter instance its own receipt and workspace volumes; sharing a
workspace volume across independent adapters could cause one to stop the
other's children. This bounds crash leftovers but does not resume their work.
While the adapter stays up, a one-minute sweep also retries only persisted,
undelivered terminal callbacks. It does not reinterpret active executions as
crashed jobs; the control plane still rejects a callback whose attempt lease
has expired or been cancelled. An explicit HTTP 409 lease rejection is retained
as terminally rejected and stops further delivery attempts; transient network
errors and 5xx responses remain retryable.

The adapter deliberately starts Codex/Claude with a scrubbed environment. It
does **not** forward `AGENT_TASK_ADAPTER_SECRET`, provider tokens, `OPENAI_API_KEY`
or arbitrary container variables to the executor process. The development Codex
profile uses a job-scoped local Responses broker: configure the separate
`AGENT_ADAPTER_CODEX_MODEL_API_BASE_URL`, `AGENT_ADAPTER_CODEX_MODEL_API_KEY`
and `AGENT_ADAPTER_CODEX_MODEL`; the JEV admission key does not authorize coding.
Only a revocable capability reaches the CLI. The opt-in Claude profile uses a
separate job-scoped Anthropic Messages broker with
`AGENT_ADAPTER_CLAUDE_MODEL_API_BASE_URL`,
`AGENT_ADAPTER_CLAUDE_MODEL_API_KEY` and `AGENT_ADAPTER_CLAUDE_MODEL`; it
requires the Claude Docker sandbox. **Environment scrubbing and a leased UID
alone are not a per-job sandbox.** The Docker profile has passed an isolated
Codex/tool fixture, cancellation and orphan-container cleanup. Claude CLI has
also completed a real-model coding run in the Docker sandbox and an isolated
Issue-to-Draft-PR feedback fixture; a write to a live GitHub or GitLab Draft
PR/MR has not yet been accepted. The post-executor Git path now rejects
changed repository configuration/HEAD, disables hooks and redirects, scopes
HTTP authorization to the admitted repository URL, and pushes an exact commit;
these checks do not replace that isolation boundary.

Repository owners can optionally enable **Create candidate from labeled Issues**
in Agent Work and choose an exact label (default `openreview:implement`). A
matching user-authored GitHub/GitLab Issue webhook creates only a JEV-classified
candidate and posts a marker-keyed acknowledgement. It still requires immutable
source capture plus a distinct owner/admin plan approval before the adapter can
receive anything. Do not treat this switch as automatic coding or merging.

Run `agent-task-source-admitter` beside the other provider-read workers. It
uses the dedicated `Dockerfile.agent-task-source-admitter`, which builds and
ships only this binary; the shared control-plane image is not required for
this worker. It
uses the deployment-owned GitHub App/GitLab credential only to resolve the
repository default branch and exact base SHA before any plan is accepted. It
does not receive `AGENT_TASK_ADAPTER_SECRET`, cannot invoke a coding CLI, and
cannot write a branch or PR. If that read fails, the task enters
`needs_attention`; planning and execution remain fail-closed. Provider metadata
429/5xx and transport timeouts first schedule at most two delayed, durable
rereads against the same task revision (honoring bounded `Retry-After`), for
three attempts total. Each attempt rechecks the live Issue and exact base SHA.
GitHub App installation-token exchange uses the same bounded retry for
429/5xx or transport timeouts. App authorization failures, missing
installations, and redirects remain terminal; the signed App JWT is never
forwarded to a redirect target.
401/403/404, redirects, malformed/oversized metadata, changed Issues and an
exhausted retry budget still enter `needs_attention`; none authorizes an Agent
or creates a branch.

## Data governance exports

Set `DATA_GOVERNANCE_ARTIFACT_KEY` to a base64-encoded 32-byte secret in both
`control-api` and `data-governance-worker`. Generate it with
`openssl rand -base64 32`; do not commit it. The worker leases queued exports,
builds a tenant-scoped JSON snapshot, encrypts it with AES-256-GCM, and stores
only ciphertext plus integrity metadata. The authenticated download endpoint
decrypts in memory, emits `Cache-Control: private, no-store`, and audits access.

`DATA_GOVERNANCE_ARTIFACT_TTL` defaults to `24h` and
`DATA_GOVERNANCE_EXPORT_MAX_RECORDS` defaults to `10000` per selected data
class. Built-in deletion performs content erasure for raw webhook payloads,
review finding text/suggestions, and non-governance audit content after dual
approval and the 24-hour cancellation window. It preserves structural keys,
fingerprints, timestamps, `data.*` governance evidence, and an erasure
tombstone. Usage ledger and external operational-log deletion fail closed.
Region migration uses the optional deployment-owned orchestrator configured by
`DATA_GOVERNANCE_REGION_ORCHESTRATOR_URL` and
`DATA_GOVERNANCE_REGION_ORCHESTRATOR_SECRET`. Commands and responses are
HMAC-SHA256 signed. The orchestrator must make `job_id` idempotent: it returns
`accepted`/`running` with a stable operation ID while work continues, then
`completed` with an observed timestamp and a primary region exactly matching
the requested destination. Open Review polls durably and does not change the
residency declaration before that evidence is verified. Without this adapter,
region migration fails closed.

## Durable worker health evidence

`runner`, `interaction-responder`, `interaction-admitter`, `notifier`,
`sso-prober`, `provider-prober`, `provider-feedback-poller`, `model-prober`, and `data-governance-worker` write a
durable heartbeat every 15 seconds. The registry records process kind,
immutable deployment version, declared capacity, current occupancy, startup
time, heartbeat time, and expiry. Set `OPEN_REVIEW_BUILD_VERSION` to an image
digest, Git SHA, or release identifier; the default `development` value is only
suitable for local Compose.

Use stable replica-local identities through `RUNNER_ID`,
`INTERACTION_RESPONDER_WORKER_ID`, `INTERACTION_ADMITTER_WORKER_ID`,
`NOTIFIER_WORKER_ID`, `SSO_PROBER_WORKER_ID`, `PROVIDER_PROBER_WORKER_ID`,
`PROVIDER_FEEDBACK_POLLER_WORKER_ID`, `MODEL_PROBER_WORKER_ID`, and
`DATA_GOVERNANCE_WORKER_ID`. IDs must be unique per
live replica. The tenant console never returns these raw identities: it emits
tenant-stable aliases and combines them only with that tenant's execution
leases. Heartbeat evidence is read-only and does not grant restart, drain, or
shell execution authority.

`provider-prober` performs read-only access checks every five minutes. GitHub
uses the installation token to read one page of installation repositories;
GitLab reads the authenticated identity endpoint. Both capture rate-limit
headers when the provider supplies them. These checks prove token validity and
the named read capability only. They deliberately do not create a comment,
check, review, or merge mutation, so write permissions remain evidenced by
publication receipts. Private self-managed endpoints require
`PROVIDER_PROBE_ALLOW_PRIVATE_NETWORKS=true`; plaintext HTTP additionally
requires the development-only `PROVIDER_PROBE_ALLOW_HTTP=true` opt-in.

GitHub Apps do not expose a Reaction webhook event. The private
`provider-feedback-poller` therefore leases completed, reaction-enabled Issue
analyses for at most 30 days, locates the stable marker comment, and reads its
thumbs reactions with an installation token. The complete snapshot is
reconciled idempotently: new/re-added reactions become feedback, missing
reactions are retracted, and no reaction ever queues model work. The default
cadence is five minutes (`PROVIDER_FEEDBACK_POLL_INTERVAL`); worker credentials
must never be placed in the Console.

New provider installations begin in `pending` state. The console queues a
durable verification request, and `provider-prober` transitions it through
`checking` to `verified` or `failed`. Until it is verified, webhook admission,
comment commands, CLI review admission, and the remaining workspace setup
steps are denied. A tenant owner or administrator can request another probe
from the connection setup screen; the API only queues work and never resolves
or returns a provider credential. Upgrade-era installations retain explicit
`legacy` eligibility rather than being retroactively represented as a fresh
probe result.

`model-prober` runs only a workspace administrator's explicit Models & BYOK
connectivity request. The request is fixed to `Reply exactly OK.`, carries no
repository content, and has a four-completion-token limit. The durable receipt
records the route revision, endpoint host, latency, a response SHA-256, and a
safe failure code—not the provider response, credential value, or credential
reference. It rejects private, loopback, link-local, and unspecified resolved
addresses by default. For an intentionally reachable self-managed gateway, set
`MODEL_PROBE_ALLOW_PRIVATE_NETWORKS=true`; doing so should be accompanied by
an egress policy that limits the worker to the expected model network. The
built-in worker resolves only `env://OPEN_REVIEW_MODEL_SECRET_*`; a
`secret://` reference needs a deployment-specific worker resolver and will
otherwise finish as `credential_unavailable`.

## Operator monitoring and alerts

The private `GET /metrics` endpoint combines bounded-cardinality HTTP series
with one PostgreSQL-authoritative workflow snapshot. It includes queue and
outbox age, acknowledgement SLA breaches, expired review leases, stale worker
heartbeats, provider-publication failures, terminal-run failures and pending
notification age. It intentionally has no tenant, repository, provider
identity, credential, source, payload or query-string labels. Keep the endpoint
on the operator network; it is not a tenant API and must not be routed through
the public Console or webhook ingress.

For local Compose, start the optional collector with:

```sh
docker compose --profile observability up -d prometheus
```

Prometheus is bound to `127.0.0.1:9090`, scrapes `control-api:8080` and the
RabbitMQ Prometheus plugin on `rabbitmq:15692`, and loads the rules in
`deploy/observability/open-review-alerts.yml`. Production deployments should
mount the same rules into their managed collector and attach their own
Alertmanager/notification routing. The repository deliberately does not ship a
default paging destination or credential.

Before a release, run `./scripts/verify-observability.sh`, then verify both
scrape targets report `up=1`. The script validates Compose and runs `promtool`
against the checked-in config and rules; it does not require GitHub Actions.
Every alert points to a versioned runbook in `docs/runbooks/`. A successful
local scrape proves metric and rule integration only; it does not prove
production notification delivery, a sustained-load SLO, or a recovery drill in
the production network topology.

## Required boundaries

1. Terminate TLS at an ingress and expose only `/healthz` and the webhook/API
   endpoints required by the dashboard and Git providers.
2. Store `GITHUB_WEBHOOK_SECRET` and `GITLAB_WEBHOOK_SECRET` in a secret
   manager. Before JSON parsing or queueing, the service verifies GitHub's
   payload signature or compares GitLab's `X-Gitlab-Token` shared-secret header
   in constant time. GitLab's header check is not a payload signature; require
   HTTPS with certificate verification in production.
3. Use a dedicated Casdoor OIDC application. Configure its issuer and this
   service's audience; production will not start in development-auth mode.
4. Run general review workers without host mounts, privileged mode, Docker
   sockets, or persistent repository workspaces. The optional Agent adapter
   is a separate dedicated-host workload; only it may access the daemon
   socket, while every coding job runs in a new restricted container.
5. Use the built-in GitHub App resolver (`credential_ref=github-app`) to mint
   short-lived installation tokens. Never put plaintext provider tokens in a
   webhook, job payload, log, review comment, or database column. GitLab OAuth
   material is persisted only in encrypted credential records; other records
   and messages retain opaque references. A separately configured GitLab
   deployment token remains an explicit fallback.
6. Set `GITHUB_API_URL` and `GITLAB_API_URL` to the HTTPS API endpoint for
   each configured provider instance. The control API derives each
   installation's stored endpoint from these deployment-owned values; a
   browser or management API caller cannot redirect runner credentials to an
   arbitrary host. Workspace administrators can inspect the selected cloud or
   self-managed profile through the tenant-scoped provider-profile endpoint,
   but it never returns credentials or enables choosing another URL.
7. Set a retention policy for raw webhook payloads and audit logs according to
   the tenant's data-residency and privacy requirements.
8. Keep notification webhook URLs in the deployment secret manager. The
   control plane stores only an `env:NAME` reference, never the URL or signing
   secret. Give each destination its own secret so it can be rotated or revoked
   without affecting another repository or team. The base Compose profile
   exposes six notifier-only slots: `OPENREVIEW_NOTIFY_PLATFORM`,
   `OPENREVIEW_NOTIFY_ENGINEERING`, `OPENREVIEW_NOTIFY_SECURITY`,
   `OPENREVIEW_NOTIFY_INCIDENTS`, `OPENREVIEW_NOTIFY_RELEASES`, and
   `OPENREVIEW_NOTIFY_PRODUCT`. Add any custom slot explicitly under
   `notifier.environment`; saving an `env:NAME` reference does not make that
   process environment variable exist.
9. Keep Compose/service environment allowlists explicit. A notification
   destination secret belongs only on `notifier`; a model-route secret belongs
   only on `runner` and `model-prober`; a provider credential belongs only on
   `runner`, `interaction-responder`, `interaction-admitter`,
   `terminal-reporter`, `issue-publisher`, and `provider-prober`. Do not attach
   a shared `.env` file to every worker.

## Bootstrap order

1. Create a tenant and a least-privilege GitHub App or GitLab application.
2. Register its external installation ID in `provider_installations`. GitHub
   App installations use `credential_ref=github-app`; mount the App private
   key only in the provider-call workloads: `runner`,
   `interaction-responder`, `interaction-admitter`, `terminal-reporter`,
   `issue-publisher`, `issue-triager`, `provider-prober`, and
   `provider-feedback-poller`. For local Compose, use both
   `docker-compose.yml` and `docker-compose.github-app.yml`; the override maps
   `GITHUB_APP_PRIVATE_KEY_HOST_PATH` to the container-only
   `/run/secrets/github-app.pem` path. GitLab OAuth installations use their
   encrypted credential reference with worker-side refresh. A separately
   configured deployment token (`credential_ref=gitlab-token`) is the explicit
   self-hosted fallback, not a requirement for the OAuth path; mount it only
   in that same provider-call set when the fallback is enabled.
3. Configure the webhook URL and a unique provider secret.
4. Send a provider test delivery. It must return `202` only for a known active
   installation; repeats return `202` with `duplicate: true` and create no
   second job.
5. Confirm the interaction responder publishes the provider-visible command
   acknowledgement before `interaction-admitter` consumes
   `review.interaction.admission`; then confirm the acknowledger is consuming
   `review.run.acknowledged`, the runner is consuming `review.run.admitted`, the terminal-reporter is consuming
   terminal run events, and comments are published using an
   installation identity, not a personal access token. The runner's database
   poller is only the recovery path for a lost broker notification.
6. Confirm `provider-prober` has verified the installation before accepting a
   live provider delivery. Then confirm `notifier` consumes
   `openreview.notification.v1`. Create a
   destination and at least one route in the console, then verify one matching
   terminal event is recorded once in `notification_deliveries`.

Focused and critical review modes materialize a temporary commit rooted at the
trusted base SHA with only risk-selected paths applied. The OCR process reviews
that exact range, while the original checkout stays read-only. A deterministic
model-context exhaustion is terminal rather than retried with the same input;
operators must narrow the scope or select a model with a larger context.
When every changed file is intentionally deferred, the runner publishes an
empty scoped result without starting OCR.

Set `OCR_CONCURRENCY`, `OCR_REVIEW_EFFORT`, `OCR_MAX_PROMPT_TOKENS`,
`OCR_MAX_TOKENS_BUDGET`, and `OCR_SUBTASK_TIMEOUT_MINUTES` explicitly from the
release's `.env.example`. Their bounded recommended values are also runtime
defaults so an older environment file cannot silently remove model limits.
The effective platform process deadline is the shorter of
`OCR_TIMEOUT` and `OCR_SUBTASK_TIMEOUT_MINUTES`; this is enforced outside the
OCR CLI so a stuck wrapper cannot keep consuming model capacity.

Workspace/repository model routes configured in the Console are versioned and
snapshotted at review admission. The database stores only a `credential_ref`.
The Console uses the operating system's built-in sans and monospace font stacks;
it does not download a font from Google or another CDN at runtime. This keeps
air-gapped and restricted-network self-hosted deployments renderable.
For self-hosted environment-backed BYOK, mount the token on the runner as a
variable beginning with `OPEN_REVIEW_MODEL_SECRET_` and reference it as
`env://OPEN_REVIEW_MODEL_SECRET_<NAME>`. The runner resolves it immediately
before launching OCR and exposes it only to that child process. References
using `secret://` require a deployment-specific external secret resolver; the
built-in self-hosted resolver rejects them rather than reading another process
environment value.

Saving a route does not call a model. Operators can request a separate,
asynchronous connectivity probe from Models & BYOK after acknowledging the
small billable request; the API itself requires that acknowledgment field and
does not rely only on the browser checkbox. The probe snapshots the exact route revision under a
lease, so a later settings change cannot redirect a queued test. It is health
evidence only: it does not prove review quality, access to repository source,
or production cost behavior.

The GitHub App JWT exchange is implemented. The web console uses a Casdoor
Authorization Code + PKCE browser session bridge: its server-side BFF forwards
the short-lived OIDC ID token to the control plane, which verifies the token
and applies tenant RBAC. Configure `OPEN_REVIEW_APP_URL`, `CASDOOR_ISSUER`,
`CASDOOR_CLIENT_ID`, and (when applicable) `CASDOOR_CLIENT_SECRET` on the web
service, and register `https://review.example.com/api/auth/callback` with
Casdoor. It returns installation summaries without credential references.
Before asking a user to complete sign-in, run
`node scripts/verify-oidc-preflight.mjs https://review.example.com` against
the public Console origin. This credential-free check verifies the configured
callback origin, scoped Secure/HttpOnly/SameSite attempt cookie and both
matching/missing-cookie callback branches. It intentionally sends no
authorization code and cannot prove an authenticated session; a human OIDC
login and workspace read remain separate acceptance checks.

CLI Quickstart uses `OPEN_REVIEW_PUBLIC_API_URL` for its copyable command,
falling back to `OPEN_REVIEW_APP_URL` when the same public origin routes
`/v1/tenants/*/cli-reviews`. Configure the former on the Console when the
API has a separate public HTTPS origin. Never use the private Docker
`CONTROL_API_URL` or an example domain: the UI will withhold the command
when it cannot resolve a safe public origin. The API key is supplied by the
caller's secret manager as `OPEN_REVIEW_API_KEY`, not embedded in the page.
For an offline installation, the HTTPS origin may resolve only inside the
organization's network; it still must be reachable from the CLI user's host.

For GitHub onboarding, configure `GITHUB_APP_INSTALL_URL`, a separate 32+
character `OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET` (or shared
`OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET`), `GITHUB_OAUTH_CLIENT_ID`, and
`GITHUB_OAUTH_CLIENT_SECRET`. Set the GitHub App setup URL to
`https://review.example.com/api/setup/github/complete` and its exact OAuth
redirect URI to `https://review.example.com/api/setup/github/authorize/complete`.
Configure the receipt secret on the private `control-api`; the Console BFF
forwards the opaque receipt only server-to-server, and the control plane rejects
direct installation requests without a matching, unexpired receipt. The browser
signs a ten-minute state/tenant/next intent before it opens GitHub. The Setup
URL callback then starts a second server-side user authorization: the Console
exchanges its code without exposing it to browser code and confirms that the
authorized GitHub user can enumerate the returned installation before it creates
the short-lived HTTP-only receipt. A direct, stale, mismatched, or unauthorized
callback returns to setup instead of attaching to any workspace. The worker then
performs deployment-owned read-only verification of the exact declared repository
scope before webhook, CLI, or review admission.

For GitLab.com or self-managed GitLab, register
`https://review.example.com/api/setup/gitlab/complete` with the configured
GitLab OAuth application. Set `GITLAB_OAUTH_CLIENT_ID`,
`GITLAB_OAUTH_CLIENT_SECRET`, and `OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET` on
the web service. Set the OAuth client ID, secret, and optional
`GITLAB_OAUTH_BASE_URL` on every provider-calling worker too, so a short-lived
access token can be refreshed without involving a browser. The base may include
a relative URL prefix such as `/gitlab`.
If the browser-facing GitLab origin differs from the private network path used
by the Console or workers, set `GITLAB_OAUTH_INTERNAL_BASE_URL` to the latter.
Authorization redirects always retain `GITLAB_OAUTH_BASE_URL`; only the
server-side token exchange, identity lookup, and refresh use the internal URL.
Both origins must be trusted HTTPS endpoints except in the explicit local
development profile below.
Set the stable 32+ character `OPEN_REVIEW_GITLAB_INSTALLATION_IDENTITY_SECRET`
before rotating `OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET`; it derives an opaque
workspace-and-scope key, so a later authorization reuses the same durable
installation record while non-overlapping scopes retain separate records.
Configure that same stable identity key on the private `control-api`, which
recomputes the scope key rather than accepting a browser-provided external ID. The
authorization-state secret remains the compatibility fallback when this
optional key is absent.

For a disposable, local self-managed GitLab acceptance environment, the
repository provides `deploy/docker-compose.gitlab-local.yml`. It is intentionally
development-only: GitLab is joined to the Open Review Docker network as
`gitlab`, `GITLAB_API_URL` is `http://gitlab:8929/api/v4`, and
`GITLAB_ALLOW_HTTP=true` is required together with `ENVIRONMENT=development`.
The same explicit pair is passed to Console, so the GitLab OAuth handoff is
available only in that disposable local mode; all other Console deployments
continue to reject HTTP OAuth endpoints.
The control plane rejects that flag outside development; production and shared
self-managed installations continue to require HTTPS. The local Compose file
does not use provider credentials from this repository and must never be used
with production tokens or data.

The optional Agent adapter has separate, fail-closed local exceptions. For a
disposable CE Agent-task test, set `ENVIRONMENT=development`,
`AGENT_TASK_ADAPTER_URL=http://agent-task-adapter:8090`,
`AGENT_TASK_CALLBACK_URL=http://control-api:8080/v1/agent-adapter/events`,
`AGENT_TASK_ADAPTER_ALLOW_HTTP=true`,
`AGENT_ADAPTER_GITLAB_CLONE_BASE_URL=http://gitlab:8929`, and
`AGENT_ADAPTER_GITLAB_ALLOW_HTTP=true`. The runner and adapter both reject the
HTTP flags outside development, and the adapter only sends its GitLab clone
credential to the exact origin/path matching the admitted API base. Supply a
browser-reachable `AGENT_GITLAB_PUBLIC_BASE_URL=http://127.0.0.1:8929` when
the internal GitLab API hostname is `gitlab`; the local acceptance overlay
sets this value on both adapter and control API and binds it to the exact
`http://gitlab:8929/api/v4` API base. Supply a
fresh 32+ character `AGENT_TASK_ADAPTER_SECRET`, a disposable repository-scoped
GitLab write token **only to the adapter**, a reviewed fixed executor image and
`AGENT_ADAPTER_ALLOWED_PATHS`; without these, the Agent execution remains
disabled. Do not use this HTTP profile with production data or tokens.

### Disposable local GitLab acceptance

This procedure proves the integration against GitLab CE; it is not a
production installation recipe. Start GitLab after the main Open Review stack:

```sh
GITLAB_LOCAL_ROOT_PASSWORD='local-only-password' \
  docker compose -f deploy/docker-compose.gitlab-local.yml up -d
```

Keep the generated root password and any GitLab access token in the local
secret store, never in Compose files or shell history. The instance is exposed
only as `127.0.0.1:8929`, and provider workers reach it on the private Docker
network as `http://gitlab:8929/api/v4`.

For a current-source acceptance run, use the architecture-aware builder and
apply the non-production overlay with `deploy/docker-compose.gitlab-e2e.yml`:

```sh
export GITLAB_E2E_BIN_DIR="$(mktemp -d)"
./scripts/build-gitlab-e2e-binaries.sh
docker compose \
  -f docker-compose.yml -f deploy/docker-compose.gitlab-e2e.yml \
  up -d --force-recreate
```

If the same local stack has a GitHub App configured, include its key-mount
override as well:

```sh
docker compose \
  -f docker-compose.yml -f docker-compose.github-app.yml \
  -f deploy/docker-compose.gitlab-e2e.yml \
  up -d --force-recreate
```

The script derives the Docker server architecture and emits static
`linux/amd64` or `linux/arm64` binaries for the review workers and all three
Agent services (source-admitter, runner, optional adapter), using the pinned
`golang:1.25.14-alpine` compiler image rather than the host compiler. The
overlay also points Agent source reads and OAuth refreshes at the disposable
CE instance; without it, a local GitLab Issue could be resolved against the
GitLab.com default. The adapter remains profile-gated, and the runner still
requires an explicit `AGENT_TASK_ADAPTER_URL` before it can hand off any work.
Do not bind-mount a macOS or mismatched-architecture binary into the Linux Runner:
emulation makes the acceptance result unreliable. The overlay is intentionally
the only place that enables the provider probe's HTTP/private-network
exemptions and the Console local preview at `http://127.0.0.1:3110`; provider
status and report links must use that browser-reachable address, not a Docker
bridge or an arbitrary development port. The GitHub-App override keeps the
existing key mount valid for shared provider workers. For a GitLab-only local
environment, omit the GitHub App override and variables entirely rather than
leaving a stale host key path in the base Compose configuration. Do not carry
these settings into a shared or production Compose profile.

For local GitLab OAuth, register the callback
`http://127.0.0.1:3110/api/setup/gitlab/complete` in the disposable GitLab
application, then provide only its client ID and secret to the Compose process.
The overlay fixes the public authorization origin at `127.0.0.1:8929` and uses
the private `http://gitlab:8929` endpoint for Console/workers' token exchange;
override either with `GITLAB_E2E_OAUTH_PUBLIC_BASE_URL` or
`GITLAB_E2E_OAUTH_WORKER_BASE_URL` only when the local topology differs.
The overlay also persists `http://gitlab:8929/api/v4` as `GITLAB_API_URL` for
new installations; use `GITLAB_E2E_API_URL` only when the private API path is
different. Without that explicit API base, an otherwise valid local OAuth
credential would be probed against the production GitLab.com default.
The short-lived local-preview cookies are intentionally non-`Secure` solely in
this `ENVIRONMENT=development` + `OPEN_REVIEW_LOCAL_PREVIEW=true` profile, so
the HTTP callback can retain state. All non-preview deployments retain Secure
HTTP-only cookies.

Create a GitLab project webhook for the exact selected repository. In Docker
Desktop local acceptance its callback is
`http://host.docker.internal:8080/v1/webhooks/gitlab`; enable GitLab's
local-webhook allowance only on this disposable instance. Subscribe to merge
request, note, issue, and emoji events. A production self-managed GitLab must
instead send HTTPS webhooks to the public control-plane ingress with
certificate verification enabled and the configured `X-Gitlab-Token`
shared-secret header. The receiver verifies that header, not a payload HMAC.

The minimum acceptance evidence is: provider scope verification; a merge
request that produces a check and evidence report; an Issue Hook that produces
structured triage; and an Emoji Hook that records feedback. Test a command as
the GitLab identity that is linked to the workspace owner or member: a
deployment token proves provider access, but it cannot by itself establish a
human command identity.

GitLab CE `17.11` emits `event_type=award` and `event_type=revoke` for emoji
webhooks while leaving `object_attributes.action` empty. Open Review maps both
event types explicitly: a thumbs-up/down creates feedback and revoking that
same award retracts the existing record. Keep the **Emoji events** subscription
enabled; a `204` response means the award was unrelated to an Open Review
marker, whereas an accepted create or revoke returns `202` and writes an audit
event without scheduling another model run.

### Self-managed GitLab versus a disconnected deployment

Running GitLab on the private Docker network proves the self-managed provider
path. It does not by itself prove that Open Review can run without internet
access. The current local acceptance stack still uses its configured external
OpenAI-compatible model endpoint. OAuth-only means no static GitLab token;
it does not mean there is no external model dependency.

A disconnected installation also needs an internal model endpoint (and its
admitted Models/BYOK route), reachable internal OIDC issuer, mirrored pinned
container images and OCR dependencies, and trusted internal TLS certificates.
Both model analysis and the connectivity probe must reach that model service;
enable `MODEL_PROBE_ALLOW_PRIVATE_NETWORKS` only for the intended private
gateway and retain the network allowlist. Notification destinations and any
optional external services must also be reachable inside that network or
disabled. Build/package retrieval happens before disconnection.

Acceptance of that topology requires an outbound-denied run that completes
login, GitLab OAuth/refresh, repository checkout, real model inference,
comments, Issue triage and the blocked-to-fixed MR gate. No such disconnected
run is claimed by the local GitLab evidence below.

### GitLab CE merge enforcement

Open Review writes `Open Review / Analysis` as a GitLab external commit status.
GitLab CE places that status in an `external` pipeline for the reviewed SHA, so
the project can block a merge without a GitHub Action or a second CI bridge:

1. In **Settings → Merge requests → Merge checks**, enable **Pipelines must
   succeed** for every repository that should be gated.
2. Ensure every merge request produces a pipeline for its source SHA. GitLab
   creates one automatically when Open Review publishes its external status.
3. Keep the Open Review status non-optional (`allow_failure=false`); a
   `failed` review gate then makes GitLab report `ci_must_pass`, while a
   successful terminal status restores `mergeable`.

The disposable CE acceptance project verifies this exact behavior: the same
external pipeline moved from success to a controlled failure (`ci_must_pass`)
and back to success (`mergeable`). GitLab Ultimate **external status checks**
are an additional project feature, not a prerequisite for the CE pipeline
gate. Protect the target branch and limit who can change merge settings; an
administrator can otherwise disable any project-side gate.

The server uses authorization code + PKCE and verifies the `/api/v4/user`
identity. Configure a shared base64 32-byte `PROVIDER_CREDENTIAL_ENCRYPTION_KEY`
on the private control API and every provider-calling worker: the control plane
encrypts the OAuth material before persistence, while the signed setup receipt,
browser, audit rows, and broker messages retain only an opaque credential
reference. Refresh writes use a compare-and-set encrypted value: concurrent
workers adopt the winning refresh rather than overwrite a rotated refresh token.
Keep this key durable across every restart and inject the same value into all
provider-calling workers, including Issue triage and publication. A verified
OAuth installation does not prove a newly started worker can open its
ciphertext. If the key is absent, provider acknowledgements can remain queued
and delivery can reach the DLQ; inspect the tenant's Provider Issue triage
health queue and durable inbox error before retrying. Restore the original
key to recover existing ciphertext. Do **not** substitute a new key or
silently fall back to a deployment token for an OAuth installation. If the
original key is lost, explicitly re-authorize GitLab under a new key and
verify the new credential before replaying the failed operation. An expired
OAuth access token also requires the same application's
`GITLAB_OAUTH_CLIENT_ID` and `GITLAB_OAUTH_CLIENT_SECRET` on the refreshing
workers; restoring only the encryption key is not a refresh acceptance test.
After the original key and refresh credentials are restored, the Provider Issue
triage inspector offers **Retry retained snapshot** when the current Issue
acknowledgement or analysis delivery has exhausted the queue's five
redeliveries. A normal queued job or a still-retrying delivery cannot be
manually restarted. The retry creates a new fenced attempt from the same Issue,
model, prompt, and format snapshots; it republishes the marker-keyed
acknowledgement before analysis. Check the provider comment and job state after
retrying. Do not replay a shared RabbitMQ dead-letter message directly or
replace the OAuth credential with a deployment token.
GitLab merge-request and note webhooks then route by the selected
exact repository list or `group/*` scope. Overlapping active GitLab scopes fail
closed. The browser never accepts a GitLab identity or token. `GITLAB_TOKEN`
remains only the explicit self-hosted fallback. To enable the Console's **Use
deployment token** handoff, set `GITLAB_DEPLOYMENT_TOKEN_CONFIGURED=true` only
on control-api after mounting the actual `GITLAB_TOKEN` on every provider-call
worker. The control plane receives just that non-secret capability flag; it
does not receive the token. The Console obtains a short-lived signed receipt
after an owner/admin chooses the slot, and a worker probe still fails closed if
the configured token cannot read the selected scope.

For a Cloudflare-fronted installation, use the public ingress layout in
[Cloudflare public ingress](cloudflare.md). Cloudflare terminates HTTPS and
forwards only to the private `control-api`; it is not a replacement for the
stateful database, broker, relay, or runner.
