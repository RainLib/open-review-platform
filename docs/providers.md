# Provider integration contract

## GitHub

Configure a GitHub App with pull-request read/write, issues read/write, contents
read, metadata read, and webhook events for `pull_request`, `issue_comment`,
and `issues`. GitHub's current GitHub App registration does not expose a
selectable `reaction` webhook event; provider reaction feedback therefore
requires a separate polling/synchronization capability and must not be treated
as an App-registration checkbox. The control API checks
`X-Hub-Signature-256` with constant-time HMAC-SHA256 comparison and uses
`X-GitHub-Delivery` as the idempotency key. Only `opened`, `reopened`, and
`synchronize` events become review jobs.

Production publishing obtains a short-lived installation token from the
GitHub App private key mounted into the runner, interaction responder,
interaction admitter, terminal reporter, issue publisher, provider prober and
source-admitter. For Docker Compose, set `GITHUB_APP_PRIVATE_KEY_HOST_PATH` to
the host PEM; it is mounted read-only at `/run/secrets/github-app.pem` inside
only provider workers. Existing local deployments that set the host path in
`GITHUB_APP_PRIVATE_KEY_PATH` stay compatible. Configure `GITHUB_APP_ID`, then save
`credential_ref=github-app` for the installation. Each of those workers
exchanges an App JWT immediately before its provider call; the token never
enters PostgreSQL, RabbitMQ, logs, or traces. The
publisher uses the PR review API; low-confidence/unpositioned findings go to a
summary, never a guessed line. The interaction responder uses the same
short-lived installation identity to post a marker-keyed command response
before the queued review executes. For a first command-triggered run, the
response consumer releases durable admission only after the provider accepts
that response; the interaction admitter then reads the PR's current base/head
revision through the GitHub API before it creates the run. Once that response
is durably published, it also adds an idempotent reaction to the
source command comment: `eyes` for an
accepted command and `confused` for a rejected command. This acknowledgement
uses the GitHub issue-comment reactions API and is retried through the same
inbox fence; it does not alter review authorization or merge policy.

User-authored Issues use the same reply-before-work contract. The triage worker
first creates the stable progress comment, then adds an idempotent `eyes`
reaction to the source Issue, and only then releases model analysis. The final
comment uses one semantic status emoji (`✅`, `⚠️`, or `⛔`) for the verdict
and restrained navigation icons for the main sections. Exact repository-relative
paths supplied by the Issue are rendered as provider deep links; the model is
not allowed to invent paths. Editing the Issue creates a new durable revision
and updates the same comment instead of adding a second analysis thread.
Where the provider delivers reaction events (currently GitLab Emoji Hook),
reactions on that marker-keyed comment are durably recorded as useful/not-useful
feedback. They never start model work: only an admitted Issue lifecycle event
or an explicit, authorized `@openreview` command can create a task. GitHub
reaction feedback is read by the durable `provider-feedback-poller`; GitHub
still has no Reaction event checkbox, so feedback freshness follows the
configured polling interval rather than webhook latency.

Issue analysis format is governed by the `issue-triage` review-configuration
section. It inherits from the workspace and may be overridden for one exact
repository. Operators choose a bounded preset (`engineering`, `concise`,
`security`, `incident`, or `product`), required Issue-description sections,
visible response sections, language, item limits, collapsible detail, file-link
behavior, reaction feedback, and up to 4,000 characters of trusted guidance.
The Console renders the same required headings as a copyable GitHub/GitLab
Issue-template preview. Missing headings become evidence gaps; they do not
silently reject a provider webhook. Arbitrary provider-comment Markdown is not
accepted as configuration. Admission snapshots the effective format and its
SHA-256 beside the model route and prompt policy, so later configuration edits
cannot change a queued analysis revision.

Webhook admission requires both the matching GitHub App installation and the
workspace's recorded repository scope. An App may be installed on more
repositories than Open Review is configured to review; its provider permission
does not expand the workspace's selected-repository boundary.

Changing an installation's repository scope is a compare-and-set operation.
It atomically moves the installation to `pending` verification and queues a
fresh read-only provider probe; webhook/CLI admission resumes only after that
probe verifies every entry in the new scope using repositories returned by that
probe. Previously retained inventory is history, not authorization evidence.
GitLab exact projects and groups are queried directly rather than inferred
from the first page of general project inventory; GitHub exact repositories
are queried directly, while wildcard scopes require a fresh matching inventory
entry. A scope edit is rejected while a probe is
running. The Console keeps Advanced scope editing available even if the old
scope matches no synchronized repository, but setup cannot advance until a
matching repository appears in the worker-synchronized inventory. Neither an
empty picker nor a browser-entered path is treated as provider authorization.
For an already verified installation, a transient provider outage leaves the
authorization state intact; a permanent authentication/scope failure (or a
live probe without fresh scope evidence) changes it to `failed`, closes new
webhook/CLI admission, and writes a revocation audit event. A later probe may
restore `verified` only with current evidence for every declared scope entry.

For enforced merge blocking, require the `Open Review / Analysis` check from
the installed App in the GitHub branch protection rule or ruleset for **each
PR target branch**. Enabling Open Review's merge gate only publishes a failing
check; a rule on `main` does not cover a PR targeting another branch. Verify
the behavior with a failing check on a protected non-production target before
relying on it for releases.

Console setup starts at the GitHub App installation page rather than accepting
an App ID in the browser. The Console signs a ten-minute, HTTP-only handoff
intent and verifies the GitHub return against it. Because a Setup URL's
`installation_id` is not proof of who installed the App, the return then starts
a second server-side GitHub user authorization. The Console exchanges the code
with its Client Secret, verifies that the user can enumerate the installation,
and only then creates the thirty-minute installation receipt. It checks the
caller's workspace Owner/Admin membership before both redirects and again before
creating the receipt, so a spoofed installation ID or a permission change while
GitHub is open cannot complete setup. Register the exact OAuth redirect URI
`https://review.example.com/api/setup/github/authorize/complete`; the browser
never receives the Client Secret or the resulting user token.

## GitLab

Configure a GitLab application or project/group webhook with merge-request,
Issue, note, and emoji events plus a random secret token. The control API validates `X-Gitlab-Token`
with constant-time comparison and uses `X-Gitlab-Event-UUID` (or a computed
content ID only where the provider does not supply one) for idempotency. Only
open/update merge-request actions become jobs.

GitLab webhook normalization prefers `project.git_http_url` for the clone and
API origin and accepts the deprecated `project.http_url` only for older
self-managed payloads. This applies consistently to merge requests, Issues,
review commands, and Agent task/feedback notes; an event that supplies neither
Git URL is rejected where a clone origin is required. For a self-managed
relative-URL installation (for example `/gitlab`), the API root retains that
prefix by removing the exact `path_with_namespace` from the clone path;
disagreeing project paths are rejected before admission.
The worker's OAuth refresh endpoint uses the same relative URL root and
refuses HTTP redirects so the client secret and refresh token are never
resent to another origin. A redirect is a refresh failure requiring the
normal credential-recovery path, not authorization to follow `Location`.

The publisher creates discussions with a diff position only when it can prove
the base/head SHA and changed-side line. Otherwise it produces a normal MR
note; it must not fabricate an inline location. `Note Hook` commands are only
accepted for merge-request notes and receive a marker-keyed MR note response;
other noteable types are ignored. First-command admission follows the same
response barrier as GitHub: after its visible marker-keyed note succeeds, the
interaction admitter reads the current MR revision through the deployment-owned
credential and creates the durable run from that version.
Accepted and rejected commands also receive `eyes` and `confused` award emoji
on the exact triggering note. User-authored Issue admission posts the stable
progress note before adding `eyes` to the Issue through GitLab's award-emoji
API; duplicate awards are treated as an idempotent retry. Emoji webhook input
is quality feedback only and never becomes a review or Issue-analysis trigger.

Console onboarding uses a separately registered GitLab OAuth application with
authorization code + PKCE and redirect URI
`https://review.example.com/api/setup/gitlab/complete`. Its token is used only
to verify the returning user through `GET /api/v4/user`, then is sent directly
to the control plane for envelope encryption at rest. The browser never enters,
receives, or persists a GitLab identity or token; it retains only a short-lived
signed authorization receipt. Owner/Admin membership is checked both before
the OAuth redirect and before the authorization code is exchanged, so revoked
access cannot leave a credential receipt behind. Because webhook payloads
carry the project ID rather than the OAuth user ID, active GitLab installations
route by an explicit `group/project` allowlist or `group/*` scope; an
overlapping active scope is rejected rather than being routed arbitrarily.
Provider-call workers decrypt the deployment-owned GitLab credential only at
the provider side-effect boundary.

GitLab webhook admission is resolved by the deployment-selected GitLab API
profile and the recorded `group/project` or `group/*` scope. GitLab webhook
project IDs are not OAuth identities and do not bypass that scope; an event
outside every active scope is rejected without creating a job.

The GitLab publisher posts `Open Review / Analysis` as an external commit
status on the admitted head SHA and, when available, its source ref. To make a
failed status affect MR mergeability, enable **Pipelines must succeed** in the
GitLab project and verify that this job appears in the pipeline GitLab actually
uses for the MR. GitLab can have multiple pipelines for one SHA/ref; supplying
the ref narrows selection but does not guarantee the correct pipeline when
duplicates exist. Avoid duplicate branch/MR pipelines or use a deliberately
selected pipeline ID in an integration that owns that lifecycle. The local
GitLab CE MR #5 blocked-to-fixed exercise in the acceptance matrix proves one
working path, not duplicate-pipeline or GitLab.com behavior.

## Normalized event

Every adapter produces the same fields: provider, provider API base URL,
external installation ID, repository slug/clone URL, review number, base
ref/SHA, head ref/SHA, and delivery ID. This is the extension point for GitHub Enterprise, GitLab
self-managed, and a future Kodus ingress without forking the review engine.
