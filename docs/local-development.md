# Local development and CLI

Detailed local Compose footprints, worker commands, and CLI examples. Run all commands from the repository root. For a product overview, return to [README](../README.md).

## Local development

Requires Go 1.25+, PostgreSQL 16+, Git, Node.js, and the pinned OCR CLI:

```bash
cp .env.example .env
npm install --global @alibaba-group/open-code-review@1.12.5
go run ./cmd/migrate
go run ./cmd/control-api
go run ./cmd/outbox-relay
go run ./cmd/acknowledger
go run ./cmd/interaction-responder
go run ./cmd/interaction-admitter
go run ./cmd/terminal-reporter
go run ./cmd/issue-publisher
go run ./cmd/notifier
go run ./cmd/sso-prober
go run ./cmd/provider-prober
go run ./cmd/provider-feedback-poller
go run ./cmd/model-prober
go run ./cmd/data-governance-worker
go run ./cmd/review-scheduler
go run ./cmd/rule-exception-expirer
go run ./cmd/runner
```

`AUTH_MODE=development` is accepted only when `ENVIRONMENT=development`; it
exists solely for local bootstrapping. Production requires a Casdoor OIDC
issuer and audience.

Choose the Compose mode for the task instead of starting the complete stack.
For Console and API iteration, the targeted development command starts only
PostgreSQL, the API, and Console. Use `auth` for production-shaped OAuth and
callback checks; `ui` enables local hot reload and must not be exposed through
the public tunnel. Neither mode processes real webhooks or reviews.

The local helper makes the three footprints explicit. It never builds or pulls
an image implicitly; add `--build` on first use or when image inputs change.
Ordinary Console page edits in `ui` mode hot-reload:

```bash
./scripts/local-stack.sh ui
./scripts/local-stack.sh auth
./scripts/local-stack.sh setup --github-app
./scripts/local-stack.sh review --github-app
./scripts/local-stack.sh compact --github-app
./scripts/local-stack.sh compact --github-app --stop-unused
```

`ui` and `auth` each start three long-lived containers plus the one-shot
migrator. `setup` adds only `provider-prober` as a fourth long-lived container:
the newly authorized GitHub App or GitLab connection can complete its
read-only provider check and repository inventory without RabbitMQ. Build the
small worker image once with `./scripts/local-stack.sh setup --github-app --build`,
then omit `--build`; omit `--github-app` for GitLab-only setup. The worker's
provider key/token stays in its own deployment boundary, not the browser or
Control API. `setup` does **not** process webhooks, PR/Issue reviews, or Agent
Work. `auth` uses the immutable Console image, so add `--build` only after
its source or dependencies change. `review`
uses the lean review overlay below; omit `--github-app` for a GitLab-only
installation. Switching between `compact` and `review` stops the counterpart
review consumers first; `ui` and `auth` leave existing workers running by
default. For a deliberate
switch to the three-container UI/OAuth footprint,
first inspect `docker compose ps` and the work queue, then run
`./scripts/local-stack.sh auth --stop-unused` (or `ui --stop-unused`). This
pauses only this Compose project's other running services without removing
containers, images, volumes, or queued messages. New PR/Issue jobs cannot
progress until `review --github-app` or `compact --github-app` is started again; do not use this switch
while the public installation must keep processing reviews.
Use `setup --github-app --stop-unused` instead when onboarding must complete
but the review queue may be paused after the pending-work guard. A running
`compact` bundle already includes the provider probe; `setup` refuses to run
another copy alongside it. The Control API still runs bounded local cleanup of abandoned GitLab OAuth
credentials in this mode; it needs neither provider-prober nor RabbitMQ for
that maintenance step and does not revoke grants at GitLab itself.
The launcher refuses `--stop-unused` while runnable PR/Issue jobs or active
Agent Work source/execution jobs exist. Switching to `ui` or `auth` also
refuses to pause a queued/running provider verification. If PostgreSQL is
stopped but other project services are running, it fails closed because it
cannot check pending work. `--allow-paused-work` is an explicit override when
pausing them is intentional. Nonterminal PR/Issue jobs whose
installation is inactive or unverified are retained but cannot be claimed by
workers, so they do not force the full stack for a UI/OAuth session. Check and
repair those connections before resuming review mode. `auth` and `review`
preserve existing validated container images unless `--build` is explicit;
`ui` switches to a hot-reload image and can recreate Console. Neither flag is
needed for a fresh three-container UI/OAuth startup. The OAuth `error=state`
callback is unrelated to the number of review worker containers.

When Docker has too little free space for a full Console build, a host-side
`pnpm run build` can be packaged with `apps/web/Dockerfile.runtime-local`.
Pass only `apps/web/.next/standalone` and `apps/web/.next/static` as named
contexts, never the whole `.next` cache. Verify the resulting image in a
temporary container before replacing the running Console and retain the old
image under a rollback tag. This is a local deployment shortcut, not a
portable CI release: validate the host build in the target Linux runtime.

For a local development machine that must process PR/Issue reviews with fewer
containers, `compact` starts PostgreSQL, RabbitMQ, API, Console, and one
supervised review bundle (plus the two one-shot init/migration tasks). Build
its image once with `./scripts/local-stack.sh compact --github-app --build`;
subsequent starts omit `--build`. The build requires a local OCR runner image;
`OPEN_REVIEW_RUNNER_IMAGE` selects its tag. The launcher checks the rendered
`ENVIRONMENT=development`, verifies the bundle image before pausing existing
split workers, and checks Docker's free space both before and after an explicit
build. Add `--stop-unused` to pause other optional project workers after the
bundle is healthy; it refuses while Agent Work is active unless paired with
`--allow-paused-work`. This reduces the steady-state local footprint to five
containers without deleting data. It never runs both sets of review consumers
together. A child
worker exit fails and restarts the whole bundle. This is a **local development
shortcut**, not a production topology: workers share one container and its
provider/model credentials and cannot scale independently. Model-route and
SSO metadata probes, one-shot review scheduling, rule-exception expiry and
Canary failure monitoring run in the same bundle. The provider-read-only Agent
source worker can opt into this development bundle with
`OPEN_REVIEW_COMPACT_AGENT_SOURCE=true` and `AGENT_DECISION_JEV_API_KEY`; this
only enables immutable Issue/source resolution and Jev classification, not
automatic admission or coding. The Agent runner and isolated coding adapter
still require separate services. Notification
delivery can join this development bundle with
`OPEN_REVIEW_COMPACT_NOTIFICATIONS=true` and explicit `OPENREVIEW_NOTIFY_*`
credential slots. The default is false and retains events in RabbitMQ until
a notifier runs. Inspect existing destinations and routes before enabling it:
queued events may then be delivered. Production uses the separate notifier
for credential isolation and independent scaling. Audit export and approved
data-governance jobs can also join the development bundle with
`OPEN_REVIEW_COMPACT_DATA_GOVERNANCE=true` and a configured
`DATA_GOVERNANCE_ARTIFACT_KEY`; the default is false. Check for queued jobs
before enabling it. No process-level filtering inside this shared container
provides a production secret boundary: use the split worker for production.
Use `review` or the full deployment for
production isolation. Switching back to `review` stops the compact bundle;
`auth --stop-unused` also pauses it after the pending-work safety check.

If that bundle image predates source edits and Docker cannot safely run its
full Go/OCR build, `Dockerfile.compact-review.runtime-local` can layer seventeen
Go 1.25.14 Linux/amd64 binaries over an explicitly tagged, verified base
image. Build the changed binaries outside the Docker VM, pass only their directory
as the `compact-binaries` named context, verify the candidate rejects
production mode, then update the `open-review-platform-compact-review:local`
tag. Keep the former image tagged for rollback. This shortcut must not hide
that Agent Work and other optional workers remain separate, while notification
delivery is absent unless explicitly enabled.

In lean `review` mode, `issue-triager` has its own small image. When Issue
analysis source changes, build only that service with
`docker compose -f docker-compose.yml -f docker-compose.github-app.yml -f docker-compose.core.yml build issue-triager`,
then recreate only `issue-triager` with the same Compose files and `up -d
--no-deps issue-triager`. Omit the GitHub App overlay for GitLab-only setups.
The full deployment still uses the shared core image; do not run this build
while Docker free space is near RabbitMQ's disk alarm threshold.

For real GitHub/GitLab PR-and-Issue review, add
`docker-compose.core.yml` **after** the base and any GitHub App override:

```bash
docker compose -f docker-compose.yml -f docker-compose.github-app.yml \
  -f docker-compose.core.yml up -d
```

This leaves 14 long-running containers: API, Console, PostgreSQL, RabbitMQ,
and the independently retryable review/Issue workers. It keeps outbox,
review/Issue queue consumers, installation verification, and rule-exception
expiry. It deliberately
pauses Agent Work, third-party notifications, reaction polling, model/SSO
probes, scheduled reviews, rule-rollout monitoring, and data-governance jobs.
Their durable work waits in PostgreSQL or RabbitMQ; this mode is not a full-featured
production deployment. Enable a needed group with `--profile agent-work`,
`--profile notifications`, `--profile feedback`, `--profile model-probes`,
`--profile sso`, `--profile schedules`, `--profile rule-governance`, or
`--profile governance`; `--profile observability` adds Prometheus, and
`--profile full` starts every optional worker in this overlay. `up` does not
stop optional containers previously started by the full stack. After checking
that no Agent task attempt is queued or running, stop the two Agent Work
containers when switching to the review mode:

```bash
docker compose stop agent-task-runner agent-task-source-admitter
```

Do not stop review workers just to reduce the count while review runs are
active: their durable work will pause until they restart.

After the images for the current source are built, omit `--build` for a routine
restart. Never use `--no-build` on a fresh checkout or after adding a service.
The `migrate` service builds the shared `open-review-platform-core:local` Go
image once. The other Go services run separate entrypoints from that image;
`console` and `runner` retain their own Dockerfiles. After a Go source change,
build `migrate` once, apply pending migrations, then recreate only affected
services with `up -d --no-deps --no-build --force-recreate <service>` using the
same Compose files. This avoids both duplicate Go builds and unnecessary
restarts of PostgreSQL or RabbitMQ.
The core and runner Dockerfiles share a named BuildKit Go compiler cache so
source-only rebuilds do not copy a new compiler cache into every image layer.
For a targeted Control API rollout, set `OPEN_REVIEW_CONTROL_API_IMAGE` to the
validated API image in the local Compose environment. This affects only
`control-api`; `migrate` and the other core workers keep `OPEN_REVIEW_CORE_IMAGE`.
Unset the API-specific pin before a full shared-core `review --build` release,
then validate and deploy the API and workers together. Do not use the shared
core image variable to promote an API-only candidate.
Keep at least the configured RabbitMQ free-disk watermark plus build headroom;
do not run a broad image, volume, or build-cache prune against a shared Docker
Desktop instance just to make room for a deployment.

For a full-featured deployment, start the base stack with the GitHub App
override when App credentials are configured. Add `--build` only for a fresh
checkout or changed source/dependencies; routine restarts do not need it.

The default Console is a production-shaped standalone image. It deliberately
does not watch the source tree, so use the explicit local-only overlay while
changing Console pages instead of rebuilding the image after every edit:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml \
  up -d postgres migrate control-api console
```

This targeted command runs only PostgreSQL, the API, and the hot-reload Console
(`migrate` exits after applying schema); it does **not** start RabbitMQ or any
review, Issue, notification, or Agent Work worker. It is for UI/API development,
not a live provider or merge-gate acceptance test. On a fresh checkout, add
`--build` to the `up` command; routine page edits require no image rebuild.
The overlay bind-mounts only `apps/web`, starts Next development mode and
enables the local-preview route only when the existing
`CONTROL_API_DEVELOPMENT_SUBJECT` is configured. Do not use it for a shared or
internet-facing deployment; return to the review/full Compose command before
shipping an image. `up` does not stop workers from an earlier full-stack run:
check `docker compose ps` and explicitly stop the no-longer-needed project
workers before treating this as a three-container running stack. Do not pause
workers while they own live review/Agent tasks; queued work resumes only after
the consumers restart. Never use `down -v` to switch modes.

When `.env` contains a real `GITHUB_APP_ID` and
`GITHUB_APP_PRIVATE_KEY_HOST_PATH`, always include the GitHub App override:

```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.github-app.yml \
  up --build -d
```

The override mounts the host PEM read-only at
`/run/secrets/github-app.pem` only in provider-calling workers. Running the
base file alone while a GitHub App is enabled deliberately fails those workers
closed; a host path must never be treated as a path inside the container.
Rebuilding is required only after source, dependency, or Dockerfile changes;
routine restarts can omit `--build` while retaining both Compose files.

Run the unit suite with `go test ./...`. For the Cloudflare-hosted webhook
layout, see [Cloudflare public ingress](cloudflare.md).

## CLI-triggered reviews

Install the CLI directly from this repository:

```bash
go install github.com/RainLib/open-review-platform/cmd/openreview@latest
```

Create a key in `Workspace settings -> API keys` with the minimum required
scopes and repository allowlist. Keep the secret in the environment; the CLI
does not accept a secret flag and never prints it:

```bash
export OPEN_REVIEW_API_URL="https://review.example.com"
export OPEN_REVIEW_TENANT="acme"
export OPEN_REVIEW_API_KEY="<one-time-secret>"

openreview review create \
  --installation "<installation-uuid>" \
  --repository "RainLib/open-review-platform" \
  --number 3 \
  --base-ref "main" \
  --base-sha "<full-base-sha>" \
  --head-ref "feature/reliable-review-workflow" \
  --head-sha "<full-head-sha>" \
  --mode security
```

The first slice intentionally reviews only an existing provider PR/MR. It does
not accept a caller-supplied clone URL or synthesize a pull request number, so
provider comments, Check Runs, merge gates, immutable snapshots, and evidence
remain attached to the real code-review object.
