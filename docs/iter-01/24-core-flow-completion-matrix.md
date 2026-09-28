# 24 — Core Flow Completion Matrix

> Updated: 2026-09-27 (Asia/Shanghai)
> Scope: phase-one/two core flow only. Subscription and payment stay explicitly
> out of scope until the operational review workflow has deployment evidence.

This matrix is an acceptance aid, not an assertion that a listed capability is
production-ready. A green source test proves the named contract only; a real
provider, broker, database, or browser run is separately required where noted.

## 2026-09-27 Findings evidence increment

The review worker now retains a bounded source excerpt from the exact reviewed
head and, only when Git accepts it against that checkout, an optional candidate
patch. The Console renders both beside the finding and labels the patch as
uncompiled, untested, unapplied guidance. The new fields are persisted in
`000112_review_finding_source_evidence.sql` and included in tenant export and
erasure. Full Go tests, a fresh PostgreSQL migration plus focused store
integration tests, and the production Console build passed; a synthetic
browser preview rendered the evidence panel. The local running database then
advanced from `000111` to `000112`; the Control API, compact review worker
(including the runner and data-governance worker), and Console were replaced
with tagged candidate images while retaining the prior
image tags for rollback. Local API and Console health returned 200, the public
Console health endpoint returned 200, the Console became Docker-healthy, and
all RabbitMQ queues reported `running`. An unauthenticated evidence request
returned 401. This proves deployment and basic health, not signed-in reading
or a real provider review producing the new source fields.

A real GitHub Draft PR [#6](https://github.com/RainLib/open-review-platform/pull/6)
was reviewed after that rollout with the exact unchanged head
`32ba779017b7f366916556674a75a70821a31287`. The
[`@openreview review --force --mode=standard` command](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5853489791)
received its [queued acknowledgement](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5853490369)
before the run began. Run `498c4b71-aca3-408f-97a7-68734e4422e0` selected
the PR's one changed file, reached `completed`, updated the
[evidence report](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5853491725),
posted the [terminal comment](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5853502711),
and published successful GitHub Check `108568471336`. The model produced **zero**
findings in this pass (an earlier run on this same head retained one), so this
validates the no-finding provider path but **not** live persistence or signed-in
rendering of the new source excerpt and patch fields. The current Casdoor
browser tab remains at its password form; no callback/session was observed.

The later [2026-09-27 forced review command](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5853627289)
on the same PR received an `eyes` reaction from `rainlib-open-review[bot]` and
no new queued/start comment. The next Bot Issue comment was the single
[evidence report](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5853639171).
The older queued and terminal comments above remain historical provider
artifacts; the current reaction-only path is covered by
`TestReviewReactionOnlyDoesNotCreateProviderComment`. This is live GitHub
evidence for the latest command only, not a claim that all old comments were
removed or that every provider and failure mode has been retested.

An isolated Console process at `localhost:3121` and temporary development
Control API at `127.0.0.1:8081` used the existing PostgreSQL data without
changing the public OIDC service. Development authentication reached the
control plane (HTTP 200), but a signed-in visit to the `repositories` setup
route for `gitlab-oauth-e2e-1790064839` rendered **Provider access needs
attention**. Its installation is `failed` with `credential_unavailable`, zero
recorded repositories and a disabled Save scope action. This verifies the
failed-connection UI boundary, not a successful repositories to review-scope
browser transition. Both temporary processes were stopped after the check.

The Agent adapter, decision, interaction and runner Go packages passed their
focused suite in this checkout. The integration test for reaction-gated review
admission skipped because `OPEN_REVIEW_TEST_DATABASE_URL` was not configured.
The current six-container local stack runs the Agent source-admitter inside
the compact review bundle (`OPEN_REVIEW_COMPACT_AGENT_SOURCE=true`) and a
separate live Agent task-runner with `adapter_configured=false`, but has no
coding adapter process. Its two repository Agent policies are disabled and
the `.env` has a Jev decision key but no configured coding Adapter/model
credential. Therefore no real Issue to approved plan to CLI edit to Draft
PR/MR to feedback iteration was accepted in this deployment; do not infer
closure from source tests or the PR review result.

The compact supervisor now cleans up an exited worker's entire process group,
including a descendant that ignores `TERM`; living siblings retain a bounded
ten-second graceful shutdown. A process-level regression test, race test,
`go vet`, and the full Go suite passed. A Linux/amd64 overlay image replaced
only `/app/compact-review` on top of the retained
`terminal-failure-recovery-20260927` base. The deployed binary SHA-256 matched
the tested host build; the image still rejected production mode. Before the
local-only container recreation there were no live Review or Agent leases and
no unacknowledged review/interaction/Agent broker messages. After recreation,
the bundle had zero restarts, its expected worker heartbeats were live, the
relevant queues were `running` with zero unacknowledged deliveries, and local
API and Console health returned 200. The old base image remains tagged for
rollback. This proves a local deployment and basic recovery guard, not a
production-topology failure drill or completion of the coding-Agent loop.

| Flow | Required invariant | Current repository evidence | Remaining acceptance evidence |
| --- | --- | --- | --- |
| Entry and workspace | A user without an initialized workspace cannot enter a console workspace; setup resumes at the durable checkpoint. | Sign-in, workspace creation/directory, setup checkpoint API and Console guards are implemented. An isolated authenticated browser run covered an identity with zero workspaces, an unavailable slug, UI workspace creation, the unconnected guard, a verified connection paused at the durable `review_scope` checkpoint, and the same workspace after a retained `complete` readiness snapshot. A subsequent real local GitLab CE OAuth authorization completed repository verification and the ordered setup checkpoints for a fresh workspace, retaining its `complete` readiness snapshot. | Repeat the lifecycle with deployed OIDC identities and production HTTPS ingress. The real local CE provider handoff does not establish production identity or fully disconnected deployment acceptance. |
| Provider authorization | Browser never supplies a private key, app identity, OAuth token, deployment token, or self-managed host. GitHub App/GitLab OAuth returns only through signed short-lived state. | GitHub App Setup URL return is followed by server-side GitHub user authorization that must enumerate the installation; GitLab supports PKCE BFF with an encrypted credential reference and, for a self-managed deployment-owned token, a non-secret Control API capability flag plus an owner/admin-only, HTTP-only signed receipt. The latter never returns token/host to the browser and remains read-only until the worker's provider probe succeeds. The worker-side GitHub App probe distinguishes repository access from App event registration and reports GitHub reaction feedback as `polling_required`, because GitHub Apps expose no Reaction event checkbox. A separate durable worker consumes that required polling path without moving credentials into the browser. A real user `+1` on the App-owned Issue #9 analysis comment was observed as useful feedback, then its removal was reconciled as a retraction with both audit transitions retained. A verified installation can explicitly recheck its read-only provider health without clearing webhook/CLI admission; initial, failed and legacy verification still pass through pending/checking and remain fail-closed until a live receipt. An isolated GitLab provider fixture covers signed state, PKCE exchange, encrypted credentials, probe/inventory and the complete eight-step setup checkpoint. Subsequent real local CE runs separately verified deployment-token access and OAuth authorization, encrypted access/refresh-token persistence, repository inventory and complete onboarding. An OAuth-only MR run also completed with static `GITLAB_TOKEN` unset on every execution worker. | GitLab.com authorization, production self-managed HTTPS ingress and an actual expired-token refresh against GitLab remain required. Refresh/concurrent-refresh tests are not live provider refresh evidence; local CE acceptance does not prove a disconnected topology. |
| Admission | Unverified/incomplete setup or installation cannot create a review job, run, provider write, or usage reservation. | Webhook, command, CLI, retry, and schedule admission checks plus payload-free audit state. A real local CE workspace advanced from OAuth callback through the provider verification worker and complete setup to admitted MR execution; subsequent push/MR webhook admission triggered the fixed-revision review automatically. | Repeat callback → verification → first admitted PR/MR against the target production identity/ingress topology, including fail-closed rejection checks. The local CE success path does not replace negative-path or production acceptance. |
| Durable execution | Work is idempotent, staged, lease-owned, and recoverable; retry after publish begins at persisted findings rather than rerunning the model. | Outbox/inbox, run stages, snapshots, execution plans, receipts, retry tests. A real RabbitMQ 4.1 run verified publisher confirms, channel-interruption reconnect and mandatory-return rejection; the complete store integration suite passed from an empty PostgreSQL 16 database migrated through `000064`. A two-process RabbitMQ/PostgreSQL chaos test covers crash-before-inbox-completion lease takeover, crash-after-inbox-completion-before-ACK redelivery, and an extra physical copy with the same immutable message ID. A switchable TCP proxy additionally proves broker partition after inbox/effect commit but before ACK, and database partition before claim with bounded NACK/redelivery recovery; both finish with one durable effect. A real quorum-queue burst proves the high security band leads while the normal band receives service before the high backlog drains; isolated PostgreSQL proves cross-tenant recovery rotation, security-first selection without skipping another tenant, and durable deferral at the tenant concurrency limit. Real PostgreSQL/provider-transport tests accept an external Issue, a terminal GitHub Check and a terminal PR lifecycle comment before dropping TCP; retries discover the stable marker or Check identity, complete the same receipts and never issue a second create. | Sustained multi-worker SLO/load evidence, production-topology partition exercises and equivalent real-provider ambiguity runs against non-production GitHub and GitLab still require deployment-level evidence. |
| Agent and review policy | The runner uses only admitted immutable configuration/rule snapshots and bounded model credentials. | OCR adapter, deterministic risk plan with persisted static impact signals, model routes, configuration snapshots, rule version/binding rules. Migration `000073` qualifies every new repository rule binding and exception by provider plus normalized API base URL; webhook, command, retry, static-impact preview and rule-test replay resolve only that identity, while legacy unqualified rows remain an observable compatibility fallback. A real security run retained one selected deployment boundary and 130 deferred files while preserving the complete admitted head tree for repository context. Static signals explicitly do not claim a runtime dependency graph. | Repeat provider evaluation across representative languages and changed-file sizes; retain quality and latency baselines. |
| Agent task admission and bounded handoff | An Issue must be explicitly enabled, classified against its immutable Issue revision, frozen to an exact provider base commit, planned and approved before an external executor can receive it. Critical/untrusted instructions never enter planning; cancellation wins over late executor callbacks. | Migrations `000077`–`000090`, deterministic Judge/Evaluate/Verify classifier `deterministic-v3` and TypeSafe Jev decision adapter, task/policy/plan/attempt/audit records, distinct-owner approval for high/critical work, RabbitMQ source-resolution and execution requests, HMAC-authenticated adapter callback, GitHub/GitLab `@openreview implement`, `status`, `cancel`, `stop` and Draft PR/MR-only `@openreview revise <feedback>` routing are implemented. A `manual` repository may additionally opt in to one exact label that creates a JEV-classified candidate from a user-authored Issue webhook; this path still cannot plan, execute, branch, PR, or merge without later gates. Feedback is independently deduplicated and creates a child task that reuses only the original agent branch, re-reads the provider Draft PR/MR head, runs a fresh deterministic classification, and must pass the ordinary source, plan and owner/admin approval gates. It is bounded by a frozen 0–3 cycle policy and cannot resume a sandbox, create another PR/MR, widen scope, or merge. `agent-task-source-admitter` has deployment-owned read-only provider credentials and records the exact source ref/SHA before plan creation; it has no adapter secret, clone, CLI, branch, or PR capability. A synthetic Issue made one real Jev request with the configured deployment key on 2026-09-23 and passed the bounded-choice parser; no task or provider write was created by that smoke test. The optional separately deployed `agent-task-adapter` accepts only signed immutable submissions, deduplicates an attempt within its receipt window, supports exact cancellation/heartbeats, checks out the frozen SHA, runs the exact frozen Codex/Claude profile, applies path/secret/diff budgets, pushes only the task's frozen agent branch, and creates only a Draft PR/MR (or updates the feedback task's existing Draft PR/MR). The repository policy's executor, 1–3 attempt, 1–120 minute and feedback-cycle limits are frozen onto each task and included with the immutable source pair in the signed adapter handoff. Self-managed GitLab Draft MR links now validate the exact admitted repository/IID against the internal or configured public base, then persist the public canonical link for Console and Issue comments. Unit/store integration tests cover source resolver endpoint contracts, automatic label revision/dedupe semantics, adapter signature/receipt/cancel semantics, deterministic classification, Jev protocol, source/plan/claim fences, immutable policy-envelope snapshot, feedback dedupe/head-staleness fences, status/cancel routing, callback validation, cancellation/expiry dispatch and stale-callback suppression. The adapter handoff contains only immutable task/plan/source/provider identity and callback metadata; it never carries browser secrets or provider tokens. | Build a pinned executor image, supply adapter-only repository credentials, then prove sandbox isolation, automatic candidate acknowledgement, provider Draft-PR creation/update, remote cancellation delivery and GitHub/GitLab end-to-end acceptance. Single-instance disk receipts now retain job identity and terminal callbacks across restart; multi-replica durable execution, provider publication reconciliation and live code-writing acceptance remain unproven. |
| Provider publication and merge gate | Inline findings, summary, and status/check use stable markers; an out-of-date head never receives a false passing gate. | GitHub/GitLab fixtures, marker receipts, terminal reporter, gate snapshots; a real GitHub App Check Run write now retains the provider Check ID in the immutable run receipt. Endpoint-level GitHub Check Run and GitLab commit-status tests exercise 429 → 5xx → success against one stable status identity. Terminal recovery persists failed receipts and schedules attempts 2–5 through `outbox.available_at`, respecting bounded `Retry-After` without worker sleep or immediate broker redelivery; isolated PostgreSQL plus a provider that closes accepted HTTP writes proves a cancelled run recovers one Check and one lifecycle comment across three completed inbox messages and two durable successors, without model execution or duplicate creates. | Repeat the same ambiguity and 429/5xx recovery against real non-production GitHub and GitLab providers; the local transport fixture does not establish provider-side production behavior. |
| Human task loop | `@openreview` is acknowledged first, work runs asynchronously, and terminal evidence is updated on the same provider surface. | Interaction responder, durable command admission, task queue/intervention, terminal messages. Real browser refresh replays the retained timeline and keeps the live SSE connection open. A disposable self-managed GitLab CE OAuth-only run now proves the same provider lifecycle: acknowledgement/update → queued run → terminal 🎉 comment, with the final `Open Review / Analysis` commit status `success` and an evidence target URL. | GitHub and local self-managed GitLab provider E2E are verified. GitLab.com, production ingress and provider-side ambiguous-write recovery remain separate acceptance gates. |
| Findings and issues | Findings retain line/evidence provenance; feedback, aggregation, exceptions and optional external issue creation are tenant-scoped and auditable. User-authored provider Issues are acknowledged before bounded AI triage and retain one revision-stable bot comment. | Issue/occurrence/event model, exception expiry worker, external issue receipt/outbox and provider-Issue triage outbox/inbox. Repository Issue triage policy provides fifteen complete format packs plus bounded Custom guidance, workspace inheritance, provider-qualified repository overrides and admission-time immutable SHA-256 snapshots. A workspace-owned catalog now saves named reusable formats as immutable revisions with optimistic locking, roles, archive and audit; applying one only prepares an unsaved repository/workspace draft. The editor exports provider-ready GitHub and GitLab Markdown template files with the exact validated headings and explicit repository target paths, while keeping trusted administrator guidance out of provider-visible files. Failed Issue analysis has a tenant-scoped, audited, idempotent attempt retry that reuses the exact retained body/config snapshots; revision and attempt fences prevent stale work from overwriting a newer retry. Real GitHub Issue #8 proves hierarchical generated evidence, exact file/PR/commit deep links and automatic close; real user-authored Issue #9 proves one marker-keyed comment updated from acknowledgement/failure to the final structured analysis. Two real GitHub-origin `issues.edited` deliveries advanced that same job through revisions 2 and 3, retained three admission receipts and updated the same comment without duplication; the second edit restored the exact original body hash. GitHub feedback uses a 30-day durable lease/poll/reconcile path with complete-snapshot retraction; a real RainLib `+1` was retained as `useful`, and removing it preserved the row with `retracted_at` plus created/deleted audit events. A real local CE deployment-token run produced structured triage for a user-authored Issue; actual `award` and `revoke` Emoji Hooks created and retracted the same useful-feedback record with audit evidence. Neither provider feedback path schedules model work. | Deliberately failed real GitHub retry and real GitLab automatic Issue creation/closure, edit/retry lifecycle and OAuth-only Issue/feedback execution remain required. The local deployment-token Issue evidence does not prove those paths or fully disconnected execution. Automatic provider template-file publication remains a later enhancement, not a blocker for per-repository analysis policy. |
| Notifications | Routes have deliberate, tenant-local precedence; preview and notifier select the same first eligible route per destination. | `000056_notification_route_priority.sql`, optimistic full-order mutation, notifier/preview shared `(priority,id)` ordering. The complete store integration suite now passes from an empty PostgreSQL 16 database migrated through `000070`, including notification route ordering, preview, test delivery and retry contracts. A local TLS receiver verifies test-admission → HTTPS send → failed receipt → same-event retry → delivered receipt, while an isolated full-flow fixture additionally crosses durable outbox → relay → real RabbitMQ → exactly-once inbox → notifier HTTPS → delivery ledger and proves duplicate consumption causes only one receiver call. Generic webhooks can authenticate the timestamp and exact body with a deployment-owned HMAC secret. Compose and Console now expose six explicit notifier-only credential slots instead of implying that an arbitrary database `env:NAME` is automatically available to the process. | Real Feishu/DingTalk/Slack delivery remains required. A production generic Webhook still needs a receiver-owned credential and external receipt even though its local signed send/retry contract is verified. |
| Usage governance | Every admitted run has one tenant/repository reservation; terminal run state can reconstruct mutable counters without rewriting the append-only ledger. | Atomic admission reservation, terminal settle/release, immutable ledger trigger and monthly reconciliation API/BFF/Console are implemented. An isolated PostgreSQL 16 run created one missing reservation, one state mismatch and one missing-proof reservation; owner reconciliation repaired all three, appended exactly three adjustment events, preserved the original reserve row, rejected billing-viewer mutation, replayed the same receipt without extra rows and rejected idempotency-key reuse with different parameters. A tenant-authorized CSV export now emits only UTC-month repository aggregates, omits internal identifiers and metadata, neutralizes spreadsheet-formula prefixes and is exposed through the Console BFF. The rebuilt production Console read the current month as clean across 18 runs with zero missing reservation/state/proof. | Run a deliberately controlled drift repair and the CSV download against the target deployment. Integrate and verify an optional commercial billing adapter only when subscription work begins. The production reconciliation read was intentionally non-mutating. |
| Operational readiness | Accepted work remains observable without exporting tenant/repository identity; alerts have versioned, non-destructive recovery guidance. | The private control API exposes low-cardinality HTTP metrics and a two-second-bounded PostgreSQL snapshot for queue/outbox age, ACK SLA, leases, worker heartbeats, publication failures, terminal failures and notifications. An isolated PostgreSQL 16 integration seeds every signal and reads it back. Prometheus 3.5.1 validated and loaded 11 rules; a real local collector scraped both `control-api:8080` and RabbitMQ `:15692` with `up=1`, and every rule reported `health=ok`. Three versioned runbooks preserve PostgreSQL/outbox authority and stable provider identities. A separate PostgreSQL 16 scheduler run completed 108 jobs across 6 tenants with 8 workers, no duplicate claim, tenant concurrency at most 2, bounded first service and a 26.6ms claim P95; it exposed and regressed a stale-candidate duplicate-execution race. | Configure and prove Alertmanager delivery in the target deployment, run a production-topology recovery drill, and retain a target-duration checkout/OCR/model/provider end-to-end workload SLO. Local scheduler and scrape success are not production acceptance. |
| Console | A page reflects the actual control-plane state or explicitly renders unavailable/unconfigured/read-only; no demo mutation is presented as live. | Console routes, BFFs, source notices, workspace shell, local visual checks. The authenticated live Review detail has been verified at 390 px against the retained GitHub run with no page overflow or browser-console error; Overview/Files/Checks/Activity deep links and exact provider links resolve from live evidence. A subsequent authenticated route sweep returned 200 for 32 static pages and the available installation/review/task/Issue dynamic pages; Provider Issue gained a tenant-checked stable detail URL with inaccessible IDs remaining 404. The live Issue #9 analysis renders its signal matrix as a table, headings/lists semantically and secondary details collapsed. The shared shell exposes a first-focus “Skip to main content” control and one top-level `main` landmark; Review, Issue and CLI detail panels are named sections instead of nested main landmarks. An authenticated local browser run verified the skip link, command-palette focus loop and previous-focus restoration, plus initial focus, bidirectional focus trapping, Escape close and trigger-focus restoration for Issue policy and API-key dialogs. A later 390×844 + 1440×900 sweep rendered all 32 static pages at both breakpoints (64/64) with one `main`, one `h1`, no Next overlay, no browser warning/error and no page-level overflow; it found and fixed the only narrow regression in the Issue inbox action group. The current production image was then rebuilt and is served through the existing Cloudflare tunnel; an authenticated `review.rainlib.com` read verified the stable Issue #9 analysis URL, current Provider Issue shell, Issue-inbox triage entry and repository-scoped Issue-format editor with fifteen presets, 13 input sections, seven response sections, a retained reusable format revision and immutable policy provenance. The deployed editor was also checked at 390×844 with one `main`, one `h1`, exact-width layout and no browser warnings/errors; first Tab exposed Skip to main, activation focused `main-content`, and command-palette Escape restored the preceding main focus. An isolated live-control-plane SSO run additionally verified the ready-to-enforce sheet, initial focus, reverse focus wrap and Escape restoration, then browser-read the audited `enforced` and `suspended` states. That run exposed and fixed stale readiness copy so active and suspended login policy now present distinct recovery guidance. A subsequent authenticated production audit covered the repository Issue-format editor at 1440×900, 1024×768 and 390×844: all three retained one `main`/`h1`, named controls, unique IDs, >=24px targets and no horizontal overflow. The first computed-style pass found six dark-theme text contrast failures; shared accent/tertiary/on-accent tokens were corrected, the Console image was rebuilt, and 96/96/91 reliably resolvable visible text nodes then reported zero failures. Reduced-motion emulation covered 398 visible descendants with zero transition/animation above 0.01ms or repeated animation. The rebuilt production Console then completed a 35-route authenticated audit at both 1440×900 and 390×844 (70/70): every page retained one `main`/`h1`, named controls, unique IDs, effective >=24px targets, no horizontal overflow or Next overlay, and both bounded log windows contained no warning/error. The audit exposed and fixed small targets on Home/Reviews/Tasks/Audit/API Keys plus a Platform Health SSR/browser time hydration mismatch; fixed `en + UTC` formatting and server-observed relative time passed the full redeployed rerun. | Add authenticated route-suite axe, real 200% zoom and human screen-reader coverage; the custom structural/target/log and representative computed-style audits do not cover composite backgrounds or replace those gates. |
| Governance | High-risk state changes retain revision, actor, reason and immutable audit evidence; settings only affect future admissions. | Rule approvals, exceptions, data-governance approvals, immutable configuration/run snapshots, plus migration `000057`: initial model route bootstrap is direct, while active model-route updates and repository-inheritance restores are hash-bound proposals requiring a different owner/admin. Empty PostgreSQL 16 migration and lifecycle integration have passed. An isolated authenticated two-person Console run additionally proved that the requesting Owner cannot decide, a different Admin can approve the exact proposed hash, and only that approval activates revision 2. | Repeat the same two-person operation with deployed OIDC identities and the production secret broker; no connectivity probe was run locally. |

2026-09-23 Agent-task correction: the old `deterministic-v3` Judge/Evaluate/Verify rules are not the TypeSafe Jev service. Migration `000089` adds a frozen decision backend; new policies select Jev by default, while legacy policies remain deterministic. A synthetic Issue completed one real Jev HTTPS request and passed the production response parser; this proves protocol/key wiring, **not** a provider Issue admission or a coding-Agent run. The adapter now rejects a changed submission for the same attempt, checks the approved plan summary against its SHA-256 before starting work, and retries a terminal callback with one stable delivery ID after a transient 5xx. Race and full Go suites passed. That earlier process-local limitation is superseded by the single-instance disk receipt work below; multi-replica recovery and real GitHub/GitLab Draft PR/MR execution remain open gates.

An offline adapter-chain test now crosses a verified Issue revision, a disposable bare Git checkout, a fixed test executor, patch validation, exact branch push and Draft MR response; changing the Issue before publication leaves no remote Agent branch. This test uses a provider API fixture and a temporary Git repository, not a real GitLab/GitHub write or isolated production executor. Automatic candidate acknowledgements now have one stable task marker/outbox key across distinct webhook deliveries, and rejected or context-deficient Issue commands state their actual blocked condition. An isolated PostgreSQL 16 integration test verified two deliveries create one automatic acknowledgement outbox record; provider-visible timing and delivery are still unverified.

The runner/adapter handoff is now two-phase: a signed submission reserves a job without executing code, the runner persists that job ID against its leased attempt, and a separately signed, idempotent start request releases execution. An attempt cancelled before start cannot run. This closes the fast-callback-before-attachment race in source and service tests; it does not make an adapter reservation durable across process restart. No local live Agent task exists yet, so provider Issue→Jev→Draft PR/MR acceptance remains unproven.

Migration `000090` adds an atomic, one-use control-plane start claim for the exact running attempt and attached adapter job. The adapter must consume it before launching a coding CLI; a restarted or duplicate adapter cannot consume it again, and callbacks before the claim are rejected. Focused race tests, API signature/replay tests and an isolated PostgreSQL 16 migration/integration run passed; `000090` is applied to the local dev database. The configured Jev key passed a new synthetic live HTTPS smoke request. Local control API and Console health checks passed after rebuild. There are still zero Agent policies/tasks in that database and no pinned, deployed coding executor, so **no real GitHub/GitLab Issue→Jev→Draft PR/MR acceptance** is claimed. The one-use claim prevents duplicate starts; it does not recover an adapter job after a crash.

The tenant-scoped Platform Health snapshot now separates Agent source capture from execution handoff, including queued/running/needs-attention attempts, expired attempt leases, and matching runner heartbeats. Agent Work displays these live PostgreSQL counters when permitted and explicitly does not interpret them as adapter reachability or executor isolation. A PostgreSQL 16 integration run checked queue counts, own-tenant heartbeat visibility, foreign-tenant exclusion, and viewer denial; the local Console build and control API health checks passed. The current browser session stops at organization SSO, so this new Agent Work presentation has not yet had an authenticated visual acceptance run. A fresh synthetic Jev request initially hit the backend's 10-second response timeout, then passed on one retry; connectivity and configuration are proven for that sample, but model latency/reliability are not. The local database still has zero Agent policies, tasks, and attempts.

The disposable self-managed GitLab path now has an explicit development-only HTTP adapter/callback and clone opt-in. The runner receives `ENVIRONMENT`, the adapter rejects these flags outside development, and GitLab clone URLs must match the admitted API origin/path before a repository token is attached. Default and production behavior remain HTTPS-only; unit/race tests cover opt-in, mismatched origin, and GitHub remaining HTTPS-only. This enables local-stack acceptance but does not itself prove a live coding-Agent run.

The local GitLab Draft MR publication and result path now uses the same exception: API calls, returned `web_url`, adapter terminal evidence and control-plane storage all accept the exact admitted HTTP GitLab origin, while rejecting a changed host/scheme. The provider contract test asserts the `[Draft]` title and verifies the returned `draft=true`; GitHub remains HTTPS-only. Both Agent adapter and runner images now build locally. The runner is live with a fresh database heartbeat, while a disposable adapter container answered an unsigned submission with HTTP 401 and was removed. The source-admitter is live with a configured Jev key, model and URL; control API `/healthz` and Console sign-in returned HTTP 200. These checks do **not** exercise a real coding executor, provider write token, or Issue-to-Draft-PR/MR run. The adapter remains undeployed in the normal Compose profile.

Patch evaluation now includes newly created source/test files via Git intent-to-add and NUL-delimited path parsing before any commit. A temporary-repository test confirmed a new allowed file is included in the diff budget, while an unallowed credential file and a secret-bearing new source file are rejected. This closes a real omission in the local executor path, but is still static validation rather than proof of sandbox isolation or live Agent quality.

Draft publication now performs a provider branch-scoped preflight and validates the returned Draft's repository, source branch and exact pushed head SHA. A lost POST response or server error triggers one read-only reconciliation instead of another POST; a stale existing Draft fails closed. GitHub and local GitLab protocol tests, including lost-response and stale-SHA cases, passed. This is synchronous idempotence at one publication boundary, not a durable provider receipt or a real Agent-task acceptance run.

Publication now uses GitHub PR `body` and GitLab MR `description` correctly, each with a stable marker derived from the frozen Agent branch. Reconciliation rejects an otherwise matching but unowned Draft. The marker remains stable across feedback cycles on the same branch; tests cover both provider create contracts and missing-marker rejection.

The post-executor Git path now rejects changed `.git/config`, branch or base revision before inspecting a patch; disables repository hooks and HTTP redirects; supplies the provider write header only to the admitted repository URL; and pushes the exact validated commit rather than a mutable branch ref. Git subprocesses no longer inherit adapter secrets or deployment Git overrides. A local Git 2.23 test exposed that `GIT_CONFIG_COUNT` silently dropped the scoped header, so the adapter now uses the compatible quoted environment form; focused tests and the rebuilt Alpine adapter image confirm it is recognized. Full Go, adapter race, vet and diff checks passed. This reduces credential misdirection in the Git publication path but **does not prove executor isolation**: the coding CLI still shares the adapter container UID and may read parent process state. A separate executor identity and credential broker remain mandatory before production write credentials.

The optional Compose adapter now mounts `/workspaces` with UID/GID 10001, matching its non-root image user. Compose rendering and a disposable read-only container both confirmed that this user can create the ephemeral workspace; the previous root-owned `0700` mount would have stopped every task before clone. No real Agent task was launched by this filesystem smoke test.

The adapter now cancels its executor when a periodic control-plane heartbeat is rejected, and the concrete Git pipeline must obtain a fresh signed control-plane heartbeat immediately before branch push and again before Draft PR/MR publication. A focused service test proves a rejected heartbeat stops a live executor; a pipeline test proves publication fails closed without its service-injected lease guard. The coding CLI now runs in its own Unix process group; cancellation and normal exit kill remaining same-group descendants before checkout validation, covered by a background-write regression. This narrows the stale-lease and ordinary child-process write windows but does not give atomicity across a provider HTTP call, contain children that escape the process group, separate the shared container UID/PID namespace, or resume an already-running CLI after restart. Those remain separate acceptance gaps.

Jev classification, Agent source rereads, and Draft PR/MR publication now use a shared no-redirect HTTP client boundary. A deployment API key, provider credential, Issue snapshot, or Draft write body cannot follow a provider-supplied 3xx to another endpoint; local HTTPS/HTTP fixtures cover all three paths with race detection. This is a credential-routing guard, not proof of a production-isolated coding executor.

Agent source rereads now require HTTPS by default. The source-admitter reads the existing validated `GITLAB_ALLOW_HTTP` development flag, and the adapter reread uses its development-only GitLab flag; GitHub cannot use plaintext HTTP. Focused resolver/adapter race tests cover the default rejection and explicit local-GitLab exception. Both Linux images built, the full Go suite, focused vet and Compose validation passed after the change. The running containers were not restarted, so this is source/image evidence, not a deployed runtime acceptance result. It closes an inconsistency between provider setup and Agent source capture without claiming a disconnected GitLab production acceptance run.

The adapter now binds the signed task's provider API base to its deployment-owned clone base before its first credentialed Issue/PR reread. Hosted GitHub must pair `api.github.com` with `github.com`; GitHub Enterprise accepts a same-origin API base (including `/api/v3`), and self-managed GitLab retains its exact `/api/v4` pairing. A focused test proves a mismatched GitHub API receives zero credentialed requests. This prevents a task from redirecting the adapter-only token to an unrelated API host, but the static adapter token still needs a per-installation scoped broker before multi-tenant production execution.

Signed control-plane → adapter submissions/starts/cancels and adapter → control-plane start claims/heartbeats/terminal callbacks now refuse HTTP redirects. A 3xx is a failed delivery to reconcile or retry through the same durable identity, never authorization to forward the HMAC signature and request body elsewhere. Focused race tests prove redirected signed start and callback requests do not reach their destination; the full Go suite, focused vet, Compose validation, and rebuilt Linux adapter image passed afterward. The current local `.env` has a Jev key but no adapter URL/secret, fixed executor, or adapter-only provider token, so no live Agent job was started. This does not replace the missing per-installation credential broker or provider-side Draft acceptance run.

The signed adapter envelope now carries the frozen installation UUID, and the adapter rejects submissions that omit it. A single-node file credential source selects by the exact installation/provider/API/repository tuple; it rejects broad file permissions, symlinks, duplicate scopes, and missing matches. The adapter opens the file without following a final symlink, checks permissions and size on the same file descriptor, validates at startup and re-reads for each task; malformed initial configuration and later revocation both fail closed. The offline GitLab Issue→validated patch→Draft MR chain now exercises that scoped file source without putting a token in the handoff. The full Go suite, adapter race tests, focused vet, Compose validation and rebuilt Linux runner/adapter images passed afterward. The running services were not updated; runner and adapter must be deployed as one compatible handoff version. This is local source/fixture evidence only: the coding CLI still shares the adapter UID and can potentially inspect the file or parent process, so a separately isolated executor and production credential broker remain mandatory.

The adapter now bounds coding-CLI stdout/stderr to 1 MiB without retaining its text, and bounds each Git subprocess output to 8 MiB before parsing or validating a patch. Both over-budget subprocesses are terminated as process groups, while an adapter diff-byte configuration above that Git ceiling fails startup. Noisy fake executors and Git commands were stopped promptly in race-enabled tests. This closes an unbounded-memory path in the Agent execution boundary, not the missing separate-identity sandbox or live provider acceptance.

The adapter now uses an exclusive, private `/state` volume for atomic fsync-and-rename receipts. Submit persists the immutable attempt/job mapping before returning; a reserved job keeps its ID after restart, while `starting`/`started` jobs never relaunch the executor and instead replay one `needs_attention` callback. A terminal event is persisted before delivery, and a lost acknowledgement is retried with the same delivery ID on restart. Receipt corruption and a second owner of the same volume fail startup. Focused restart, terminal replay, lock and corruption tests passed with race detection. This is **single-replica fail-closed recovery**, not a distributed durable queue: a crash between control-plane start claim and local state update, or between provider push and terminal persistence, may still require lease reaping or manual provider reconciliation. The shared-UID executor isolation gap remains open.

A running adapter now retries retained but undelivered terminal callbacks every minute without needing another restart. Per-job serialization prevents a sweep from racing the live terminal publisher; a repeated sweep does not redeliver a confirmed event, and a live started executor is never classified as interrupted by the sweep. Focused race tests cover all three cases. The control plane remains authoritative: a stale lease or cancelled attempt can reject the replay, which does not restart code execution.

An explicit control-plane HTTP 409 on a terminal callback is now retained as a final lease rejection instead of causing five requests every minute for the receipt lifetime. Transient transport/5xx errors remain eligible for replay. A focused race test verifies one rejected delivery stays suppressed both during the current process and after a restart.

The adapter now resolves its one configured Codex/Claude CLI at startup, follows the image-owned executable target to an absolute path, and refuses to serve if the CLI is absent or invalid. That prevents an approved task from being the first place a missing binary is discovered. The complete Go suite, focused vet and diff check passed; a rebuilt Linux image also exited before serving with the expected missing-`codex` error when launched without a coding CLI. It does not validate model login, isolate the CLI from the adapter UID, or prove a pinned production executor image; the real provider Issue-to-Draft path is still open.

Jev source admission now distinguishes transient transport timeout/429/5xx from permanent authentication and protocol failures. Only the former schedules a durable, delayed successor with the same frozen task revision and at most three total attempts; the outbox key deduplicates the successor, a later Issue/task generation cannot reuse the delayed message, and exhaustion fails closed for human intervention. An isolated migrated PostgreSQL 16 integration verified the retry/revision fence and deduplication; the dedicated empty test database was removed after the run. Focused race, full Go, vet, and diff checks passed. This is retry control for an already admitted candidate, not evidence of a live provider Issue-to-Draft execution.

OIDC discovery no longer blocks control-API process startup when Casdoor is temporarily unreachable. Discovery occurs on the first authenticated request, with a bounded timeout and backoff; missing, invalid, wrong-audience, and undiscoverable tokens remain rejected. A fake issuer test verified outage, recovery, signature and audience checks; the rebuilt local control API returned HTTP 200 at `/healthz` and HTTP 401 for missing and invalid bearer tokens at `/v1/me`. The source-admitter container has a nonempty deployment-owned Jev key, and an additional synthetic live Jev HTTPS request passed with the current `.env` key. Neither health nor that synthetic protocol request proves a new authenticated Console session or a real GitHub/GitLab Agent task.

Review recovery now performs a read-only provider preflight **before** publishing a started status or invoking OCR: the PR/MR must still be open and its head must equal the frozen admitted SHA. The publication step repeats the check. A changed/closed review is superseded without model execution; an absent state is an error, not assumed open. GitHub/GitLab transport fixtures, runner early-exit tests, full Go, focused race/vet and diff checks passed. The rebuilt Runner image contains Git `2.47.3` and OCR `1.12.5`. The local runtime also exposed a separate operational issue: several workers still had deleted temporary binary mounts from an earlier acceptance fixture. Eight event/provider workers were recreated from the current Compose profile. At that audit point the Review runner remained stopped because 15 historical test jobs had non-SHA revisions; no database rows or provider artifacts were changed then.

The next source revision adds a local full-commit-SHA guard to the real Review
publisher. Both base and head must be 40- or 64-character hexadecimal commit
identities before a credential is resolved, a provider is contacted, or the
model is invoked. The runner applies the preflight before resumed publication
as well as fresh execution; an invalid retained job becomes failed without
direct provider writes, and terminal recovery records a suppressed receipt
instead of creating a Check or comment. Full Go tests, focused race/vet, diff
checks, and rebuilt Linux Runner/terminal-reporter images passed. Both worker
images were then deployed locally. Two historical `running/publishing` jobs
also exposed an unowned `NULL` lease that the recovery scanner could not
claim. The ordinary and exact-run claim paths now treat that missing lease as
expired; an isolated, freshly migrated PostgreSQL integration proved both
paths, and the temporary test database was removed. The rebuilt runner
reclaimed those two jobs, marked them failed, and the terminal reporter
retained one unpublished `status` receipt for each with an explicit
"provider publication was suppressed" reason and no external ID. The other
13 malformed rows are in ineligible run/installation states; they were not
rewritten or silently archived.

RabbitMQ's quorum processes had previously crashed when Docker disk space
reached zero. After free space returned, a broker restart recovered all but
the zero-message `openreview.interaction.response.v1` queue, whose Raft log
reported `missing_segment_header`. Only that queue was deleted and recreated;
its exact binding was checked, and the other queues and PostgreSQL data were
preserved. The Review execution, terminal, and interaction-response queues
now report `running` and empty. The Review DLQ still contains 73 historical
messages and has not been purged. A 2 GB broker free-disk watermark is now
declared in the Compose-mounted RabbitMQ config and applied to this running
broker; `docker compose config` validated. The Compose healthcheck now checks
that every declared queue reports `running`, because the old node-only ping
stayed green while all quorum queues were down. The new healthcheck definition
has not been applied to the current container, which was deliberately not
recreated. An isolated RabbitMQ 4.1 container loaded the mounted file as its
second config source and reported the 2 GB threshold, then was removed. This
is an early-stop safeguard,
not proof that Docker Desktop has adequate disk capacity. The existing broker
was not recreated merely to apply the Compose mount: its current Erlang node
name derives from the container hostname, so a careless recreation could
orphan its persisted quorum state. Any node-name migration needs a separate
backup and recovery plan.

Two local GitLab OAuth command runs remain `acknowledged`. Their original
published response envelopes predated the required tenant ID and had failed
validation. Two exact, tenant-bound successor outbox events were inserted
without editing the original records, but the deployed responder rejected
both because its OAuth encryption key is absent from the current Compose
environment. Both successors reached the DLQ after bounded attempts; no
provider acknowledgement or review execution was claimed. Restoring the
**original** encryption key and GitLab OAuth refresh configuration, or
reauthorizing a new installation with a newly configured key, is required
before this real local GitLab command chain can be accepted. The current
`.env` has no `PROVIDER_CREDENTIAL_ENCRYPTION_KEY`,
`GITLAB_OAUTH_CLIENT_ID`, or `GITLAB_OAUTH_CLIENT_SECRET`; the original
encryption key cannot be regenerated from the stored ciphertext. The Jev key
is present in the source-admitter process, but that only establishes the
decision-layer dependency. A live coding-Agent handoff additionally needs a
configured `AGENT_TASK_ADAPTER_SECRET`, a separately isolated adapter with
installation-scoped repository credentials, and an approved Agent policy/task.

The local Console sign-in was also checked in a browser. Its current
`OPEN_REVIEW_APP_URL` is the public Console origin, so a request opened at
`127.0.0.1:3110` previously sent the user to Casdoor with a callback to that
other origin. The login and callback routes now reject this loopback-origin
mismatch before an authorization-code exchange; the sign-in page explains the
exact callback requirement instead of offering a looping SSO action, and
failed attempts retain the safe `next` path. A temporary Next.js dev server
rendered that warning, preserved `/acme/agent-work` through the error redirect,
and loaded the public home route. This does not establish an authenticated
Agent Work visual run: a matching local callback has not been configured and
verified for the current deployment profile.

On 2026-09-24, the running source-admitter was checked for a non-empty Jev
credential without reading or logging its value. The opt-in synthetic live Jev
protocol test passed again with the current deployment key. At that audit point
the local database had zero repository Agent-task policies. On 2026-09-27 it
has two rows, but both are `disabled` with automatic admission off. The key
alone therefore does not enable Issue admission; the agent-task runner also
has no adapter URL. A real Issue-to-Agent-to-Draft-PR run remains unaccepted
until a chosen
repository policy is enabled, its bounded plan is approved, and an isolated
executor is configured and verified.

The public Console origin `https://review.rainlib.com` returned 200 for its
home, sign-in, and health pages. A real in-app-browser click on the SSO action
reached `https://casdoor.rainlib.com` with a callback to the same public
Console origin and displayed the Casdoor password form. That browser has no
authenticated session, so the callback, workspace guard, and live Agent Work
page remain unverified in this run; no account credentials were entered.
The Agent Work queue now navigates with stable `?task=<id>` links instead of
holding the selected detail only in client state. The server reloads the
selected task on each route refresh, and the task-detail request runs in
parallel with policies, tasks, and installations. This addresses stale detail
after source verification or plan approval, but authenticated click-through
and provider-comment deep-link acceptance still require a signed-in browser.
The Agent Work inspector now links a feedback child task to its actual GitHub
pull request or GitLab merge request, while an Issue-origin task still links
to the Issue. Policy save, plan creation, plan approval, and task cancellation
surface the control plane's rejection instead of failing silently. Focused
ESLint, TypeScript, and a production Next build passed; the rebuilt local
Console returned HTTP 200 at `/api/health`. An unauthenticated Agent Work
request still redirects to sign-in, so these controls have not been accepted
through a signed-in browser session.

An operator-only recovery path has been added in source for an acknowledged
Review comment/retry run whose provider progress reply exhausted its durable
delivery attempts. It requeues the original marker-keyed reply at most three
times, requires the exact run revision and an idempotency key, and cannot
release execution before the provider accepts the reply. An isolated PostgreSQL
integration covers active-delivery rejection, replay, the retry bound and the
unchanged review-run state. The control API and Console were rebuilt together
and deployed locally; health probes returned 200, and an unauthenticated retry
returned 401. An authenticated browser operation has not yet been exercised.
The detail-page recovery action now comes from a server-side eligibility read
model rather than merely checking `acknowledged`: owner/admin visibility requires
the latest original-marker reply to be published, released by the responder at
its delivery limit, a valid installation and setup, and unused manual retry
capacity. PostgreSQL integration verifies that active delivery, a newly queued
retry, exhausted manual capacity, and a viewer each hide the action; the
mutation still rechecks all conditions under the run lock. The updated local
API and Console images passed production builds and returned 200 health probes;
the evidence endpoint returned 401 without authentication.
This is not evidence that the two historical
GitLab OAuth replies can succeed: their original encryption credential and
refresh configuration are still missing. No live provider retry was triggered.

## Local provider-handoff evidence

On 2026-09-21, isolated local Next output directories and synthetic
provider/control-plane fixtures exercised Console BFF handoffs without using a
real provider account. GitLab covered local session → authorization redirect →
PKCE callback → server-side credential receipt → installation creation; its
fixture observed an `authorization_code` exchange with a code verifier. The
former GitHub fixture only covered installation redirect and signed state; it
predates the mandatory server-side GitHub user-authorization proof and is no
longer counted as current callback acceptance evidence. The current GitHub
route requires code exchange and `GET /user/installations` confirmation before
it can issue a receipt. Across the covered flows, installation requests
contained only authorized identity, credential reference, scope and review
policy fields, never an access or refresh token, and the signed browser
authorization receipt was cleared after the control plane returned `201`.

This fixture proves the covered internal handoff boundaries and receipt
consumption contract. It does **not** by itself prove a GitHub user-authorization
callback, a GitHub App installation, or any GitLab.com/self-managed GitLab
OAuth application, callback registration, inventory permission, webhook or
provider write. The real GitHub evidence below now covers the GitHub path; the
GitLab paths remain required external acceptance evidence.

On 2026-09-22, a second isolated GitLab run extended that handoff through the
durable worker and complete onboarding state. A local provider fixture enforced
the signed state and PKCE challenge, returned a mock identity and one repository,
and was exposed only long enough for the containerized read-only probe. The first
credential write failed closed with `503` when the encryption key was absent;
after the deployment-owned key was supplied, access and refresh tokens were
stored as non-empty `bytea` ciphertext. Installation creation then failed closed
with `403` while the BFF and control plane used different receipt secrets; the
matching deployment secret allowed the signed receipt to create the installation.
The first probe attempt also failed safely under the normal worker, which could
not resolve the isolated credential. A temporary worker with the matching key
completed attempt 2 with `authenticated_identity:read` and
`repository_inventory:read`, synchronized `acme/demo@main`, and advanced the
browser through coverage, privacy boundary, merge threshold and rules to the
`complete` setup checkpoint. Audit and outbox searches found zero occurrences of
the mock access-token plaintext. This proves the internal GitLab OAuth, encrypted
credential, receipt, probe, inventory and onboarding chain; it still does not
prove GitLab.com, a self-managed GitLab deployment, webhooks, comments, reactions
or commit-status writes.

## Real GitHub command evidence

On 2026-09-21, the locally self-hosted stack used the configured GitHub App
installation and a signed-in workspace to process a real command on
[RainLib/open-review-platform#3](https://github.com/RainLib/open-review-platform/pull/3).
The final verification command was accepted at `08:02:26 UTC`; its durable
outbox handoff completed at `08:02:27 UTC`, and GitHub displayed the visible
[queued acknowledgement](https://github.com/RainLib/open-review-platform/pull/3#issuecomment-5757292561)
at `08:02:29 UTC`. The run completed at `08:03:10 UTC`, after which GitHub
received a separate [terminal celebration and result](https://github.com/RainLib/open-review-platform/pull/3#issuecomment-5757299871)
at `08:03:13 UTC`.

The responder now carries the immutable interaction creation time with each
marker-keyed response. GitHub marker lookup is restricted to comments updated
after that boundary: a marker cannot predate the command, while an ambiguous
provider timeout remains discoverable on retry. This reduced this real PR's
acknowledgement path from roughly seven seconds of historical-comment paging
to roughly 2.3 seconds without treating provider delivery as exactly-once.

This proves the GitHub command interaction order and the local P95 target for
this sample only. It does not prove GitLab delivery, GitHub Check Run retry,
notification delivery, or a workload-level P95. External GitHub Issue creation
is covered separately below; duplicate-delivery idempotency against GitHub is
not yet exercised. The browser reconnect path is covered separately below.

At `08:20 UTC`, a further real `@openreview review --force --mode=security`
command on the same PR produced run `1aa5de9c-d777-497e-8493-22387846739c`.
It completed at `08:20:53 UTC`; its durable status receipt at `08:20:55 UTC`
contains the provider Check Run ID `105544475389`. A read after the run returned
that same GitHub App-owned `Open Review / Analysis` check as `completed` with a
`success` conclusion. This proves run-to-Check identity retention for a real
provider update, but not rate-limit/5xx retry behaviour or branch-protection
enforcement.

## Real GitHub review and Issue lifecycle evidence

On 2026-09-21, real review commands on the same pull request exercised the
bounded risk plan. A standard pass selected eight files and explicitly deferred
123; it completed in roughly 77 seconds with no findings. A security pass then
selected `apps/web/Dockerfile` as the single highest-risk deployment boundary
and deferred 130 files. The first implementation built a selected-file-only
synthetic tree, so repository searches could not see supporting changes in the
same pull request. That produced two false positives and the configured Issue
automation created [GitHub Issue #4](https://github.com/RainLib/open-review-platform/issues/4)
and [GitHub Issue #5](https://github.com/RainLib/open-review-platform/issues/5),
each with one successful durable provider receipt.

The OCR range now keeps the full admitted head tree as its synthetic head while
reverting only selected paths in its synthetic base. The model diff therefore
remains bounded to the selected paths, but code search and repository context
see every admitted pull-request change. Security run
`8ad1b93e-b26e-43d4-ac66-41fe59e1e443` reviewed exact head
`29a9a8ed4a16b3ef022d29a7c889be1fdeb53917` with one selected file and 130
deferred files. Docker Desktop interrupted its first worker attempt; the same
durable job resumed with attempt count two, completed with zero findings, and
published its summary and status receipts. The current GitHub
`Open Review / Analysis` check is completed successfully.

The same retained run exposes job-level execution claims separately from stage
history: the live Evidence API and authenticated Checks tab show `2 job claims`
and `6 stage records`. This preserves the resumed Docker-interrupted execution
without pretending that every stage ran twice; reconstructed historical stage
rows retain their conservative attempt/timing values.

Reconciliation advanced both internal aggregates to `resolved` revision 2 with
zero active occurrences. The two provider Issues were closed at
`09:30:07 UTC` with the false-positive explanation, while their immutable
creation receipts continue to point to the original provider records. The live
Console initially exposed `Resolved 2`; both records were then classified from
the authenticated Console as `false_positive`, advancing them to revision 3
and the `Suppressed 2` governance view with actor, reason, issue event and audit
event retained. Opening either row shows its exact head SHA, file deep link,
pull-request deep link, decision evidence and occurrence timeline. Migration
`000060` defines `pull_request_count` as historical distinct pull requests and
backfilled both records to one affected PR while leaving their active
occurrence count at zero.

This proves real GitHub Issue creation, receipt retention, aggregate
reconciliation, provider cleanup, and the Console history path. It deliberately
does **not** claim a true-positive Issue lifecycle or provider retry
idempotency: the created Issues were semantic false positives and were closed,
and neither receipt required a retry.

A later true-positive security run created
[GitHub Issue #8](https://github.com/RainLib/open-review-platform/issues/8).
Its generated body uses a heading hierarchy, a compact evidence table, exact
file-line links, pull-request and commit links, and collapsed traceability. A
clean rerun resolved the aggregate and closed the same provider Issue through
the durable close receipt. The live GitHub page was read back in the browser;
this evidence covers rendering and the generated-Issue lifecycle, not arbitrary
provider-Issue ingestion.

User-authored Issue triage is a separate workflow. `issues.opened`, `edited`
and `reopened` normalize into a revisioned durable job; generated/bot Issues are
ignored. The worker first publishes a marker-keyed acknowledgement, then updates
that same comment with a deterministic Markdown report built from structured
model output. Stale revisions no-op and model credentials are resolved only at
the worker boundary. A signed local replay for real
[GitHub Issue #9](https://github.com/RainLib/open-review-platform/issues/9)
produced one GitHub App comment and, after a transient TLS timeout, updated that
comment to the final hierarchical analysis without duplication. PostgreSQL and
RabbitMQ retained the completed revision and drained the queue. That first run
proved the local signed-webhook-to-real-provider-write path. On 2026-09-22 the
App registration reported `issue_triage:ready`; appending a hidden verification
marker to the real Issue body produced a GitHub-origin `edited` receipt and
completed revision 2. Removing the marker produced a second GitHub-origin
receipt and completed revision 3. Both revisions updated the same App comment
ID `5761024858`, whose provenance now identifies revision 3. The restored Issue
body SHA-256 exactly matches the pre-test hash
`3504a109e1f96aa63476268abc40962dd91b2654adc6f30610f6a89b145b1f67`.

Migration `000064` binds every admitted user-Issue callback to the same
tenant-scoped, payload-free connection evidence timeline used by pull-request
reviews. It records the durable job, Issue number, action and revision without
returning raw payloads, signatures or provider delivery identifiers. Existing
Issue #9 was backfilled and read back as `completed / opened / r1`; the Console
labels it as an Issue and derives its GitHub or GitLab deep link from the
verified installation rather than treating it as a missing review run.
An isolated authenticated local Console read against the real control-plane
database displayed that row as `Issue #9 · opened · r1` with a direct
`github.com/RainLib/open-review-platform/issues/9` link, alongside the existing
PR evidence rows. The temporary frontend and development-auth API were stopped
after the read; no workflow state was mutated.

After the disposable internal aggregates and their dependent receipts/events
were explicitly removed, the authenticated Issue Inbox correctly returned zero
live aggregates. GitHub Issues #4 and #5 remain closed provider records. The
Inbox intentionally represents durable Open Review finding aggregates; it is
not a mirror of historical GitHub or GitLab Issues, so importing those closed
provider records as live aggregates would fabricate current finding state.

Provider-authored Issue analysis now also has a separate management read model
and Console route at `/{workspace}/provider-issues`; it is not inserted into the
finding aggregate Inbox. Tenant members can filter the latest durable job by
state or search repository/title/author/Issue number, open the exact GitHub or
GitLab Issue, and inspect the structured analysis, revision receipts, model
route hash and prompt-policy hash. The API deliberately excludes raw Issue
body, credential reference, stable comment marker, model route content and
prompt content. An isolated PostgreSQL 16 database verified two revisions,
tenant denial, list/detail and receipt ordering. An authenticated browser read
against the real control-plane database displayed GitHub Issue #9 as Completed,
linked to `github.com/RainLib/open-review-platform/issues/9`, and rendered its
hierarchical analysis without raw GitHub `<details>` markup. The temporary
development-auth API/frontend were stopped after the read; the OIDC production
services remained healthy.

Provider acknowledgements now use restrained, state-bearing emoji semantics.
The stable Issue progress comment is published before an idempotent `eyes`
reaction is added to the source GitHub or GitLab Issue; accepted and rejected
`@openreview` commands likewise react on the exact trigger comment with `eyes`
or `confused` on both providers. Terminal Issue analysis uses one status icon
in its heading and keeps detail headings undecorated, so emoji communicate
state rather than becoming visual noise. Provider write tests cover ordering,
GitHub reactions, GitLab award emoji, and retry-safe duplicate handling.
Incoming reactions cannot create model work; they remain feedback evidence,
while Issue lifecycle events and authorized commands are the only task triggers.

Issue triage formatting is now a first-class versioned configuration section.
Workspace defaults and repository overrides can select Engineering, Bug report,
Feature request, Concise, Security, Incident, Product, Performance, Compliance
or Custom formatting, then combine 13 bounded
required Issue headings with seven visible report sections, language, result
limits, file links, collapsible detail, reaction feedback and trusted repository
formatting requirements. Editing a required heading switches the policy to
Custom so the displayed preset never misstates the effective contract. Every
new Issue-analysis revision stores the effective configuration
and SHA-256 in `provider_issue_analysis_jobs`; the Console exposes the snapshot
hash with the model and prompt provenance. Migration `000066` was applied to
the local PostgreSQL stack. An authenticated local browser verified the workspace
default, the `RainLib/open-review-platform` inherited repository view, all ten
format choices, Security preset selection, trusted repository guidance switching
the policy to Custom, the generated Issue template preview and an enabled
immutable-revision save action without persisting the test draft. The production Console build includes
`/{workspace}/review-config/issue-triage`.

Migrations `000069` and `000070` add the reusable named format catalog and align
all review-configuration section constraints with the `issue-triage` domain.
Formats are workspace-owned, immutable-revisioned, role-protected and audited;
applying one only changes the current draft. An empty PostgreSQL 16 run covered
catalog lifecycle, tenant boundaries, conflicts, Issue-triage configuration
save and all eight admission snapshots. The current Compose database was
upgraded without clearing existing data. An authenticated browser then created
and updated a reusable Security format to revision 2, published it to an
isolated repository scope as review-config revision 1, and restored inheritance.

Migration `000068` adds a separate idempotency ledger and monotonic analysis
attempt to failed provider-Issue retries. The retry reuses the retained Issue
body plus the exact model, prompt and Issue-format snapshots, while revision and
attempt fences prevent older deliveries from replacing newer state. The current
Compose database has applied the migration without clearing existing data, and
an isolated PostgreSQL 16 lifecycle test covers same-key replay, concurrent-key
conflict, attempt-aware outbox/audit evidence and stale-worker no-op behavior.

Migration `000067` adds a recurring, lease-owned GitHub feedback poll per
eligible completed analysis. `provider-feedback-poller` resolves the App token
only at execution, discovers the marker-keyed comment across bounded pages,
reads only `+1`/`-1`, and reconciles additions, re-additions, and removals in one
transaction with semantic-change-only audit rows. The Console exposes observed
and next-poll timestamps instead of implying webhook freshness.

## Local Console evidence

On 2026-09-21, all 34 static core Console routes were checked at a 390 px
viewport. The narrow Issue and Pull-request lists use full-detail deep links;
the details exposed their tab states and provider file/PR links without
horizontal overflow. The Pull-request and CLI Review cards, plus all three SSO
views, were corrected to use bounded grid/flex items after the check exposed
their narrow-layout overflows. All 34 routes were also checked at the 1024 px
and 1440 px desktop viewports with no page overflow. The global theme cycle was
verified as system → light → dark → system, and URL-backed SSO tabs were
verified with arrow-key focus movement and Space activation. A clean, isolated
Next production build compiled every Console and BFF route.

The remaining dynamic object routes were checked at 390 px as well: Issue
detail, one Review's Overview/Findings/Files/Checks/Activity tabs, Task detail,
CLI run detail, Rule detail, and Connection detail. All ten rendered without an
error or page-level overflow; their expected tab counts and provider deep links
were present where a provider surface exists.

The global command palette was exercised with its visible trigger and `Meta+K`:
filtering `CLI reviews` then pressing Enter reached its durable run index, and
Escape restored focus to the control that opened the palette. The workspace
menu exposed the current workspace, all-workspaces directory, and the separate
workspace-creation entry without placing creation inside the Console flow.

On 2026-09-22, an authenticated local browser run repeated the keyboard
contracts against live control-plane data. `Shift+Tab` from the command search
wrapped to the final workspace action, `Tab` wrapped back to search, and
`Escape` restored the element focused before `Meta+K`. The Issue auto-create
policy dialog and API/CLI-key creation dialog both moved focus into the dialog,
kept forward and reverse tab order inside it, closed on `Escape`, and restored
their opening controls; no policy or credential was created. The first `Tab`
on SSO focused “Skip to main content”, and activation moved focus to the unique
`main#main-content`. The tenant has no configured SSO draft, so the enforcement
dialog was not opened or mutated during this run.

A later isolated SSO lifecycle run supplied an enforcement-ready OIDC revision,
successful metadata receipt, verified domain and role mapping under a temporary
workspace. The Overview rendered `Ready to enforce`; opening the enforcement
sheet focused the break-glass owner field, reverse Tab wrapped to the final
action, and Escape restored `Review enforcement`. Local control-plane calls then
recorded one `sso.enforced` and one `sso.suspended` audit event. Browser reloads
showed `SSO policy enforced` and `Enforcement suspended` respectively, and the
suspended Identity-provider tab remained editable for the required new-revision
repair path. This run found that the readiness card continued to instruct an
already-enforced workspace to enforce; the component now distinguishes ready,
active, suspended and blocked guidance. The API continued to expose only
`secret_configured`, and audit metadata contained no secret reference. The
temporary workspace was deleted after verification.

The local unauthenticated boundary was also checked directly: `/acme/home`,
`/setup`, and `/workspaces` each returned `307` to Sign-in with their preserved
safe return path. This verifies the unauthenticated entry guard; it does not
substitute for an initialized/uninitialized authenticated-workspace lifecycle
run.

With the local authenticated preview session, the workspace directory and
creation form rendered at 390 px without overflow. Direct `/setup` without a
tenant safely returned to the directory, while visiting Sign-in returned the
existing session to its requested workspace path. The creation form visibly
kept hosting, region, and identity as deployment-owned boundaries and no create
submission was performed during this check.

On 2026-09-22, a separate development-auth identity started with no memberships
and the live workspace directory rendered `No accessible workspace`. A direct
Console URL for an absent slug returned to `/workspaces?notice=access_denied`
without disclosing tenant existence. The browser then created one uniquely
named temporary workspace through the real Console BFF and control-plane API;
its directory row showed `Connect Git`, while a direct Console URL returned to
the tenant-scoped Connect step. A fixture installation was subsequently written
only for this temporary tenant as `verified`, with a durable `review_scope`
checkpoint. The same direct Console URL then resumed the Review scope screen,
and the directory changed to `Setup incomplete`. Finally, a retained readiness
snapshot and `complete` checkpoint changed the row to `Ready`; `/setup` and the
directory entry both resolved to the live empty Cockpit. The fixture did not
contact GitHub, and the exact tenant plus all cascading rows were removed after
the browser run.

A second isolated run on 2026-09-22 exercised the Models/BYOK separation-of-
duties flow with two concurrent authenticated Console instances. The Owner saw
active route revision 1 (`model-a`), changed only the model to `model-b`, added
a reason, and submitted proposed hash `68f35e4d8390…`. The Owner's Console
showed the active revision unchanged and exposed no decision controls; a direct
self-decision attempt returned `403`. A different Admin loaded the same pending
hash, supplied an independent review note, and approved it through the Console.
The active route then rendered revision 2 and `model-b`; PostgreSQL retained one
approval by the Admin plus `requested`/`decided` audit rows attributed to the
two distinct subjects. No model probe receipt was created, so this run incurred
no provider call. The exact tenant and all cascading rows were removed after
verification.

On 2026-09-21, the authenticated `rainlib-open-review` Console was also
checked against live control-plane data: the Issue Inbox first-use state,
dark-theme rendering, and search-to-severity keyboard focus order were visible
and correct. Narrow authenticated responsive/a11y coverage remains required.

The real completed run `1aa5de9c-d777-497e-8493-22387846739c` was then opened
in a fresh authenticated browser session. A reload retained all seven durable
events and, after 6.5 seconds, still showed `Live updates` rather than
`Reconnecting`. The Control API now disables Kratos' transport-wide one-second
deadline and applies a 30-second application deadline to ordinary requests;
only the exact authenticated run-event SSE route is exempt and remains bound
to browser cancellation. A fresh production tab reported no browser errors.
Evidence timestamps now use an explicit UTC formatter, removing the observed
server/client hydration mismatch.

## Disposable self-managed GitLab CE evidence

On 2026-09-22, GitLab CE `17.11.2-ce.0` ran as a disposable local container on
the Open Review Docker network. A deployment-owned, repository-scoped token
completed the worker's read-only verification before it admitted
`openreview-local-e2e/offline-validation`. A real merge-request webhook then
created a durable run; a distinct mapped GitLab user sent
`@openreview review --force --mode=standard`, received the queued
acknowledgement, and the run completed with all six durable stages succeeded.
GitLab received one `Open Review / Analysis` commit status with `success` and a
Console evidence deep link, plus a marker-keyed summary and terminal comment.
The linked Console page showed the exact head, immutable configuration, 2/2
provider receipts and the completed state. The disposable overlay now builds
its mounted worker binaries in a pinned Go `1.25.14` Linux builder for the
Docker server architecture; the post-recreate Runner stayed healthy with no
restart, so the evidence does not depend on host-architecture emulation.
After the local provider-worker configuration was recreated, a second mapped
GitLab-user command created completed run `a0b94834-80a1-4bad-831d-33c9bbe20fdf`.
GitLab itself returned its `Open Review / Analysis` status as `success` with an
evidence target on the actual Console host port `127.0.0.1:3110`, confirming
the acknowledgement-to-terminal path and browser-reachable link after rebuild.

The same CE project received a user-authored Issue Hook and one structured
`More context required` triage comment. GitLab CE `17.11` emitted an Emoji Hook
with `event_type=award` and no `object_attributes.action` for a real thumbs-up;
the feedback record became `useful|active`. Removing that exact award emitted
`event_type=revoke`, again without `action`; the same record became
`useful|retracted` and retained `provider_issue.feedback_deleted` audit
evidence. This establishes the self-managed deployment-token path for scope
verification, MR review, provider comments, commit-status publication, Issue
triage and emoji feedback. A registered OAuth application on that same
disposable CE instance then completed the real Console redirect, code exchange
and `/api/v4/user` identity read: the authenticated callback entered the
installation-selection step, while PostgreSQL retained non-empty encrypted
access and refresh ciphertext plus the actor binding. After the provider-call
workers were recreated with the verified local credential, command run
`891597ff-a75b-4ef4-8061-d63b8054f248` completed and GitLab returned
`Open Review / Analysis = success` with the matching Console evidence URL.
This initial run did not prove GitLab.com, a production self-managed HTTPS
ingress, or GitLab-side ambiguous-write recovery; those remain separate
acceptance gates. Model-produced blocking evidence was subsequently obtained
below. The project did enable **Pipelines must
succeed** and a controlled non-optional external status changed its MR to
`ci_must_pass`; restoring that same status to success returned it to
`mergeable`, proving the GitLab CE enforcement transport independently of the
model-quality gate.

The same disposable CE instance subsequently completed the OAuth-backed path
without reusing the deployment-token installation: a fresh workspace selected
one non-overlapping GitLab project, received an encrypted OAuth credential,
passed its read-only identity/inventory probe (`verified|live` with three
inventory rows), and advanced in order through `review_scope`, `learning`,
`severity`, `rules`, and `complete`. The completed readiness snapshot retained
the GitLab scope, explicit `governed_policy` boundary, and tenant general
merge-policy revision 1. This run exposed a local-only overlay defect where a
new installation inherited GitLab.com's API base; the overlay now persists the
private CE API base, and the first failed probe remains durable recovery
evidence rather than being hidden or retried as a false success.

The OAuth-only execution regression was then repeated with every GitLab
execution worker started with `GITLAB_TOKEN` unset. A real MR command completed
through the provider after the queued and terminal response messages were
amended to carry the durable installation tenant identity required for OAuth
credential resolution. GitLab returned the final `Open Review / Analysis`
status as `success` with a non-empty target URL; its MR contained the updated
queued response and the terminal `🎉 Review complete` response. Every responder
inbox delivery completed on attempt one and the durable status/summary
publication receipts contain no error. This proves that self-managed GitLab
execution does not silently fall back to a static deployment token. It remains
local CE acceptance only, not GitLab.com or production HTTPS evidence.

The continued real MR #5 exercise proved the complete blocked-to-fixed flow:
`9ef385fbdadc2f88272ce698b17255ce699300d7` introduced an unsanitized email SQL
query. After fixing the focused scope omission described below, run
`2de66484-f3b4-44b3-bfbc-945f2ef8f373` selected `accounts/lookup.go`; the actual
model returned one `critical/security` finding at line 12. GitLab retained one
inline discussion, the evidence report and a `failed` Analysis status with an
evidence link. Its API returned `detailed_merge_status=ci_must_pass`. Pushing
the parameterized-query fix `0c1f5379e1411034f29bf3ba63833d7b723dbc1e` triggered
run `c7d376d7-09a3-4434-bb78-e96acdd96944` through the real push/MR webhook,
without a second command. GitLab then returned `success` and `mergeable` for
that new SHA. The MR was not merged and its source sample was never deployed.

## Current local Console route evidence

On 2026-09-22, the running local Console was authenticated as the owner of
`gitlab-offline-e2e` with `OPEN_REVIEW_CONSOLE_DEMO=false`. A server-rendered
route sweep followed the three deliberate canonical redirects (Review
Configuration → General, Settings → Members, and the legacy Platform Health
path → Health) and reached HTTP 200 for all 35 static workspace pages. Every
final response remained in the same tenant, contained no Next error overlay or
server-error marker, and never rendered the demo-data notice. The real GitLab
review detail for run `891597ff-a75b-4ef4-8061-d63b8054f248` and the real
provider-Issue analysis detail for job
`44ae811a-057b-4a16-af47-36958892fa1e` also returned 200 under that session.
This proves local session, BFF, control-plane read and canonical-navigation
coverage; the browser automation service was unavailable, so it deliberately
does not claim screenshot-level visual, focus, zoom, or screen-reader
acceptance.

After the GitLab gate and model-scope fixes, the local Control API, Runner,
Model Prober and Console were refreshed from the tested source while retaining
their existing runtime configuration and business data. The refreshed Console
image is `sha256:e85c1ab5d759361597fab72e699807804e0bc7f86cc40bfb862bedbbb99df2bf`.
The API health endpoint returned 200; all four refreshed services remained
running with zero restarts, and the Console health check passed. Authenticated
HTTP reads of Workspaces, the OAuth-only workspace Home, provider-qualified
Models, Reviews and the completed MR #5 task returned 200 without a server
error marker or demo-data notice. The BFF evidence endpoint returned the exact
completed run `c7d376d7-09a3-4434-bb78-e96acdd96944`; the Models endpoint retained
the requested GitLab provider and private API base instead of collapsing to an
unqualified repository name. This refresh check is HTTP/API evidence, not new
visual-browser acceptance. Full Go tests, the frontend production build and
the isolated model-scope/rule-test lease database regressions passed before
the refresh; no repository commit or push was performed.

On 2026-09-22, migration `000074_issue_saved_views.sql` was first applied to an
isolated PostgreSQL 16.10 fixture. Five non-skipped database regressions passed:
personal/workspace visibility and revision permissions, the two 100-view caps,
tenant-safe grouped predicates with anchored pagination, retained rule
attribution, and bidirectional keyset scope binding. The same migration was
then applied to the disposable local GitLab CE stack. Its refreshed Control API,
Runner and Console (image `sha256:a7314002276cdb214acedfd5a9d5f9383cdca6bed2e3703e498f14fc753aff94`)
were healthy. The loopback-only authenticated BFF verifier created, reloaded,
renamed, stale-updated, rejected malformed filters, and removed its own saved
view while preserving pre-existing records; it made no provider or model call.
The test-lab processor also now fails closed unless it can replay the source
job's immutable model route, prompts, categories and selected-path execution
plan. This is replay-context parity, not implementation evidence for Shadow or
Canary rollout.

## Completion gates

### Current-source gaps confirmed in the continued audit

- Rule rollout governance now has an immutable baseline/candidate binding pair,
  stable per-review cohort hashing, optimistic revisions, audit events and
  pause/rollback transitions. An admitted source run now queues an idempotent,
  provider-silent Shadow replay using its frozen execution plan and candidate
  snapshot; completion persists normalized-fingerprint added/removed/matched
  evidence. The isolated PostgreSQL migration and regression prove tenant/scope
  pairing, replay queue idempotency, candidate replacement, comparison totals
  and stale-transition fencing. Canary admission now requires a completed
  Shadow comparison from a different owner/admin, selects the published
  candidate by a stable PR cohort inside the admission transaction, and
  persists the binding/version/bucket decision per run. Pausing or rolling
  back affects only later runs. Migration `000093` retires and audits older
  unevidenced Canary rows before this behavior can become visible. Isolated
  PostgreSQL tests prove candidate selection, out-of-cohort baseline and
  pause fallback. The Console now exposes Shadow pair creation, comparison
  evidence, independent Canary approval, percentage selection, pause, resume
  and rollback; direct active-binding creation/promotion for the same
  rule-set/scope/filter pair is rejected by the Control API. Permanent
  The source now has staged 1%/5% → 25% → 100% exposure and an independently
  approved promotion operation that switches the binding pair atomically after
  a complete observation window and passing candidate reviews. This has
  isolated-database evidence below and is deployed to the local API/Console,
  but is not yet accepted against a real provider; false-positive and
  product-quality metrics remain separate
  release gates.
  A source-only `000094` monitor now accepts a per-Canary distinct-failed-PR
  threshold and observation window, rechecks terminal candidate-selected
  failures under a row lock, writes rollback reason/audit, and leaves frozen
  run snapshots unchanged. Isolated PostgreSQL tests cover quota exclusion,
  baseline failure exclusion, repeated heads, threshold and idempotence.
  The local Compose images include only the earlier controls through `000093`;
  `000094` and the monitor are not deployed yet, and the authenticated rollout
  screen and a real provider review have not been accepted in a browser.
- Issues now implements the approved saved-view and nested AND/OR filter
  contract: personal/workspace authorization, optimistic revisions, audit,
  bounded strict expressions, rule-occurrence filtering and cursor-bound time
  anchors. Domain/API tests, five isolated PostgreSQL regressions, frontend
  serialization tests, an optimized Console build, and the deployed local
  GitLab BFF smoke all passed on 2026-09-22. The browser visibly rendered the
  compact Saved views control and progressively disclosed filter dialog. A
  complete keyboard/focus and Light/Dark browser sweep for this new dialog is
  still required before calling its visual accessibility acceptance complete.
- Local GitLab integration is not disconnected deployment acceptance: the
  configured model still uses an external gateway. An internal-model,
  outbound-denied run is required for that stronger claim.

### Additional core regression evidence

An isolated PostgreSQL database verifies model routes for the same repository
name on GitHub and two separate GitLab instances: probe deduplication/history,
independent exact-revision approval and restoring inheritance affect only the
selected provider/API-host/repository. Models pages, tabs and mutations carry
that complete identity. New unqualified repository routes are rejected;
legacy routes remain explicit inherited compatibility sources.

The same disposable database verifies expired rule-test execution recovery,
including a restart with the same worker ID, polling recovery after broker
acknowledgement and concurrent reclaim. Renew/result/failure writes require
the current attempt and an unexpired lease; stale attempts cannot overwrite
recovered work. The worker race test also proves that losing the lease cancels
its execution. These are actual database runs, not skipped integration tests.

The continued GitLab gate exercise found a source-level scope defect:
`accounts/lookup.go` received an empty focused scope because its path had no
security keyword, producing a successful check without model execution.
Focused review now admits ordinary application code, retains the eight-file
budget and prioritizes stronger boundaries; documentation/test/generated
deferral remains explicit. Real Git fixtures cover ordinary code, Unicode,
spaces/newlines in paths and the scope budget. Deferred-only reports and check
summaries now explicitly state that AI analysis did not run. The real MR #5
rerun above supplies the resulting high-severity blocking and remediation
acceptance evidence; the original empty-scope result remains historical
evidence of the defect.

Agent Draft PR/MR callback links now share a provider-qualified URL policy in
the adapter and control plane. Unit tests reject wrong GitHub/GitLab repository,
PR/MR number, host, credentials, query and cross-instance public-base mapping;
the callback stores the canonical link used by Console and Issue comments.
GitHub.com derives its web origin from `api.github.com`, while an explicitly
paired GitHub Enterprise or self-managed GitLab web origin is deployment-owned.
The full Go suite, relevant race tests, Compose validation and both Linux image
builds passed on 2026-09-23. This is source/build evidence, not a provider
Draft PR/MR acceptance run; no Agent policy or attempt exists in the current
local database and the optional adapter lacks an installed coding CLI and
repository-scoped write credential.

Agent repository admission now selects from the workspace's active, verified
provider installations and their worker-synchronized repository inventories;
the browser cannot submit an arbitrary host/repository pair from that form.
The inventory read applies the installation scope before its result limit, so
out-of-scope entries cannot hide an allowed repository. A disposable PostgreSQL
test with a one-item limit, TypeScript/Lint checks, an optimized Console build,
and a separate authenticated local browser preview passed on 2026-09-23. The
browser showed the verified GitHub repository becoming selectable and Save
enabling only after selection; it also exposed a clipped mid-width policy form,
which was corrected to a two-column responsive layout and visually rechecked.
No Agent policy or provider resource was created by this preview. Runtime
coding-agent publication still requires the adapter and provider acceptance
evidence listed above.

The core flow is not considered complete until every row has both source-level
contract evidence and its named runtime evidence. In particular, provider
fixtures cannot prove provider-side branch protection, and a Docker/compose
parse cannot prove a database migration, RabbitMQ recovery, or robot delivery.

On 2026-09-24 the restarted local source-admitter container was confirmed to
have a nonempty Jev key, URL, and model without exposing their values. This is
runtime configuration evidence, not an Issue-to-Draft-PR/MR acceptance run.
The separate coding adapter URL and handoff secret are still absent from the
running Agent task runner, so no coding execution can be claimed. A tenant-
scoped Review admission health sample now separately counts acknowledged runs
and marks their oldest wait degraded after 15 minutes, exposing a stuck
provider-progress/admission barrier that the execution-job queue omits. Its
PostgreSQL integration test passed. The control API image was rebuilt and
recreated locally; `/healthz` returned HTTP 200. An authenticated Health-page
read remains unverified because the available browser session stops at Casdoor
login, so the deployed queue card is not yet claimed as browser-accepted.

The current Console image was also rebuilt and recreated with the loopback
callback guard. A browser refresh at `127.0.0.1:3110` showed the mismatch
warning and removed the SSO action; `/api/auth/login` returned a local 307
to `error=origin` while preserving `/acme/agent-work`. The configured public
origin instead offered SSO and redirected to Casdoor with the matching public
callback, where the browser required a fresh user login. No authenticated
Agent Work page or provider operation was reached in this check.

## 2026-09-24 Jev and publication-threshold checkpoint

The current `.env` Jev key matches the running source-admitter container, and
one synthetic Issue input received a valid bounded response from the real
TypeSafe endpoint. This proves credential loading and the adapter protocol,
not an Issue-to-PR execution: the local database currently contains zero
Agent Work repository policies and zero Agent tasks. A repository policy must
be explicitly saved before a real Issue can enter the Jev decision path.

The onboarding wizard now exposes the installation publication minimum.
Migration `000091` freezes it on each newly admitted review run; the runner
evaluates and persists the merge gate over all retained findings, then omits
only non-blocking below-threshold findings from provider inline comments.
Gate-blocking and unknown-severity findings remain visible. A dedicated
PostgreSQL integration test and focused Go/TypeScript checks passed. Migration
`000091` is now applied to the local database, and the rebuilt Runner and
standalone Console are running. Console and Control API health returned HTTP
200; the public Console still redirects an unauthenticated browser to SSO.
All 79 preexisting runs retain a NULL publication snapshot, as intended; no
real run with below-threshold findings has yet proven the inline-comment
filter. Four
targeted PostgreSQL integration tests and the full store suite passed from a
fresh PostgreSQL 16 database migrated through `000091`. Two stale test
fixtures were repaired, and the outbox recovery test now confines its
assertions to its own message rather than assuming the entire outbox is empty.
The three disposable databases were removed after verification. An
authenticated browser session is still needed for the nine-step wizard and
Agent Work page acceptance.

The real GitHub Draft PR `RainLib/open-review-platform#6` was re-reviewed twice
on its unchanged head SHA. The first run (`b779f57d-026f-43f6-90c1-c128eecb7839`)
proved webhook command admission, a frozen `medium` publication threshold,
completed report/comment publication, and a durable status receipt. It also
exposed a merge-gate window: the provider still displayed a completed check
from 2026-09-21 while a new run was analyzing. Check publication now uses one
GitHub Check Run `external_id` per review job, retains the stable branch-
protection name, and searches all matching check runs for idempotent retries.
After rebuilding Runner and Terminal Reporter, the second real run
(`b6e52e4d-4df3-49bc-a047-8fb64768701c`) produced Check Run `107474327007`
as `in_progress` during analysis and then completed that same check with
`success`; the old Check Run `106333105875` was not modified. The PR widget
showed the new pending check, and its report, completion comment, and status
receipt all correspond to the second run. This verifies publication lifecycle,
not an enforced branch-protection ruleset or a blocked-finding scenario.
Provider Check Run semantics were verified against the GitHub REST Checks API
documentation. A concurrent `go test ./...` hit the existing time-sensitive
Agent adapter output-limit test under image-build load; that test passed alone,
and `go test -p 1 ./... -count=1` passed in full. The public Console browser remains
on SSO sign-in, and the live database still has zero Agent Work repository
policies and zero Agent tasks, so the configured Jev backend has not been
accepted through an Issue-to-Draft-PR flow.

A repeat opt-in TypeSafe Jev HTTPS smoke test with a synthetic Issue passed
against the current deployment key. Jev response-body truncation after an
HTTP success is now classified as a transient transport failure, so the
existing same-revision bounded source retry can recover it; oversized or
invalid protocol responses remain permanent failures. Unit tests cover both
branches and the source/decision/store packages pass. This still does not
enable any repository policy or prove provider Issue admission. The updated
source-admitter image was rebuilt and recreated; its fresh database heartbeat
is present, its Jev key is still injected, and the live database still has
zero Agent policies and tasks. The final source revision passed serial
`go test -p 1 ./... -count=1`, focused `go vet`, and `git diff --check`.

The development coding adapter now leases a distinct Linux UID/GID to each
concurrent CLI, transfers only its temporary checkout, clears supplementary
groups, and reclaims private ownership before trusted Git/provider work.
Its Compose image drops all container capabilities except the adapter's
ownership/UID management set. A Linux container test with the same capability
set verified that the CLI cannot read a root-only repository credential file
or its parent process environment and retains no effective capabilities;
the checkout returns to root-owned `0700` even if the CLI changes its mode.
The rebuilt adapter image is `sha256:e116cf241a6712b3ce1b34c973b49ee84bfd99c0ad8d015dd7083e5253354775`.
This remains a **development boundary**, not a per-task sandbox: PID/network
namespaces are shared, a child can escape its process group, and a later job
could reuse the same UID. Production startup now fails explicitly instead of
accepting provider write credentials under a misleading partial isolation
claim. Development startup additionally requires the explicit
`AGENT_ADAPTER_ALLOW_UNSANDBOXED_DEVELOPMENT=true` opt-in, so the Compose
default `ENVIRONMENT=development` does not silently activate this adapter.
No real Issue-to-Draft Agent task was enabled by this change.

The development Codex profile now has a job-scoped Responses broker distinct
from Jev admission. The adapter retains the fixed model upstream key, while
the CLI receives a short-lived capability and user-level configuration that
points only to the local broker. The broker fixes the model, disables remote
response storage/background mode, caps one response at 8,192 output tokens,
and limits request count/body and response bytes. It now rejects server-side
conversation/reference/template state and reserves at most 65,536 requested
output tokens and 1 MiB input bytes over the job. These are conservative
request ceilings, not a provider billing cap. A local TLS fixture proved
credential rewriting, redirect refusal, unauthorized-request rejection,
budget exhaustion and revocation at job end. The installed Codex CLI also
reached that fixture through its generated config and stdin prompt; a second
synthetic protocol fixture proved that, after a tool call, its next request
replays the tool result without `previous_response_id` or server conversation.
Neither test made a paid model call or provider write. Startup now rejects a Codex profile lacking
its broker or an executor UID pool; Claude startup remains fail-closed until
its own model credential path exists. This is a development integration step,
not a production network sandbox, verified provider cost ceiling, or real
Issue-to-Draft acceptance.

A second Linux container test ran a fake coding CLI under its leased UID and
successfully called the broker without inheriting the upstream model key.
Full serial Go tests, focused race and vet, Compose validation and a rebuilt
Linux adapter image (`sha256:7d6d41477ec63a7ee3bfa89a9da6327e71fe66b7e38caec9849114dbcb3b135a`)
passed afterward. The current local `.env` has the Jev decision key but not
the separate Codex coding-model broker values, adapter URL or signing secret.
No repository policy or real coding task was enabled.

An optional `adapter-codex` image target now installs Codex CLI 0.142.1 from
the official npm package using a digest-pinned Node builder. A local Linux
image build (`sha256:8ebf0304e996c1a9fd24d071c4ebca5ef36c50c6d79c9e93c2834000960e93e4`)
passed its in-image version assertion; the CLI also reported that exact
version when run as the leased non-root UID. The same image refused
`ENVIRONMENT=production` at startup. This unblocks a **disposable development**
executor smoke once separate coding-model and adapter settings are supplied;
it does not satisfy the per-job sandbox or real Issue-to-Draft acceptance gate.

The optional Codex image was rebuilt as
`sha256:336a20b34619a7c9e228c745ae1020e1a3cce0c19da38b37270db9674d86d134`.
Inside that Linux image, both installed-CLI protocol tests passed with the
adapter's Compose-equivalent read-only filesystem, `no-new-privileges`, and
capability set: the CLI reached only its job-scoped broker with a fixed model
and `store=false`, then completed a second request containing the prior tool
result without server-side conversation state. The upstream was a local TLS
fixture, so this verifies the packaged CLI and protocol, not a paid coding
model, provider write, production sandbox, or Issue-to-Draft flow. The live
source-admitter still has its Jev key/model/URL injected; the separate coding
model, adapter URL, and signing-secret settings are absent from local `.env`.
The two-turn fixture now also uses the adapter's leased Linux UID rather than
running the CLI as root. Under those same Compose-equivalent restrictions it
completed the tool-result round trip. Its disposable workspace has a
traversable parent matching `/workspaces`; Go's default private test parent
would otherwise fail before the CLI could start. This validates the actual
development identity path, but it does not change the production sandbox
block or prove the separate coding model's real API.

On 2026-09-24, the disposable GitLab CE OAuth project
`openreview-local-e2e/oauth-onboarding-1790064971` gained Issue and Emoji
subscriptions on its existing webhook, preserving the callback, token and
MR/Note subscriptions. A user-authored acceptance Issue (`#1`) produced a real
`Issue Hook`, one tenant-scoped triage job and a published acknowledgement
outbox message. The consumer did **not** post an acknowledgement: its current
inbox was released after six attempts with `GitLab OAuth credential resolver
is not configured`. The current worker/control API processes have no
`PROVIDER_CREDENTIAL_ENCRYPTION_KEY`, so the previously encrypted OAuth token
cannot be opened; static `GITLAB_TOKEN` is also absent. No model inference or
OAuth-only Issue success is claimed. The test Issue remains open in this
disposable project so an explicitly restored original key (or a fresh OAuth
authorization under a new key) can be followed by a deliberate replay.
The stored OAuth access token is expired, and the current workers also lack
`GITLAB_OAUTH_CLIENT_ID`/`GITLAB_OAUTH_CLIENT_SECRET`; recovering the old
credential therefore requires both decryption and refresh configuration.

The tenant Platform Health read model now includes Provider Issue triage jobs
and current-revision consumer delivery failures without returning error text.
An isolated PostgreSQL 16 integration proved a released acknowledgement is
`1 ready / 1 failed / degraded` and preserved the owner/admin read boundary.
The rebuilt local Control API image (`sha256:df80be4a902c6588d3c2ca02fc33cfe7760e175d6355891a68e3907d16ad9c04`)
returned `/healthz` 200; a read-only query against the affected tenant also
returned `1 ready / 1 failed`. An authenticated Console rendering and recovery
replay remain unverified because the original OAuth encryption key is not
currently configured.

## 2026-09-24 Provider Issue terminal-delivery recovery

A real local GitLab OAuth Issue exposed an acknowledgement message dead-lettered after six inbox claims while the job remained `queued`. The control-plane retry now accepts a `queued` acknowledgement or `acknowledged` analysis only when that exact revision and attempt have exhausted the quorum queue's five-redelivery limit. Normal in-flight deliveries remain non-retryable. The Console inspector shows terminal delivery failure separately from job `last_error`; the retry republishes the marker-keyed acknowledgement with a new attempt fence.

A fresh isolated PostgreSQL 16 integration run passed normal/terminal delivery, owner/viewer access, stale revision, idempotent replay, and stale-attempt checks. The local control API image was rebuilt and `/healthz` returned HTTP 200. Real GitLab OAuth replay remains unaccepted because the original encryption key and refresh credentials are not present in the running Issue worker; the real Issue was not replayed.

## 2026-09-24 Agent source provider-read resilience

The source-admitter previously marked an Agent task `needs_attention` immediately on transient GitHub/GitLab metadata 429, 5xx or timeout, even though Jev calls already had bounded durable retries. Provider source reads now classify only transport/rate-limit/server failures as transient and use the same three-attempt, revision-fenced outbox chain for both Jev and deterministic policies. Each retry rereads the current Issue and exact base commit; 401/403/404, redirects, changed Issues, malformed/oversized responses and expired parent tasks still fail closed. A fresh isolated PostgreSQL 16 integration run passed deterministic policy retry admission and the existing Agent policy/approval/attempt lifecycle; focused provider protocol/timeout/size tests and `go vet` passed. This is resilience evidence, not a real Issue-to-Draft-PR/MR acceptance result.

GitHub App installation-token minting now uses that same bounded source retry
for 429/5xx and transport timeouts. Authentication failures, removed
installations and redirects remain terminal, and the signed App JWT is never
forwarded to a redirect target. Full Go, focused race/vet and redirect/timeout
tests passed. The rebuilt local source-admitter wrote a fresh heartbeat, and
an opt-in read-only live smoke test minted one token for the verified RainLib
App installation without printing it. This neither enables an Agent policy
nor supplies an isolated executor.

## 2026-09-24 Per-job container sandbox acceptance

The optional Agent adapter now has a deployment-owned Docker execution mode.
It preflights an immutable local image ID, the named checkout volume and an
internal-only network before accepting work, then repeats those checks before
every child. The coding CLI runs as a leased non-root UID in a new read-only,
capability-free container with bounded PID, memory, CPU and wall time; the
adapter keeps the provider write token, callback HMAC, Docker socket and model
upstream key. Only a revocable, job-scoped model capability reaches the child.
The UID-pool mode remains development-only. The Docker socket is restricted to
the trusted adapter on a dedicated execution host; this is a significant host
authority and not a general worker setting.

Two opt-in tests ran in a real Docker-internal network with the digest-pinned
Codex CLI 0.142.1 and a synthetic TLS Responses upstream. The first completed
a two-turn tool flow, checked that the child could not see provider/model/HMAC
secrets, the adapter state volume or the daemon socket, and verified container
removal plus root-owned checkout recovery. The second cancelled an active
model request and verified prompt child termination, removal and workspace
recovery. A third opt-in real-Docker test left a labeled child running, then
verified that adapter recovery removed it before admitting new work. Cleanup
selects the exact private workspace-volume label and fails closed if Docker
cannot list or remove an owned child. The rebuilt production-mode adapter
also started against a synthetic scoped credential file and removed a
deliberately left-running labeled child before listening; no task was
submitted. Its disposable container, internal network, volumes and synthetic
credential file were removed after verification. A separate restricted-container probe
could not reach external HTTPS. Full Go tests, focused race/vet, Compose
validation and diff checks
passed. This proves the local container boundary and broker path, **not** live
provider write safety, a production Docker-host hardening audit, a full
running-task process-kill/restart acceptance with durable receipts, multi-replica durable
receipts, or a real
Issue-to-Draft-PR/MR result. No Agent policy or task was enabled.

The recovery path now also removes only `agent-task-<decimal>` checkout
directories created by the adapter after owned children are stopped. A
similarly named but non-generated directory is preserved; a matching symlink
fails startup instead of following it. A focused filesystem test and a fresh
real-Docker orphan-container/checkout test passed. This closes the local
post-crash checkout-retention gap for the dedicated single-instance volume,
but not cross-node job recovery or real provider publication acceptance.

A separate abrupt-process-death test now starts an adapter job in a child
process, kills that process without calling `Close`, and opens the same private
receipt directory in a replacement process. Recovery emitted one
`agent_adapter_interrupted` callback, rejected a repeated signed start, did
not claim the control-plane start gate again, and did not relaunch the coding
executor. The full adapter race suite and focused vet passed. This test uses
a blocking synthetic executor and local HTTP fixture: it proves the receipt
and start-fence behavior under process death, not live Docker-child cleanup,
model execution, provider writes, or multi-replica durability.

## Immediate implementation order

2026-09-24 Agent Work Console recovery: the selected Issue/PR action now uses
the shared provider URL builder, preserving GitHub Enterprise and self-managed
GitLab relative paths instead of hard-coding GitHub.com. Agent Work loads
connections, policies, tasks and selected detail independently. A failed
section is shown as partial data rather than an empty list or a total page
failure; policy editing pauses without the authoritative revision, while an
available task detail can still be inspected. Focused URL/partial-response
tests, TypeScript, lint and the Next.js production build passed. A later
loopback-only development session authenticated against an isolated Control
API and inspected the live Cockpit, Agent Work, Issues, active/completed PRs
and Notifications routes. The two completed PR records and GitHub installation
came from the control plane, not demo fixtures. This does not validate
production OIDC, provider callbacks or a complete Agent execution.

2026-09-24 Jev admission check: the running source-admitter has a non-empty
`AGENT_DECISION_JEV_API_KEY`, and an opt-in synthetic Issue request to the
configured TypeSafe endpoint returned a valid bounded decision. No real Issue
was admitted or repository policy enabled. The Console previously offered a
Claude executor profile that the adapter cannot run; the option is now
disabled and domain policy validation rejects it until a separate credential
broker exists. The domain/store unit suites, focused vet, web lint and
production build passed. The first isolated PostgreSQL integration attempt
was interrupted while Docker was slow; a second run against a freshly
migrated dedicated database passed the full Agent policy/plan/attempt test,
including rejection of the unsupported Claude policy and preservation of
the frozen Codex envelope. Both disposable databases and the isolated
loopback browser/API processes were removed after their checks.

2026-09-24 Agent plan revision recovery: after a failed execution, the
Console again offers a bounded plan editor even when an approved older plan
is retained as evidence. A replacement transaction supersedes every older
pending plan; the approval transaction also requires the latest revision, so
an obsolete plan cannot be admitted through a direct API call. The Console
distinguishes an absent executor from other adapter/provider failures instead
of always advising deployment. The dedicated PostgreSQL 16 test exercised
replacement, stale approval rejection and post-failure reapproval; its test
database was deleted. Agent Work state tests, the full Go package suite,
focused vet, web lint and the Next.js production build passed. No real
 provider task was executed.

2026-09-24 Agent plan structure: the deployment-owned Jev key is present and
nonempty in the running source-admitter. Agent Work now captures objective,
scope/impact, verification, risks and unknowns separately. PostgreSQL stores
these sections alongside the canonical summary; only that summary is hashed
and delivered in the signed adapter envelope, avoiding a second instruction
source. Legacy free-text plans remain readable. A fresh, disposable PostgreSQL
16 integration database passed plan replacement, approval and execution
handoff with matching sections, summary and SHA; it was deleted afterward.
The full Go suite, focused vet, web tests, lint and production build passed.
The first local Compose rebuild saturated the shared Docker Desktop VM during
the Console image build; Console/API health requests then timed out. Docker
recovered without a manual restart. The project-local GitLab test container
was temporarily stopped to free build memory, then restarted without touching
its data volume; its health check and sign-in HTTP request returned healthy/200
after startup. Console and Go images built successfully; migration `000092`
was applied to the main database, and the replacement Console/API both returned
HTTP 200. An unauthenticated Agent Work request correctly redirected to sign-in,
so the new plan editor has not been visually accepted in an authenticated
session. Jev remains loaded in source-admitter; Agent policies/tasks are still
zero. No real provider Issue-to-Draft execution was performed. Coding-model
and adapter credentials remain separate prerequisites from Jev admission.

2026-09-24 Agent plan authorization follow-up: the Agent Work detail response
now exposes role- and state-scoped plan affordances with explicit block reasons.
The Console hides create/approve actions when the source revision is not ready,
the latest decision does not require a human, the actor lacks the required
role, or high/critical risk requires an independent approver. The API still
rechecks every mutation. Plan section length validation now uses UTF-8 bytes,
matching Go for multilingual text. Unit, isolated PostgreSQL integration,
the full Go suite, focused vet, web tests/lint and production build passed.
Console and Control API images were rebuilt and replaced locally; both health
endpoints returned HTTP 200 and the project-local GitLab returned healthy.
The source-admitter still has the Jev key. The local sign-in page reports an
expected callback-origin mismatch because `OPEN_REVIEW_APP_URL` points at the
public domain rather than `127.0.0.1`; an authenticated visual pass is not
claimed. Adapter URL/secret and coding-executor credentials are not configured,
so no real Agent execution was started.

2026-09-24 Agent Draft description evidence: the adapter's GitHub Draft PR and
GitLab Draft MR creation payload now carries a compact Outcome, Scope, Risk,
Acceptance mapping, Invariants, Verification, Rollout, Rollback, and Provenance
report. Changed paths are HTML-escaped and collapsed after publication; the
report states only the path/diff/whitespace/basic secret-pattern gates actually
run by the adapter and explicitly labels build, tests, SAST, performance, UI,
migrations, runtime blast radius, and product acceptance as unverified. Its
immutable attempt, plan hash, source and proposed head remain visible. The
disposable GitLab Issue→patch→Draft MR test asserted the provider creation
payload contains the evidence report; dedicated description/escaping tests,
the full Go suite, focused vet and diff check passed. This is source/fixture
evidence only: no coding adapter or provider write credentials are configured
in the current deployment, so no live Draft PR/MR was created by this change.

2026-09-24 Agent source-status closure: first-time source capture now queues a
provider Issue status, not only a recovery update. The message reports the
fresh post-reread classification: a plannable candidate awaits bounded plan
and owner/admin approval; rejected or context-deficient candidates explicitly
remain blocked. Automatic label candidates and explicit Issue commands both
retain a stable task-scoped source-status marker, and feedback Draft PR/MR
tasks keep their separate provider destination. Source retries still update
the same marker. A disposable PostgreSQL 16 database migrated through
`000092` and passed the Agent policy/plan/attempt integration test, including
first-time explicit and automatic Issue status payloads; it and its temporary
database proxy were deleted. The full Go suite, focused vet and diff check
passed. Provider-visible comment delivery is not claimed. Docker Desktop ran
out of space during a separate test-container attempt; only that disposable
container, five unused project images, and eight exact reclaimable project build
cache records were removed. The existing PostgreSQL recovered to healthy;
the project GitLab volume and database volume were not deleted. The Go image
was rebuilt, and both Control API and source-admitter containers were replaced
with it while preserving the GitHub App overlay. The Control API health endpoint
returned HTTP 200 and the recreated source-admitter retained its Jev key.
The project-local GitLab was restarted after the build; its Docker health
check returned healthy and its local sign-in route returned HTTP 200. These
runtime checks do not prove a provider-visible source-status comment was
actually delivered to an Issue.

2026-09-24 provider source-status retry check: GitHub Issue comments and
self-managed GitLab Issue notes were exercised against stateful HTTP fixtures.
The first source-status delivery created one comment; a later ready status
and a replay updated that same provider record, with no second creation.
The running source-admitter still has a nonempty Jev key; a new opt-in live
Jev request using a synthetic Issue passed. The live database has zero Agent
repository policies and zero Agent tasks. The full Go suite, focused publisher
vet and diff check passed. These fixtures and the synthetic request do not
prove live provider delivery or enable Agent execution; the coding adapter
URL/secret remain unconfigured.

2026-09-24 Agent source-status ordering: source failure and later recovery
now publish the frozen task revision with the stable provider marker. GitHub
Issue comments and GitLab Issue notes refuse to overwrite a higher revision
with a delayed older message, including a legacy unversioned replay. A fresh
isolated PostgreSQL 16 database migrated through `000092` verified first-time
and recovered outbox envelopes contain increasing versions; stateful provider
HTTP fixtures verified one created record and no stale regression. Full Go,
focused race/vet and diff checks passed. The temporary test database and
proxy were removed. These are sequential transport tests, not a live provider
ordering or multi-responder concurrency acceptance run. The running
interaction-responder image was subsequently replaced with the new source:
eight exact, reclaimable, unshared Go build-cache records were removed to
raise Docker VM free space from 2.6 to 5.6 GiB, then only that service was
built and recreated with the GitHub App key mount preserved. The resulting
image is `sha256:deb9fd20ad6cb4ae97835eab63a07ec65be1b506c291ce3bdcea5e41cac5b05f`;
its container is running and its database heartbeat is unexpired. RabbitMQ
and PostgreSQL remained healthy, and free space after deployment was 5.1 GiB.
The removed cache is rebuildable; no volume or business data was deleted.
There is still no live Issue status delivery acceptance for this version:
the local database has zero Agent repository policies and zero Agent tasks.

2026-09-24 Agent source-status multi-responder fence: the responder now checks
whether an Issue status is still current before resolving provider credentials,
then repeats that check while holding the source task row lock around the
provider comment/note write. Credential resolution occurs before the lock, so
the GitLab OAuth database lookup cannot deadlock a one-connection pool. An
isolated PostgreSQL 16 database migrated through `000092` verified stale
status suppression, legacy unversioned envelope suppression, and serialized
publication from concurrent callers. Full Go tests, focused race/vet, and
`git diff --check` passed; the temporary database and proxy were removed.
Only `interaction-responder` was rebuilt and recreated with the GitHub App key
mount preserved. Its running image is
`sha256:80b71b56b1a350f28bffc0db7d964c6bcef7faac3c3e63aefda21e2d602897d2`;
the database heartbeat is unexpired, RabbitMQ/PostgreSQL are healthy, and
Docker VM free space is 4.5 GiB. The running source-admitter has a nonempty
Jev key, but the live database still has zero Agent policies and tasks. No
real provider Issue status or multi-replica deployment acceptance is claimed.

2026-09-24 Draft feedback source gate: source-admitter now re-reads the exact
GitHub Issue comment or GitLab MR note that triggered `@openreview revise`,
after first checking the frozen Draft head. The provider comment ID, actor,
PR/MR identity and instruction SHA-256 must still match the immutable
admission receipt; edits, removal and identity changes fail closed before the
feedback can be planned. A verified feedback snapshot is then eligible for
the frozen Jev decision backend, while hard deterministic rejection still
precedes the model and any plan still needs owner/admin approval. Direct API
creation of a PR-origin Agent task is rejected; only the bounded completed
Draft feedback path can create one. This work also exposed and fixed an
ambiguous `id` in the source-target task/installation JOIN, which previously
prevented the worker from loading that target. GitHub/GitLab provider HTTP
fixtures, focused race tests, full Go tests/vet, and a fresh PostgreSQL 16
database migrated through `000092` verified the covered contracts; the
temporary DB/proxy were removed. GitHub's Issue-comment response includes
`issue_url` and GitLab's MR-note response includes `noteable_iid` per their
[official GitHub API](https://docs.github.com/en/rest/issues/comments) and
[GitLab Notes API](https://docs.gitlab.com/api/notes/) contracts. The rebuilt
Control API and source-admitter are running with unexpired worker heartbeat,
the source-admitter retained its Jev key and GitHub App mount, Control API
health returned 200 and an unauthenticated Agent route returned 401. Docker
VM free space was 3.8 GiB after that deployment. The signed runner-to-adapter
handoff now carries the frozen feedback binding; adapter intake rejects an
absent or malformed binding, and its pre-CLI and pre-push gates re-read both
the Draft head and original provider comment/note. Focused GitLab edited-note
and GitHub/GitLab source fixtures, full Go tests, race tests, vet, and a fresh
PostgreSQL 16 plan/lease/handoff integration passed. The rebuilt runner image
`sha256:edce8315742a649faf150370c43243e343099bf95095ba01e805bfbe917feb8b`
is running with a fresh heartbeat; the adapter image
`sha256:86460ab38515ce332d05feac3e8b8f6aaa339fc9b87728cf403354d6fe59bdb4`
was built but deliberately not started because adapter URL/secret and coding
model credentials are absent. The live database still has zero Agent policies
and tasks. No real feedback command or Issue-to-Draft PR/MR execution was
performed. The last read and provider push are not atomic, so a post-check
edit race remains subject to webhook supersession and reconciliation.

2026-09-24 Agent feedback Console provenance: an authorized child-task detail
now returns the frozen provider comment and actor IDs, without exposing the
instruction body or digest. Agent Work links to the exact GitHub Issue comment
or GitLab MR note, preserving self-managed GitLab's relative URL prefix and
supporting internal HTTP installations for browser navigation only. The link
formats follow [GitHub's Issue comment `html_url`](https://docs.github.com/en/rest/issues/comments)
and [GitLab's own MR note URL construction](https://gitlab.com/gitlab-org/gitlab/-/blob/101078dbbea74e77be827a6912aefff0da04daca/scripts/trigger-build.rb).
An isolated PostgreSQL 16 database migrated through `000092` passed the
feedback-child admission/read-model/plan/attempt test, then was deleted with
its temporary TCP proxy. Go package tests, front-end link tests, TypeScript,
ESLint and the Next.js production build passed. Control API image
`sha256:a6efb0dfc8d8dba01d52267a266eddd6375b73446a4a1a2b96998cf3a03e1976`
was rebuilt and deployed locally; `/healthz` returned 200. Four exact,
non-shared, reclaimable build-cache records from older Open Review Control API
builds were removed using [Docker's ID-scoped Buildx prune](https://docs.docker.com/reference/cli/docker/buildx/prune/),
without touching images, containers, database volumes, or RabbitMQ data. With
4.3 GiB free, Compose Console image
`sha256:376d284df91daf3b2b8f6309bacc61f77fc53d0be0f86e804d4b5f6756254ae7`
was then built and deployed. Console health returned 200, the container became
healthy, and RabbitMQ reported no alarms with 4.1 GiB free after deployment.
The browser showed the expected sign-in page and redirect from Agent Work,
but local `127.0.0.1:3110` is not the registered OIDC callback origin; no
authenticated Agent Work detail or live feedback-comment navigation has been
accepted yet. The current database also has no Agent tasks from which to
render a real feedback child.

2026-09-25 Jev and Canary admission checkpoint: the configured local Jev key
is nonempty, matches the running source-admitter container, and passed one
opt-in live HTTPS call with a synthetic Issue through the bounded response
parser. This does not create an Agent task or prove provider Issue admission.
Migration `000093` applied in an isolated PostgreSQL database; the rollout
integration test passed independent Shadow approval, selected candidate
snapshot and durable selection record, paused-baseline fallback, and a
low-percentage out-of-cohort baseline. The complete Go test suite passed.
These Canary changes were source-only at this checkpoint and had not yet been
deployed to the local Compose stack or accepted against a live GitHub/GitLab
pull request; the later deployment checkpoint below supersedes only the
deployment part of this statement.

2026-09-25 rollout control checkpoint: the Console gained authenticated
Rule Rollout proxy routes and a Shadow/Canary panel at Rule Bindings. Shadow
comparisons load on demand; Canary exposure requires a completed latest
comparison, an explicit acknowledgement and a distinct approver enforced by
the API. Binding mutations now reject direct same-scope active promotion and
state changes while an active or paused Canary owns either binding. The
isolated PostgreSQL rollout regression, full Go suite, frontend typecheck,
focused lint and production build passed. A local demo browser rendered the
empty-state panel, but no populated-rollout or live-provider browser
acceptance was performed. These changes had not been committed or deployed at
that checkpoint; the deployment checkpoint below supersedes only the local
deployment part of this statement.

2026-09-25 local rollout deployment checkpoint: before migration, the live
database was backed up to a restricted temporary dump and verified to have
no existing rollout rows. Three exact unshared/reclaimable Go build-cache
records were removed, freeing about 1.1 GiB without touching images or
volumes. Compose then built the Control API, Runner and Console from the
current source. The migrator applied `000093_rule_canary_admission.sql`;
`schema_migrations` and the new approval-evidence column confirm it is live.
The three services were recreated with the GitHub App overlay preserved:
Control API image `sha256:05b5b604d9e74ebf55e8d9e304201ebd4fd1fb67ee37aac2157388065631046f`,
Runner image `sha256:062b3b9d3fa733f2cb46b32f4ca92c3799591c6c1e0bf3424327cd6e1f299b4a`,
Console image `sha256:e4865e78761434ca3ab45de291592cba9f0a6aebc3954ba6d27ea96af0d2410a`.
Control API `/healthz` and Console `/api/health` returned 200; Console and
PostgreSQL were healthy, the review Runner heartbeat was unexpired and
RabbitMQ reported no alarms. The rollout endpoint returned 401 without a
token, as expected. An in-app browser visit to the public Rule Bindings URL
reached Casdoor's password screen, not an authenticated Console session; no
credential was entered. Therefore the deployed empty/populated rollout UI,
authenticated BFF and real provider Shadow/Canary effects remain unaccepted.
The changes remain uncommitted and unpushed.

2026-09-25 Canary automatic-rollback deployment checkpoint: an isolated
PostgreSQL 16 database migrated through `000094` and passed the rollout
integration test for distinct candidate-run failures, the configured time
window, quota exclusion, baseline exclusion, idempotent rollback and audit.
The complete Go suite, focused vet, frontend typecheck/lint, production build
and Compose validation passed. A restricted pre-`000094` database dump was
saved at `/tmp/orp-pre94.yM5BjL/openreview.dump`; the disposable test database
and TCP proxy were removed afterward. The live migrator applied
`000094_rule_canary_auto_rollback.sql`, then Control API
(`sha256:73017c416584ebd6492cb76df1e00d1791211ff2959d807dc8dc8b74f443a0b4`),
Console (`sha256:161abfb3187daa8b6602e9284bd5d554ecd68059f343f9b5e4c3a239539416f1`)
and the new rule-rollout monitor
(`sha256:41226285bbc2ecd72bd6da03906f38419adbe0f260ba75a6af0796e6aebbddb7`)
were deployed locally. Both health endpoints returned
200, the monitor heartbeat was fresh, and RabbitMQ had no alarm. No live
rollout row exists, so this proves service wiring but not provider-side Canary
exposure or an observed automatic rollback. The configured Jev credential
still matches the running source-admitter and passed a fresh opt-in synthetic
Issue HTTPS protocol test; that check created neither an Agent task nor a PR.
The live database still has zero Agent policies and zero Agent tasks. The
separate adapter endpoint/secret, pinned coding executor, and adapter-only
provider write credential are not configured in this local stack; Jev alone
cannot authorize or complete Issue-to-Draft-PR execution.

2026-09-25 per-job Docker sandbox acceptance: the current
`agent-sandbox-codex` Dockerfile rebuilt to the same immutable image ID
`sha256:85be05c6906d27603c3780c6aa145ef3ce5f8f3ddccf30617cd3e38e63d70418`
with Codex CLI 0.142.1. A Linux adapter test binary ran as the trusted root
identity against a disposable internal-only Docker network and private
workspace volume. A real child container completed two synthetic Responses
API turns and wrote the expected workspace evidence while verifying that it
could not see the Docker socket, adapter `/state`, provider token, model
upstream key or HMAC secret. Separate real-container tests passed cancellation
cleanup and orphan-child/workspace cleanup after adapter recovery. No model
credits or provider writes were used. The exact test container, network and
volume were removed after checking for leftover children and files. The
abrupt adapter-process death regression was also rerun: the replacement
reported `needs_attention` without consuming a second start claim or
relaunching the executor. That test uses an in-process callback fixture rather
than a real provider or a durable multi-replica worker. These results prove
the local sandbox boundary for that pinned image; they do not prove a
dedicated execution host, multi-replica recovery, per-installation provider
credential brokering or a live Issue-to-Draft-PR/MR run.

2026-09-25 Issue plan-approval command checkpoint: GitHub Issue comments and
GitLab Issue notes now route `@openreview approve <full-plan-sha256>` into an
idempotent provider interaction. The mapped provider actor must be an active
workspace owner/admin; the command matches the installation, repository,
Issue number and exact Issue revision, then locks the current task and checks
the latest plan digest before calling the same transactional approval gate as
Console. High/critical plans still require a distinct approver. The approval,
audit record, single durable execution request and provider acknowledgement
commit together. Migration `000095` and an isolated PostgreSQL 16 integration
passed unauthorized actor, stale plan digest, wrong Issue revision, replay,
second approval and one-request assertions. Signed GitHub/GitLab webhook
routing tests and the complete Go suite passed. The disposable PostgreSQL
container was removed. Before local rollout, a restricted `pg_dump` backup
was saved at `/tmp/openreview-pre95.Kz2ePp/openreview.dump` and its archive
index was readable. The Compose migrator applied
`000095_agent_task_issue_plan_approval.sql`; the live command constraint now
includes `approve`. Control API image
`sha256:f3b6cc3e0f49655af94cd0277c7576645a23a9aa211f175230df8a6f2ebe953d`
was deployed with the GitHub App overlay preserved. `/healthz` returned 200
and the unauthenticated Agent-task API returned 401. No live provider approval
or coding-Agent task was triggered: the database still has zero Agent policies
and tasks, and the adapter/coding-model/write-credential configuration is
absent. Local deployment does not prove Issue-to-Draft-PR acceptance.

The Agent Work plan panel now displays the complete plan SHA-256 and reuses
the shared copy control for both the digest and the exact
`@openreview approve <full-plan-sha256>` Issue command. The command is shown
only when the current plan is awaiting approval, the origin is an Issue, and
the authenticated user has approval permission; feedback-child Draft PR/MR
plans remain Console-approved. Frontend TypeScript, targeted ESLint and the
Next.js production build passed. Console image
`sha256:19104273da29bb7582a9078a55b5511380abcb28460d969e9121d4730dbcf9bf`
was deployed locally, became healthy and returned 200 at `/api/health`. An
unauthenticated Agent Work request still redirected to sign-in, as required.
No real task exists yet to render a populated plan panel, so authenticated
visual and copy interaction acceptance remain open.

The configured deployment-owned `AGENT_DECISION_JEV_API_KEY` passed a fresh
synthetic Issue request against the default HTTPS Jev endpoint on 2026-09-25.
This validates the decision adapter connection only; the separate coding-model
credential is not configured. Plan creation now queues a version-fenced update
to the originating Issue's existing Agent status comment with the exact plan
revision, SHA-256 and copyable `@openreview approve <sha256>` command. Feedback
child plans publish a Draft PR/MR notice but require Console approval instead.
Focused tests, the full Go suite, vet, and an isolated PostgreSQL 16 migration
and integration run passed. The local Control API was rebuilt as image
`sha256:6b3ff84ba48858e1558720a315d958fd028a9b95b700e0af8ea49ae5475a90d2`;
`/healthz` returned 200. No live Issue plan or provider comment was created by
these checks, and the adapter URL/secret, pinned executor image, coding-model
key and adapter-only provider credentials remain unconfigured.

Approval through either Console or a verified Issue command now atomically
queues the execution request and an Issue status-marker update that says the
exact plan is approved and execution is queued, without claiming a coding
result. Feedback-child approval emits the corresponding Draft PR/MR notice.
Issue. The source-status publication fence now also rejects a message after
the task has advanced to execution, cancellation or a terminal result, even
when no newer message uses that marker. Isolated PostgreSQL 16 integration
covered both provider surfaces, the superseded-plan version and a delayed
approval message after execution starts; the full Go suite and focused vet
passed. The local Control API and interaction responder images are
`sha256:46c81c95dbe695ed439689c0317b55b947c3219923a96ebe5c58d9f5d16eefaf`
and `sha256:1fc6fe6c77c6e5f011b7d2331de66d65eef2a8c8c2c9c7b03b325e28c0876eea`.
During that rollout, Docker's 59 GB virtual disk filled; RabbitMQ quorum WAL
failed with `ENOSPC` and PostgreSQL briefly entered automatic recovery. Three
superseded local Control API images, five unused local project images and 12
identified project Go-build cache records were removed; no container volume
or business data was deleted. The build artifacts can be recreated. The
RabbitMQ process was restarted after disk space was restored to 4.4 GB; its
queues are `running`, PostgreSQL is healthy, the interaction responder is
running and subscribed, and `/healthz` returned 200. This is a local runtime
recovery, not a durable disk-capacity fix. No live Agent task exists to prove
an actual provider comment or isolated executor transition.

The next runtime audit found that the running RabbitMQ container still used a
legacy `ping`-only healthcheck and did not mount the configured 2 GB disk alarm.
Recreating it applied those settings but changed Docker's generated hostname,
so RabbitMQ selected a fresh Mnesia node directory and the pre-existing 74 DLQ
messages temporarily appeared absent. The old and new Mnesia directories
remained on the same named volume. A read-only backup of that volume's Mnesia
tree is retained at `/tmp/openreview-rabbitmq-recovery.tV1BJB/mnesia`.
`docker-compose.yml` now pins the original `rabbit@1361b06b513f` node
identity; recreating the service with that hostname restored all 74 DLQ
messages without deleting or replaying them. The installed healthcheck now
inspects quorum queue state, the 2 GB disk threshold is active, every queue is
`running`, the interaction responder is subscribed, and there are zero
unpublished outbox rows. This does not triage the historical DLQ payloads or
make Docker Desktop's 59 GB virtual disk large enough for future builds.

The Agent credential-readiness audit on 2026-09-25 made a read-only GitHub
App registration request for the verified RainLib installation. It observed
`Contents:read`, `Pull requests:write`, and `Issues:write`; a request for a
single-repository, narrowed coding token returned HTTP 422. The new
`RepositoryWriteToken` broker primitive requests only one repository with
`Contents:write`, `Pull requests:write`, and `Issues:read`, validates the
returned repository and permission set, and fails before token issuance when
the App registration lacks a prerequisite. Provider Health now records and
labels that App-level prerequisite separately from review/Issue readiness;
it does not claim a provider write succeeded. Local credential/probe tests,
Go vet, Console TypeScript and lint checks passed. This change is not yet
deployed; no GitHub App permission was changed, no Agent policy was enabled,
and no coding task or provider write was performed. A separate isolated
credential-broker service and the executor/model configuration remain needed
before a real Issue-to-Draft-PR acceptance run.

On 2026-09-25, the disposable GitLab CE overlay was extended to mount
current-source Linux binaries for `agent-task-source-admitter`,
`agent-task-runner`, and the optional `agent-task-adapter`. The first two were
recreated without enabling the adapter: both remain running, source admission
resolves `http://gitlab:8929/api/v4` and its private OAuth origin, and the
runner still has an empty `AGENT_TASK_ADAPTER_URL`. The Docker bridge reaches
the CE API (unauthenticated `/api/v4/version` returned 401). A separate
synthetic-Issue live Jev smoke test passed with the configured deployment key;
it did not create an Agent task or provider write. The local control database
had zero Agent policies and zero Agent tasks at this checkpoint. The
model-backed coding adapter, repo-scoped write credentials, human plan
approval, and live Issue-to-Draft-PR/MR acceptance remain outstanding.

The same local runtime was then advanced to migration
`000096_agent_task_credential_issuances.sql` and current-source binary mounts
for Control API, interaction admission, and interaction response without
rebuilding the Console image or enabling the coding adapter. Control API
`/healthz` and Console sign-in returned 200; RabbitMQ reported one consumer
each for Agent source, execute, cancel, interaction admission, and interaction
response, with no ready or unacknowledged messages in those queues. This is
runtime wiring evidence only, not a real provider Issue command or Draft
publication. The public Console redirects to Casdoor and needs a user login
for authenticated Agent Work visual acceptance; the local loopback URL
correctly refuses SSO because its callback origin differs from the configured
public origin.

The Agent policy audit event now records the selected `decision_backend`
alongside its revision, so a later Jev/rules-only switch can be traced without
reading mutable policy state. A fresh database migrated through `000096`
passed the complete Agent policy/task/plan integration test with assertions
for both initial and changed backend audit metadata; that disposable database
was then removed. The full Go suite and focused vet passed. The matching
Go 1.25.14 host toolchain built a static `linux/amd64` Control API binary,
which was mounted into the local container; `/healthz` returned 200 and the
unpublished outbox count remained zero. During the containerized test,
Docker Desktop briefly stopped answering API/host health requests and Agent
consumers restarted once after their delivery channels closed. Docker
recovered without a manual service restart; Control API and Console returned
200, RabbitMQ and PostgreSQL reported healthy, the five Agent/interaction queues
had one consumer each, and unpublished outbox count remained zero. The 74
pre-existing review DLQ messages were not replayed or deleted.
Six untagged, unreferenced images labeled for this Compose project were
removed without touching containers or volumes; shared image layers meant
the Docker virtual disk still had only about 2.4 GiB free. Further Docker
builds are deferred until that capacity is addressed.

2026-09-25 Agent installation-scope regression: direct Agent policy and task
requests now resolve the one active, admitted (`legacy` or `verified`) provider
installation whose repository scope covers the target. Two disjoint GitLab installations on the
same API host no longer depend on database row order; overlapping legacy
scopes fail closed with HTTP 409 before a policy audit or task is written.
The isolated PostgreSQL regression passed against a fresh database migrated
through `000096`. The configured Jev key is present in the running source
worker, but coding-adapter URL, coding model credential, and GitLab repository
write credential remain unset; no Issue-to-Draft-MR execution was claimed.
Agent Work now distinguishes same-host connections in its policy selector by
provider, authorized repository scope, endpoint, and installation suffix;
the selected scope stays visible beside the form. Targeted Web lint and type
checks and a host production Next build passed. The running Console image has
not yet been rebuilt with this presentation change, so browser acceptance
remains pending.

2026-09-25 Review Files evidence checkpoint: migration `000097` adds
source-free `file_scopes` to each immutable execution plan. New runs retain
the exact selected/deferred path, static priority score and classification
reasons; old runs keep their original selected paths and deferred count without
invented path detail. The Review Files tab separates selected, deferred and
finding-only records, links to exact provider source and the provider review,
and labels path priority as static evidence rather than runtime dependency or
test coverage. A fresh database passed the execution-plan immutability and
evidence-read integration tests; a second fresh database passed the fair-claim
tests in isolation. `go test ./...`, targeted Web lint/typecheck and host
Next production build passed. The first full Store integration run exposed a
test-isolation defect: an Agent fixture preserved its append-only usage ledger
but also left a claimable Draft review job. The fixture now retires only that
queued job at test cleanup; a third fresh database migrated through `000097`
passed the complete Store integration suite. All three disposable databases
were removed. The local database reached `000097`, new Control API/runner
binaries returned healthy, and the Console image was rebuilt. The view has
not yet had authenticated provider-backed browser acceptance. Inline diff rendering,
binary/renamed-file handling, dependency graph and coverage mapping remain
unimplemented; provider links are the current inspection path.

The Console production image was subsequently rebuilt from this source and
the local Console/Control API health endpoints returned 200. The configured
public origin redirected to Casdoor, but the available browser profile had
no authenticated session, so live provider-backed visual acceptance remains
unproven. An isolated `3111` preview with explicit demo-mode labeling did
render the Review Files tab at desktop width: two selected and eight deferred
paths had distinct states, priority scores, static reasons and provider links,
with no visible overlap in the inspected viewport. This preview is not
evidence of a real GitHub/GitLab review or a mobile accessibility audit.

The local Jev admission key is present in the running Agent source worker.
The running Agent task runner has neither `AGENT_TASK_ADAPTER_URL` nor the
shared `AGENT_TASK_ADAPTER_SECRET`; no adapter service is running. Jev therefore
proves only classification readiness, not coding, repository writes, Draft
PR/MR publication, or the feedback loop. Those gates remain closed until a
separate sandboxed adapter, coding model route, and scoped provider credentials
are configured and accepted end-to-end.

2026-09-25 governed rule promotion checkpoint: the source now rejects a new
Canary above 5%, advances only `1% → 5% → 25% → 100%`, and permits final
promotion only after the current stage has observed its configured window,
at least one distinct candidate-selected PR/MR review has completed with a
passing immutable merge-gate decision, and no candidate review has failed or
remains in flight. A different owner/admin from the Canary creator must
approve promotion. The baseline becomes disabled and the candidate active in
the same transaction that marks the Canary and matching Shadow promoted;
stage changes, binding changes and promotion are audited. The Console exposes
the staged controls and their evidence requirements. A fresh PostgreSQL 16
database migrated through `000097` passed the entire Store integration suite,
including rejection of missing gate evidence/failed candidate work, staged
advancement, atomic binding swap and retention of the historical run snapshot.
The server also checks the stage-audit chain before advancing or promoting:
legacy 25%/100% rows cannot bypass the new sequence. A second fresh migrated
PostgreSQL 16 database passed the full Store suite with both legacy-bypass
regressions, then was dropped.
The full Go suite, focused vet, Web typecheck/lint/build and diff check passed.
Both disposable test databases were removed; no development rule policy or
binding was changed. The local Control API binary was rebuilt with its previous
binary retained as a rollback copy; the Console image was rebuilt and both
containers recreated. `/healthz` and `/api/health` returned 200, the Console
became healthy, and unauthenticated rollout/API routes still returned 307/401.
After the stage-history patch, the Control API was rebuilt again (previous
binary retained separately), force-recreated, and returned 200 from `/healthz`;
the unchanged Console returned 200 from `/api/health` and stayed healthy.
A real provider Canary, authenticated browser interaction, false-positive
quality metrics and production promotion remain unverified.

2026-09-25 isolated Agent sandbox acceptance: a Linux/amd64 test binary was
run as the trusted adapter identity inside the pinned local adapter image,
with a disposable Docker-internal network and workspace volume. Three opt-in
tests passed against the real daemon and pinned Codex sandbox image
`sha256:85be05c6906d27603c3780c6aa145ef3ce5f8f3ddccf30617cd3e38e63d70418`:
crash-orphan cleanup, two synthetic TLS model requests plus a tool call that
proved the child had no daemon socket or adapter/upstream secrets, and bounded
cancellation with child/workspace cleanup. No model charges or provider writes
were made. The exact disposable network and volume were removed after checking
no owned child remained. This proves a local process isolation boundary for
those cases, **not** a dedicated execution host, deployed adapter restart,
multi-replica receipts, or a real Issue-to-Draft-PR/MR workflow.

The adapter's restart recovery previously stopped iterating its loaded
receipts when the startup callback context was cancelled. Later `started`
jobs then retained no terminal event, so the periodic terminal retry could
not notify the control plane until another process restart. Recovery now
converts every interrupted start into a durable `needs_attention` event even
under a cancelled delivery context; a fresh `RetryPending` delivers it using
the same stable identity without re-executing the coding Agent. A two-job
regression and an actual abrupt subprocess-kill test passed both locally and
inside the Linux adapter image; full Go tests, adapter race tests, focused vet,
diff check, and a current-source adapter-base image build passed. The new
image is tagged `open-review-platform-agent-task-adapter:recovery-20260925`
(`sha256:ce83e80dcb76af0c3914417f932e4c0d59ee420755bec7556895d5321e389de0`)
but is not deployed: the local runner still lacks an adapter URL/secret, and
no provider coding credential or real Agent task was enabled.

The coding credential broker now accepts an optional GitLab-only or mixed
GitHub/GitLab configuration. GitLab uses a broker-private, 0600 exact
installation/provider/API/repository mapping that is re-read on issuance;
the adapter and review workers do not mount that mapping, and the review OAuth
token is not repurposed for coding. The existing live attempt/start/lease and
issuance-limit checks still run before lookup, while the GitLab issuer also
requires the clone origin to match the admitted API origin (HTTP only with an
explicit development opt-in). Broker-client tests cover exact issuance,
changed installation/repository/API, lost task lease, cross-origin rejection
and rotation. Full Go and focused race suites, vet, Compose validation and
the full broker image build passed. This is a **task-gated delivery of a
deployment-owned repository token**, not a short-lived GitLab token minted
per task; actual provider-side token scope, expiration/rotation and Draft MR
publication are not yet accepted. No broker or coding token was deployed.
A disposable GitLab-only broker container then started against the local
control database with a 0600 synthetic, never-used mapping and no GitHub App
configuration; an unsigned credential request returned HTTP 401. Its container
and synthetic file were removed. This validates process configuration and the
authentication boundary, not a signed grant, provider permission or Draft MR.

The signed GitLab broker/client was subsequently exercised against a fresh,
fully migrated PostgreSQL 16 database with a synthetic, broker-private token.
The integration test created a Jev-backed Issue task, required model evidence
before source admission, and proved the broker rejected requests before owner
plan approval, before adapter job attachment/start, for a different repository,
and after task cancellation. The one exact approved and started job received
the synthetic token and left an `issued` audit row; no token was persisted in
that row. The disposable database and test binary were removed. The full Go
suite passed. This connects the Store authorization gate to the HTTP broker,
but does not call a Git provider, coding model, or publish a Draft MR.
GitLab broker startup now additionally rejects every map entry whose provider
is not GitLab or whose clone origin does not match the admitted API origin;
development HTTP still requires an explicit opt-in. The same validation runs
again at issuance after file rotation. Invalid-map tests, the full Go suite,
focused race tests and vet passed. A static map check alone cannot prove that
an operator's GitLab token is project-scoped at the provider.
The issuer now adds a read-only provider identity preflight on **every**
GitLab issuance: an exact project lookup must return the admitted repository,
and `/user` must identify that project's documented `project_<id>_bot_*`
account. Personal/group identities, other-project bots, redirects and
unavailable identity responses are denied before returning the token. A
synthetic TLS GitLab fixture passed these cases; the signed broker plus
approved/started-task gate passed again on a freshly migrated, disposable
PostgreSQL 16 database. The database and test binary were removed. This
cannot establish the token's real GitLab write scopes or a successful push/MR.
The issuer now also queries the authenticated token's own metadata and
requires the same bot user ID, active/non-revoked state, plus both `api` and
`write_repository` scopes. An unavailable token-self endpoint fails closed;
synthetic tests cover missing scopes, inactive/revoked tokens and wrong owner.
This verifies declared scopes at issuance, not effective branch permissions or
a real push/Draft MR. The updated broker image is not deployed in the normal stack.

2026-09-25 broker image and browser checkpoint: the scope-preflight source
built into a Linux broker image (`sha256:4d06d43f314400f93431e00e4b961b8bc9584b05b5a973b44b31506ae2b7c571`).
A disposable container started on the private Compose network with only a
synthetic map; the exact broker credential endpoint returned HTTP 401 to an
unsigned request. That container and its synthetic 0600 map were removed.
No project token was exercised, no coding task was enabled, and this is not a
real provider write test. Browser acceptance of Agent Work remains pending:
the local `127.0.0.1:3110` origin is intentionally rejected because
`OPEN_REVIEW_APP_URL` is `https://review.rainlib.com`; a new login from the
configured public origin reached the Casdoor password page but did not produce
an authenticated Console session.

1. Verify one non-production GitHub and GitLab installation through provider
   authorization, webhook admission, review publication, check/status and
   retry; then validate one configured notification destination.
2. Extend the completed representative authenticated keyboard, responsive,
   contrast and reduced-motion checks to route-suite axe, true 200% browser
   zoom and human screen-reader evidence. Payment/subscription starts only
   after these gates are evidenced.
3. Complete dedicated-host and deployed adapter-process crash/restart acceptance
   for the new per-job sandbox. Then exercise one explicitly enabled Agent Issue on
   GitHub and self-managed GitLab through source verification, Jev decision,
   human plan approval, execution and Draft PR/MR publication; replace the adapter's
   single-instance disk receipt after a real crash/retry, then add a multi-replica durable Job backend.

2026-09-25 Agent patch-evidence checkpoint: the deployment-owned Jev key in
`.env` matches the running source-admitter without exposing its value. The
adapter source now hashes the bounded binary diff, rejects a changed staged or
committed patch before push,
and sends the SHA-256, changed-file count and diff-byte count through its
authenticated terminal callback. Migration `000098` stores this source-free
evidence on the exact attempt, and Agent Work plus the Draft PR/MR description
display it. Unit tests, the full Go suite, `go vet`, Console typecheck and lint
passed. A first isolated integration attempt could not start because the
Docker VM was at 100% disk use (`FATAL: could not write init file`); its exact
12 MB test database was dropped. A second PostgreSQL 16 container used a
host-mounted temporary data directory: all migrations through `000098`
applied, and the policy/approval/attempt/terminal-evidence and installation
scope integration tests passed. Its container was removed and the 89 MB
temporary directory moved to Trash for recoverability; no shared database or
image cache was pruned. This code is not rebuilt into the running control
API/adapter, and no real coding Agent or provider Draft PR/MR run has been
accepted.

The additive `000098` migration was then applied to the local development
database with the existing, pinned migration binary and the current SQL
directory. The migration receipt and all three attempt columns were read back;
the database had zero prior Agent attempts. One exact, exited project test
container (`open-review-manual-retry-test`, 181 MB writable layer, repository
bind mount) was removed to recover Docker VM space. No shared volume, active
container, unrelated project cache or database row was removed. The live
control API and Console still run their previous binaries, so this is schema
readiness, not runtime or browser acceptance of the new evidence display.

The Agent Work stop-task copy and governance note were also corrected: a
cancelled local lease rejects late completion callbacks, but it cannot prove
that an already-started provider push or Draft creation was undone. Operators
are directed to verify the repository after cancellation. Console typecheck,
lint and the ten Agent data tests passed; local Control API and Console health
endpoints both returned HTTP 200. Neither health check is an authenticated
Agent Work browser acceptance run.

The same cancellation boundary is now reflected in the GitHub/GitLab Issue
command response: `@openreview cancel` no longer claims that a late callback
can prevent an already in-flight provider write. Focused Store, adapter and
domain tests and `git diff --check` passed. This wording correction does not
implement automatic provider-side cleanup or reconciliation.

Adapter terminal Issue comments now use one bounded renderer. A successful
terminal callback links the canonical Draft and includes the stored patch
SHA-256/file count/diff size; `needs_attention` and `failed` tell operators to
check for an in-flight provider push or Draft before retrying, rather than
asserting that no Draft exists. A new unit test covers all three states. A
fresh, host-mounted PostgreSQL 16 database applied all migrations through
`000098`, then the Agent policy/approval/attempt integration test verified
that the completed Issue-comment outbox payload contains the Draft link and
patch SHA. Its disposable container was removed and its 88 MB host directory
moved to Trash. This verifies local formatting and durable enqueue only, not
publication to a real GitHub/GitLab Issue or provider-side reconciliation.

Agent execution readiness now has a separate deployment-level heartbeat
signal. A new runner reports whether it has a locally validated adapter
client configuration; old-version heartbeats remain unknown during rolling
upgrades. The owner/admin Platform Health response and Agent Work page
distinguish `unobserved`, `not_configured`, and `configured_unverified`.
This reveals no worker IDs, URLs, credentials, or load. Even the configured
state does **not** prove adapter reachability, coding-model access, sandbox
isolation, or provider Draft PR/MR writes. Migration `000099` was applied to
the local development database; an isolated PostgreSQL 16 database applied
the full migration chain and passed the tenant-health integration test,
including the legacy-null heartbeat case. The full Go suite, `go vet`,
Console typecheck, focused lint, and diff whitespace check passed. Both
disposable test containers were removed; their host-mounted data directories
were moved to Trash. At that checkpoint the running API/runner/Console images
were older and could not display the new signal; the Docker VM had
approximately 375 MB free, so no image rebuild was attempted then. The later
runtime verification below supersedes this deployment status.

The opt-in live Jev smoke test used the configured deployment key and a
synthetic documentation Issue; the external service accepted one request and
returned a valid bounded decision. No real repository content was sent. This
validates Jev transport/protocol for that request, not Issue webhook admission
or the separately configured coding-model and adapter chain.

After inspecting image references, 25 unused, rebuildable project verification
or dangling Compose images and four exact stale project Go build-cache records
were removed; the old `migrate` image still referenced by its container was
preserved. Docker VM free space rose from about 370 MB to 2.7 GB. The current
agent-task-runner, Control API and Console images then built and only those
three services were recreated. Both HTTP health checks returned 200; the new
runner wrote a fresh `adapter_configured=false` heartbeat. In an authenticated
browser session at the configured public origin, Agent Work displayed live
control-plane data and **Coding executor: not configured**, with zero source
and execution queue items. The same page showed one verified GitHub
installation, no enabled Agent repository policy, and no Agent tasks. This is
runtime and authenticated UI evidence for the readiness signal, not an Agent
Issue-to-Draft PR acceptance. No adapter URL/model key/provider coding
credentials were supplied or exercised, and no repository policy was enabled.

The local Canary auto-rollback monitor was rebuilt from current source and
recreated after confirming migration `000094` was applied. Its new container
is running and has a fresh durable heartbeat; no Canary rollout is active in
the local database, so no live rollback transition was exercised. The
existing immutable per-job Codex sandbox image (`sha256:85be05c6906d...`)
also returned `codex-cli 0.142.1` in a network-disabled container smoke test.
Only the Jev decision key is present in the local `.env`; the adapter URL and
HMAC key, coding-model endpoint/key/model, broker secret and repository-scoped
coding identity are absent. Therefore neither the optional adapter nor an
Agent repository policy was enabled, and the provider Issue→Draft PR/MR gate
remains open.

Agent Work now distinguishes the decision backend from the coding executor
using independent fresh worker heartbeat bits. Migration `000100` preserves
old source-worker heartbeats as unknown. The source worker reports Jev as
configured only when its deployment-owned key and HTTPS endpoint pass the
same local validity rule as an actual request; the health model deliberately
labels this `configured_unverified`, not a remote probe or coding readiness.
An isolated PostgreSQL 16 database migrated through `000100` and passed
legacy/absent, unconfigured, configured and role-boundary integration tests;
focused Go and Console type/lint checks passed. The additive migration was
applied to the local development database, and the current source worker,
Control API and Console images were built and deployed. Both health endpoints
returned 200; fresh heartbeats recorded `decision_configured=true` and
`adapter_configured=false`. An authenticated read of the deployed Agent Work
page displayed “Jev classifier: configured, not verified” independently of
“Coding executor: not configured” with no Agent policy or task enabled. This
closes the configuration-visibility gap only: a real coding-model call,
provider write and Issue→Draft PR/MR acceptance are still outstanding.

The optional Docker coding adapter now takes a non-blocking lock on the shared
checkout volume before recovering receipts or removing orphan children. Orphan
cleanup requires that exact live lock. A cross-process test proved that two
adapters with different receipt directories can each acquire their own receipt
lock but cannot both own the same checkout volume; a replacement acquires it
after the first process exits. Symlinked lock/root and cleanup without a lock
fail closed. Full Go, focused race and vet checks passed. This is single-host
misconfiguration protection, not a distributed job lease, real Docker process
kill/restart acceptance, or a provider Draft publication receipt.
The same cross-process lock tests passed again inside a network-disabled,
read-only Alpine 3.22 container with no Linux capabilities; the updated
Linux adapter image built as `sha256:e79135ea5e91ce5abec747a255bb9ae7ea17e92e8b21e6f6db405d030587a79e`.
It is not deployed: the optional adapter still lacks its coding model,
task-bound signing secret and repository-scoped write identity. The disposable
14 MB Linux test binary was moved to Trash after acceptance.

On 2026-09-25, the running source-admitter again reported a fresh
`decision_configured=true` heartbeat. A synthetic, repository-free HTTPS
request using the configured Jev key returned HTTP 200 with a `choice` answer
and all four expected probability keys (`plan`, `context`, `human`, `reject`).
No secret or provider content was logged. The live database still has zero
Agent repository policies and zero Agent tasks, so this is remote Jev protocol
evidence only; it does not authorize automation or prove a real Issue-to-Draft
PR/MR execution.

2026-09-26 Review Files local deployment checkpoint: the preceding
file-metadata change is now built into and running in the local API
(`sha256:aecb5531d740b66fc4d72b3bcc23816133554bbbc1c75ff741345827120ebe6e`),
Console (`sha256:47566b636258c5b2aab1bc4ee8075830cd7d46ae0410cd32302aed1547a5016b`),
and Runner (`sha256:11807e74d37f0caa3fc1c69bbf09184d6f2a1f68fa9e8b325bc3f0d7d0c34f63`)
images. An old, stopped Runner container retained a bind mount from a
deleted temporary directory; it was recreated from the current Compose
definition and now reports a fresh heartbeat. Only rebuildable, unshared
Docker build-cache records were pruned (about 3.8 GB); running images,
database volumes, and data were retained. API `/healthz`, Console
`/api/health`, and `/sign-in` returned 200. Browser automation rendered the
local sign-in page without an error overlay and confirmed that an
unauthenticated Files-tab URL redirects to sign-in while preserving its
`next` target. This is not authenticated Files-tab acceptance or proof of
metadata on a newly executed provider review. The Agent coding adapter
remains unconfigured and no Issue-to-Draft-PR/MR run was accepted.

2026-09-26 coding image readiness checkpoint: the fixed Codex 0.142.1
`agent-sandbox-codex` target was rebuilt locally as
`sha256:85be05c6906d27603c3780c6aa145ef3ce5f8f3ddccf30617cd3e38e63d70418`;
the current Go adapter plus fixed CLI `adapter-codex` target was built as
`sha256:8ecbddeafd32883d32033b72f191f82c3759cf4b302b7ea305089a8b9648c348`.
The sandbox image ran `codex --version` as UID 10002 with no network, a
read-only root, dropped capabilities and `no-new-privileges`. The complete
`internal/agentadapter` and `internal/agentcredentials` package tests passed.
Starting the new adapter image without coding-model broker settings rejected
startup before serving any task. These are image and fail-closed startup
checks, not a configured adapter, an isolated end-to-end sandbox run, a
credential-broker issuance, or provider Draft PR/MR acceptance. The local
runner still has no adapter URL/shared secret and no Agent policy was enabled.

2026-09-26 Agent Draft navigation checkpoint: a newly created Agent Draft
PR/MR description now links each reported changed file to the exact pushed
commit on its admitted provider repository. Deleted paths link to that
commit's diff instead of a head-tree blob that would return 404. Hosted
GitHub, paired GitHub Enterprise, paired self-managed GitLab and unrelated
GitLab API origins have focused URL tests; unsafe path or revision input
falls back to escaped, non-clickable text. The full Go suite, vet and diff
check passed. This source change is not present in the previously built
adapter image and has not been exercised against a real Draft PR/MR.

2026-09-26 Agent Draft image checkpoint: the updated adapter source above
was compiled into local `adapter-codex` image
`sha256:e494525c7e61dda6f48a56a19e9598099f9ec1bc34ad2b1d74d943d804bb1764`.
Starting that image with Docker sandbox mode but without coding-model broker
settings failed before listening, as intended. The installed sandbox image
remains `sha256:85be05c6906d27603c3780c6aa145ef3ce5f8f3ddccf30617cd3e38e63d70418`.
About 1.38 GB of individually identified, unshared, rebuildable old build
cache was pruned over the two build steps; no running container, in-use
image, or database volume was removed. Docker free space ended near 2.5 GB,
with PostgreSQL and RabbitMQ healthy. The new adapter image is **built but
not deployed**: no coding-model broker, adapter URL/secret, scoped provider
write credential, Agent policy, or real Draft PR/MR acceptance is present.

The adapter now fsyncs a bounded publication checkpoint containing the exact
validated commit and patch evidence **before** its first branch push. On a
single-instance restart, an Issue-origin attempt with that checkpoint uses
the configured provider credential only for GETs: it rereads the
unchanged Issue and requires exactly one open Draft on the frozen branch with
the ownership marker and exact head SHA. A match is returned as a completed
callback only while the control-plane lease still accepts it; absent,
ambiguous, unowned, changed-head, expired, or unavailable evidence becomes
`needs_attention`. The executor, push, and Draft POST are never replayed.
Feedback-child attempts remain conservative manual-attention cases because
their original Draft head legitimately changes during execution. GitHub and
GitLab provider fixtures, restart receipt tests, full Go tests, focused race,
vet, and diff checks passed. The optional adapter image was not rebuilt or
deployed; this is source/fixture recovery evidence, not a live provider
Issue-to-Draft or multi-replica durable-publication acceptance.

The control plane now accepts a signed `publication_checkpoint` callback
before provider push and retains the exact validated commit/patch per attempt.
Agent Work shows it separately from a Draft result, explicitly marking
provider publication as unconfirmed. Callback replay cannot renew an expired
lease; a completed callback with different commit/patch evidence is rejected.
An isolated PostgreSQL migration and store integration test passed. This
checkpoint improves operator visibility after lease expiry but does **not**
prove that a provider branch or Draft exists, and it does not perform a
post-expiry provider reconciliation or authorize a retry of provider writes.
Agent Work now pairs each checkpoint with its exact attempt ID and number;
older attempts are available in a collapsed history with an explicit count of
unconfirmed publications. A later retry no longer displays an earlier
attempt's commit as if it belonged to the current execution. Frontend mapping
tests, typecheck and focused lint passed; this history state still needs an
authenticated browser run with a real multi-attempt task before visual
acceptance.
The GitLab adapter fixture now also rejects a pre-push checkpoint callback
and verifies that neither the dedicated branch nor a Draft MR is published.
Focused normal and race tests passed. The adapter's current Draft evidence
still explicitly says build/tests/SAST were not attested; running an approved
repository verification strategy inside a production-grade sandbox remains
core execution work, not a claimed pass from this fixture.
The provider push now carries an exact Git remote lease: an Issue task can
create only an absent Agent branch, while a feedback child can advance only
the approved parent Draft head. A disposable bare-Git/GitLab fixture seeded
an occupied branch at the original base commit; the push failed and the
remote SHA did not change or create a Draft. The feedback lease contract has
a focused unit test, but a real provider concurrent-update race is still
unverified.
The optional adapter verification profile is deployment-owned and must match
the provider, API origin, and repository exactly. A pinned image runs the
approved argv in a separate networkless, read-only sandbox before publication;
the adapter then rechecks Git metadata and the exact patch. A Linux container
fixture exercised the sandbox arguments and credential isolation, and the
JEV backend completed one real request using a synthetic Issue. Neither check
is a live Issue-to-PR acceptance run: the coding model broker, verification
image/profile, provider Draft publication, and authenticated UI journey still
need to be configured and verified together.
Verification evidence now survives the adapter's publication checkpoint and
terminal callback as a bounded profile hash, output hash, and output-byte
count. The control plane stores it on the exact attempt, requires the final
result to match the retained checkpoint, and exposes it in the Agent Work
attempt history and the originating Issue's completion comment. An isolated
PostgreSQL 16 instance applied all migrations through `000102` and passed the
Agent policy/attempt/checkpoint/completion integration test, including
conflicting verification evidence rejection. Full Go tests, focused race,
vet, Console typecheck/lint, and Agent data tests passed. The isolated test
container was removed. The additive migrations `000101` and `000102` were
then applied to the local development database; all six new evidence columns
and both receipts were read back. The new control-api image built and was
recreated on the existing service, and its `/healthz` returned 200.
The first Console Docker production build stalled under Docker resource
contention with under 900 MiB of VM disk free and was cancelled. Three exact
old, rebuildable Open Review build-cache records and six dangling project
images were removed without touching database volumes. The supported Next.js
Webpack production-build path then passed on the host and in Docker; the new
Console image was recreated and became healthy. `/api/health` and the control
API `/healthz` both returned 200, while an unauthenticated request to
`/acme/agent-work` redirected to sign-in. RabbitMQ's Agent queues were healthy
and empty, and the source/runner heartbeats became fresh again after transient
Docker DNS and consumer-channel errors during the first build contention.
The new UI is deployed locally, but authenticated Agent Work evidence display
has not been browser-accepted because the live database has zero Agent tasks.
No real coding-model or provider Draft run was exercised; complete verification
logs remain outside durable storage.

2026-09-25 workspace-shell continuity checkpoint: an authenticated production
browser session exposed that Provider Issue triage still took the legacy
sidebar fallback while Issues and Agent Work used the approved Console shell.
The workspace layout now renders one shared shell for every current and future
workspace route, removing the path-prefix allowlist and legacy fallback.
Console typecheck, focused lint, Docker Webpack production build, and diff
checks passed; the rebuilt image was recreated and became healthy with
`/api/health` returning 200. The authenticated public-origin browser then
rendered the retained GitHub Issue #9 analysis, provider link, reaction-sync
state and revision history in the new shell with no browser warning/error.
At a 390 px emulated viewport, document scroll width remained 390 px with one
`main` and one `h1`. This verifies the Provider Issue navigation/design seam,
not Agent task execution or a full route-suite visual audit.

2026-09-25 JEV and CLI boundary checkpoint: the running source-admitter has
the configured JEV endpoint/model/key, and its fresh worker heartbeat reports
`decision_configured=true`. A synthetic Issue request had already exercised
the real JEV endpoint; this turn did not send another paid request. The fresh
Agent runner heartbeat still reports `adapter_configured=false`, and the local
database has zero Agent policies. No real Issue was admitted or coded.

CLI Quickstart and the API-key usage guide now render the configured public
control-plane origin (or the explicit `OPEN_REVIEW_PUBLIC_API_URL` override),
not `review.example.com`. They never render a copyable review command without
live installation evidence. The copied shell template reads the API key from
the caller's environment and guards repository, PR/MR number, refs, and exact
SHAs before invoking the CLI. Offline deployments may use an internal HTTPS
origin that is reachable from the CLI host. Four focused template tests,
TypeScript, ESLint, host and Docker production builds passed. Console image
`sha256:04cabde1d6f92bcf1b9cc1ba5cde6da909e526eb745cc344e10bb680911188ce`
was deployed; `/api/health` returned 200. In an authenticated public-origin
browser, CLI Quickstart showed the real `https://review.rainlib.com` origin,
the active GitHub installation and guarded copyable command; the API-key
guide showed the same origin. The browser reported a `Copied` button state,
but the browser automation clipboard bridge returned no text, so clipboard
contents were not independently verified.

During the Docker image builds, its virtual disk reached 100%, and PostgreSQL
temporarily failed checkpoints and entered recovery. Two exact unused Console
images and eight exact reclaimable `pnpm build` cache records were removed;
no database volume or business row was removed. The virtual disk recovered to
about 2.4 GB free, PostgreSQL accepted connections, both Agent heartbeats
were fresh, and the interrupted API-key page loaded normally on retry. Disk
headroom remains low and must be monitored before further image builds.

2026-09-25 CLI review-list and runtime checkpoint: the CLI Reviews list now
shows the caller/source, elapsed time or an explicit unavailable state, the
submission/start timestamps, and separate links for the provider repository,
exact commit, PR/MR, and internal run evidence. GitHub.com, GitHub Enterprise,
and self-managed GitLab repository/commit URLs are provider-qualified; invalid
or incomplete SHAs do not become links. Seven focused tests, Console
TypeScript, diff checks, host and Docker production builds passed. Docker
Desktop had stopped before this check; it was restarted without recreating
volumes. Nine exact old reclaimable `pnpm build` cache records were removed to
restore roughly 4.5 GB of VM disk headroom before building. Console image
`sha256:c374f6efcb5b458ed77936cc905b1c80a9a524bf44dc5473735c95882f52b70e`
was deployed and `/api/health` returned 200; PostgreSQL was accepting
connections with roughly 4.1 GB VM disk free after deployment. Fresh Agent
heartbeats show `decision_configured=true` for the JEV source-admitter and
`adapter_configured=false` for the runner; the local database has zero Agent
policies and tasks. The CLI list has no live CLI-run row to browser-accept,
and no real Issue-to-Draft-PR coding run was performed.

2026-09-25 Provider Issue → Agent request checkpoint: a logged-in operator
can now explicitly request a governed Agent candidate from a retained Provider
Issue analysis. The browser sends only its analysis ID and visible revision;
the control plane derives `issue-sha256` from the server-held title/body,
checks the operator role and manual repository policy, and binds the request
to the original verified installation. The source worker still rereads the
open provider Issue and rejects a changed revision before planning; this UI
action never approves a plan or runs a coding CLI. API tests cover malformed,
stale, disabled, duplicate, forbidden and missing requests. An isolated
PostgreSQL 16 integration, migrated through the current schema, verified the
server-derived digest, exactly one task, and rejection when repository scope
moves to another installation; its temporary database container was removed.
The full Go suite, vet, Console TypeScript/lint and host/Docker production
builds passed. Control API image
`sha256:67c7f60be4c33748aadcf7bb8f6ea54de979e3e22f1ed96ed9d383f96ece0739`
and Console image
`sha256:6cd857c6895ae85d330e016a795ec8df980dd89c1ea46868750d9f261e20d485`
were deployed locally. Both health endpoints returned 200, both unauthenticated
Agent-request routes returned 401, PostgreSQL stayed healthy with about 3.0
GB VM disk free, and the live database still held zero Agent policies/tasks.
The browser automation CLI is unavailable in this environment, so the new
authenticated control has not been visually or interactively accepted. A real
Issue request and Issue-to-Draft-PR run remain unverified until a deliberate
repository policy, coding adapter, model broker and provider credentials are
configured.

2026-09-25 Provider Issue admission visibility checkpoint: the detail read
model now exposes the exact repository policy mode, original installation
readiness, current user's manual-request eligibility and any task already
recorded for the same Issue revision. The Console links to that existing task
instead of offering a duplicate request, and explains disabled policy,
installation and role gates before an operator clicks. The Issue body stays
server-side. An isolated PostgreSQL integration covered owner/viewer access,
policy changes, duplicate task lookup and installation-scope transfer; the
full Go suite, vet, focused web lint, host and Docker production builds passed.
The source-admitter's running environment has a JEV endpoint/model/key and a
fresh `decision_configured=true` heartbeat. The runner still reports
`adapter_configured=false`; there are zero Agent policies and tasks. Control
API image `sha256:b4aef39f9b4e7b9dd9bb0ac801d9ab7a00658716663ef7ac7cd119cb0d93b1e2`
and Console image `sha256:d15dc095853b87c529991ec7417a705e43dfed160a65a7b66996d627c516adbe`
were deployed locally. Both health endpoints returned 200, PostgreSQL was
accepting connections and the Docker VM had about 3.1 GB free. Six exact old
reclaimable frontend build-cache records (about 1.1 GB total) were removed
before building; no database volumes or business rows were removed. No fresh
paid JEV call, authenticated browser acceptance or real Issue-to-Draft-PR run
was performed in this checkpoint.

2026-09-26 Agent Issue cross-entry checkpoint: automatic label candidates and
explicit Issue/Console requests now resolve the same current Issue snapshot
before creating another task. GitHub and GitLab Issue comment commands can
find a label-frozen candidate for status, bounded plan approval and
cancellation; GitLab Note Hook normalization retains `issue.labels`. A changed
Issue body does not address the old task. If a direct API caller omits labels
and there is no matching retained triage snapshot, an existing automatic task
causes a fail-closed conflict instead of a second branch. The detail read
model links either revision flavor; ambiguous pre-existing pairs were rejected
by the detail endpoint at this checkpoint.
An isolated migrated PostgreSQL 16 test covered both creation orders, direct
API fallback, commands and changed-revision rejection. Agent-specific store
tests, the full Go suite, vet and diff check passed; the temporary database
container was removed. Control API image
`sha256:4e9aeeb8d2d912916093afebef273cb2f81ccc00a019d835b16f80529090fbab`
was deployed locally. API/Console health returned 200 and PostgreSQL remained
ready. This did not enable any repository policy or coding adapter; the live
database still has zero Agent policies/tasks, and no provider Issue-to-Draft
PR/MR run was attempted. Docker VM headroom was about 2.4 GB after the build.

2026-09-26 historical Agent task conflict recovery checkpoint: the Provider
Issue detail now preserves its retained analysis if both legacy revision
flavors have tasks. Admission exposes `existing_task_conflict`, clears any
arbitrary task link, and disables a new request; the Console directs the
operator to Agent work. An isolated migrated PostgreSQL integration seeded
the historical pair and verified the analysis remained readable while
admission failed closed. The full Go test suite, vet, web typecheck and focused
lint, Docker API/Console production builds, and diff check passed. The new
images (`sha256:0a04b8aa84fa597871b879df6e6b69e05a9b37df791d77c44a80d8253af4c278`
for API; `sha256:9367888b02f5840293d2a04e00ac8220f374a811eb25fc560816b4d073875f51`
for Console) were deployed locally. API `/healthz`, Console `/` and `/sign-in`
returned 200. The source-admitter container still has a non-empty JEV URL,
model and key, but no new paid decision was invoked. The runner still has no
coding adapter URL and the live database still has zero Agent policies/tasks.
No provider Issue-to-Draft-PR acceptance was performed.

2026-09-26 historical task navigation checkpoint: the same bounded Issue
read model now returns both exact task IDs and states when the two legacy
revision flavors coexist. The Console links each task directly from the
Issue detail and keeps creation disabled for that snapshot; an edited Issue
is required to request genuinely new work. The isolated migrated PostgreSQL
regression verified both IDs, retained analysis and fail-closed admission.
Full Go tests, vet, web typecheck/focused lint and Docker production builds
passed. API image
`sha256:c02587b598cb3c6dcd05a52e54d95e77845ffcb6e961205525648e03ed2a842e`
and Console image
`sha256:c043431a6ff81539f29339a204d6c75e0e332535276e884f591cd80e5c1f9b22`
were deployed locally; API `/healthz`, Console `/` and `/sign-in` returned
200. Only old recoverable Docker build cache was pruned (about 2.2 GB);
containers, images in use and database volumes were preserved. The live
database still has zero Agent policies/tasks, so the duplicate-task UI was
not exercised against a live provider record and the Issue-to-Draft-PR
chain remains unaccepted.

2026-09-26 Issue rule-provenance checkpoint: the Issue detail now reads the
persisted finding-to-rule attribution ledger for each occurrence in one
tenant-scoped query, preserving the exact rule key, immutable version ID,
rule-set ID/name and version number. The right-hand Issue context links the
latest occurrence's attributed rule set and offers the exact version ID to
copy. It does not fetch current repository content and present it as the
historical code snippet in the design mockup. An isolated migrated
PostgreSQL regression verified the viewer's exact attribution and that it
does not leak to another occurrence. Full Go tests, vet, web typecheck and
focused lint, Docker API/Console production builds, and diff check passed.
API image `sha256:6561862146059d6f9ccf651451b6b12c7f2e298079d54dc713ec64ed92ff9b2b`
and Console image `sha256:d3dc54d9bd8617ab5c713cfc203a8772e8813d3cd39a65bdfa83f2c9cf7c03e2`
were deployed locally; API `/healthz`, Console `/` and `/sign-in` returned
200, and the Console health check became healthy. The available browser
automation CLI is absent on this host, so the authenticated visual state is
not accepted. The running GitLab source worker has neither a static token nor
the OAuth decryption key, and the coding adapter remains unconfigured; the
local GitLab installation's prior verification does not prove current Agent
source capture or Issue-to-Draft-PR execution.

2026-09-26 JEV/GitHub source and Review Files checkpoint: the current JEV
deployment key passed the opt-in synthetic live request again. The real
GitHub App installation `162428396` also minted an installation token; its
public registration declares `contents=read`, `pull_requests=write`, and
`issues=write`. Neither check read Issue content, enabled an Agent policy,
or attempted coding/publication. The Agent runner still reports
`adapter_configured=false`, with zero Agent policies/tasks.

Review execution plans now retain source-free Git change kind, rename/copy
origin path, binary marker, and added/deleted line counts alongside the
existing immutable selected/deferred scope. A diff with more than 200 paths
skips the second line-count pass and records statistics as unknown. The Files tab exposes this
metadata, opens deleted paths in the provider diff rather than a missing
head-file URL, and labels the provider diff as current PR/MR state rather than
the run's immutable source. A real temporary Git repository exercised rename,
deletion, binary and text changes; Go full tests/vet, frontend type/lint,
provider link tests and host production build passed. A disposable PostgreSQL
16 database migrated through the current schema and passed the execution-plan
immutability/reload regression, including a changed-statistics conflict; its
container was removed. The running API/Console/Runner images were not rebuilt
because the Docker VM had only about 1.4 GB free. This closes file-metadata
visibility for future runs, not an in-Console exact diff, runtime dependency
graph, test coverage, live rendered Files-tab acceptance, or Issue-to-Draft
PR/MR execution.

2026-09-26 Issue/PR file-navigation checkpoint: Issue triage can now map an
exact provider API base to a deployment-owned browser origin for GitHub
Enterprise or self-managed GitLab. Internal HTTP GitLab origins without a
public mapping are omitted instead of appearing in provider comments. Any
Issue file link is explicitly labeled as current-default-branch navigation,
not evidence that the Issue-only analyzer read that file or pinned its
revision. GitHub PR summaries now route removed files to the PR's Files diff
instead of a deleted head blob. Focused regressions, the full Go suite,
`go vet ./...`, `docker compose config --quiet`, and `git diff --check`
passed. The running `agent-task-source-admitter` container retains a
non-empty JEV URL/key; this checkpoint made no new paid JEV request. The
Issue triager image was not rebuilt or deployed and no real provider comment
was posted to accept these new links. This does not close the Agent
Issue-to-Draft-PR chain or authenticated Console acceptance.

2026-09-26 isolated coding sandbox checkpoint: an opt-in Linux acceptance
ran the current Go adapter test binary inside the pinned
`open-review-agent-adapter:core-flow-20260926` image against the pinned
`open-review-agent-sandbox:core-flow-20260926` image. On a disposable
internal-only Docker network and private workspace volume, the real Codex CLI
completed two requests to a local synthetic TLS model, executed a tool
command, and proved that the child had no Docker socket, adapter state,
provider token, model upstream key, or adapter signing secret. Real-container
tests also passed orphan-child recovery and cancellation cleanup. The test
originally reported its own parent container as a leftover because its name
matched a broad prefix; the assertion now filters the exact sandbox owner
label, and all three tests passed on rerun. The temporary network and volume
were removed; PostgreSQL, RabbitMQ and Agent workers remain running. No real
model call or provider write occurred. Focused Go tests, vet and diff check
passed. The currently deployed runner still has no adapter URL, and Compose
has no coding-model route, sandbox image ID, shared adapter secret or
credential-broker URL, so this is sandbox acceptance rather than a live
Issue-to-Draft-PR completion.

2026-09-26 feedback publication recovery checkpoint: after a one-use Agent
start and a durable pre-push commit/patch checkpoint, a restarted adapter can
now reconcile a feedback child task against the exact original GitHub Draft
PR or GitLab Draft MR. It re-reads the admitted feedback comment and requires
the same author, comment ID and instruction hash; the Draft must retain its
agent-branch ownership marker, exact review number, source branch, provider
repository and validated new head SHA. Recovery issues only provider GETs and
does not rerun the CLI, push or create a Draft. Changed comments, stale heads,
wrong review numbers and unowned Drafts fail closed to `needs_attention`.
Provider-contract tests covered both providers and service receipt recovery;
the full Go suite, focused race tests, vet and diff check passed. This code is not deployed to the
running adapter (which remains absent from the normal profile), and no real
feedback push/restart/provider recovery has been accepted.

2026-09-26 Agent patch publication guard: the adapter now rejects binary Git
patches before commit or provider write. It also rejects non-canonical paths,
Git control files, case-insensitive environment/key files, and common
high-confidence credential formats on newly added lines without blocking
repairs solely because an old secret appears in removed/context lines. Focused
patch and path regressions passed. This is a bounded local guard, not a full
secret-scanning, license, SAST, or generated-artifact attestation; the running
adapter is still not deployed and no real provider Draft was published.
The current adapter source also built into Linux `adapter-base` image
`sha256:71ea5df4da101bcdf90250d47bd1c6563dc365e360850915b37d23ffda820d2e`
and pinned Codex CLI derivative
`sha256:bd3b1e467ea54506255d9339b8414d6c8556657f6efa7833958a965dc59da608`.
The Docker VM had about 1.4 GB free after these builds, so no additional
container acceptance or deployment was attempted against the live
PostgreSQL/RabbitMQ host. These image builds establish Linux compilation and
the pinned CLI package only, not operational readiness.

2026-09-26 Provider Issue Console navigation checkpoint: the Issue triager now
loads the workspace slug from the same tenant as the verified installation and
adds one stable analysis-detail link to acknowledgement, completed and failed
comments. The public host comes only from deployment-owned
`OPEN_REVIEW_APP_URL`; an absent or non-HTTPS/malformed origin omits the link
without preventing triage. Localized link labels and unsafe-origin/slug tests
passed, as did the complete Go suite, vet, diff check and Compose validation.
A read-only join against the local PostgreSQL database found four existing
Issue analysis jobs with an active, tenant-matched installation and workspace
slug. The rebuilt Issue triager image
`sha256:6607f265a6359d86a2ad0a3dae967c62f8bcd26a6320d50bad444d7fd6d250ce`
is running without restart; RabbitMQ reported no alarms. Only recoverable
Docker build cache older than 24 hours was pruned for build headroom; provider
comments and business rows were not changed. A new real GitHub/GitLab Issue
comment and authenticated click-through have not yet accepted the link, and
the Issue-to-Draft coding flow remains unconfigured.

The next live GitHub acceptance used temporary
[`RainLib/open-review-platform#10`](https://github.com/RainLib/open-review-platform/issues/10).
The configured App webhook URL returned the expected 405 to an unauthenticated
GET before admission. The bot first posted acknowledgement comment
`5839099572`, then updated that **same** comment to a completed structured
analysis with the exact retained job link
`https://review.rainlib.com/rainlib-open-review/provider-issues/5fa69851-7190-4e7a-b6f9-e40303e01e28`.
The database held the matching completed job at revision 1; an unauthenticated
GET of that link redirected to sign-in while preserving its exact `next` path.
No Agent task was created for Issue #10. The test Issue was closed with an
explicit validation comment and remains recoverable in GitHub history. This
proves real GitHub acknowledgement/update/link publication and the public
unauthenticated redirect, **not** an authenticated Console click-through,
GitLab publication, or Issue-to-Draft-PR execution.

2026-09-26 GitHub coding-adapter fixture checkpoint: the running
`agent-task-source-admitter` container has non-empty JEV endpoint, model and
key configuration; this check did not make another paid JEV request. A new
TLS GitHub Enterprise API fixture and disposable bare Git repository now
exercise the complete Issue-origin adapter path. It verifies scoped credential
refresh, current-Issue recheck, validated patch checkpoint, exact remote
branch head, Draft-only PR payload and provider response validation. When the
Issue changes before publication, the test confirms no remote branch and no
Draft PR. Focused GitHub/GitLab fixture tests, `go test ./...`, `go vet ./...`
and `git diff --check` passed. This is **offline provider-contract evidence**;
the coding adapter is not deployed in the normal Compose profile, and a real
GitHub Issue-to-Draft-PR run remains unaccepted.

2026-09-26 current JEV credential checkpoint: the configured `.env` key matches
the key loaded by the running `agent-task-source-admitter` container. An
explicit opt-in synthetic Issue request (`TestLiveJevBackend`) completed
against the real JEV endpoint in 1.21 seconds and returned a valid bounded
decision; `docker compose config --quiet` passed. The active PostgreSQL
database currently has zero Agent repository policies and zero Agent tasks.
`agent-task-runner` and source-admitter are running, but neither the coding
adapter nor credential broker is running. This proves current classifier
connectivity, **not** coding-model readiness or a real GitHub/GitLab Agent
task. No provider Issue, branch or Draft PR was created by this check.

2026-09-26 Work queue design-parity checkpoint: the desktop Running/Needs
attention queue now uses a compact, accessible run table instead of repeated
large cards; the 390 px layout retains the touch-friendly cards. Existing
provider links, intervention actions, URL filters and cursor pagination remain
available. TypeScript, page ESLint, and the optimized Next.js build passed.
A disposable read-only preview at 1440×900 and 390×844 rendered the table/card
switch without page overflow. After a one-time development hot-compile
hydration warning, a fresh reload had no warning; a correctly assembled
standalone production preview had no browser errors or Next overlay. This is
layout evidence with visibly labeled fixture data, **not** authenticated live
queue data, screen-reader acceptance or full-page 1:1 completion.

2026-09-26 Scheduled queue follow-up: desktop Scheduled now uses the same
compact table family as Running/Needs attention, with review, admission time,
state, requester and next action columns. Mobile keeps cards. Shared detail
rendering preserves provider/source/run links and cancellation behavior.
TypeScript, page ESLint and `git diff --check` passed. The Docker Console
image built successfully and was deployed to the local `:3110` service.
Read-only fixture browser checks at 1440 px found three scheduled rows and
working tab navigation; at 390 px, three cards rendered without horizontal
overflow or browser errors. Unauthenticated `:3110/acme/tasks?tab=scheduled`
returned the expected 307 redirect to sign-in. This does **not** establish an
authenticated live Scheduled queue, a real provider admission, or end-to-end
Agent execution.

2026-09-26 Issue automation dialog keyboard checkpoint: a real Chromium
preview reproduced Tab being consumed by descendants of a disabled fieldset
in the read-only policy editor. The shared modal-focus helper now excludes
controls matching `:disabled` (including fieldset descendants) and inert or
ARIA-hidden subtrees, and starts from the correct edge when focus enters from
outside the dialog. At 1440 px the fixed editor retained focus on its only
enabled Close control, Escape closed it and returned focus to the trigger;
at 390 px the dialog had no horizontal overflow, and a temporarily enabled
fieldset in the disposable browser check let Tab advance to the first input.
Both checks had no page errors or Next overlay. This is a fixture-only
keyboard/layout check, not authenticated live saved-view filter acceptance.
The optimized Console image containing this fix built successfully, was
recreated at local `:3110`, and reached a healthy container state; its
unauthenticated Issues route still redirects to sign-in.

2026-09-26 Agent provider-acknowledgement ordering checkpoint: the running
`agent-task-source-admitter` has non-empty JEV URL, model and key configuration.
Provider-originated Issue commands, automatic Issue candidates and Draft PR
feedback now carry an exact task/revision source-release capability in their
first acknowledgement. The interaction responder queues source capture only
after the provider accepts that marker-keyed reply. The release rechecks
tenant, installation, repository, resource, marker binding, task revision and
pending state; duplicate delivery is idempotent, while cancelled or stale
tasks cannot enqueue work. Console-created tasks retain their direct source
path. The full Go suite passed, and the full store suite plus dedicated
acknowledgement and Agent lifecycle integration tests passed on disposable
isolated PostgreSQL databases. Updated `control-api` and
`interaction-responder` images were built and recreated locally; `/healthz`
returned 200. This fixes response ordering, **not** end-to-end coding
acceptance: the normal local profile still has no running Agent adapter or
credential broker, and its coding executor, sandbox, model and provider-write
settings are not configured. No live Issue-to-Draft-PR run was attempted.

2026-09-26 local sign-in recovery checkpoint: when the browser opens the
Console on an unregistered loopback origin, the page still refuses to start
OIDC with the wrong callback, but now offers a link constructed only from the
deployment-owned `OPEN_REVIEW_APP_URL` plain origin. It preserves the sanitized
internal `next` path. The rebuilt local Console returned `/api/health` 200;
an actual Chromium navigation at `:3110/sign-in?next=/acme/agent-work` showed
the link to `https://review.rainlib.com/sign-in?next=%2Facme%2Fagent-work`.
Clicking it reached the configured public origin and exposed the real
organization SSO action. At 1440×900 and 390×844 the recovery action was in
the initial viewport with no page-level horizontal overflow. TypeScript,
focused ESLint and optimized production build passed. This proves recovery
navigation and callback-source separation, not an authenticated Agent Work
session or a live coding execution.

2026-09-26 Agent Work first-run guidance checkpoint: the empty task queue now
checks the independent provider-connection and repository-policy read results
before telling an operator to use `@openreview implement`. With no verified
installation it links to Connections; with installations but no `manual`
policy it links to the repository-admission form; only a manual-policy state
describes the Issue command. Failed connection/policy reads remain explicitly
unknown instead of looking like a successful empty setup. The queue copy also
recognizes opt-in labeled Issue candidates. Twelve focused state tests,
TypeScript, focused ESLint and the optimized Console build passed. The new
Console image is running locally and `/api/health` returned 200; an
unauthenticated Agent Work request correctly redirected to sign-in. The
authenticated empty-state visual flow remains unverified because the current
local origin is not an OIDC callback and the public browser session is not
authenticated.

2026-09-26 Jev live connectivity checkpoint: the running
`agent-task-source-admitter` has all three Jev environment entries, and its
fresh worker heartbeat reports `decision_configured=true`. The opt-in
`TestLiveJevBackend` sent only a synthetic documentation-typo Issue to the
configured HTTPS endpoint and passed, verifying authentication, protocol and
bounded response parsing. This is a real remote classifier smoke test, not
an evaluation of classification quality. The local control plane has zero
repository Agent policies and zero Agent tasks; no provider Issue was sent to
Jev, and no coding adapter or credential broker was exercised.

2026-09-26 Agent Work configuration/first-run checkpoint: the running
source-admitter still has non-empty Jev URL, key and model entries (values were
not printed), and the prior synthetic live Jev smoke test remains the only
remote classification evidence. The Console now shows four independent gates:
verified provider connection, repository Manual admission, Jev worker
configuration and separately deployed coding executor. The empty queue hint
matches a Manual policy only to a verified, active installation with the same
provider, API base and permitted repository scope; the reserved `suggest` mode
is labeled as creating no tasks. Queue/worker details are collapsed by default.
Thirteen focused state tests, TypeScript, focused ESLint and the optimized
Console build passed. The rebuilt local Console is healthy and `/api/health`
returned 200. A disposable internal-only development-auth preview used the
same live database to check the desktop and 390 px mobile layouts, repository
selector and Manual form without saving a policy or making a provider write;
the preview containers were removed. PostgreSQL still reports zero Agent
policies and zero Agent tasks. The running task runner has no adapter URL,
callback URL or shared secret, and no adapter service is running, so this
checkpoint is not an authenticated OIDC acceptance or Issue-to-Draft-PR run.

2026-09-26 Agent reservation cleanup checkpoint: the runner now issues an
exact task/attempt/job cancellation when adapter submission succeeded but the
authoritative attempt-to-job attachment failed, including a lost lease or an
ambiguous database response. Cleanup gets its own five-second deadline if the
incoming request context has already been cancelled; a failed cancellation
is logged and never causes the runner to start the unbound job. The normal
two-phase start fence and one-use control-plane claim remain authoritative.
Focused uncached runner, adapter and decision tests, runner race tests,
targeted vet, diff check and the full Go suite passed. The local runner image
was rebuilt as `sha256:a61dc68696eef65b2db2c39f2716cd9e6db1937b30a1bbdbbcbe94c8e2f252d0`
and recreated; its fresh database heartbeat confirms `adapter_configured=false`.
No Agent policy/task, adapter execution or provider write was created. This
improves failed handoff cleanup, but does not prove live coding acceptance.

2026-09-26 Jev and adapter reachability checkpoint: the running source worker
still has a non-empty Jev key and a fresh `decision_configured=true` heartbeat.
The opt-in live Jev test sent one synthetic documentation-typo Issue to the
configured endpoint and passed; it created no provider Issue or Agent task.
Runner-to-adapter health now uses a signed, bounded, read-only probe, records
fresh pass/fail evidence in durable heartbeats, and distinguishes unobserved,
unconfigured, configured-only, unreachable, partially reachable, and reachable
but coding-unverified states. The Agent Work readiness panel and Platform
Health queues view expose that distinction without publishing URLs, secrets,
worker identities or fleet counts. Migration `000103` was applied locally.
An isolated migrated PostgreSQL integration test covered failed, partial and
all-reachable aggregation, then its temporary test database was removed. The
full Go suite, targeted race/vet, Console TypeScript, focused ESLint and
optimized production build passed. The rebuilt API, runner and Console are
running; `/healthz` and `/api/health` returned 200. Current runner heartbeat
still reports `adapter_configured=false`: no adapter or Draft PR execution was
verified. Authenticated Agent Work rendering was not accepted in this local
callback profile.

2026-09-26 isolated coding-sandbox acceptance checkpoint: built the pinned
`agent-sandbox-codex` image and ran the opt-in Linux Docker tests in a
disposable internal-only network and private workspace volume. The first
Codex child exited before the synthetic model fixture accepted a request;
the later integrated run captured `No space left on device` from Codex's
in-process app-server. Docker Desktop's 58.4 GB virtual filesystem had zero
available bytes even though the macOS host had ample free space. Reclaiming
1.64 GB of rebuildable BuildKit cache, without touching active images,
containers, database or user volumes, restored execution. The same
image then passed one rerun, five consecutive repetitions, and a run after
disconnecting/reconnecting the test network. Each passing run made two local
TLS-fixture model requests and proved that the child could not read the
adapter's Docker socket, state volume or provider/model/HMAC credentials.
The real Docker recovery and cancellation tests also passed. Diagnostic
output capture was used only while investigating and removed from product
code. The exact disposable test container, network, volume and image tag were
removed afterward. This is stronger sandbox evidence, but no real model
provider, scoped GitHub/GitLab coding credential, Issue task, provider branch
or Draft PR/MR was exercised. Docker-host free space remains an operational
preflight for deployment acceptance.

2026-09-26 isolated Issue-to-Draft fixture checkpoint: extended the existing
GitHub API/bare-Git adapter fixture with an opt-in pinned Codex Docker profile.
The actual child used a job-scoped model broker backed by a synthetic TLS
Responses stream to edit `README.md`; the trusted adapter validated its patch,
retained a pre-publication checkpoint, pushed the exact commit to the temporary
`agent/test-task` ref and created one Draft PR through the local GitHub API
fixture. A second case changed the Issue after coding: it made no push or
Draft, despite a completed model/tool round trip. Both cases passed twice
and again after removing the temporary diagnostic instrumentation; ordinary
non-Docker fixture cases and the full Go suite also passed. No real GitHub
installation, coding credential or paid model was used. The disposable test
resources and image tags were removed. Real provider acceptance still needs
an explicitly scoped Coding App, coding model configuration, adapter callback
secret, enabled repository policy and approved task; the current local runner
has none of these configured.

The sandbox adapter now checks for at least 1 GiB of available space on its
shared workspace volume before accepting or starting new coding/verification
work. Startup first validates the pinned image, internal network and named
volume, then removes only its own orphan child/workspaces, and only afterward
enforces the capacity gate. This ordering permits recovery cleanup when disk
space is low. Docker daemon storage may be on a different filesystem and
still requires an independent deployment alert. Capacity boundary and
resource-preflight tests passed; this guard does not substitute for a real
provider acceptance run.

Recovery now also requires the workspace root's full path to be canonical:
checking only the final directory allowed a symlinked parent to redirect
owned-workspace cleanup outside the private volume. A regression rejects that
parent-symlink case before listing or removing any child workspace. The
supported container mount at `/workspaces` remains the expected canonical
path; macOS fixture roots are resolved before this Linux-oriented preflight.

2026-09-26 Audit history navigation checkpoint: Console Events now uses a
tenant-scoped keyset cursor instead of stopping at the newest 100 records;
each page shows 50 events and an Older/Latest navigation state. An event URL
loads its exact ID independently of the current page, so older shared links
retain a detail panel. Export jobs no longer depend on the Events request for
their freshness state. Control API rejects malformed cursor IDs; PostgreSQL
orders by `(created_at, id)`, uses a matching tenant index, resolves the
cursor only inside the authorized tenant and returns 404 for another tenant's
detail. Action filtering treats `%` and `_` as literal prefix characters.
An isolated PostgreSQL 16 database migrated through `000104` and passed
same-timestamp pagination, foreign-cursor/detail, role and literal-filter
regressions; the temporary database was removed. This is source and isolated
database evidence. Migration `000104` is also applied to the local development
database. Updated control API and Console images were built and recreated;
`/healthz` and `/api/health` returned 200. A temporary loopback-only
development-auth API read the existing 480-event workspace: pages one and two
each returned 50 events with different first IDs; an older detail returned
200, a malformed cursor returned 400, and a foreign-tenant detail returned
404. A separate
loopback local-preview Console rendered the real initialized workspace Events
and Exports routes with HTTP 200; its authenticated BFF forwarded a one-item
cursor page and loaded an older event detail, and server-rendered HTML retained
the detail panel. Both disposable preview processes were stopped. This is
live local-data HTTP evidence, not deployed OIDC/browser visual or human
accessibility acceptance of the Audit page.

2026-09-26 Agent repository selection checkpoint: the Agent Work policy form
now searches the authorized installation inventory on the server, so a target
repository beyond the first 500 can be selected without widening the
installation's repository scope. The Control API applies a literal,
case-insensitive substring filter before the existing scope check; the Console
BFF forwards only a bounded query, and the client discards stale searches when
the installation or text changes. An isolated PostgreSQL 16 database migrated
through `000104` passed a 503-repository fixture, out-of-scope match exclusion,
and literal `%`/`_` checks; the temporary database was dropped. The new Go API
binary read the existing workspace inventory through a disposable
development-auth container, and a local-preview Console BFF returned the same
repository from that API; Agent Work rendered the search control with HTTP 200.
Full Go tests, focused vet, Web typecheck/lint and production build passed.
The temporary processes and test binaries were removed. This is not an
authenticated browser interaction, a provider resync of more than 500 real
repositories, or a live Agent Issue-to-Draft PR acceptance run.

2026-09-26 Agent Work Console deployment checkpoint: the source worker has a
non-empty Jev credential and a fresh `decision_configured=true` heartbeat;
one opt-in HTTPS request using a synthetic Issue passed against the configured
Jev endpoint. Focused decision tests and vet passed. Three unreferenced old
Console image records and three exact, project-specific old Go BuildKit records
were removed; no running container, database volume or queue was deleted.
Docker free space rose from about 676 MiB to 1.9 GiB before the Console build.
The production Console build passed compilation and TypeScript, and the new
image `sha256:b8895b9a77155a401b41102346d9ab49b30d4517d3ac12fad6dc3f5177168125`
was deployed at `:3110`. `/api/health` and the Control API `/healthz` returned
200; unauthenticated Agent Work redirected to sign-in, which rendered 200.
The live database still has zero Agent policies and zero tasks. This verifies
deployment and the Jev protocol only, not authenticated Agent Work interaction
or a real provider Issue-to-Draft PR run; the coding adapter and its model
credential are not configured in this local deployment.

2026-09-26 Agent policy exact-identity checkpoint: Agent Work no longer
depends on the first 100 overview policies to edit a selected repository.
After repository selection, the Console reads the provider, API base URL and
repository identity exactly, waits for the authoritative revision, then keeps
the existing optimistic lock on save. A stale revision triggers a re-read;
unavailable reads disable saving instead of treating an existing policy as a
new one. When the overview reaches its 100-item bound, readiness and the empty
queue now say to check the selected repository rather than claiming no Manual
policy exists. A fresh isolated PostgreSQL 16 database migrated through
`000104` passed a 101-policy fixture, exact lookup and foreign-actor denial;
the disposable database was removed. API tests, full Go suite, targeted vet,
13 Agent Work state tests, Web typecheck/lint and production build passed.
The new Control API and Console images were deployed locally, with `/healthz`
and `/api/health` returning 200; a disposable development-auth API checked
200 for absent exact policy, 400 for an incomplete selector and 401 without
authentication, then was removed. The live database still has zero Agent
policies/tasks, so authenticated browser editing and real Issue-to-Draft PR
acceptance remain unverified.

2026-09-26 Agent task history checkpoint: Agent Work now pages tasks with a
tenant-scoped `(created_at, id)` keyset cursor instead of permanently hiding
tasks after the newest 100. Selecting a task preserves the current page, and
the queue provides older/latest navigation plus an escape path from a stale
cursor. Migration `000105` adds the matching index. A disposable PostgreSQL
16 database migrated through `000105` and passed a 102-task same-timestamp
fixture with no duplicates or omissions; a foreign-tenant cursor and actor
were denied, and the test database was removed. API cursor validation, the
full Go suite, 14 Agent Work state tests, Web typecheck/lint, host and Docker
production builds passed. Migration `000105` was applied to the local database,
and the rebuilt Control API and Console returned 200 from `/healthz` and
`/api/health`. A disposable loopback-only development-auth API read the live
empty task page (`{"agent_tasks":[]}`), rejected malformed and unknown cursors
with 400/404, and required authentication (401); that API container was
removed. The source worker still reports a fresh configured Jev heartbeat,
but the live workspace has zero Agent policies/tasks; the isolated coding
adapter and model credential are absent. This is not a real Issue-to-Draft PR
acceptance run or authenticated browser proof of multi-page navigation.

2026-09-26 Agent task Console pagination acceptance: a fresh disposable
PostgreSQL 16 database migrated through `000105` and retained 102 synthetic,
same-timestamp tasks after the isolated store test. A loopback-only
development-auth API and Console, both using the current production images,
served this data without changing the running OIDC deployment. The first
Console task page rendered `Older tasks` and the BFF returned 100 tasks with
a cursor; the next Console page returned HTTP 200, rendered `Latest tasks` and
`End of history`, and its BFF returned the remaining two tasks. Opening one
of those older tasks with its cursor rendered its exact detail at HTTP 200.
An invalid cursor kept the queue unavailable and exposed a Latest recovery
link instead of showing an empty queue; an unknown cursor returned 404. The
same temporary preview against the real, read-only workspace rendered Agent
Work at HTTP 200 with four distinct gates: verified provider, no admission
policy, configured-only Jev, and no coding executor. The three disposable
containers and their tmpfs database were stopped/removed; a headless browser
profile and screenshot were moved to Trash after inspection. These checks
prove server rendering and BFF/API pagination, not interactive browser-click
acceptance, mobile visual fidelity, provider writes, or a real Issue-to-Draft
PR run. Claude remains explicitly disabled in the Console and rejected by the
policy API until a separate credential broker exists.

2026-09-26 Agent decision evidence presentation checkpoint: the configured
Jev endpoint accepted a fresh synthetic Issue request, and the source worker
reported a fresh `decision_configured=true` heartbeat. Agent Work now renders
the normalized `model/advisory` result separately from the deterministic
Judge/Evaluate/Verify checks; its type contract includes the actual advisory
stage and no longer counts that stage as a hard gate. Fifteen Agent Work data
tests, focused Go decision/domain tests, Web typecheck, focused ESLint, and a
host production build passed. Three unreferenced older Console image records
and three exact rebuildable BuildKit records were removed to restore Docker
capacity; no active image, container, database, queue or volume was removed.
The Docker production build passed and Console image
`sha256:8ff16c8b28aa169412add04ab9433a7ed0ab232a832d7a77ac80a856e1499857`
is running healthy at `:3110`. A separate tmpfs PostgreSQL 16 database
migrated through `000105` passed the Agent policy/task lifecycle test, retaining
one synthetic Jev-classified task. A disposable development-auth API/Console
rendered that task at HTTP 200: the BFF returned
`judge,evaluate,verify,model` with `passed,passed,passed,advisory`, while the
page showed three deterministic checks and a separate model advisory. Those
three disposable containers and their synthetic tmpfs database were removed.
The live workspace still has zero Agent policies/tasks and no coding
adapter/model broker, so this is evidence clarity, not Issue-to-Draft PR
acceptance.

2026-09-26 Issue inbox tab-selection checkpoint: built-in tab links now carry
the selected issue ID. The list API checks selection membership against the
same tenant-scoped SQL predicate as the target view, independent of its current
pagination page; the inspector stays open only when the issue still matches.
For a mismatch, the client closes the inspector, focuses the active tab, and
replaces the stale selection URL. A PostgreSQL-backed integration test on a
uniquely scoped temporary tenant passed matching/nonmatching views and an
additional repository filter; its fixture was removed by test cleanup. Go
domain/API/store tests, Web typecheck, focused ESLint, host production build,
and `git diff --check` passed. The local Control API and Console images were
then rebuilt as `sha256:c3ae00907323a15eab410ad2402634612ce61c34b37be2b2616b40069a37c042`
and `sha256:2cf936b5252b47d1a4f3f4867671716e2c6b6b0c77917b455656c96ff05c4d21`;
only those two services were recreated. Both health endpoints returned 200,
while PostgreSQL and RabbitMQ stayed healthy. A loopback-only, disposable
development-auth API/Console used the existing synthetic Issue test tenant:
the control API returned `selected_in_view=true` for All/Open and `false` for
Resolved; the BFF returned the same selected record and membership evidence.
In a real browser, All → Open retained the inspector and selected URL, while
Open → Resolved closed the inspector, removed `selected` from the URL and
focused the active Resolved tab. Browser console/page errors were empty, and
both preview containers and the browser session were stopped. This verifies
the local UI/API/data path for this interaction, not authenticated OIDC or a
production provider Issue workflow. Docker VM space was 1.5 GB afterward;
only six exact unused old images and three exact rebuildable BuildKit records
were removed, without touching active services or volumes.

2026-09-26 Issue inbox facet and deployment checkpoint: active-only,
critical, and assigned views now exclude inactive statuses and zero-active
occurrence aggregates. Repository/category suggestions come from bounded
tenant-wide facets rather than the current page, and an empty unfiltered view
offers a route to All instead of a no-op Clear filters link. Go domain/API/store
and decision tests, the PostgreSQL-backed keyset/filter integration test, Web
typecheck, focused ESLint, host production build, and diff whitespace checks
passed. The Docker VM has 8.3 GB RAM and local GitLab occupied about 5.3 GB;
the initial Console image build was killed during compilation. Limiting Next
build workers to two and enabling its Webpack memory optimization yielded a
complete production Docker build without stopping GitLab. Control API image
`sha256:6477bca025852ee4f91c4a1b7767f646aa8dba00c2a7fe115f38ff415cc6aa6e`
and Console image
`sha256:167c0caabed7e91292674b8e49b897bb72cab495be4a78acc698a65af5d94acb`
were deployed; Console health and Control API metrics returned 200. The
running source-admitter has non-empty Jev endpoint/model/key configuration,
and its latest durable heartbeat was fresh, unexpired, and reported
`decision_configured=true`; no paid Jev request was repeated. An independent
browser session reached the sign-in page but could not inspect the protected
Issue inbox because the local `127.0.0.1:3110` origin is not the configured
OIDC callback (`review.rainlib.com`). This is deployed-build and service
evidence, not authenticated browser or Issue-to-Draft-PR acceptance.

2026-09-26 current Jev and authenticated-preview checkpoint: the configured
deployment key matched the running source-admitter key without exposing it.
The opt-in `TestLiveJevBackend` sent one repository-free synthetic documentation
Issue to the actual Jev endpoint and returned a valid bounded signal in
1.22 seconds. It created no task, branch, or provider write. The live database
has zero Agent repository policies and zero Agent tasks, and the coding adapter
URL/model/write credentials remain unset; classification connectivity alone
does not complete the Issue-to-Draft-PR chain. A loopback-only disposable
development-auth API/Console read the existing workspace without mutations:
the empty Open view retained two category and one repository suggestions from
tenant-wide facets, while Resolved showed two retained issues and Critical /
Assigned both showed zero active issues. Browser inspection found a remaining
no-op toolbar Clear filters action on an unfiltered view; the action now renders
only when a filter exists. After a production Docker rebuild, a browser verified
the unfiltered Open view has no Clear filters control, View all issues navigates
to All with two records, and a filtered view exposes a valid Clear filters URL.
Web typecheck, focused ESLint, diff check and production Docker build passed;
image `sha256:e4f4598165579014b510eafa34bbe7fa28ec5a053e739aa1050c9b16fb23475c`
was deployed to the local Console, whose health endpoint returned 200. The
preview browser and its two disposable containers were removed. During the
memory-pressured image build, the source-admitter container restarted once;
its later heartbeat was fresh, unexpired and `decision_configured=true`.
This verifies a local read-only UI/API/data path, not deployed OIDC identity,
coding execution or provider Draft-PR acceptance.

2026-09-26 pull-request empty-state checkpoint: the PR index no longer offers
a no-op Clear search link for an empty unfiltered view. A pure decision helper
now distinguishes a search miss, stale cursor, empty current tab with records
in All, true first use, unavailable data, and a count/list contradiction. First
use links to the manual PR/MR review command guide; an unavailable backend is
never described as an empty workspace. Four direct helper tests, Web typecheck,
focused ESLint, diff check and a host production build passed. A loopback-only
production-mode Next preview and disposable development-auth Control API read
the existing workspace without writes: Active contained zero current runs,
All contained two, View all reviews opened both, and a nonmatching search's
Clear search link removed the query and returned to All. The browser, host
preview process and disposable API container were stopped afterward. To
deploy the same edit safely on the memory-constrained local Docker host, the
Console Dockerfile now caps only the build process's Node heap at 1024 MiB.
Four exact, reclaimable, unshared stale build-cache records were pruned; no
images, containers, or volumes were removed. A new production Docker build
passed, producing image
`sha256:94c1b83eec7acd349b0394e0b826f581877f3729a5a7226a4a410cf82f9743ab`.
Only the primary Console was recreated on `:3110`; its health endpoint
returned 200, and the source-admitter's restart count remained unchanged at
10 during the bounded build. A second loopback-only preview using the exact
new Docker image and real workspace data verified Active 0, All 2, the View
all reviews transition, both PR rows, and their GitHub source links. Its
browser and two disposable containers were then removed. This verifies the
packaged Console and a local read-only UI/API/data path, not deployed OIDC
identity, coding execution, or provider Draft-PR acceptance.

2026-09-26 coding-model tool-boundary checkpoint: the Codex job now disables
Web Search and multi-Agent both in its isolated config and via CLI overrides,
so a repository-local config cannot re-enable them. The model broker rejects
hosted/remote tool declarations and tool choices, including entries nested
inside client-function namespaces, before sending the request upstream. A
synthetic TLS upstream verified the installed Codex CLI still reaches the
broker and completes a two-turn local function-call round trip with these
restrictions. Focused broker tests cover allowed local tools and denied Web
Search, Code Interpreter and remote MCP; `go test ./...`, `go vet ./...` and
the installed-CLI opt-in fixture all passed. No paid model, provider Issue,
coding credential, branch or Draft PR was used. The local deployment still
does not configure a coding model, isolated executor image or scoped write
broker, so this boundary fix is source-level evidence, not a real Agent run.

2026-09-26 adapter image checkpoint: the current source built the optional
`adapter-codex` target into
`sha256:b0f537d44d15cb0d1c2823f976a84118b7e36218993144dfbaf1531163a36abc`.
The image itself reported `codex-cli 0.142.1`. Its adapter binary includes
the job-scoped tool boundary above; the separate existing per-job sandbox
image remains unchanged. The build did not recreate any running worker, and
the source-admitter restart count stayed at 10. Three exact build-cache
records verified reclaimable and unshared were removed after the build,
restoring Docker overlay free space from about 745 MiB to 2.1 GiB; no image,
container or volume was deleted. No adapter service was started: the local
deployment still lacks coding model and repository-scoped write credentials,
so a real coding attempt or GitHub/GitLab Draft publication remains unverified.

2026-09-26 current-image Docker sandbox acceptance: a newly created
internal-only `open-review-platform_agent-sandbox` network and dedicated
`open-review-platform_agent-adapter-workspaces` volume were used with the
new adapter binary image above and the existing immutable Codex child image
`sha256:85be05c6906d27603c3780c6aa145ef3ce5f8f3ddccf30617cd3e38e63d70418`.
An opt-in Linux test container ran the actual per-job Docker path. The child
completed two synthetic TLS model-broker requests and one local tool call;
the tool confirmed it could not access the Docker socket, adapter receipt
volume, provider token, model upstream key, or adapter HMAC secret. A second
opt-in test cancelled an in-flight model request and confirmed the child and
workspace were reclaimed. Both tests passed; no labeled sandbox containers
or temporary test containers remained, and the generated test binary was
removed. The internal network and empty dedicated volume remain for the
optional adapter profile. This verifies the packaged local isolation and
cancellation path only: no paid coding model, repository coding credential,
provider Issue, branch, or Draft PR/MR was exercised.

2026-09-26 real local GitLab Agent admission checkpoint: the already verified
OAuth installation for the disposable
`openreview-local-e2e/oauth-onboarding-1790064971` repository was given a
revision-1 `manual`/Jev policy with automatic-label admission disabled. GitLab
created real Issue #2 and root user note #67 (`@openreview implement`); the
existing project webhook reached Open Review, retained accepted interaction
`3100d8bb-b16d-42cf-94ba-4658742771f1`, and created task
`3c7cb2d0-8fd0-4da2-ad34-e3fb4ca3269e` with the frozen Jev backend.
Source capture did **not** begin: the acknowledgement outbox was published,
but the interaction-responder inbox released six attempts with
`GitLab OAuth credential resolver is not configured`. The running responder,
source-admitter and other provider workers, plus the current `.env`, lack
`PROVIDER_CREDENTIAL_ENCRYPTION_KEY` and GitLab OAuth refresh configuration;
the prior encrypted OAuth installation cannot be assumed usable merely from
its retained `verified` row. No Jev request, coding executor, branch or Draft
MR occurred for this Issue. The test task was cancelled at revision 2, its
policy restored to `disabled` at revision 2, and the database confirmed zero
execution attempts. The disposable development-auth API container was stopped;
Issue #2, note #67, interaction, audit and task evidence remain. To resume this
OAuth test, restore the **same** original encryption key and refresh client
configuration to every provider-calling worker, or deliberately reauthorize
the test installation under a new persistent key; then verify provider comment
publication and source release before claiming Jev Issue admission. Coding
model, sandbox adapter and independent repository-write credentials remain
separate later gates.

2026-09-26 acknowledgement recovery hardening (source-code checkpoint before
the local deployment recorded below):
the interaction responder now checks the exact task/revision/installation and
bound Issue command, automatic candidate or Draft feedback while holding the
task row lock through the provider acknowledgement and source-release outbox
write. A cancelled, superseded or timed-out task therefore cannot publish a
late “received” acknowledgement or release source capture. The Agent task
runner also expires tasks that have waited over 30 minutes for the first
provider acknowledgement, marking them `needs_attention` with an audit event
without pretending that a provider comment was posted. Its scan excludes
already released tasks before applying the batch limit and rechecks under the
row lock. Migration `000106` adds the pending-ack scan index. An isolated
PostgreSQL 16 integration run verified provider failure, stale revisions,
cancellation serialization, released-task exclusion and timeout idempotence;
the full Go test suite, focused `go vet` and `git diff --check` passed. This
turn also repeated the opt-in live Jev smoke test with the current `.env`
credential and a synthetic Issue; it passed without starting a coding Agent.
Both isolated test databases and the generated Linux test binaries were
removed after verification. This does **not** resolve the real GitLab OAuth
credential failure above: the
running provider workers still lack the encryption key and GitLab OAuth
refresh settings, so no live Issue→Jev→Draft path was accepted by this check.

2026-09-26 local deployment of the acknowledgement recovery hardening:
the current control-plane migrator advanced the existing local `openreview`
database from schema version `000105` to `000106`, and the pending-ack index
was observed in `pg_indexes`. Only `interaction-responder` and
`agent-task-runner` were recreated, from images
`sha256:27e567383e0c456d82a6c68ca8e5a9222d0735d113d66d535773cf26bb9b8ac8`
and
`sha256:217e9746dddeb7d07c9a05664e1f5168f41368971599f656373051db4b7815e9`
respectively. Both containers were running with current, unexpired worker
heartbeats; neither logged a startup error. The cancelled real GitLab test
task remained revision 2/cancelled, and there were no active tasks older than
30 minutes still waiting for a first acknowledgement. Deployment confirms
startup and schema compatibility, not an end-to-end provider ACK. The live
responder still reports empty GitLab OAuth client ID/secret and credential
encryption key, so the previous OAuth installation remains unusable for
publication until the original key is restored or it is reauthorized.

2026-09-26 real GitHub Agent admission checkpoint: the existing verified
`rainlib-open-review` GitHub App installation and owner actor mapping were used
with a revision-1, repository-exact `manual`/Jev policy and automatic admission
disabled. Previously closed E2E Issue
[RainLib/open-review-platform#9](https://github.com/RainLib/open-review-platform/issues/9)
was reopened, and RainLib posted `@openreview implement` as comment
`5843453653`. The real webhook created accepted task
`197e8bbe-acaa-4e9c-a069-639d1995f39f`; the bot posted a received comment,
then source-admitter reread the Issue and froze `main` at
`cb6650372d3e621ce923d485d2e96e8832178c2c`. The persisted revision-2
classification is `deterministic-v3+jev:jev-latest`, with a bounded Jev
`uncertain` signal at confidence 50 and final `needs_context` /
`request_context` gate. The bot published that outcome on the Issue. There
were zero plans and zero execution attempts, so this run proves real
GitHub Issue→acknowledgement→source capture→Jev decision and fail-closed
publication, **not** approved planning, coding, Draft PR, review feedback or
merge. The test task was cancelled at revision 3, policy restored to
`disabled` at revision 2, Issue #9 closed with an evidence comment, the
temporary loopback development-auth API stopped, and no unpublished outbox
rows remained.

The same run revealed a runtime dependency: Docker Desktop's RabbitMQ volume
had about 1.3 GB free against the configured 2 GB disk watermark, so the broker
blocked publisher confirms and delayed both Issue and Agent acknowledgements.
Eight untagged, unreferenced old images and rebuildable build cache older than
one hour were removed; no user volume or source file was deleted. RabbitMQ
subsequently reported no alarm and about 9 GB free, and the retained outbox
messages completed without replaying the GitHub command. The relay now claims
one message per 30-second lease by default, bounds each publish to 15 seconds,
applies a socket write deadline, abandons an ambiguous AMQP channel and
reconnects before the next row, and reports released publication failures.
Focused and full Go tests, `go vet`, and an opt-in real RabbitMQ reconnect
protocol test passed. The local `outbox-relay` was recreated from image
`sha256:9a9627b1f9a982a65646207eedab652a1f47ddbbf790ee964a00c25a212561f1`;
it was running with no unpublished outbox rows or broker alarm. The temporary
test binary was removed. A second forced disk-alarm run was not performed
against the live local broker, so failure-mode recovery after this change is
supported by bounded tests and the previous observed alarm, not a new live
fault-injection acceptance.

2026-09-26 Agent acknowledgement retry checkpoint: the configured Jev key in
`.env` matches the value in the running `agent-task-source-admitter` container;
the running container also has a non-empty Jev URL and model. No additional
paid Jev request was made for this configuration check. Source retry now
distinguishes a provider-confirmed first source release from an ACK timeout:
when the first release is missing, it requeues the immutable, identity-bound
provider acknowledgement at the new task revision and waits for the responder's
publication fence before source capture; a prior retry row alone cannot prove
ACK success. The Console labels this recovery accurately. An isolated
PostgreSQL integration test passed for timeout, idempotent retry, provider
failure, eventual ACK publication, and missing-original-ACK fail-closed
behavior. The existing source-retry/plan integration test, full Go suite,
`go vet`, Console typecheck and targeted ESLint passed. This verifies the
local state transition, not a newly injected live provider timeout.

The updated `control-api` image
`sha256:3fa45176a7f8948b7f087686b396e40e3ae25a6b0539201fc00df84694f3bc30`
was rebuilt and the local container recreated; its `/healthz` returned 200 and
the main database had zero unpublished outbox rows. A local Console production
build completed. Two default-network Docker builds stalled on npm registry
package downloads (one returned `ECONNRESET`); a targeted
`docker build --network host` completed dependency installation and the full
Next.js production build. The local Console was then recreated from image
`sha256:57d2068bdc73819509d084efbca0c8d89e1a91537edb620f3fc060e0ec014766`.
Its health check became healthy, `/api/health` returned 200, and an unauthenticated
`/acme/agent-work` request redirected to sign-in. The built server bundle
contains the updated retry wording; authenticated browser behavior remains
unverified in this checkpoint.
Only the isolated test database and test binary created for this checkpoint
were removed. The Jev key is present in both `.env` and the running
source-admitter; coding-executor and GitLab OAuth credentials remain separate
unconfigured acceptance prerequisites.

2026-09-26 Codex sandbox-image checkpoint: the existing fixed
`@openai/codex@0.142.1` Docker target built locally as
`agent-sandbox-codex` image
`sha256:0316615fb023e63444923f3330a22bd5cfc788ce012bc4b44a81f364b6807001`;
the separate `adapter-codex` image built as
`sha256:8b8689c7ee669d841b04743ca6498a9d4aeb108aedff9956d0456fe10ec63a24`.
The sandbox image now defaults to non-root UID/GID 10002, has no baked
provider/model credentials or volumes, and ran `codex-cli 0.142.1` under a
read-only filesystem, dropped capabilities and the existing internal-only
Docker network. The installed same-version Codex CLI passed the opt-in local
TLS fixture for a job-scoped Responses broker and stateless two-turn tool
history, without real model tokens or provider writes. Production adapter
startup without a task-bound credential broker exited nonzero as expected.
These checks establish image/build/protocol readiness only: the adapter was
not deployed with a coding broker, no approved task was executed inside the
child image, and no GitHub/GitLab Draft PR or feedback loop was created.

2026-09-26 real Docker sandbox acceptance: an opt-in Linux test ran the
`adapter-codex` image as a trusted parent with a disposable Docker socket
mount, a dedicated internal-only network, and a dedicated empty workspace
volume. It launched the immutable non-root `agent-sandbox-codex` image
`sha256:0316615fb023e63444923f3330a22bd5cfc788ce012bc4b44a81f364b6807001`
as a real child. Codex completed two Responses turns through the parent's
job-scoped broker and a local TLS model fixture; its tool command verified
that the child had no Docker socket, adapter state, provider token or upstream
model key. Separate real-container tests verified crash recovery removes an
owned stale child and cancellation stops a running child, restores trusted
checkout ownership, and leaves no labelled child container. All three opt-in
tests passed. The test-only network, volume (containing only its generated
lock file), Linux test binary and parent container were removed. No paid
model, provider write, approved production task or Draft PR/MR was involved;
the separately configured coding model and repository-write credential broker
remain required for that end-to-end acceptance.

2026-09-26 Jev configuration recheck and public UI acceptance: the running
`agent-task-source-admitter` has a non-empty Jev key, and the opt-in live
`TestLiveJevBackend` completed one HTTPS request with a synthetic Issue and
validated the bounded response. This is adapter connectivity only; no real
provider Issue, coding Agent, branch, or Draft PR/MR was created. The current
`RainLib/open-review-platform` Agent task policy remains `disabled` with
`auto_admission_enabled=false` despite selecting `jev`; setting a key does not
enable autonomous work. Real 390px Chrome device emulation found the public
landing heading clipped by a grid item's intrinsic minimum width. A zero-min
mobile grid track and min-width reset fixed the heading; local Chrome captures
at 320px, 390px and 1440px show complete headings and no document-level
horizontal scrolling. Frontend typecheck and targeted ESLint passed. This is
public-page visual evidence, not authenticated Console or provider-flow
acceptance.

The production Console image `sha256:5cba4a2c61b2cd66280620926efe5c5ac4e9728c2e12a3539becabb809fb9034`
then built successfully and replaced the local `console` container. Both local
`/api/health` and public `https://review.rainlib.com/` returned HTTP 200.
Post-deployment Chrome captures at the public origin confirmed the complete
390px landing heading and a usable 390px sign-in card; both had viewport and
document widths of 390px. No authenticated session, owner action, or real
provider task was exercised in this visual pass.

2026-09-26 Agent Work provider-readiness correction: the Console previously
reported `Verified` whenever the Agent task inventory contained an active,
verified installation, even if the installation's current read-only provider
probe was critical or stale. The current database contains both cases: the
real numeric-ID GitHub App installation has a fresh `live` probe, while a
retained local GitLab OAuth test installation is `verified` but its latest
probe is `critical`. The checklist now joins provider-health evidence by the
exact installation ID and distinguishes observed read access, partial access,
degraded/failed/stale probes, and unavailable health. It never infers provider
write permission or coding-executor readiness from a read-only probe. A
16-case Agent data test suite, frontend typecheck, targeted ESLint and a full
Next.js production Docker build passed. The resulting Console image is
`sha256:c4a855501d2d0371180ecb88f7cc300416ff83701b23420c2d69cc2b93936a76`.
The recreated local Console became healthy with zero health-check failures;
both local and public `/api/health` returned 200, and an unauthenticated Agent
Work request still redirected to sign-in. Authenticated visual acceptance and a real
GitLab OAuth credential recovery remain open.

A subsequent disposable development-auth preview used the existing
`rainlib-open-review` workspace without mutating its policy or tasks. The
Control API returned one real GitHub App provider health row in `live` state;
the authenticated Agent Work route rendered `Read access observed` with HTTP
200 at 1440px and 390px. Real Chrome device emulation then exposed an
unrelated mobile grid min-content expansion below the readiness cards:
`documentElement.scrollWidth` was 625px at a 390px viewport. Giving the
Agent Work content grid a zero-minimum mobile track and child fixed it; repeat
1440px/390px captures have `scrollWidth` equal to the viewport and no
overflowing elements. This visual check used explicit loopback development
authentication, not the deployed Casdoor/OIDC session. Focused React checks,
TypeScript, ESLint and diff whitespace checks passed. The disposable API,
Next dev server and Chrome profile were stopped/removed after capture; no
production authentication setting was changed for this test.

The final responsive Console image
`sha256:d6d0d355f6b4d15d36caa415369ff0d18b58ac96b2a94deced76f2daeae3c5cb`
built successfully and replaced only the local `console` service. Local and
public `/api/health` returned 200; an unauthenticated Agent Work request
still returned 307 to sign-in. This deploys the tested mobile grid fix, but
the public OIDC-authenticated Agent Work screen has not been visually accepted.

2026-09-26 continuation: the running source-admitter was confirmed to have a
Jev credential, and the opt-in synthetic `TestLiveJevBackend` completed one
real HTTPS evaluation with a valid bounded signal. A diagnostic output
inadvertently exposed the old key, so the credential must be revoked and
replaced before further external Jev acceptance. No real Issue payload was
sent during this continuation. The local Agent policy for
`RainLib/open-review-platform` is still `disabled` with automatic admission
off; the task runner has no adapter URL/signing secret, and no coding model or
repository-write credential is configured. Thus decision connectivity is not
Issue-to-Draft-PR readiness.

An isolated, explicit development-auth Console preview checked 36 static
workspace routes and real tenant-bound dynamic records. Real Chrome at 1440px
and 390px verified Agent Work, Issue, PR and task routes without document-level
horizontal overflow. The Issue detail previously put a full Markdown finding
and mitigation into the page header and repeated it in Summary. It now derives
a short display title and one-sentence summary while keeping the complete
finding in Evidence, safely rendering emphasis, inline/fenced code and no
model-supplied active HTML or arbitrary links. The fingerprint is compact and
copyable. Fourteen focused tests, Web TypeScript, targeted ESLint, production
Console build and browser checks of a real GitHub-origin finding passed. This
is loopback development-auth and local-provider-data evidence, not production
OIDC acceptance or a completed coding-Agent handoff.

2026-09-26 current Jev configuration acceptance: the running
`agent-task-source-admitter` has non-empty Jev URL, API key and model fields.
Using the current local `.env` credential, the opt-in `TestLiveJevBackend`
sent one synthetic, non-repository Issue to the real HTTPS decision endpoint
and passed in 8.20 seconds; the complete `internal/agentdecision` package
tests also passed. A credential value was inadvertently exposed in an earlier
diagnostic output and must be revoked and rotated before further use. No
GitHub Issue was changed, and no task or coding attempt was created by this
protocol test. At this checkpoint, the `RainLib/open-review-platform` policy
was `disabled` at revision 2, and the running task runner had no adapter URL,
callback URL or adapter
secret. This proves configured decision connectivity, not real Issue admission,
plan approval, code execution or Draft PR creation.

2026-09-26 real GitHub Issue-to-plan checkpoint: temporary
[Issue #11](https://github.com/RainLib/open-review-platform/issues/11)
specified a bounded README-only task with explicit acceptance criteria. The
exact repository policy was temporarily changed from `disabled` revision 2 to
`manual`/Jev revision 3, with automatic admission still off. RainLib's real
`@openreview implement` Issue comment created task
`7b448d18-5328-4e4b-b1e5-485f2da5cd48`; the bot published a received
acknowledgement and a source-verified reply. The source worker re-read the
Issue, froze its SHA-256 revision and `main` at
`cb6650372d3e621ce923d485d2e96e8832178c2c`, then recorded a
`deterministic-v3+jev:jev-latest` classification. Jev chose `plan` at
confidence 91; the effective decision remained `requires_human` with
`await_plan_approval`. An owner-authored, five-section bounded plan
`5876e3dd-9c23-4e26-9be9-adfe09a83533` reached `awaiting_approval` at
revision 1 with summary SHA-256
`6d0f63510730d977d339ae525433c5521f2a01740ac987d880bfa0990f68daf5`.
The real `@openreview status` reply showed that frozen commit, plan revision,
and digest. The real `@openreview cancel` reply cancelled the task at revision
4. The task had zero execution attempts; no plan approval, branch, Draft PR,
or README change occurred. The policy was restored to `disabled`/Jev at
revision 4, Issue #11 was closed as a temporary test artifact, there were
zero unpublished task outbox rows, RabbitMQ reported no alarms, and the
loopback-only development-auth API was stopped. This extends live acceptance
through Issue admission, decision and plan/status/cancel but **not** through
coding, provider publication, review feedback or merge.

2026-09-26 real Agent task detail presentation checkpoint: the retained,
cancelled task from Issue #11 was opened in an isolated loopback Console with
explicit development authentication and the existing live workspace data.
At 390px, Chrome rendered its Jev advisory, exact frozen commit, five-section
plan and zero-attempt state without horizontal overflow. The task detail
previously hid the task UUID and displayed the source-time
`await_plan_approval` action and a stored `awaiting_approval` plan without
explaining that cancellation had made approval impossible. The Console now
shows and copies the task UUID, labels classification next action as recorded
admission evidence, and marks the pending plan as historical and unapprovable
for a cancelled task. A 17-case Agent data test suite, Web TypeScript check,
targeted ESLint, live-data development render and 390px Chrome DOM/visual
check passed; `documentElement.scrollWidth` equalled the 390px viewport.
The local production Console build produced image
`sha256:61b9f50a593b01b533ee5e28a1fdc3517be91a6c7c29ae1d5f9a20d2e34132c8`;
only `console` was recreated. It became healthy with zero failures, and local
and public `/api/health` returned 200. The public OIDC-authenticated Agent
Work screen remains unaccepted; this presentation change does not prove a
coding executor or provider Draft PR. The disposable development API, Next
server and Chrome profile were stopped/removed after verification.

2026-09-26 provider merge-enforcement checkpoint: read-only GitHub API checks
confirmed that `RainLib/open-review-platform` protects `main` with a required
`Open Review / Analysis` status from the configured GitHub App. That rule does
not cover PR #3, whose target is `feature/saas-architecture-design`, or PR #6,
whose target is `feature/reliable-review-workflow`; both target branches report
no branch protection, and no applicable branch ruleset was returned. The
present successful checks on those PRs establish publication, not that a
failing review would prevent merging. No branch rule was changed. The provider
summary now distinguishes a failed review gate from a provider-enforced merge
block, and the Console explains the required provider-side setup. A failing
check on a deliberately protected test target is still needed for live GitHub
enforcement acceptance. GitLab CE enforcement was already live-accepted in the
2026-09-22 MR #5 blocked-to-fixed exercise above: its model-produced failed
status returned `ci_must_pass`, and the fixed SHA returned `mergeable` after a
successful status. A 2026-09-26 read-only database check also found the failed
external pipeline 7, successful external pipeline 8, and MR #5 pointing to
pipeline 8. This proves that one local CE path, not arbitrary duplicate-pipeline
selection, GitLab.com, self-managed HTTPS, or Agent Draft-MR creation. The
GitLab status publisher now includes the admitted source `ref` when available;
focused HTTP tests cover both running and failed status payloads, but a live
duplicate-pipeline test is still pending.

The focused publisher and runner tests, Web typecheck/lint, local production
build, and Docker Console image build passed. Only `console` was recreated with
image `sha256:79dd6aa7c355de5daae8add92c5265cc846bddcdc3f5d92c30f16808d24d39ac`;
its local health endpoint returned 200 and the container became healthy. The
unauthenticated policy route redirected to sign-in (307), so the new disclosure
was not accepted in an authenticated browser. Runner and terminal reporter
were not restarted: four older review-job rows remain marked `running`, and
the provider-comment copy change is source-verified but not yet deployed.
During the following source-only GitLab `ref` change, Docker Desktop's daemon
became unavailable; neither the new status payload nor the Console health was
re-accepted against the current runtime after that event.

2026-09-26 Agent start redelivery hardening (source only): after an adapter
reservation is durably attached, a lost `Start` response no longer causes the
execution message's retry to be silently acknowledged as “not queued.” The
runner may retry `Start` only for the same task/plan digest, original worker,
live lease and attached job, and only while the control-plane one-use start
gate is unclaimed. It never submits another job for this redelivery; a claimed
gate, cancellation, changed plan or expired lease remains excluded. Focused
unit, race and vet checks passed. The PostgreSQL integration assertions
were added but not run against a database because Docker Desktop's daemon was
unavailable. This does not configure an executor or prove an Issue-to-Draft-PR
provider run.

The same audit found an older lease-reclaim path that could run a second
coding job after an attached job's lease expired, racing the reaper despite
the design's no-automatic-rerun boundary. Reclaim now applies only before any
adapter job is attached; an attached expired job remains for reaper
`needs_attention` plus exact-job cancellation. Provider status copy also no
longer claims “no coding action was performed” for an attached job: it warns
that a branch push or Draft may already exist and requires human inspection.
The existing isolated-store integration scenario was revised to distinguish
safe pre-adapter reclaim, attached-job expiry, cancellation, and a separately
approved new-plan attempt; its database assertions still need a running
isolated PostgreSQL instance. Pure formatting tests, the complete Go suite,
focused race and vet checks passed without Docker. The named
PostgreSQL lifecycle test explicitly skipped because
`OPEN_REVIEW_TEST_DATABASE_URL` and an isolated database are unavailable;
neither its new SQL gate nor provider cancellation has runtime acceptance
from this source-only pass.

A follow-up source check confirmed there is no standalone PostgreSQL binary
installed locally and the Docker daemon is still unavailable. The reclaim
predicate is now independently unit-tested for an expired pre-adapter claim,
an attached reservation, a still-live lease, exhausted attempt budget,
terminal state and missing lease; all cases and the focused runner/store tests
passed. This improves the source-level guard evidence, but does not replace
the skipped SQL lifecycle integration or a live provider run.

2026-09-26 repository selection follow-up (source only): the post-verification
Connections editor now filters synchronized repositories, limits the scroll
surface, preserves exact scope entries outside its 500-repository page during
checkbox changes, and refuses a selection over the control plane's 100-entry
scope limit. Wildcard narrowing and inventory truncation are disclosed before
saving. Four focused selection tests, web TypeScript, ESLint and production
build passed. The first-install wizard still has no authorized provider
inventory before the installation receipt is persisted and verified; its
selected-scope input remains manual. A pre-install read-only preview needs a
separate server-side credential boundary, not a fabricated client list. The
local Console and Docker daemon were unavailable for browser/provider
acceptance in this pass.

The same day's live Jev smoke check found the deployment key present, but two
synthetic decision POSTs received no headers within the adapter's 10-second
HTTP timeout. An unauthenticated endpoint probe returned HTTP 405 in about
1.2 seconds, so network reachability alone did not prove decision readiness.
This supersedes the earlier successful smoke check for current-state
acceptance; no real Issue or provider write was attempted.

The setup `review_scope` checkpoint now reads the installation's actual
worker-synchronized, scope-filtered inventory on the server and embeds the
same precise repository picker directly in onboarding. A demo, missing or
unavailable inventory is explicitly non-editable; the Console connection
page remains a secondary path. The browser receives only repository metadata,
not provider credentials. Web type checking, focused lint and the production
build passed. This does not create a pre-install inventory: GitLab still needs
an initial exact or group scope from its authorized administrator, and the
provider picker remains limited to repositories already inside that scope.

The post-verification setup review-scope step now saves the real tenant
`review_drafts` and `rereview_on_push` settings before advancing its durable
checkpoint, alongside the existing installation automatic/manual choice.
Missing policy data disables advancement instead of implying the settings
were saved. If a policy write succeeds but checkpoint advancement fails, the
Console reloads current revisions before retry. The backend already applies
both flags at automatic webhook admission; its focused domain test and the
web TypeScript, ESLint and production build passed. No authenticated browser
or provider run validated the new setup controls while the local runtime is
unavailable.

2026-09-26 author-scoped onboarding and admission (source only): the provider
OAuth return now puts the verified GitHub/GitLab user ID into the signed,
HttpOnly installation receipt. The Console sends only the selected `all` or
`mine` mode; its BFF derives the author ID from that receipt. The control API
requires the same signed ID, and the store checks that it remains mapped to
the current workspace subject before creating or replaying an installation.
Migration `000107` retains `all` for existing connections and stores the
author scope separately from repository scope. Verified GitHub PR and GitLab
MR webhooks normalize the actual author ID (not GitLab's update actor); only
automatic admission checks that ID, failing closed when it is missing or
different. Explicit, separately authorized commands are unchanged. GitLab
deployment-token connections cannot select `mine` because they prove no user
identity. Setup displays both choices and its recorded coverage. API receipt,
normalizer, admission helper and UI draft tests, complete Go suite, web
typecheck/lint and production build passed. The new PostgreSQL identity-
binding integration test was added but explicitly skipped because
`OPEN_REVIEW_TEST_DATABASE_URL` is unavailable; migration execution, signed
OAuth browser handoff and a real GitHub/GitLab PR admission are still
unverified. The local Docker daemon and Console port remain unavailable.

GitLab MR updates can be triggered by someone other than the MR author. The
webhook's actor username is now used as the displayed author only when its
ID matches `object_attributes.author_id`; otherwise author name remains
unknown while the stable author ID still governs `mine`. An unknown name
fails closed if username-based `exclude_authors` is configured, because the
webhook alone cannot establish whether that author is excluded. Obtaining a
display name in that case needs a provider-authorized MR lookup and remains
unimplemented; it is not evidence that the updater authored the MR.

Current GitLab webhook payloads may omit the deprecated `project.http_url`.
MR, Issue, review-command, Agent Issue-command and Draft-MR feedback
normalizers now prefer `project.git_http_url` and retain `http_url` as a
self-managed compatibility fallback. Focused fixtures cover modern-only
payloads across these paths and a conflicting old/new pair preferring the
modern URL. This removes a source-level intake rejection, but live GitLab.com
and post-deployment self-managed acceptance are still required.

Self-managed GitLab can also be installed below a relative URL such as
`/gitlab`. The GitLab webhook normalizers now derive an API base with that
exact prefix from the clone URL and `path_with_namespace`, for both legacy
and modern project URL fields; a mismatched project path is rejected. Root,
nested-prefix, and mismatched-path fixtures pass. This is source validation,
not an accepted provider webhook from a relative-URL deployment.

The GitLab relative-URL path was also checked at the HTTP intake boundary:
a modern-field MR webhook with a valid secret reached the queue adapter with
the configured `/gitlab/api/v4` base, while an invalid secret did not. Worker
OAuth refresh derives `/gitlab/oauth/token` from that API base and now uses a
bounded, no-redirect HTTP client. Local 302/307/308 fixtures proved that a
redirect target receives zero client-secret/refresh-token requests. Real
provider token rotation, database credential replacement and live MR intake
under a relative-URL deployment remain unverified.

After the earlier Jev timeout, a fresh synthetic request using the configured
deployment key returned a valid bounded decision in 1.58 seconds. This proves
the current key and endpoint responded for that smoke test, not that the
source-admitter worker is running or a real Issue→Draft PR was accepted.

A subsequent opt-in `TestLiveJevBackend` run against the configured key passed
again in 4.55 seconds with a synthetic documentation-only Issue. Compose
configuration resolves a non-empty source-admitter Jev key, endpoint and model.
This confirms one current external decision response; it does not establish
worker readiness, repeatability under load, or coding execution. Docker's
daemon and the local Console were unavailable during this check, so an
authenticated Issue→decision→plan→Draft PR acceptance run remains open.

The live repository-scope editor now treats its Advanced scope text as the
current unsaved draft: typing exact or wildcard coverage updates the visible
checkboxes, and a subsequent checkbox change preserves newly typed exact
entries outside the synchronized page. Disabled or in-flight editors cannot
change selections. Five focused selection tests, Web type checking, targeted
lint and a production Next.js build passed. The current runtime was unavailable,
so authenticated setup and provider-side scope behavior were not browser-tested
after this change.

An empty in-scope inventory previously hid the repository-scope editor in
both onboarding and Connections, leaving no UI path to correct an initially
wrong scope. A live inventory now keeps Advanced scope available even with
zero matches; onboarding blocks advancement and offers a refresh until a
worker-synchronized repository matches. More importantly, a compare-and-set
scope update now atomically suspends admission (`verification_state=pending`)
and queues a fresh read-only provider probe; an in-flight probe rejects the
edit, and only successful re-verification restores admission. The request,
update, and completion paths use probe-before-installation locking. Six focused
selection tests, Web typecheck/lint/build, complete Go tests, vet and diff
checks passed. The PostgreSQL integration assertion for queued→running→
verified was added but explicitly skipped because the local Docker daemon and
`OPEN_REVIEW_TEST_DATABASE_URL` are unavailable; the new SQL transition and
authenticated browser recovery still require runtime acceptance.

The failed-first-verification branch now fetches the same tenant-scoped
repository read model and exposes Advanced scope correction directly on the
setup failure screen, including when the old scope yields an empty list.
Saving that correction returns the connection to pending verification instead
of asking the user to reinstall. Web typecheck, targeted lint and production
build passed after wiring this branch; an actual failed GitHub/GitLab install
and authenticated recovery remain unverified while the runtime is down.

A follow-up source audit found that GitLab's general project inventory reads
only its first bounded page. An exact project selected outside that page is
now probed by encoded full path; a wildcard group is probed through its
group-project endpoint with bounded results and subgroup inclusion. The
installation transition additionally requires a fresh matching repository
for **every** declared scope entry and a current probe receipt; retained old
inventory or a legacy worker's identity-only success cannot restore admission.
Provider HTTP fixtures, scope-evidence unit tests, complete `go test ./...`,
and `go vet ./...` passed. Once Docker became available, migrations and the
stale-inventory PostgreSQL transition test passed in an isolated temporary
PostgreSQL 16 database. The complete Go suite with that database also passed
on its first run. Repeating the suite against the same already-used test
database fails in unrelated claim-fairness and installation-routing fixtures
because some existing fixtures leave claimable jobs/installations; integration
verification therefore requires a fresh database until fixture cleanup is
repaired. No user workspace database was used for these test runs.

The configured JEV key was used for another opt-in HTTPS smoke test with a
synthetic, documentation-only Issue. `TestLiveJevBackend` passed in 1.05 s.
This verifies one external decision response, not a running source-admitter,
provider Issue admission, agent coding, or Draft PR/MR publication.

The local Console returned HTTP 200 after Docker startup. Its sign-in page
correctly reports that `127.0.0.1:3110` is not the configured OIDC callback
origin and links to the deployment Console instead. This is not an
authenticated browser acceptance of Agent Work or onboarding at the local
origin; bypassing that identity boundary would not prove the requested flow.
The configured HTTPS Console exposed the ordinary SSO entry, and its redirect
reached the Casdoor password screen; no credentials were entered, so the
post-login Agent Work UI remains unverified. The already-running
`agent-task-source-admitter` container has a nonempty Jev key in its environment
(value not printed). The isolated temporary PostgreSQL container used for
these tests was stopped and removed after verification.

## 2026-09-26 Verified-installation permission revocation

The periodic provider probe previously left an installation in `verified`
after a permanent authorization or selected-repository failure, so the
webhook/CLI admission queries could still accept new work. The probe
completion now preserves `verified` for a transient degraded response but
atomically changes it to `failed` on a critical result and writes a separate
`installation.verification_revoked` audit event. A claimed live result from
an old worker that lacks fresh evidence for every selected scope is also
critical rather than an implicit renewal. An isolated PostgreSQL 16
integration test proved degraded → still verified, permanent scope failure
→ failed with webhook admission rejected and the previous running review's
lease renewal refused, fresh scope receipt → verified with webhook admission
restored, and missing receipt → failed again. On a fresh
isolated database, migrations, complete `go test ./...`, `go vet ./...`, and
`git diff --check` passed. The running provider-prober image predates this
source change, so deployment-level behavior remains unverified. A replacement
provider-prober image built successfully from the current source (image
`sha256:b9ae2aac5d0ae3065d97af996ad6122d8502a7cc6da596d14f15cf6f4cc485eb`),
but the live container was intentionally not replaced: its database still
contains historical test installations, and a new periodic probe could alter
their admission state. The isolated test PostgreSQL container was removed.
The active
RainLib Agent policy is still `disabled` with automatic admission off even
though its decision backend is JEV; this is an intentional per-repository
switch, not evidence of a live Agent task run.

## 2026-09-26 Jev connection-reset recovery

The decision adapter now treats a transport-level connection reset or EOF,
whether before the HTTP response or while reading its body, as eligible for
the existing revision-fenced, at-most-three-attempt source retry. A cancelled
parent context, HTTP 401 and malformed model response remain permanent
fail-closed outcomes. Two deterministic transport tests cover the request and
response-body reset paths; the complete Go suite and focused `go vet` passed
without a test database. The source-worker image built as
`sha256:5cf2073ff76abfee578da1fc9e3ef0b0b87cdb6949b33e97e0e58438e0d147a2`,
but the running worker remains on image
`sha256:dbf06268f2bb08b6d5d9e31185f69afef33ea529469767398c7cb750534fb9a1`.
Database-backed retry and real provider delivery were not re-run for this
narrow change; the live worker was not replaced against the shared historical
test database.

## 2026-09-26 Source-worker deployment and disk recovery

Before replacement, the local database had only three cancelled Agent tasks,
no unpublished outbox messages, and no ready/unacknowledged Agent source,
execution, or cancel messages. The rebuilt source worker was then recreated
on image `sha256:5cf2073ff76abfee578da1fc9e3ef0b0b87cdb6949b33e97e0e58438e0d147a2`.
During startup PostgreSQL exposed a separate Docker-VM disk exhaustion:
end-of-recovery checkpoint writes failed with `No space left on device`.
Five exact untagged, unreferenced old Console images and six exact reclaimable
build-cache records from this repository were removed; no volume or database
row was deleted. PostgreSQL completed recovery and accepted a query; the new
source worker remained running and registered a fresh `decision_configured=true`
heartbeat. The Docker VM had approximately 2.3 GiB free at that check, so
retention/capacity still needs operational attention before load testing.
The `.env` contains the Jev key but no coding-adapter URL/signing secret,
Codex model-broker route/key, or sandbox image selection. The RainLib policy
remains disabled; this deployment verifies worker startup, not Issue-to-Draft
acceptance.

The source worker subsequently gained a single-binary Dockerfile with a
shared Go compiler cache mount. The production-shaped Compose build produced
image `sha256:2cd1810892a26202d8e0435d88b7c825260f99011ffa904d4ba6fcd5b318c686`
at 13,365,874 bytes versus 221,307,749 bytes for the previous all-binaries
image. Compose rendered successfully; the replacement container ran as
distroless UID 65532 and registered a fresh Jev-configured heartbeat. The
control API and Console health endpoints returned 200. The old large image was
retained as a local rollback artifact. This reduces one worker's rebuild
footprint, not the other shared-image services or the underlying Docker-VM
capacity limit; no real Agent Issue was admitted in this check.

Agent Work now renders an explicit coding-adapter gap when the live health
snapshot reports `agent_executor=not_configured`. The self-hosted view names
the runner URL/signing secret, reviewed per-job sandbox, separate Codex
Responses route/key and installation-scoped provider write credential; cloud
view directs the user to the operator. It does not accept a browser secret or
call Jev configuration proof an executable coding Agent. The change stays in
the server-rendered route, passes Web typecheck, targeted ESLint and a local
Next.js production build. A subsequent Docker production build produced Console
image `sha256:25137ca7d6aa8eff1bbcc4fe8373cb0027a1bb10e255209ce3306aad755c7531`,
which replaced only the local `console` container. Its health check became
healthy, `/api/health` returned 200, and unauthenticated `/acme/agent-work`
redirected to sign-in as expected. An authenticated view of the new Agent Work
callout remains unverified because the local loopback URL is not the configured
OIDC callback origin. This is deployment and route evidence, not live Agent
execution or full UI acceptance. The Docker VM had about 1.5 GB free after
the build, so further image builds require capacity headroom.

The prepared provider-prober image
`sha256:b9ae2aac5d0ae3065d97af996ad6122d8502a7cc6da596d14f15cf6f4cc485eb`
was then deployed without rebuilding or restarting its dependencies. Before
replacement, 21 active historical test installations were still marked
`verified` despite a latest `critical` probe (credential unavailable or
invalid local endpoint); the one real `rainlib-open-review` GitHub installation
was `verified/live`. After the next scheduled probe cycle, all 21 stale
verified test installations had transitioned to `failed`, with 21
`installation.verification_revoked` audit events. The real RainLib installation
remained `verified/live` on a fresh probe. PostgreSQL and Console stayed
healthy and Console `/api/health` returned 200. This validates the deployed
permission-revocation transition, not a new real webhook admission or
provider-side PR review.

The Review Checks tab now separates external provider CI from Open Review's
immutable merge-gate decision, durable stages, and publication receipts. Since
provider CI is not yet ingested by this read model, the new panel explicitly
shows `Not observed for this revision`, names the reviewed head, and links to
the provider PR/MR for current checks; it never infers CI success from a passed
Open Review gate. Existing provider-link tests (including self-managed GitLab),
Web typecheck, targeted lint, host and Docker production builds, and
`git diff --check` passed. Console image
`sha256:3fe391088b9dee0086b0db282a9b1161e0cc4c4dbb3f31034491f51a789ed701`
was deployed and became healthy; local `/api/health` returned 200, and an
unauthenticated Checks URL redirected to sign-in while preserving its tab
parameter. Authenticated visual acceptance and actual provider CI ingestion
remain open. Docker VM free space was about 1.2 GB after this build.

Provider CI collection now has a source-level, provider-qualified read adapter
in `internal/providerchecks`. It resolves a short-lived installation credential
only for an exact 40/64-character commit SHA, reads GitHub check runs plus
combined commit statuses or GitLab pipelines (including self-managed API path
prefixes), rejects mismatched response SHAs, redirects, oversized responses,
invalid bases, and default private-network targets, and marks truncated pages
instead of treating them as complete. The safe provider HTTP transport is
shared with installation health probing. Injected probe clients now also pass
through the no-redirect guard; a local redirect test proves the bearer token
is not forwarded to a second host. Focused tests and `go test ./...`,
`go vet ./...`, and `git diff --check` passed; database integration tests were
not run in this source-only step. There is **no** durable probe job, stored
observation, API read model, or Console display of real CI yet. The running
provider-prober still uses its prior image; this source refactor was not
deployed. The Checks tab must continue to say `Not observed` until those
remaining stages are implemented and accepted with real provider credentials.

The next source step adds migration `000108`, a per-run exact-head provider
observation with a recoverable lease, a separate read-only collection loop
inside the existing `provider-prober` security boundary, and an optional
Review Evidence field. The Console Checks tab now distinguishes
queued, running, failed, stale, truncated, zero-check and observed states; none
is converted into an Open Review merge-gate conclusion. The SQL migration
passed a PostgreSQL `BEGIN`/`ROLLBACK` syntax smoke check, and focused Go
tests, full `go test ./...`, `go vet ./...`, Web typecheck and targeted lint
passed at source level. The migration was rolled back after validation, and
the new provider-prober/API/Console images have **not** been deployed. No real GitHub/GitLab CI snapshot or
authenticated browser acceptance is claimed yet.

Jev configuration was checked again without printing its value: the local
key matches the running source-admitter container, and a fresh opt-in
synthetic HTTPS `TestLiveJevBackend` passed. RainLib's real repository policy
remains `disabled` with `jev` selected and automatic candidate admission off.
This verifies decision-provider connectivity only; it neither enables Issue
automation nor configures the separate coding adapter/sandbox.

The local Compose topology now has an explicit `docker-compose.core.yml`
evaluation override. The base file stays full-featured; the override profiles
optional notification, feedback, probe, scheduling, rollout, governance and
Agent Work workers while retaining the PR/Issue queue chain and rule-exception
expiry. Database checks showed no enabled notification destination, SSO
configuration, active Agent policy, queued feedback/model/governance job,
scheduled review, or active rollout before idle optional workers were stopped.
The current local instance runs 17 containers with Agent Work retained; core
review/Issue queues each had one consumer and zero ready messages, and Console
`/api/health` returned 200. This is a local operating mode, not evidence that
the paused optional capabilities work without their corresponding profiles.

During the change, an old `interaction-admitter` container was found exited
because a deleted GitLab-test binary remained bind-mounted. Recreating that
single service from the normal Compose definition restored a fresh heartbeat
and the interaction-admission queue consumer. A Compose dependency recreation
then disconnected the runner's RabbitMQ channel while leaving the runner
process alive. The runner now owns separate reconnecting consumers for review
and rule-test queues; race-enabled unit tests cover both a closed delivery
channel and an initial broker connection failure. Image
`sha256:a74cf6145dcb0ba83c0d9c589134eb4ec97964ba5086d51373dd0c40675cfc4d`
was deployed as the runner only; both queue consumers are present. No live
broker-disconnect drill was attempted because four review jobs were running.

The lean Compose deployment now has 16 running containers, including the two
Agent Work workers selected for the configured JEV path. Prometheus and idle
optional workers are stopped, not deleted; the PostgreSQL/RabbitMQ volumes are
untouched. All Go entrypoints in `Dockerfile` share one image built by the
`migrate` service instead of declaring duplicate builds per worker. Migrations
`000107` and `000108` were applied to the live local database after an isolated
PostgreSQL integration run. The new `provider-prober` and control API were
recreated without their dependencies, and 13 historical GitHub run snapshots
reached `observed` with nonempty checks. The later Console image
`sha256:9972c2c84aa976767ed06f708d84a547808b3f61723b419d474c61c415592d61`
compiled, passed TypeScript, and served `/api/health` at 200. This is live
backend ingestion and image deployment, not authenticated UI acceptance.

Those historical snapshots exposed a semantic error: their only check was
the App-owned `Open Review / Analysis`, not independent CI. A direct GitHub
API read reported App ID `4975689`, matching the deployment-owned App ID.
The read adapter now classifies check runs using that ID, treats missing App
identity and GitLab external pipelines as unclassified, and the Checks page
counts only explicit independent results. Old JSON with no origin remains
unclassified; historical runs older than the 24-hour refresh window are not
silently rewritten. Focused Go and Web tests passed. The new provider-prober
image `sha256:17707ee637bba81b492bebf56aa8ea90b1520ecb4b85d88a4fff66410487db35`
and Console image `sha256:d9729a91978f7f525ade8d5cc0f370e1afca73080984f1d5bc95e5507a72989a`
were deployed individually; both health endpoints returned 200 and the core
RabbitMQ consumers remained connected. A fresh GitHub review has not yet been
run to persist the new classification end to end.

Public OIDC login still requires the user's Casdoor credentials, so the Checks
page remains unauthenticated-browser-unverified. The previous `error=state`
could not be attributed to a single cause from the old callback. The public
login response did set a Secure, HttpOnly, SameSite=Lax cookie with a ten-minute
age, while the source used one cookie name shared by all concurrent attempts.
The deployed Console now uses a distinct cookie per 32-byte state, accepts
in-flight legacy callbacks, logs only coarse rejection reasons, and allows
twenty minutes for a slow second factor. Unit tests, Web typecheck/lint,
production image build, and a public HTTPS response confirmed the new
state-suffixed cookie, `Max-Age=1200`, and retained deep-link destination.
No credential was entered and no completed OIDC callback was observed; this
is prevention and diagnostics, not proof that the earlier failure is fixed.

The apparent recovery backlog was audited read-only before considering a new
live review. Eleven queued and four running legacy jobs belong to synthetic
test tenants: their linked runs are terminal/superseded or their installations
are inactive or verification-failed. None belongs to the live RainLib tenant.
The runner's claim query requires an admitted/active run and a verified active
installation, so these records are not executable backlog. The 76 messages in
the historical review DLQ were left untouched; no job was requeued, purged,
or sent to GitHub. Full `go test ./...` and `go vet ./...` passed after the
origin-classification and login changes. These source checks do not replace a
fresh review and authenticated Console acceptance.

GitLab provider CI now resolves bounded pipeline jobs in addition to the
pipeline list: at most five exact-head pipelines and twenty current jobs per
pipeline. A pipeline aggregate remains unclassified because GitLab may append
Open Review's external status to an existing CI pipeline or create an
external-only pipeline. A named job other than `Open Review / Analysis` is
independent check evidence; same-named or unnamed jobs remain unclassified.
Unreadable or wrong-pipeline job responses fail the observation instead of
creating a false passing CI result. A saturated page is marked partial.
The Console puts failed/running independent checks first and collapses rows
beyond the first eight. See the [GitLab external status behavior](https://docs.gitlab.com/ci/ci_cd_for_external_repos/external_commit_statuses/)
and [pipeline jobs API](https://docs.gitlab.com/api/jobs/). Focused adapter
tests, full `go test ./...`, `go vet ./...`, Web typecheck/lint and the Next
production build passed. The revised worker and Console were **not** deployed:
Docker's RabbitMQ filesystem had only 3.9 GiB available (94% used), and an
unscoped image/volume prune would risk other projects and broker state. No
real GitLab CI observation or authenticated browser pass is claimed for this
new revision.

A synthetic public-ingress OIDC round trip used the deployed
`https://review.rainlib.com` login endpoint, returned its state-scoped cookie
to the callback, and supplied a deliberately invalid authorization code. The
callback returned `error=exchange`, not `error=state`, so the public proxy
preserved this one attempt's cookie and state across both requests. It did not
authenticate a person, validate a real code exchange, or establish that a
browser will complete Casdoor login; the live browser remains on sign-in.

After verifying two exact BuildKit records were project-specific, reclaimable,
and unshared, only cache IDs `h5whpec20jkagi6rh7cjvkx5o` and
`sgjld570q35elnl382o8vqcv3` were removed (424.8 MB each). No image,
container, database volume, or RabbitMQ volume was pruned. Available space
rose from approximately 3.9 to 4.7 GiB before rebuilding. The core and
runner Dockerfiles now mount one named Go compiler cache instead of baking a
fresh compiler cache into each source-revision build layer. The core build
completed with image
`sha256:98fc15b8f7ef50aee23870ab2276d647988bc95759bd88d697a0955e83bb4b59`;
only `provider-prober` was recreated from it. A new Console build produced
`sha256:508c30e012196f468fa6c0693541903527d6a4f0e11922084c4358e62a23ed33`,
and only Console was recreated. Local and public `/api/health` returned 200,
Console became healthy, the provider-prober retained a live database heartbeat,
and RabbitMQ/PostgreSQL were not restarted. Docker still had approximately
3.8 GiB free afterward, above the 2 GiB RabbitMQ alarm threshold.
The database currently has no verified active GitLab installation (old local
CE/test installations report failed verification), so the newly deployed
GitLab job classification has not been exercised against a real provider.

2026-09-27 live GitHub review and independent-check classification: the
dedicated Draft PR [#6](https://github.com/RainLib/open-review-platform/pull/6)
received one owner-authored `@openreview review --force` command at
[comment 5847795030](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5847795030).
The webhook returned 202, the Bot posted a queued acknowledgement, and run
`129ecf6f-498a-4abd-985d-1dab3c790991` moved through acknowledged,
admitted, preparing, analyzing, normalizing, publishing and completed against
the exact head `32ba779017b7f366916556674a75a70821a31287`. The focused plan
selected one file; the immutable medium publication threshold retained one
lower-severity finding in evidence but published no inline finding. The Bot
posted its evidence report and terminal celebration, and GitHub reported
`Open Review / Analysis` success. The new provider-check observation became
`observed` about four seconds after run completion and classified that sole
check as `origin=open_review`; it did **not** count it as independent CI.
RabbitMQ review acknowledgement, execution and terminal queues each retained
one consumer with no ready backlog. This is live GitHub command-to-report
and GitHub App check-identity evidence, not a passing independent CI test,
authenticated Console Checks-tab inspection, GitLab acceptance, or a full
Issue-to-Draft-PR Agent Work run. The public OIDC browser login remains
unaccepted; no Casdoor credential was entered during this test.

2026-09-27 Jev configuration checkpoint: the already-running
`agent-task-source-admitter` contains nonempty `AGENT_DECISION_JEV_URL`,
`AGENT_DECISION_JEV_API_KEY`, and `AGENT_DECISION_JEV_MODEL` values. The
opt-in `TestLiveJevBackend` sent one synthetic documentation-typo Issue to
the configured endpoint and received a valid bounded signal in 2.27 seconds;
`go test ./internal/agentdecision -count=1` also passed. No container was
created or restarted. This proves this sample's configuration and remote
decision connectivity, not admission of a real provider Issue, model
reliability, coding-Agent execution, or Draft PR publication. A fresh public
browser SSO attempt reached Casdoor's password screen but had no authenticated
session; therefore post-login Console acceptance remains open.

2026-09-27 GitLab changed-file navigation: provider report context now links
same-project MR files to the immutable reviewed head blob, including renamed
paths and self-managed GitLab installations under a relative URL prefix.
Deleted files, fork MRs, malformed paths, missing project IDs, and invalid
head SHAs retain the MR diff-page fallback. Focused provider-response and
rendering tests, `go vet ./internal/publisher`, and the uncached full
`go test -count=1 ./...` passed. This source change has not been deployed or
clicked in a real GitLab MR. GitLab author-name admission on updates from a
different actor is a separate unresolved gap: its webhook carries an author ID,
but resolving the name requires the credential-bearing asynchronous boundary;
the public control API intentionally does not possess the provider token.

2026-09-27 GitLab author-resolution first slice: a provider-worker-only
`providermetadata.GitLabAuthorClient` now performs a bounded, no-redirect MR
read with the exact installation credential and checks the MR IID, author ID,
and immutable reviewed head before returning a username. A local self-managed
GitLab path, changed actor, newer head, missing author, redirect, and invalid
API base all have focused tests; the package race suite, vet, and repository
Go tests passed. This is deliberately **not yet wired** to automatic webhook
admission: the durable pending-event/lease transition and existing
`provider-prober` worker loop must be added before the current fail-closed
unknown-author skip can become a verified asynchronous author decision.
No provider token was added to `control-api`, no container was started, and
no GitLab MR was admitted by this change.

2026-09-27 GitLab author-resolution follow-up: webhook admission now stores a
verified delivery with an unresolved MR author in a durable, leased queue only
when an author-name exclusion rule needs that evidence. The existing
`provider-prober` process performs the exact-commit, credential-bearing MR
read and then admits or skips the review transactionally; the public API
returns `202 deferred` and never receives the provider token. An isolated
PostgreSQL database inside the already-running container applied all 111
migrations and passed the pending-delivery, duplicate, exact-author,
excluded-author, and stale-lease integration path. The database and temporary
test binaries were removed afterward. The uncached Go suite, focused API
deferred-response test, and Go vet passed. This code is not deployed and has
not been accepted against a real GitLab MR; provider lookup, OAuth refresh,
and end-to-end GitLab review remain open.

For local resource use, the already-running Agent Work pair was stopped after
confirming no queued or running Agent attempts, leaving 14 review-mode
containers at approximately 440 MiB. Eight review runs still carry active
states, so review workers were not stopped merely to reduce container count.
The Console/API-only mode remains three long-running containers but suspends
real webhook and review processing; OAuth `error=state` is a separate login
problem and was not verified or fixed by this container change.

2026-09-27 OIDC state follow-up: the public `/api/auth/login` response set
the scoped, Secure, SameSite=Lax attempt cookie and used the configured public
callback. A real in-app-browser navigation through the Casdoor login page,
followed by a deliberately invalid-code callback in that same tab, reached
`error=exchange` rather than `error=state`. This proves Cookie/state continuity
for that synthetic browser attempt but does not reproduce the user's earlier
failure or prove a successful authenticated session. No CSRF/PKCE check was
relaxed and no account credential was entered.

GitLab deferred-author completion now takes the same transaction-scoped
review-identity lock as normal run creation before checking for a newer head.
This serializes the check and run creation against a concurrent webhook for
the same installation/repository/MR. An isolated PostgreSQL integration proved
that a newer admitted head remains current when the older deferred lookup
finishes; the temporary database and compiled test binary were removed.

The deferred GitLab author-admission queue is now visible in each tenant's
Platform Health view with ready/running/failed/expired counts and an operator
runbook. Exhausting the fifth provider-read retry writes one tenant audit event
instead of silently leaving only a failed row. The tenant-scoping and final
failure path passed isolated PostgreSQL integration tests; the temporary test
database and binaries were removed. Uncached `go test -count=1 ./...`,
`go vet ./...`, Web typecheck/lint, and the OIDC cookie unit tests passed.
This is source and database-test evidence,
not a deployed Console inspection or a real GitLab MR acceptance result.

The local development launcher now selects either the three-container UI/API
footprint or the lean review topology without defaulting to the full stack.
It never silently stops existing workers. The currently running stack was
left in review mode because eight review runs and two Issue-analysis jobs are
still nonterminal (the two Issue jobs belong to an installation marked failed).
This launcher does not change the deployed service topology or resolve OIDC.

The existing local Compose PostgreSQL has now applied
`000109_gitlab_author_admission.sql` through the versioned migration runner
using one disposable container and the current migration directory. The
`schema_migrations` row and empty `gitlab_author_admissions` table were read
back, while the same 14 long-running containers remained and API `/healthz`
and Console `/api/health` returned 200. The running API/provider-prober
images still predate the new source, so schema readiness is **not** runtime
deployment or a live GitLab acceptance result.

The shared post-login/provider-install `next` path guard previously accepted
`/\\external-host` and paths such as `/..//external-host`; WHATWG URL
resolution can turn either into an external redirect. The guard now parses
against a fixed origin, rejects any normalized network-path result, controls,
backslashes, and oversized values, and preserves ordinary internal filters.
Dedicated regression tests, Web typecheck/lint, and a Next production build
passed before the scoped rollout below. This vulnerability is not an
explanation for the earlier OIDC `error=state` report.

2026-09-27 scoped runtime rollout: the prior Control API, core worker and
Console image IDs were retained as explicit rollback tags before building.
The shared Go image `sha256:cd57c9edbe25704b0680b302a440999d9d3ffe33a864a74031815b8dda106dcb`
and Console image `sha256:bc16643a594e861a5fe26a75a17ce568fb2d88e03f82d70e3770e57a2700a34b`
were built from current source. Only `control-api`, `provider-prober`, and
`console` were recreated with the existing GitHub App/core overlays; the
14-container count, PostgreSQL, RabbitMQ, and other review workers were left
untouched. API `/healthz` and local/public Console `/api/health` returned
200, all three processes remained running with zero restarts, and the new
`gitlab-author-admitter` heartbeat had an unexpired lease. Docker free space
remained near 3.0 GiB above the 2 GiB RabbitMQ disk alarm threshold. The
eight nonterminal review runs and two Issue jobs were not reset or deleted.
Two unauthenticated public POST probes to the logout redirect returned 303 to
`https://review.rainlib.com/workspaces` for `/\\evil.example` and
`/..//evil.example`; an ordinary `/acme/issues?status=open` target retained
its same-origin path and query. These probes establish deployed path-guard
behavior, not a completed human OIDC sign-in or a real GitLab MR review. The
new GitLab author queue currently has no pending row to execute end-to-end.
A fresh post-rollout synthetic OIDC request returned a 307 with an attempt
cookie, and a deliberately invalid-code callback carrying that same cookie
returned `error=exchange` rather than `error=state`; this verifies the current
public proxy's state continuity for one request, not account authentication.

2026-09-27 local Compose footprint follow-up: `scripts/local-stack.sh auth`
now targets PostgreSQL, the Control API, and the production-shaped Console for
OAuth/callback checks without enabling the UI-only hot-reload/preview overlay.
`ui` remains the three-container local development choice; `review` retains
the independently retryable provider workers. Script syntax, invalid-mode
rejection, base Compose service resolution, and diff whitespace checks passed.
No containers were restarted for this script/documentation change. The current
14 review-mode containers measured about 430 MiB of memory in total and the
database still has six acknowledged and two admitted review runs plus two
queued Issue jobs. Their consumers were left running; reducing the active
count by pausing them would also pause real review/Issue processing. This
does not resolve the human-reported OAuth `error=state` failure.

2026-09-27 GitLab author recovery visibility: the tenant Platform Health queue
view now includes a bounded (12-row), failed-first list of deferred GitLab MR
author checks with repository, MR number, exact head, attempt count, error code,
last update, a validated provider MR link, and the local connection-repair
entrypoint. This is read-only: restoring a credential cannot replay a stale
webhook. An isolated PostgreSQL database applied the current migration set and
passed queued/failed detail and foreign-tenant exclusion assertions; it and
the temporary test binary were removed. The full Go suite, targeted vet,
Web typecheck/lint, and six provider-link tests passed. The Web package has no
single `test` script. Source and isolated-database evidence do not prove a
deployed Console render or a live GitLab MR acceptance: the running database
still has no active verified GitLab installation and the author-admission
queue has no real pending item.

The public login endpoint was checked again: it redirects to the configured
Casdoor authorization endpoint with a callback on `review.rainlib.com` and
sets one Secure, SameSite=Lax, 20-minute attempt cookie. A fresh in-app-browser
click reached Casdoor's password form; no credentials were entered, so the
reported `error=state` remains unproven on the current version. Separately,
the callback now retires only its own attempt cookie on terminal state,
exchange, or token errors, and keeps it on a transient unavailable response.
Three cookie-selection tests, Web typecheck, lint, and diff checks passed.
This source change is not deployed and is login-state hygiene, not a proven
fix for the earlier browser report or authenticated end-to-end acceptance.

The Issue triager's terminal model-failure path no longer re-invokes the model
on broker redelivery: it persists `failed` first, then retries only the
marker-keyed failure comment if that provider write fails. On a successful
model response, the report is now staged for the exact Issue revision and
analysis attempt before provider publication. Redelivery and a deliberate
manual retry after exhausted publication attempts reuse that retained report;
the tenant detail API hides it until the comment succeeds and completion is
committed. Failed-model manual retries still start a new analysis. Stale
revision/attempt writes and terminal-state overwrites are rejected. Worker
unit tests, two isolated PostgreSQL integration tests, full Go tests/vet and
diff checks passed; the temporary database and test binary were removed.
The running Issue worker was not rebuilt or restarted, so live GitHub/GitLab
publication and production failure recovery remain unverified.

The lean review Compose overlay now builds Issue triage in a dedicated
distroless image instead of recompiling all 20 core binaries for one worker
change. The final worker binary cross-compiled as an 11 MiB static Linux
executable, and the merged GitHub App/lean Compose configuration retained its
PostgreSQL migration and RabbitMQ dependencies, read-only root filesystem,
and deployment-owned App key mount. No image was built or container replaced:
Docker's broker filesystem had only 3.0 GiB free above a 2 GiB disk alarm
threshold, leaving too little margin for an unmeasured BuildKit build.
The local mode launcher now passes Compose `--no-build --pull never` unless
`--build` is explicitly requested, so switching modes cannot silently build
or pull the new Issue worker image under that low-space condition.

The recovery entrypoint was tightened from the generic Connections index to
the exact tenant-owned installation detail, keyed by the admission row's
installation UUID. The isolated GitLab author integration re-ran after this
contract change and confirmed the returned installation identity and
foreign-tenant exclusion; targeted Go tests/vet plus Web typecheck/lint passed.
The existing `/:org/connect/:installationId` route is the destination, but a
browser click on a populated live queue remains unverified. A host-side Next
production build passed again after the final link adjustment. No runtime
container was restarted. Docker had only 3.0 GiB free
over a 2 GiB RabbitMQ disk alarm threshold, so a fresh two-image build was
deferred. Six precisely identified untagged, unreferenced prior images were
removed without touching the current images, rollback tags, containers,
volumes, or build cache; shared layers meant available space remained about
3.0 GiB. The images are rebuildable, not retained rollback artifacts.

Issue comment publication is now fenced by the current job row and active
installation in one PostgreSQL transaction. An Issue edit or manual retry
cannot advance its revision/attempt while the previous acknowledgement,
failure, or staged-result comment is being written to the shared provider
marker. A worker that resumes after the newer revision is admitted finds the
old revision stale and does not call GitHub/GitLab. The provider write remains
marker-keyed because an HTTP timeout after acceptance is still ambiguous.
Worker tests, an isolated PostgreSQL concurrency test with an in-flight edit,
the full Go suite, vet and diff checks passed; the temporary database/binary
were removed. This change is source-only: the running Issue worker was not
replaced, and no live provider concurrency acceptance was claimed.

The Console's OIDC discovery now requires the exact configured issuer and
same-origin authorization/token endpoints. Discovery and authorization-code
exchange have bounded timeouts and refuse redirects, so a malformed or
redirecting metadata response cannot forward the code or client secret to
another host. Six focused OIDC cookie/discovery tests, Web typecheck/lint,
a host production build, and a read-only check of the current public Casdoor
metadata passed. The Console image was not rebuilt or deployed; this does not
prove a successful signed-in browser callback or resolve the earlier reported
`error=state` incident. The configured Console URL now also rejects a path,
matching the sign-in page's existing plain-origin requirement.

The live GitHub PR #6 at head `32ba779017b7` matched the local latest
completed run, but its prior successful `Open Review / Analysis` Check Run had
`details_url` pointing to the GitHub repository root. The publisher now sends
the tenant-authorized durable Console review URL on GitHub Check Run creation
and update, both at start and terminal publication; GitLab already sends the
equivalent `target_url`. Four provider-transport cases and focused Go tests/vet
passed. Three scoped worker images (`runner`, `terminal-reporter`, and
`issue-triager`) were then built from the pinned Go 1.25 container image and
recreated without replacing the other 11 running containers or their volumes.
A real `@openreview review` command on PR #6 created run
`f4f59d46-28a8-436f-bfb9-dfd15b4a028d`; its GitHub Check Run
`108468969758` was observed in progress and then completed successfully at
`2026-09-26T19:16:21Z` with `details_url` equal to
`https://review.rainlib.com/rainlib-open-review/reviews/f4f59d46-28a8-436f-bfb9-dfd15b4a028d`.
This proves the exact-link start/terminal path on that live GitHub revision,
not a signed-in Console visit, GitLab target URL, or a concurrent Issue edit.
The public OAuth `error=state` report remains a separate acceptance gap.

2026-09-27 public OIDC follow-up: a fresh unauthenticated browser attempt
rendered the public sign-in page and reached Casdoor's password form through
the configured HTTPS callback. No account credential was entered in that
session. A public HTTP probe observed a dynamically served 307 login response
with a per-attempt Secure/SameSite=Lax cookie; sending that exact cookie and
state back with a deliberately invalid code reached token exchange and
returned `error=exchange`, not `error=state`. The currently deployed Console
did not retire the attempt cookie on that terminal exchange error, matching
the source-only callback hygiene noted above. A new host Next production build
passed from current source, and a scoped ~64 MiB runtime overlay replaced only
the Console image with rollback tag `open-review-platform-console:pre-oidc-20260927`.
The new Console became healthy with zero restarts; local and public health
returned 200, RabbitMQ reported no alarm and all queues were running. The
same public invalid-code probe still returned `error=exchange` but now retired
only its own attempt cookie. The new image is tagged as the local Compose
default `open-review-platform-console:latest`; the prior image remains tagged
for rollback. This proves cookie/state continuity and terminal cookie hygiene
through the current public ingress, **not** a real signed-in OIDC callback or
the cause of the earlier human `error=state` incident.

2026-09-27 Work queue/lean runtime follow-up: `scripts/local-stack.sh auth
--stop-unused` now checks runnable PR/Issue work against installation active
and verification eligibility, plus active Agent Work source/execution states.
The current deployment had eight nonterminal PR runs and two Issue jobs, all
attached to ineligible installations; none were runnable. The operator
explicitly requested fewer local containers, so the launcher paused the eleven
review/Issue/broker workers without deleting queues, volumes or run records.
Only PostgreSQL, Control API and Console remain running; real webhook reviews
will not complete until review mode is resumed. Switching modes without
`--build` now preserves existing targeted rollout images instead of silently
replacing them with older base images.

The server-side Work queue now partitions a nonterminal run by live
installation eligibility: verified/active runs remain Running; inactive or
unverified installations appear in Needs attention with a connection-specific
reason and link while retaining the original immutable run state. Isolated
PostgreSQL 16 integration covered failed verification, inactive installation,
restored eligibility, exact counts and pagination, then dropped its dedicated
database. Full `go test ./...`, focused vet, Web lint, typecheck and production
build passed. Scoped API and Console overlays were deployed while keeping the
three-container footprint; local/public Console health and API `/healthz`
returned 200, and the live database classifies the eight old PR runs as
connection-blocked rather than Running. This is source, database and health
evidence, **not** an authenticated browser assertion of the new queue UI or
proof that restoring a connection automatically retries an already stranded
run. Real signed-in OIDC callback acceptance also remains open.

2026-09-27 Work queue reply-gate recovery follow-up: the queue now separates
two recoverability cases. An admitted, queued review job remains in PostgreSQL
and can be claimed by the runner's database poller once its installation is
active and verified again. A comment-triggered run whose provider-visible
acknowledgement has exhausted six delivery attempts remains in Needs attention
even if the installation recovers: it cannot enter analysis before its reply is
accepted. The queue labels that case “Reply needs attention” and directs the
operator to the run's bounded recovery or cancellation controls. Isolated
PostgreSQL 16 tests proved the admitted-job claim after reconnect and the
5-attempt → 6-attempt → completed reply queue partition; the dedicated test
database was removed. These tests establish store-level recovery, **not** that
historically malformed provider replies can be retried automatically; those
still require operator inspection. The review workers remain paused in the
three-container auth profile, so no live GitHub review or signed-in browser
acceptance is claimed here.

The follow-up passed full `go test ./...`, focused `go vet`, Web lint,
TypeScript check, production Next build, and scoped diff whitespace checks.
API and Console were rebuilt as thin overlays with local rollback image tags;
only those two services were recreated. The running set remained PostgreSQL,
Control API and Console. Local API health, local Console health and public
`https://review.rainlib.com/api/health` returned 200; Console reached healthy
with zero restarts. A single `docker stats --no-stream` sample observed about
72 MiB PostgreSQL, 7 MiB API and 48 MiB Console memory, not a sustained
capacity measurement. Authenticated Work queue rendering and the human OIDC
callback remain unverified in the public environment.

2026-09-27 OIDC callback classification follow-up: the deployed Console now
keeps state validation distinct from a provider-declined authorization and a
matching callback with no authorization code. The latter two return actionable
`cancelled`/`provider` messages instead of the misleading `state` message;
terminal provider and token-exchange errors retire only their own attempt
cookie. Web lint, sequential TypeScript check and production build passed.
Three credential-free HTTPS probes against the deployed public callback
observed `cancelled`, `provider` and `state` respectively, and the first two
retired their matching attempt cookie. The public sign-in page visibly rendered
the new cancellation guidance. This improves failure classification and
recovery guidance; it does **not** prove a real Casdoor authorization-code
exchange, persisted browser session or post-login workspace route. No fourth
long-running container was started.

2026-09-27 compact local review mode: a development-only supervised bundle
now packages the nine essential PR/Issue review executables into one
OCR-capable container, preserving the split-worker production deployment.
Compose renders PostgreSQL, RabbitMQ, API, Console and the bundle as five
long-running services; the RabbitMQ volume initializer and migrator remain
one-shot. The launcher requires rendered `ENVIRONMENT=development`, checks
that the bundle image exists before stopping split consumers, and refuses to
start RabbitMQ with less than 3 GiB free in Docker (above its configured
2 GB broker alarm). Supervisor unit/race tests prove production rejection,
whole-bundle failure on child exit, and cancellation of sibling process
groups. Full `go test ./...`, focused vet, shell syntax and Compose rendering
passed. A pinned Go 1.25.14 build produced all ten Linux binaries; the local
image contains OCR 1.12.5 and Git 2.47.3, and rejected a production-mode
startup. The actual compact launcher refused to switch with 2,320,516 KiB
free, leaving the existing three services and durable broker volume intact.
No real broker/worker/provider review run was performed in this footprint;
additional Docker space and a safe provider fixture are required for its live
acceptance. This mode deliberately does not consolidate Agent coding or
notification/governance workers into the review credential boundary.

2026-09-27 public OIDC preflight follow-up: the credential-free
`scripts/verify-oidc-preflight.mjs` check passed against
`https://review.rainlib.com`. It observed a 307 to the configured Casdoor
origin, an exact HTTPS callback to the Console origin, PKCE S256 and a scoped
Secure/HttpOnly/SameSite=Lax attempt cookie. Returning the matching state and
cookie without a code reached `error=provider`; returning the same state
without the cookie reached `error=state`. An isolated browser also reached
the Casdoor password form from the public sign-in action. This rules out a
basic fresh-attempt origin/cookie mismatch in the current ingress; it does
not establish why an earlier browser landed on `error=state` or prove a real
authenticated callback, session persistence or workspace access. The local
stack remained at three running containers throughout.

2026-09-27 Usage unavailable-state correction: the Console previously
rendered fallback `settled=0`, `reserved=0` and `monthly_review_limit=0` as
real-looking zero usage and “Unlimited” whenever the control-plane request
was unavailable. All three Usage tabs now retain their URL-backed navigation
but show the shared unavailable/unconfigured PageState with a recovery action
until live or explicitly labeled demo evidence exists. This follows the
design's no-fabricated-metrics invariant; Web typecheck, focused ESLint and a
production Next build passed. An isolated browser with a development-only
session and read-only control-plane fixture observed a 503 across Overview,
Ledger and Limits: the URL-selected Tab remained visible, one unavailable
PageState replaced all fallback metrics/empty ledgers, and the earlier stray
“0 events” under Ledger was corrected after the first visual pass. Switching
the fixture to HTTP 200 restored the expected 7 settled / 2 reserved / 91
remaining read model. This is local browser evidence, not a production outage
exercise; deployment of the updated Console image remains to be verified.

2026-09-27 Cockpit/PR/Work queue evidence correction: the PR and work-queue
tabs no longer render their fallback zero counts when their respective
control-plane reads fail. Cockpit no longer reports zero policy sets or
assumes an unconnected first-use workspace when the installation/rule
inventory read fails independently of a successful PR index. It marks that
mixed result as partial while preserving verified PR counts. Web typecheck,
focused ESLint and a production Next build passed. A local production-build
server with a development-only session and a read-only API fixture returned
HTTP 200 for all three pages while the PR and work-queue reads returned 503:
the PR/queue tabs displayed no count badges, and Cockpit showed its
unavailable index state. With the PR index switched to 200 and policy
inventory still 503, the PR tabs displayed their real zero counts while
Cockpit showed “Partial control-plane data” and “setup status unavailable”
instead of a false connection instruction. The isolated browser could not
load that localhost origin, so this run used HTTP-rendered HTML assertions,
not a visual browser acceptance. The updated Console has not been deployed;
live OIDC callback and authenticated production rendering remain unverified.

2026-09-27 reduced-footprint Console promotion: the host production build
above was packaged into a fresh Node 22 Alpine runtime image using only the
66 MB standalone and 2.6 MB static output directories as named build
contexts. A first attempt that passed the whole 3.6 GB `.next` directory was
cancelled before image creation; no running container was replaced. The
corrected image `sha256:70b5e5abbb13f047a6bc26b93b209b72a30348d4c80c7b111afce5a2770e1a9c`
is about 227 MB and passed read-only temporary-container health and sign-in
HTTP checks. The previous Console image remains tagged
`open-review-platform-console:pre-runtime-20260927`; only Console was
recreated. Local and public `/api/health` returned 200, the public
credential-free OIDC preflight passed, and the steady-state set remained
PostgreSQL, Control API and Console. This proves image promotion and the
unauthenticated login boundary, not a real Casdoor code exchange or
authenticated rendering of the newly corrected pages.

2026-09-27 five-container review recovery: four exact, old, reclaimable
Console `pnpm build` cache records were removed by BuildKit ID, releasing
about 1.46 GB without touching images, database/RabbitMQ volumes, or queued
messages. Docker free space reached 3,663,772 KiB before the compact image
refresh. Go 1.25.14 `go test ./...` passed, then the ten current review
roles were cross-compiled to Linux/amd64 and layered over the retained OCR/Git
base. Candidate image
`sha256:0e05fbc85bd46ae3e2d50a7a52ca8481fbecaada57d1da56d6925758db96913b`
rejected production-mode startup; the previous bundle remains tagged
`open-review-platform-compact-review:pre-runtime-20260927`. Read-only DB
checks showed no runnable review/Issue or active Agent task before the switch;
schema migration `000109` was already applied. The local compact launcher
started PostgreSQL, RabbitMQ, Control API, Console and one supervised
review bundle without rebuilding or recreating API/Console. All nine child
processes stayed running with zero bundle restarts; RabbitMQ reported running
queues and one consumer for each PR/Issue review queue. API/Console health
returned 200 and Docker free space remained above 3 GiB. The notification
queue retained two messages with no consumer, and Agent Work likewise stays
paused by this mode. No new provider event or fresh model-backed PR/Issue
review was submitted, so provider-visible end-to-end acceptance remains open.

2026-09-27 compact-review live GitHub PR acceptance: on the existing Draft
test PR [#6](https://github.com/RainLib/open-review-platform/pull/6), a single
`@openreview review --mode=security` comment was submitted for head
`32ba779017b7f366916556674a75a70821a31287`. The bot acknowledged the
request, admitted run `3a1d8222-a87e-477f-aa7a-4f25bd4fa896`, and completed
acknowledge, admission, preparation, analysis, normalization and publication.
Its [evidence report](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5849868863)
was updated at completion: one changed/selected file, zero actionable findings,
and a passed high-threshold merge gate for the exact revision. The report links
the file at that revision, the Open Review run, and command help. The
[terminal reply](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5849875197)
was posted, and GitHub's `Open Review / Analysis` check passed in 58 seconds
with a run URL. Database publication receipts show published summary and
status records with no recorded error; the supervised bundle remained running
with zero restarts. This validates the compact worker's live PR-comment-to-
check/report path for one GitHub installation and one revision. It does not
validate finding/inline-comment or Issue creation (there were no findings),
GitLab, Agent Work, notifications, authenticated Console use, or production
deployment.

2026-09-27 compact-review live GitHub Issue acceptance: a temporary
user-authored [Issue #12](https://github.com/RainLib/open-review-platform/issues/12)
was created while the five-container mode was running. The real GitHub webhook
admitted revision 1 as job `36f8c025-1271-440b-8240-30c59706d8ce`; the bot
first posted an [analysis-started acknowledgement](https://github.com/RainLib/open-review-platform/issues/12#issuecomment-5849904197)
at 21:09:08 UTC. At 21:09:18 UTC the same comment ID was edited, not duplicated,
with a context-quality verdict, missing-context list, proposed acceptance
criteria, risk and next-step disclosures, model/config provenance, reaction
instructions and a tenant-scoped Open Review detail link. The database job
finished `completed`, attempt 1, with no recorded error. The test Issue was
then closed with its purpose and result recorded. This proves one GitHub
Issue-open → webhook → durable triage → in-place provider reply path under
compact mode. It does not prove Issue edit/retry on this image, authenticated
Console click-through, GitLab, Agent Work or notification delivery.

2026-09-27 compact Issue feedback completion: the development-only bundle now
includes the existing provider-feedback-poller as its tenth supervised worker
role, using the same deployment-owned provider credentials already present
for review. Its split-container equivalent is stopped before
a compact switch, avoiding duplicate pollers; production still uses independent
containers. The full Go suite, focused vet, shell syntax, Compose rendering,
binary-presence and production-mode rejection checks passed. The refreshed
bundle `sha256:ef6a2bf25bbb68f5c595efe632d493e933a25544a035942fc1880e17eef43869`
ran all ten child workers with zero restarts and kept the steady-state stack at
five containers. RainLib added a real GitHub 👍 reaction `425942966` to the
[Issue #12 analysis comment](https://github.com/RainLib/open-review-platform/issues/12#issuecomment-5849904197).
The persisted next poll ran on its natural five-minute schedule, moved from
attempt 1/zero reactions to attempt 2/one reaction, and stored that exact
reaction as active `useful` feedback for the completed Issue job. No new model
analysis was scheduled. Retraction and GitLab Emoji delivery remain separately
unverified for this compact image; earlier split-worker evidence does not
substitute for those checks.

2026-09-27 workspace directory authentication recovery: a current public
browser visit started the registered Casdoor PKCE flow and reached its
password form. No credential was entered, so a real public callback remains
unverified; current Console logs only show the deliberate preflight's
missing-code and missing-cookie cases. A separate code audit found that
`/workspaces` treated a control-plane 401 or service failure as an empty
directory, displaying zero-count tabs and a create-workspace action. It now
distinguishes the three states: 401 from either the directory read or a
subsequent per-workspace initialization read shows a session-renewal action
that POSTs to the existing logout route and returns to sign-in; other
unavailable reads show retry without invented workspace counts; only a
successful 200 empty directory shows the first-use create action. A
development-only Next 16 server
and isolated local control-plane fixture were exercised in the browser at
401, 503 and 200/empty, plus a 200 workspace list whose per-workspace
initialization returned 401. The 401 button cleared the local session and
reached the sign-in page; the 503 page showed a non-technical recovery
message; the 200 page retained the intended first-use UI. TypeScript,
focused ESLint and
diff checks passed. The temporary browser tab, fixture and dev server were
closed. The production Console image was not rebuilt or deployed, and this
local fixture does not prove a real Casdoor code exchange or authenticated
production workspace rendering.

2026-09-27 workspace-recovery Console promotion: one host-side Next 16
production build passed compilation, TypeScript, static-page generation and
standalone tracing. Only its 66 MB standalone tree and 2.6 MB static tree
were packaged into candidate image
`sha256:27fd3422261c5fe0288c6b23c41f5ce28aa067818f502002f0137556b528ecbe`;
an isolated temporary container reached healthy and served `/api/health` and
`/sign-in` with HTTP 200. The prior live image was retained as
`open-review-platform-console:pre-workspace-recovery-20260927` before only the
Console container was recreated. The new container reached healthy with zero
restarts; local and public `/api/health` returned 200, and public OIDC
credential-free preflight retained exact-origin PKCE, scoped Secure/HttpOnly/
SameSite=Lax state cookie, and expected missing-cookie rejection. An inert
invalid test ID-token cookie against both local and public `/workspaces`
rendered **Your session needs renewal** and **Sign in again**, not **Create
workspace**. The steady-state set remains five containers. This verifies the
deployed negative-authentication recovery path, not a real Casdoor code
exchange, valid-session workspace access or production provider onboarding.

2026-09-27 authentication-only footprint and OIDC state recheck: with zero
runnable Review/Issue jobs and zero active Agent tasks, the local launcher was
switched to `auth --stop-unused`. It exposed a mode-switch bug: the base Compose
view listed the compact worker as an orphan but could not stop it by service
name. The launcher now stops that worker with the override that declares it,
before stopping RabbitMQ. A live rerun and shell syntax check passed; only
PostgreSQL, Control API and Console remain running, and local/public Console
health and the public sign-in page return HTTP 200. This mode deliberately
pauses PR/Issue processing; it is not a three-container full-review topology.
The current Console log's `missing_attempt_cookie` followed a credential-free
callback probe, not a proven user login failure. A new public-origin protocol
round trip obtained a per-state Secure/HttpOnly/SameSite=Lax cookie and returned
that exact cookie to the callback with a deliberately invalid code. The
callback advanced to `error=exchange` rather than `error=state`, proving the
proxy/cookie boundary for that attempt only. A fresh browser attempt reached
the Casdoor password form; no credentials were entered, so valid-code exchange
and authenticated workspace rendering remain unverified.

2026-09-27 audit filtering follow-up: the audit Events view now accepts a
literal target substring and inclusive UTC calendar-date range in addition to
actor and action, retaining that scope through event detail and cursor links.
The Control API accepts RFC3339 `from` (inclusive) and `until` (exclusive),
rejects malformed/reversed ranges and oversized filters, and applies all
predicates inside the authorized tenant query. The full Go suite, web typecheck,
focused ESLint and diff checks passed; PostgreSQL accepted a prepared form of
the added target/time predicates. The opt-in database integration test was
extended but not executed against a live test database, and the local
three-container Console/API images were not rebuilt with this follow-up. A
later targeted run of the audit integration test against the existing local
PostgreSQL container passed; it used an isolated temporary tenant and removed
its fixture.

2026-09-27 bounded provider inventory follow-up: the provider probe previously
retained only the first ten GitHub/GitLab repositories. It now reads up to ten
fixed-origin pages of ten, deduplicates stable provider IDs and records
`inventory_state=partial` if a later page fails or the cap is reached. The
entire inventory pass is limited to 20 seconds so a slow provider cannot hold
the probe's one-minute lease indefinitely. The worker also budgets the entire
credential/inventory/scope pass against its actual claim lease and reserves up
to five seconds to commit a degraded result before that lease expires; a
processor test checks the resulting deadline. The
verified first page remains available on a later-page failure; no raw provider
response or token is returned to the Console. The tenant-safe installation
receipt carries that state, and onboarding plus Connections warn that an
absent repository may not yet have been checked. GitHub/GitLab multi-page,
failure and cap tests, the full Go suite, TypeScript and focused ESLint passed.
The provider-health store integration test passed inside the existing local
PostgreSQL container with a temporary tenant; the temporary test binary was
removed. The three-container authentication footprint was unchanged. A real
provider installation with more than ten repositories and the rebuilt worker/
Console images remain required for live acceptance; this source change is not
yet in the running containers.

2026-09-27 repository-inventory freshness follow-up: a provider probe retains
old repository rows for audit, but current repository selection, installation
counts and the workspace `review_scope` checkpoint now admit only rows stamped
by the latest probe whenever that probe reports an inventory state. A partial
or failed refresh therefore cannot present a formerly visible repository as
freshly verified; older rows remain durable for investigation. Two isolated
PostgreSQL integration tests covered an out-of-scope row, a retained stale row,
the current inventory count, pagination/search and a stale-only setup gate;
the tests removed their temporary tenants and binaries. Public Casdoor login
again reached the password form, but no valid-code callback was attempted.
This is source/database evidence, not a deployed Console/provider acceptance.

2026-09-27 targeted three-container promotion: the current Control API was
cross-compiled with the pinned Go 1.25.14 toolchain and layered over its
retained validated image, then replaced without starting any broker or review
worker. The prior core image remains tagged
`open-review-platform-core:workqueue-reply-20260927`. The current Console
passed a host-side Next 16 production build, TypeScript and static-page
generation; only its standalone/static outputs were packaged, with the prior
image retained as `open-review-platform-console:pre-inventory-audit-20260927`.
The API and Console replacements both started with zero restarts; local API,
local Console, public Console health and the public sign-in page returned 200.
Docker still runs exactly PostgreSQL, Control API and Console, with more than
3 GiB free. This promotes the source changes and proves deployment health,
not a valid Casdoor callback, authenticated repository selector, a fresh
provider probe, or resumed PR/Issue processing; workers remain intentionally
paused in authentication-only mode.

2026-09-27 full store integration sweep: a new isolated PostgreSQL database
applied all 111 current migrations, then ran the complete `internal/store`
test binary with `OPEN_REVIEW_TEST_DATABASE_URL`. The first run exposed a
test-isolation assumption, not a scheduler defect: the installation-restore
test asked the global fair-claim queue for its own job while another test's
append-only usage ledger retained a runnable foreign-tenant fixture. That
assertion now claims the exact admitted run; separate fairness/recovery tests
continue to exercise global claims. A fresh database and rebuilt test binary
then passed the entire store suite. Both exact-name temporary test databases
and binaries were removed; the business database and three running services
were not reset. This expands local database evidence, but still does not
establish authenticated public-browser or live-provider acceptance.

2026-09-27 installation credential lifecycle: deactivating a GitLab OAuth
installation now revokes its stored credential in the same transaction after
the last active workspace scope using that reference is stopped. An active
scope sharing the reference keeps it usable; revoked credentials cannot pass
the existing resolver or token-refresh guard. An isolated PostgreSQL 16
database with all 111 migrations passed the focused deactivation/revocation
checks and the complete store integration binary; the full Go suite, focused
vet, Console typecheck/lint and diff check passed. The temporary database and
test binaries were removed. The Console explicitly distinguishes this local
credential stop from **provider-side** GitHub App uninstallation or GitLab
OAuth grant revocation. Those remote revocations and a valid Casdoor callback
remain unverified; the public browser reached Casdoor's password screen only.

2026-09-27 abandoned GitLab OAuth setup cleanup: migration `000110` indexes
unrevoked credentials by age; the existing provider-prober process now scans
up to 100 credentials every five minutes and locally revokes ones older than
one hour with no active installation. It rechecks after taking each credential
lock so a concurrent completed setup is preserved. Installation creation now
requires a live, tenant-owned OAuth credential and holds a shared lock until
the installation transaction commits; revoked or nonexistent references fail
closed. An isolated PostgreSQL 16 database applied all 112 migrations and
passed the focused orphan/active/fresh/revoked tests plus the complete store
integration binary. The full Go suite, focused vet and diff check passed;
the temporary database and binary were removed. No additional container was
started. The current three-container authentication footprint does not run
provider-prober, so the scheduled cleanup itself remains undeployed and has
not been observed in a live worker process. Provider-side OAuth revocation is
still a separate lifecycle step.

2026-09-27 authentication-only image promotion: the existing business database
was advanced in place to 112 migrations (`000110_provider_oauth_orphan_cleanup.sql`)
with a one-shot migrator, after the candidate image had applied all 112 migrations
to a temporary database. The refreshed Control API and Next standalone Console
were promoted independently without starting RabbitMQ or review workers. Docker
reports exactly three running project services: PostgreSQL, Control API and
Console; both replaced containers have zero restarts. Local API `/healthz`,
local Console `/api/health`, public Console `/api/health` and the public sign-in
page returned HTTP 200. The previous images remain tagged for rollback. This
proves the lightweight authentication deployment is healthy, not an OIDC
callback, signed-in workspace flow, provider probe, or active PR/Issue review.
The orphaned-credential cleanup code is present in the source, but its
five-minute schedule will not run while provider-prober remains paused.

2026-09-27 three-container OAuth cleanup follow-up: the orphaned GitLab OAuth
credential reaper now runs in the always-on Control API, not provider-prober,
so `auth` mode does not require a fourth worker container to retire abandoned
authorization attempts. Its bounded five-minute loop uses the existing
transactional row lock and active-installation recheck, remaining safe with
multiple API replicas. The full Go suite, focused race test and vet passed.
Before deploying, a read-only query found one expired, five-day-old credential
in the `gitlab-offline-e2e` test tenant with zero installation references.
After only the API image was replaced, its log recorded one local revocation
and the same orphan query returned zero. The API `/healthz` returned 200 and
PostgreSQL, API and Console remain the only three running project containers.
This does not revoke the GitLab-side OAuth grant and does not resume paused
PR/Issue processing or prove a real Casdoor authorization-code callback.

2026-09-27 provider-switch setup draft correction: the detailed onboarding
design requires separate unsaved choices for GitHub and GitLab. Previously the
provider switch reset the form, and a pre-authorization draft was discarded
after OAuth returned. The Console now stores bounded, non-secret drafts per
workspace and provider, migrates the old single-provider draft, retains each
provider's repository scope/review choices when switching, and preserves those
choices after authorization while restarting at Install rather than trusting
the browser's old stage. A signed, HttpOnly provider receipt remains the only
authorization to save an installation; one provider's draft cannot authorize
the other. Five draft tests, Web typecheck, focused lint, the full Next
production build and a temporary Linux image HTTP smoke test passed. The
candidate Console image was built but not promoted over the public Console
while a real Casdoor sign-in attempt is open in the browser; authenticated
provider-switch and authorization-return interactions remain to be accepted.

2026-09-27 scoped Console promotion: after confirming the retained Casdoor
browser tab was still on its password form, the verified provider-draft image
`sha256:4cf449fd3a33b8f8cc53aa87e0bb1d1427b334dc08f3debc9fccc35cfe2cd7e8`
replaced only Console; its predecessor remains tagged
`open-review-platform-console:pre-provider-drafts-20260927`. The public sign-in
and `/api/health` endpoints both returned HTTP 200, Console reported zero
restarts, and Docker still runs only PostgreSQL, Control API and Console.
This promotes the provider-scoped draft implementation, but a user-completed
Casdoor callback and authenticated GitHub↔GitLab draft-switch exercise are
still required for browser acceptance. PR/Issue workers remain intentionally
paused in this three-container mode.

2026-09-27 JEV configuration recheck: the rendered source-admitter environment
has a nonempty `AGENT_DECISION_JEV_API_KEY`; URL and model resolve to their
deployment defaults. An opt-in test sent one synthetic, non-repository Issue to
the real Jev HTTPS endpoint and accepted a bounded response in 1.20 seconds.
It created no task, branch, PR, or provider comment. The current three-container
authentication footprint still has no RabbitMQ or source-admitter, so this is
credential/protocol evidence, not live Issue admission. The onboarding copy now
distinguishes a queued provider verification from a claimed probe and states
that the task cannot progress without a running verification worker. Web
typecheck, focused ESLint and diff checks pass; this copy change is source-only
and has not been promoted to the public Console image.

2026-09-27 exact-revision file-link guard: review finding and changed-file
links now require the same complete 40/64-character commit SHA already required
by commit links. A branch, abbreviated revision, or malformed historical value
renders as non-clickable evidence rather than a mutable provider blob URL.
Focused GitHub/GitLab/self-managed link tests, Console typecheck, focused ESLint,
the complete Next production build, and diff checks passed. This is a source
and build check, not yet a deployed-browser assertion. A fresh organization
SSO attempt was opened after the prior long-lived Casdoor page; its password
step still requires the user and a successful callback remains unverified.

2026-09-27 scoped Console deployment: the verified standalone build was
packaged into `open-review-platform-console:exact-evidence-20260927`
(`sha256:fa4c04756ada1ac721e2e5f447d62d2ff5ad9d1bd84e17ba8d32e233ca9a9f96`).
A temporary container returned HTTP 200 for `/api/health` and `/sign-in`, then
was stopped. The prior image was retained as
`open-review-platform-console:pre-exact-evidence-20260927`; only Console was
recreated. Local/public health and the public sign-in route returned HTTP 200,
Console had zero restarts, and PostgreSQL/API/Console remain the only running
project services. The public endpoints establish deployment reachability, not
an authenticated OIDC callback, live provider verification, or PR/Issue review.

2026-09-27 four-container onboarding footprint: a separate `setup` Compose
overlay builds only the latest `provider-prober` binary and keeps its mounted
GitHub App private key outside API/Console. It requires PostgreSQL but no
RabbitMQ; `local-stack.sh setup --github-app --build --stop-unused` started
PostgreSQL, API, Console, and provider-prober. A second no-build invocation
reused the image without recreating the worker. The new worker image
`sha256:84f3234fb90ac648a2b4aa837bb7e2f180f4f872c16d0b17bb31d5ed7ae5fddd`
is 13 MB, runs as UID 65532 with a read-only root filesystem, reported zero
restarts, and persisted fresh provider-prober, provider-checks, and GitLab
author-admission heartbeats. Compose rendering, shell syntax and diff checks
passed. The database had zero pending provider probes and zero active
review/Agent jobs before switching, so no actual GitHub/GitLab installation
was probed in this checkpoint. This mode can complete first-connection
verification after authorization, but intentionally does not process PR/Issue
reviews, merge gates, notifications, or Agent Work; RabbitMQ and those workers
remain stopped. A valid OIDC callback and an end-to-end provider installation
still require browser/provider acceptance.

2026-09-27 mode-switch guard follow-up: `auth`/`ui --stop-unused` now count
queued or running provider-health probes before pausing the sole verification
worker. When PostgreSQL is stopped but project services are still running,
the launcher refuses to pause them instead of skipping its pending-work
check. Three mocked-launcher regression tests cover the DB-down refusal,
pending-probe refusal, and `setup` retaining the provider worker. Shell syntax
and the real `setup --github-app --stop-unused` invocation passed with exactly
PostgreSQL, API, Console, and provider-prober running. The public OIDC
preflight passed the exact callback origin and scoped Secure/HttpOnly/Lax
state-cookie checks; the retained browser tab is still on Casdoor's password
form. No account credential, real authorization code, or authenticated
workspace flow was exercised in this checkpoint.

2026-09-27 onboarding status handoff: the verification gate now re-reads the
persisted provider result every 10 seconds only while its tab is visible, and
refreshes immediately when the tab becomes visible again. It does not submit
a new probe or admit reviews; the existing manual refresh remains available.
The pre-verification repository form now explicitly calls its input an initial
authorization boundary and points to the synchronized repository selector
that appears after verification. Console typecheck, focused lint, and the
complete Next production build passed. The verified standalone output was
packaged into `open-review-platform-console:setup-auto-refresh-20260927`
(`sha256:bcbf48be3b127d445e206fdf27a5c1b3de1ff5077494e028e57c694f42e99c1c`).
A temporary container returned 200 for health and sign-in before only the
Console was recreated; the previous image remains tagged
`open-review-platform-console:pre-setup-auto-refresh-20260927`. The new
Console reports zero restarts and healthy; local/public health and public
sign-in return 200 while PostgreSQL, API, and provider-prober remain running.
The browser was moved from an older Casdoor password form to a fresh SSO
attempt after deployment, without submitting credentials. A real authenticated
browser completion from Casdoor through installation, verification, and
repository selection remains unverified.

2026-09-27 repository-inventory capacity: the provider-prober now requests up
to 100 repositories per GitHub/GitLab inventory page instead of 10. The
existing 10-page and 20-second ceilings remain, so more than 1000 repositories
still produce an explicit partial inventory rather than a false complete
authorization. Inventory responses have a separate 1 MiB bound; non-inventory
probe responses retain their 64 KiB bound. The full Go suite, focused
provider-health race test, and vet passed. Only the provider-prober image was
rebuilt and recreated (`sha256:758e709eea4941c199009e5bad053172b869f08459e1a1283637292ecbfafb1a`);
the previous image is tagged `open-review-platform-provider-prober:pre-inventory-page-20260927`.
The new worker has zero restarts and a fresh, unexpired database heartbeat;
there were no queued/running provider probes during this switch. A live
installation with more than 100 repositories, and the authenticated
Casdoor-to-GitHub setup flow, remain unverified. The current lightweight stack
does not run RabbitMQ or PR/Issue review workers.

2026-09-27 compact-review activation: the development-only five-container
stack was rebuilt from the current source and started with PostgreSQL,
RabbitMQ, Control API, Console and one compact worker. This supersedes the
four-container setup-mode observation above, but does not turn the local
bundle into a production deployment shape. A misplaced Dockerfile ARG had
prevented a clean rebuild; it is now declared before the first FROM. The
rebuild exposed a second startup race: the final `--no-deps` recreate launched
consumers before RabbitMQ had finished recovering its quorum queues. The
launcher now waits for the broker's healthy state first; a mocked ordering
test, shell syntax check and diff check pass. The first deployment incurred
eight supervised restarts while the broker was starting, then stabilized with
all ten child worker processes present. RabbitMQ is healthy with no resource
alarm; the local and public Console health endpoints return 200. This is
runtime readiness evidence, not a fresh real-provider PR/Issue review: the
database has no runnable old run for an active verified installation, while
the broker retains 76 historical review DLQ messages and three pending
notification messages outside the compact bundle's notifier scope.
The in-app browser subsequently opened the public `/workspaces` route and
was redirected to `/sign-in?next=%2Fworkspaces`, confirming that this browser
has no authenticated Console session. No Casdoor password or second-factor
credential was entered, so a signed-in route sweep remains unverified.

2026-09-27 rebuilt compact-review live PR acceptance: a single owner-authored
[`@openreview review --force --mode=security`](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5851249926)
on the existing Draft test PR #6 at head `32ba779017b7` received a bot queue
acknowledgement within about five seconds. Run
`65fc4fbb-7112-497b-99f7-670be68a48e6` completed all six durable stages;
its existing [evidence comment](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5851251889)
was updated from in-progress to one medium maintainability recommendation,
the matching [inline comment](https://github.com/RainLib/open-review-platform/pull/6#discussion_r4113515567)
was published on `internal/api/hook_runner_e2e.go:13`, and a
[terminal reply](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5851264922)
was posted. GitHub Check Run `108516764767` completed `success` with the exact
Console run URL: the configured blocking threshold was high, so one medium
finding did not block the merge gate. The compact bundle held its existing
restart count at eight (all incurred before RabbitMQ became healthy) and the
broker reported no alarms. This is current-image GitHub PR command-to-inline-
finding/check acceptance only; it does not prove a new provider Issue triage,
authenticated Console use, GitLab production HTTPS, or Agent execution.

2026-09-27 rebuilt compact-review live Issue acceptance: a user-authored,
clearly titled test [Issue #14](https://github.com/RainLib/open-review-platform/issues/14)
was opened without an Agent Work label. The bot created one
[analysis comment](https://github.com/RainLib/open-review-platform/issues/14#issuecomment-5851274864)
at `00:29:22Z` with an analyzing acknowledgement, then updated the same
comment at `00:29:37Z` with structured assessment, missing-context,
acceptance, collapsed risk/next steps, provenance and a Console detail link.
The retained job `165f544c-e1a4-4673-95de-3a24541b2890` completed at
revision 1, attempt 1. The agent-task store had zero tasks for Issue #14 and
GitHub had no open PR referencing it. The human operator then closed the
test fixture with a verification note; no production defect was asserted.
This validates one current-image GitHub `issues.opened` path. It does not
prove edit/retry under the current image, GitLab Issue handling, notification
delivery, or browser-authenticated Issue management.

2026-09-27 private Agent write-credential readiness: the optional credential
broker now emits a short-lived process heartbeat only after its startup
configuration and database checks. The tenant-authorized Platform health read
model exposes an aggregate `unobserved` or `configured_unverified` state, not
the broker identity, installation map, credential, or provider token. Agent
Work displays this as a fifth independent gate beside provider admission,
repository policy, Jev, and coding-adapter reachability. An isolated PostgreSQL
16 integration verified absent, fresh, and expired heartbeats and that the
broker does not appear in tenant worker cards; the temporary database was
removed. The complete Go suite, focused vet, Console TypeScript and ESLint,
and Compose validation passed. The current running images have **not** been
rebuilt for this source change. A broker heartbeat does not prove a tenant
mapping, token issuance, provider write, sandbox isolation, or a Draft PR/MR.

2026-09-27 rejected-Console-session recovery: a control-plane 401 while
opening a workspace or resuming setup now redirects to a fresh sign-in prompt
instead of presenting setup with an invalid token. The sign-in page deliberately
does not auto-redirect merely because that rejected HttpOnly cookie is still
present; a new OIDC attempt can replace it. Safe `next`, tenant and setup-step
context survive the round trip. Five focused path/renewal tests, frontend
TypeScript, targeted ESLint, and the full production web build passed. A temporary Next.js 16 dev process
against the current Control API returned 307 from an invalid-cookie workspace
request to the exact safe review URL, 307 from setup to its retained checkpoint,
and 200 for the renewal page with the same invalid cookie. The page rendered
without a visible Next error overlay, and the temporary process/tab were
closed. The Console was subsequently rebuilt as
`sha256:4d8f5f61dbabf9a18b2e667d37e4077d5abba13c3230014ca6c0a1175941bfa8`
and only that container was recreated. The previous image remains tagged
`open-review-platform-console:pre-session-renewal-20260927`; PostgreSQL,
RabbitMQ and the compact review worker were not restarted. The Console is
healthy with zero restarts, local and public `/api/health` return 200, and
RabbitMQ reports no alarms with about 3.25 GB free. The public
`verify-oidc-preflight.mjs` check passed: Casdoor origin, exact HTTPS callback,
PKCE and state-scoped secure cookie match; a matched callback without a code
returns `error=provider`, while a missing cookie returns `error=state`.
Public invalid-cookie requests now return 307 from `/acme/issues?status=open`
and the setup checkpoint to exact safe `error=session` sign-in URLs; the
sign-in page returns 200 even with that rejected cookie. The public OIDC
preflight script now asserts this renewal behavior on every run. The deployed
renewal copy and SSO action were also visually inspected. A real Casdoor
authorization-code completion and post-login workspace flow remain unverified.

2026-09-27 public entry-form session guard: `/workspaces/new` and
`/invitations/accept` now perform a bounded, read-only one-row control-plane
identity probe before rendering a mutation form. A rejected cookie redirects
to the session-renewal sign-in route while retaining the exact local return
path, including an invitation token; a transient/unconfigured control plane
shows a retryable unavailable state with no create/accept button. The accepted
path still renders the original forms. The invitation's decorative heading
was demoted to `h2`, leaving one semantic `h1`. Frontend TypeScript, targeted
ESLint and full production build passed. A local Next runtime against the
current API returned 307 for both invalid-cookie paths; against an isolated
read-only fixture it rendered the accepted forms, while an unavailable API
rendered the guarded state. The temporary browser tabs, fixture and Next
processes were closed. Console image
`sha256:742c565958b85b0910cc0c59802cb400d66dfc6bc68a61fde311139285a3ee43`
was built and only Console was recreated; the prior image is tagged
`open-review-platform-console:pre-public-entry-guard-20260927`. The Console
is healthy with zero restarts; local/public health return 200. Public
invalid-cookie requests to both entry pages return 307 to the expected
renewal URL. RabbitMQ has no alarms, but Docker has only about 2.8 GB free,
so further image builds should wait for scoped space recovery. This does not
prove a real Casdoor login, accepted invitation, or workspace creation.

2026-09-27 Finding Explorer read-model correction: the cross-run Console page
no longer filters only the feedback dashboard's fixed 30-item recent sample.
It requests an independently authorized, server-filtered keyset page for
Active, High risk, or Actioned findings, with repository/text search and
forward/backward navigation. The opaque cursor is scoped to view, search,
viewer, and workspace; rule-quality Insights keeps its separate recent
feedback sample. A disposable PostgreSQL 16 schema clone with migration
`000111` passed a 36-finding regression: the oldest high-risk finding was
visible although 35 newer rows preceded it, the second page and reverse
navigation worked, an actor disposition moved only that viewer's finding to
Actioned, exact repository/text filters worked, a changed-view cursor was
rejected, and a nonmember was denied. `go test ./...`, Console production
build/typecheck and targeted ESLint passed. The local browser preview rendered
the filter form, tab switch and filtered empty state without an error overlay;
it was explicitly in demo mode. **The new Control API and Console
have not been deployed to the running five-container stack**, so no signed-in
production Console or real-provider Finding Explorer acceptance is claimed.

2026-09-27 Finding evidence navigation follow-up: both the cross-run Explorer
and recent-feedback list now receive the legacy Job's immutable head SHA and,
only when a uniquely linked Review Run has that same SHA, its run ID. The
file path opens the exact provider commit and line range; the internal evidence
link opens that finding's anchored card in the run's Findings tab; the PR/MR
button continues to open the provider's current review. An unlinked or
revision-mismatched legacy finding does not advertise an internal run link.
The shared provider link builder also rejects literal `.`/`..` path segments,
which previously let URL normalization redirect a model-supplied file path to
a different file. A disposable PostgreSQL 16 schema clone passed run/SHA,
historic high-risk, dashboard and mismatch assertions; the clone and binary
were removed. Go full-suite, Console production build/typecheck, targeted
ESLint, and focused frontend link tests passed. This is
source/database evidence only: the Console/API images have not been replaced
during the active Casdoor authorization attempt.

2026-09-27 live Finding Explorer index migration: the five-container business
database held 17 findings and two feedback rows before the additive
`000111_finding_explorer_indexes.sql` migration. The exact migration file and
version registration ran in one transaction under the migrator's advisory
lock; PostgreSQL now reports version `000111` and all three indexes. No
business row or service was reset. The public Console and private API health
endpoints still return 200. **The running Control API and Console images still
predate the Finding Explorer read model and evidence links**, so deployed
behavior remains unverified until those images are safely replaced and an
authenticated browser checks the real page.

The current source was packaged without replacing live containers as
`open-review-platform-core:finding-links-candidate-20260927`
(`sha256:228f679c3f567b10d05a1769a25f7b25124e9646c3cefc5261bc3cd646d20ab5`)
and `open-review-platform-console:finding-links-candidate-20260927`
(`sha256:520abe2e0acb8627a80ef21d537d14c253ab1efc498d30db0254f3864954a0b1`).
The latter returned 200 for `/api/health` and `/sign-in` in a temporary
localhost-only container, which was then removed. RabbitMQ reported no alarm
and Docker retained about 2.6 GiB free. This is image-startup evidence, not
an authenticated Finding Explorer or Casdoor callback acceptance; the five
long-running containers were not recreated during the browser authorization
attempt.

2026-09-27 deployed Finding Explorer acceptance: the existing Control API and
Console images were first retained as local rollback tags
`open-review-platform-core:pre-finding-links-20260927` and
`open-review-platform-console:pre-finding-links-20260927`. Only those two
stateless services were recreated, using the prepared candidate image IDs
`sha256:228f679c3f567b10d05a1769a25f7b25124e9646c3cefc5261bc3cd646d20ab5`
and `sha256:520abe2e0acb8627a80ef21d537d14c253ab1efc498d30db0254f3864954a0b1`.
PostgreSQL, RabbitMQ and compact-review were not restarted; the broker had no
alarm. Both replacements had zero restarts, Console became healthy, and private
API, local Console and public Console health returned 200. The public OIDC
preflight still passed but intentionally did not perform a fresh authorization-
code exchange.

An existing authenticated Chrome session opened the public
`/rainlib-open-review/findings` route against live control-plane data. It showed
six active published findings; High risk selected only unresolved high/critical
rows, and a `Dockerfile` search narrowed the page to the two matching high
findings. The exact-file link targeted
`RainLib/open-review-platform` at immutable head
`29a9a8ed4a16b3ef022d29a7c889be1fdeb53917`, `apps/web/Dockerfile#L20`,
and opened that GitHub commit-line page. The Review evidence link opened run
`e9d88332-4fc6-4ada-acb2-7725ad0a6a7b` with the Findings tab selected and
the exact `finding-61d477b1-fa59-4415-a5a4-175d569435b6` card present.
Control API logs showed 200 for the authenticated findings and evidence reads.
This is a real logged-in read-only Console/API/provider-link acceptance, not a
fresh Casdoor callback, feedback mutation, pagination across multiple live
pages, or a new PR/Issue/Agent execution.

2026-09-27 scoped image-pinning follow-up: base Compose now accepts
`OPEN_REVIEW_CONTROL_API_IMAGE` for `control-api` alone. The local ignored
`.env` pins the accepted API candidate, while `migrate` and the other shared
core workers still resolve `open-review-platform-core:local`. A rendered
Compose check proved the scoped and unset/default cases; it did not recreate
any container. This protects the running API from an ordinary Compose `up`
reverting to the older shared image. A future full shared-core release must
remove the local API pin and roll out its compatible binaries together. The
`review --build` launcher now refuses to build while the rendered Control API
and migrator images differ; a shell syntax check and the current pin's
expected fail-closed preflight passed without building or stopping workers.

2026-09-27 live Finding narrative rendering: the shared, inert formatter now
renders bounded headings, lists, emphasis, inline code and fenced code in
Review detail, while Explorer strips broken Markdown delimiters from its
240-character previews. Arbitrary model-supplied links and HTML remain text;
verified provider file/run links are still generated separately from immutable
review evidence. A first live browser pass found that unfenced patch comments
beginning with `#` were mistakenly treated as headings. The suggestion variant
now preserves multiline code and indentation instead; five formatter tests,
TypeScript, targeted ESLint, diff checks and the full production Next build
passed. Console image `sha256:76b210923a6485738d7bea460ee75d73d14ef4e4662b442203b445bf28e5192b`
was deployed by recreating only Console; the previous image remains tagged
`open-review-platform-console:finding-narrative-candidate-20260927` and the
earlier accepted image as `pre-finding-narrative-20260927`. The authenticated
public Chrome session showed PR #3's two real findings with inline code and
literal code-block suggestions, not erroneous `h3` headings, and Explorer
showed six live findings with clean summaries and exact evidence links.
Console health and broker alarm/disk checks passed. This does not verify a
fresh Casdoor callback, all possible model formatting, or a new provider run.

2026-09-27 compact-worker GitHub Issue revision acceptance: the existing,
clearly marked [E2E Issue #14](https://github.com/RainLib/open-review-platform/issues/14)
was reopened and then edited after its reopened analysis completed. Its one
durable provider-Issue job advanced from revision 1 to completed revision 2
(`reopened`) and completed revision 3 (`edited`), each at attempt 1. GitHub
retained the same [bot analysis comment](https://github.com/RainLib/open-review-platform/issues/14#issuecomment-5851274864)
ID `5851274864`; the final comment reports revision 3 and was updated at
`02:34:00Z`. Before closing, the Issue still had only its original two
comments, so neither revision posted a duplicate bot comment. The tenant
store had zero Agent Work tasks for Issue #14, consistent with its lack of an
automation label. The test Issue was then closed with a verification note;
the stored job remained completed at revision 3, and the compact worker stayed
running with no new restarts or RabbitMQ alarm. This verifies real GitHub
reopen/edit → new durable revision → same-comment update in the current compact
deployment; it does not establish concurrent-edit supersession, GitLab Issue
edits, a fresh Casdoor session, or Issue-to-Draft Agent execution.

2026-09-27 compact-worker Issue reaction retraction and restoration: the
existing RainLib `+1` reaction `425942966` on the bot's
[Issue #12 analysis comment](https://github.com/RainLib/open-review-platform/issues/12#issuecomment-5849904197)
was removed through GitHub. The same live five-container worker's natural
poll at `02:40:08Z` advanced attempt 34 → 35, observed zero reactions,
set `reaction_count=0`, marked that feedback row retracted and wrote one
`provider_issue.feedback_deleted` audit event. The reaction was restored on
GitHub as new ID `426044724`; the next natural poll at `02:45:08Z` advanced
attempt 35 → 36, observed one reaction, retained the old row as retracted,
created a new active `useful` row and one `feedback_created` audit event.
GitHub again shows one RainLib `+1`. The Issue analysis job remained completed
at revision 1/attempt 1; the compact worker had no new restart and RabbitMQ
reported no alarm. This proves current-image GitHub reaction add/remove
reconciliation without another AI analysis. GitLab Emoji delivery and a
production-topology poller remain separate acceptance gaps.

2026-09-27 GitLab first-use recovery copy: the setup wizard and Connections
page now share one operator-facing, expandable explanation when neither GitLab
OAuth nor the deployment-token capability is available. It identifies the
Console OAuth client/state variables, exact callback, separate public/internal
self-managed bases, and the deployment-owned token alternative without adding
browser fields for secrets or provider identity. Frontend TypeScript, targeted
ESLint, `git diff --check`, and the full Next production build passed. The
running Console image was not replaced during the outstanding fresh Casdoor
login attempt, so public browser rendering of this copy and a newly enabled
GitLab handoff remain unverified.

The same first-use paths now distinguish a live, tenant-authorized provider
profile from a temporarily unavailable profile response. Connections shows
the configured GitLab instance label/base when known, and neither screen calls
an unreadable deployment-token capability "not configured." The fallback
instead names the unavailable control-plane check and asks for a refresh after
recovery. Frontend TypeScript, targeted ESLint, diff checks and full Next
production build passed. Public Console remains on the earlier image; its
existing Casdoor authorization tab was still open, and Docker's overlay had
about 2.5 GB free, so this change was not rolled out or falsely counted as a
live browser acceptance.

The revised Console source was subsequently packaged from the verified host
Next standalone output as
`open-review-platform-console:gitlab-state-candidate-20260927`
(`sha256:ff06902614eeb3c60ffc6f0a3e5d6eefed9c72dab540bec7bc350f200e2555d1`).
A temporary container returned 200 for `/api/health` and `/sign-in` and was
removed; the five long-running services remained healthy and unchanged. An
authenticated public read confirmed the **old** Console still shows the
GitLab button without the new guidance. Candidate startup is not live
rendering or authorization acceptance; replacing the public Console remains a
separate step after the current browser authorization attempt is resolved.

2026-09-27 live GitLab connection recovery acceptance: the pending Casdoor
browser remained on an untouched password form, while a separate Chrome tab
already had a valid Console session. The accepted Console image was retained
as `open-review-platform-console:pre-gitlab-state-20260927`; only the
stateless Console service was recreated with the verified candidate. The
authenticated public Connections page then showed the live GitLab.com
deployment profile and an expandable operator setup guide. A visual pass
found one missing space after an inline environment variable, which was fixed
in source, typechecked/linted, built and repackaged as
`open-review-platform-console:gitlab-state-v2-20260927`
(`sha256:424ac7fd301f4315a5da3de916e1c660b256d45267dd3d22cd66703ad336f22b`).
The first candidate was retained as `pre-gitlab-copy-20260927`, then only
Console was recreated again. A second authenticated public browser pass
showed the corrected text; Console became healthy with zero restarts, public
health returned 200 and RabbitMQ had no alarms. PostgreSQL, RabbitMQ,
Control API and compact-review were not restarted. This verifies the live
profile/guidance branch, not the unavailable-profile fallback, a fresh
Casdoor callback, or actual GitLab authorization.

2026-09-27 Provider Issue connection-blocked queue follow-up: the worker
already refuses queued/acknowledged Issue jobs attached to inactive or
unverified installations, but the management list previously counted them as
ordinary pending work and omitted them from `needs_attention`. The read model
now derives `connection_blocked` without changing the durable job state,
includes those jobs in the attention filter, and computes a distinct total so
delivery exhaustion and connection failure cannot double-count one job. The
Console shows the blocked count, status, exact connection link, and recovery
boundary; an exhausted delivery still requires a separate authorized retry.
An isolated PostgreSQL 16 test covered failed verification, inactive
installation, restored eligibility, list/detail/filter counts and the
delivery-failure overlap, then its empty dedicated database was removed.
`go test ./...`, Web typecheck, targeted ESLint, and the full Next production
build passed. The scoped Control API and Console images were deployed with
rollback tags while the other three running services were untouched. Local
API health, local/public Console health, container restart counts and broker
alarms passed. A read-only live database query found two pending Issue jobs,
both connection-blocked. The existing browser session had expired and reached
`error=session`; therefore the new UI has **not** passed an authenticated
public visual acceptance or a fresh Casdoor callback. Restoring those legacy
GitLab connections and manually retrying their exhausted deliveries remain
separate provider acceptance work.

A follow-up audit found another pending-job recovery case: when an
installation becomes ineligible before the Issue worker loads the exact
revision, the worker can complete its inbox message without advancing the
retained job. Reconnection alone cannot replay that consumed message. The
read model now derives `skipped_delivery` from the exact current
revision/attempt/topic/inbox receipt, keeps it in `needs_attention` even after
the connection returns, and offers a role-, setup-, installation- and
revision-gated retry that creates a fresh attempt/outbox message. A second
isolated PostgreSQL test proved blocked → consumed → restored → authorized
retry → old receipt excluded from the new attempt. Both focused integration
tests, `go test ./...`, Web typecheck, targeted ESLint, diff checks and the
full Next build passed. The Control API and Console were each replaced once
more with scoped images (`open-review-platform-core:issue-queue-recovery-20260927`
and `open-review-platform-console:issue-queue-recovery-20260927`); prior images
remain tagged for rollback. This still has no fresh authenticated browser
acceptance because the earlier session expired, and no live GitLab connection
was reactivated or provider Issue was retried.

2026-09-27 compact notification follow-up: the five-container development
stack had four ready `openreview.notification.v1` messages and no consumer.
The compact supervisor now includes an opt-in notifier role, with the six
explicit notification credential slots withheld from its other child
environments. This remains a shared-container/UID convenience, not production
credential isolation; the split notifier remains the production topology.
Before enabling it locally, the retained database was read-only checked:
zero destinations, zero routes and zero deliveries, so the four pending
events had no external recipient. The scoped bundle image
`open-review-platform-compact-review:notification-candidate-20260927`
(`sha256:2fb5c3c1fb108be67a41e5f0ace8bcd1890f547bb20621af529ae2a624641a52`)
was built from twelve tested Linux/amd64 binaries, rejected production mode,
and replaced only the compact-review container. The queue drained to zero
with one consumer; destinations/routes/deliveries remained zero, the bundle
had zero restarts, local API and public Console health returned 200, full Go
tests and compact race tests passed. Real DingTalk/Feishu/Slack sends and
receiver receipts are still outstanding; this proves queue consumption, not
third-party delivery.

2026-09-27 compact rule-governance follow-up: the local database is migrated
through `000111`, but the five-container bundle had no exception-expiry or
Canary-failure monitor process. The compact development supervisor now runs
both database-only roles by default; the production topology still keeps
them as independent services. Before rollout, four approved exceptions had
passed their expiry timestamp, but none had a currently suppressed Issue
linked to it; there were zero active Canary rollouts. The scoped image
`open-review-platform-compact-review:governance-candidate-20260927`
(`sha256:886690517e38909ec28284b3e18f5d54dad9fc75852e22d59245dc7ec86483bf`)
replaced only compact-review. Both workers started and retained unexpired
heartbeats; no expired Issue suppression remained, no expiry audit was newly
written, the bundle had zero restarts, and API/public Console health stayed
200. Full Go tests, compact/expiry race tests, targeted vet, shell syntax,
Compose validation and diff checks passed. Because no Canary is active, this
is worker liveness plus tested reconciliation logic, not a live rollback
acceptance against a real provider review.

2026-09-27 compact scheduled-admission follow-up: Console and Control API
already allow an authorized user to schedule a one-shot review of an exact
revision, but the five-container development bundle had no scheduler process.
The retained database had no queued schedule (two admitted, four blocked,
two cancelled and two coalesced historical records), so activating the worker
could not unexpectedly admit a waiting provider review. The database-only
`review-scheduler` is now supervised by the compact bundle; production retains
its independent worker. The scoped image
`open-review-platform-compact-review:scheduler-candidate-20260927`
(`sha256:ed9b29b42157314f4fa775b8a49dce8ea276d1c3b7dab2c35d37812c2b940646`)
replaced only compact-review. Its scheduler heartbeat is unexpired, the
review/notification queues retain one consumer each, the bundle has zero
restarts, API/public Console health returned 200, and the five-container
footprint is unchanged. Full Go, compact/scheduler race tests, targeted vet,
shell syntax, Compose validation and diff checks passed. No new schedule was
created for this acceptance run, so actual timed admission against a real
GitHub/GitLab revision remains unverified.

The scoped build briefly brought Docker Desktop below RabbitMQ's 2 GB free
disk safeguard and raised a broker alarm. No queue or volume was purged.
After verifying that no container referenced them, two old compact image
tags were removed; a project-label-filtered prune then removed only
untagged, unused Open Review build images and reclaimed 184.7 MB. Docker now
reports 2,371,805,184 free bytes against RabbitMQ's 2,000,000,000-byte
limit, no broker alarms, and the five services remain running. This is a
short-term recovery margin, not adequate long-term capacity for repeated
image builds or production queue growth; the Docker VM still needs more
headroom before sustained-load acceptance.

2026-09-27 compact model-probe follow-up: permission revocation was first
rechecked and found already present in the live `provider-prober` process;
its current `provider-checks-worker-1` and `provider-prober-1` heartbeats
were unexpired. The separate model-prober, however, was absent from the
five-container runtime even though Console exposes model-route connectivity
checks. There were zero retained model probe receipts, so starting it would
not send a historical test request. `model-prober` is now supervised in
development compact mode with the same deployment-owned primary model secret
slot; production retains the separate worker boundary. The scoped image
`open-review-platform-compact-review:model-prober-candidate-20260927`
(`sha256:f6890db03303d169a45d16829c62a131a35ff651031371d1d3831c013f3ffb79`)
replaced only compact-review. Its heartbeat is unexpired and all five
services remain running. Full Go tests, compact/modelprobe race tests,
targeted vet, shell syntax, Compose and diff checks passed. No provider
probe request or receipt was created in this run; real model connectivity
and cost acceptance remain open.

That image build again exhausted the small Docker VM free-space margin and
triggered RabbitMQ's 2 GB disk alarm. A scoped project-only dangling-image
cleanup was insufficient, so **rebuildable Docker BuildKit cache only** was
pruned down to a 6 GB retention target. No business volume, queue or running
image was removed. The VM then had 9,121,764 KiB free; RabbitMQ reported
8,738,537,472 bytes free against its 2,000,000,000-byte limit and no
alarms. This restores local headroom but does not replace production capacity
planning or a sustained-load drill.

2026-09-27 compact SSO-probe follow-up: the Console can queue an owner/admin
SSO metadata connectivity test, but the five-container development bundle
previously omitted its database worker. The retained database had zero SSO
probe receipts, and the probe client's default network policy rejects private
targets, so adding the worker did not dispatch historical network requests or
relax the SSRF boundary. `sso-prober` is now supervised in development compact
mode with an explicit `SSO_PROBE_ALLOW_PRIVATE_NETWORKS=false` default;
production retains the separate SSO worker. The scoped runtime-local image
`open-review-platform-compact-review:sso-prober-candidate-20260927`
(`sha256:3aee9d6074f913e9668a591a4497a53d94448c8ba04ff860ecc2e86f0e36d124`)
replaced only compact-review. The new worker logged startup and retained a
fresh heartbeat; the bundle had zero restarts, public Console health returned
200 and RabbitMQ reported no alarms. Full Go tests, focused compact/SSO race
tests, targeted vet, shell syntax, Compose and diff checks passed. No SSO probe
was submitted, so a real identity-provider metadata result and the Console's
authenticated test action remain unaccepted.

2026-09-27 compact data-governance follow-up: Audit export and approved
data-governance jobs were admitted by the Control API, but the five-container
development runtime had no executor. The retained database had zero queued,
running, or historical governance jobs, and the local deployment already had
an artifact encryption key. An explicit
`OPEN_REVIEW_COMPACT_DATA_GOVERNANCE=true` now adds the existing executor only
in development compact mode; it fails startup selection without an artifact
key. Its governance environment variables are filtered from the other child
processes, while notifier credentials are filtered from the governance child.
This is best-effort process environment hygiene, **not** production credential
isolation within one container. Production retains the separate worker.
The scoped runtime-local image
`open-review-platform-compact-review:governance-candidate-20260927`
(`sha256:962d2d104846d72ce69db65e1e42e54a689328b87342df57e49c839f9caeb214`)
replaced only compact-review. Both SSO and governance workers logged startup
and retained fresh heartbeats, the bundle had zero restarts, public Console
health returned 200, and RabbitMQ had no alarms. Full Go tests, focused
compact/governance race tests, targeted vet, shell syntax, Compose and diff
checks passed. No real export/download/deletion was submitted, so encrypted
artifact receipt and authenticated browser acceptance remain open.

2026-09-27 provider-command and Agent-task Console links: the Control API
Compose environment previously omitted the deployment-owned
`OPEN_REVIEW_APP_URL` even though Agent-task acknowledgement/status comments
resolve their link inside the API process. It now receives the public Console
origin, as does the optional separate Agent runner; provider workers retain
the existing shared setting. The store now rejects malformed Console bases
and builds tenant-qualified routes for Agent tasks and PR `help`, `status`, and
`explain` responses. `explain` links to the exact run's Findings tab and
finding anchor, not just the current PR run. A dedicated PostgreSQL database
with the current migrations passed the real interaction/outbox integration
test for all three command links; the temporary database and test binaries
were removed. `go test ./...`, targeted vet, Compose resolution and diff
checks passed. A scoped Control API image
`open-review-platform-core:console-links-20260927`
(`sha256:f512abe9a861cea3cfa1bea48657dea9dd2233fd5e6dfd9392f9687623aabf72`)
replaced only the API; the previous `issue-queue-recovery-20260927` image is
still available for rollback. The API has zero restarts, local `/healthz` and
public Console `/api/health` return 200, and RabbitMQ reports no alarm.
No new provider comment or authenticated browser link click was made for this
change, so live click-through and the optional Agent runner's future runtime
remain unverified. The current compact Issue triager still uses its previous
binary; automatic Agent admission remains disabled.

2026-09-27 live provider-command link and finding-focus follow-up: the open
Draft test [PR #6](https://github.com/RainLib/open-review-platform/pull/6)
received one owner-authored
[`@openreview status`](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5852603388)
for completed run `65fc4fbb-7112-497b-99f7-670be68a48e6`. The bot added
its `eyes` reaction and a
[terminal status reply](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5852603857)
with the exact Console run link. An
[`@openreview explain`](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5852607916)
for retained finding `77e9dacb-3908-45e6-bd26-750d87b35e9c` likewise
received an `eyes` reaction and a
[finding reply](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5852608458)
with the Findings-tab anchor. Neither command admitted another review run.
Unauthenticated HTTP verified that the status target redirects to sign-in
with its exact run path. The finding target exposed a weaker boundary: its
fragment was not carried explicitly in the sign-in `next` parameter. The
reply now also includes a `finding=<id>` query parameter; the Review page
validates that ID against this run's retained findings and scrolls/focuses
the matching card after rendering. A fresh
[`explain` reply](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5852650295)
from the new Control API includes both the query fallback and anchor, and
the unauthenticated public redirect retains the exact run, Findings tab and
finding ID in `next`. A dedicated migrated PostgreSQL test verified all
three `help`/`status`/`explain` queued response links; its database was
removed. Web typecheck, targeted lint, full Next production build, Go store
tests and Compose validation passed. The scoped Console image
`open-review-platform-console:finding-focus-20260927`
(`sha256:4d7ea698f9ee085adadbe95fb470d91ccd5db088a65cfe3bbd2eeea83bcb2b34`)
and Control API image `open-review-platform-core:finding-focus-20260927`
(`sha256:3b13d359d568a54c6968db3444d3589d362d03664010b60e5b6d3b9a8423bbf0`)
were deployed independently; both have zero restarts, local API and public
Console health return 200, and RabbitMQ has no alarm. A complete authenticated
Casdoor return and actual browser scroll/focus remain unverified: the existing
Chrome tab is still at the provider authorization page with no credentials
entered in this pass.

2026-09-27 compact Agent source follow-up: the five-container development
bundle previously left `openreview.agent-task.source.v1` without a consumer
despite a configured Jev credential. An explicit
`OPEN_REVIEW_COMPACT_AGENT_SOURCE=true` now adds only the existing
provider-read-only `agent-task-source-admitter` role. Role selection requires
the Jev key; the compact supervisor filters `AGENT_DECISION_JEV_*` from its
other child environments. This is process-level hygiene in a shared container,
not production credential isolation. The normal release Dockerfile now builds
the role, and the source worker remains a separately deployed service outside
development compact mode. Full `go test ./...`, focused vet, Compose resolution,
and a candidate image's production-mode rejection passed. Before enabling it,
the source queue had zero messages, both repository policies were disabled,
all three retained Agent tasks were cancelled, and the relevant Agent outbox
rows were already published. The scoped runtime-local image
`open-review-platform-compact-review:agent-source-candidate-20260927`
(`sha256:21a52cae105459da086ac8fe7f72ba64078a38fcb274a6689c42455e5526da7e`)
replaced only compact-review. The source queue now has one consumer and zero
ready messages; its heartbeat is fresh and records Jev configuration. The
bundle has zero restarts, API/Console health return 200, and RabbitMQ reports
no alarms. No live Issue was admitted, no Jev request was triggered in this
rollout, and no coding adapter or Agent runner was enabled. End-to-end
Issue-to-Draft-PR acceptance remains open.

2026-09-27 public sign-in recheck: the current Chrome Casdoor authorization
tab still displayed the password form, and a fresh Chrome request for
`/workspaces` redirected to `/sign-in?next=%2Fworkspaces`; there was no
reusable authenticated Console session in that browser. A separate anonymous
in-app browser followed the same guard. The current public
`verify-oidc-preflight.mjs` passed the exact HTTPS callback, PKCE challenge,
state-scoped Secure/HttpOnly/SameSite cookie, missing-code and missing-cookie
branches, and rejected-session renewal route. No password or authorization
code was submitted, so a real Casdoor return, workspace selection and
post-login Finding scroll/focus still require authenticated acceptance. The
five-container deployment and provider data were not changed by this check.

2026-09-27 Review Files design follow-up: the Review detail now has durable
`?tab=files&view=files|dependencies|coverage|blast-radius` navigation. The
Files table supports URL-backed path/scope/change filters and links to the
exact retained head where the provider supplies a valid SHA; deleted files
link to the explicitly labeled current provider diff. The Dependencies view
lists only retained manifest/lockfile path hints, Coverage distinguishes
selected/deferred admission share from unavailable test coverage, and Blast
radius displays only retained static signals and path-priority scores. None
claims a resolved dependency graph, runtime reachability, or measured test
coverage. Three new pure-data tests, the 88-test Web library suite, TypeScript,
scoped ESLint and an optimized Next build passed. A separate localhost demo
preview visibly exercised all four URL views and path search with a
preview-data banner; it is not live
provider evidence. The scoped Console image
`open-review-platform-console:review-files-evidence-20260927`
(`sha256:27edf2723323068ff42b8de7181cab0cde25d7dce3c979bc82787b86f0c18a97`)
replaced only the Console and reached healthy with zero restarts; local and
public health returned 200, while an
unauthenticated public detail request still redirected to sign-in. A real
Casdoor-authenticated Review Files visual run, resolved dependency/coverage
ingestion, and the rest of the approved tab design remain open acceptance or
implementation work. The previous Console image
`open-review-platform-console:finding-focus-20260927` remains the rollback
candidate.

2026-09-27 Review Activity follow-up: the Review detail Activity tab now
renders the retained run-event timeline newest first with URL-backed event and
actor filters, collapsed allowlisted metadata, and an explicit snapshot/stream
connection badge. For live evidence only, it subscribes through the existing
tenant-authenticated SSE BFF, validates run identity, replays the previous
revision on reconnect, and deduplicates by immutable event ID because multiple
events may share one run revision. A newer revision or interrupted stream asks
the user to refresh the durable snapshot; an open SSE connection is not
presented as proof of a gap-free event history. Four new pure-data tests, the
92-test Web library suite, scoped ESLint, TypeScript, and an optimized Next
build passed. An isolated localhost demo browser verified timeline rendering,
URL filter submission, metadata expansion, and light/dark appearance with the
preview-data banner. The scoped Console image
`open-review-platform-console:activity-timeline-20260927`
(`sha256:4e13f31d02a0146a17b8a87b2e1b46dd07b46db587a4c26478f7fea0ab03ac0a`)
replaced only Console; it reached healthy with zero restarts, local health
returned 200, an unauthenticated Activity URL retained its exact destination
through the sign-in redirect, and RabbitMQ had no alarms.
The previous `review-files-evidence-20260927` image remains available for
rollback. A Casdoor-authenticated live SSE and reconnect run, and the rest of
the approved tab design, remain unaccepted.

2026-09-27 Review Checks follow-up: the Review detail now has URL-backed
`?tab=checks&view=all|open-review|provider-ci` subviews. It separates the
immutable Open Review gate, durable execution stages and publication receipts
from independent provider CI observed for the exact head SHA. Missing,
stale, partial and unclassified provider results cannot be counted as a
passing external check; provider branch protection remains authoritative.
Four focused provider-check tests, the 94-test Web library suite, TypeScript,
scoped ESLint and an optimized Next build passed. A separate localhost demo
browser exercised all three subviews and showed an explicit "Not observed"
Provider CI state, with a preview-data banner; this is not live provider
evidence. The scoped Console image
`open-review-platform-console:checks-evidence-20260927`
(`sha256:ec9e8d3a8c317ab40ab0a65f3d44292ec236a4bdeb656b24f38ea4376dd4d558`)
replaced only Console. Local health returned 200, unauthenticated Checks
navigation preserved the exact destination in sign-in `next`, and RabbitMQ
reported no alarms. A Casdoor-authenticated live Checks session and an actual
fresh GitHub/GitLab CI observation remain unaccepted; the previous
`activity-timeline-20260927` image remains available for rollback.

2026-09-27 Review Overview follow-up: the detail page now has durable
`?tab=overview&view=overview|scope|risk|verification|evidence` subviews
matching the approved information hierarchy. Scope uses only the retained
selection plan; deleted paths link to the provider diff, while other selected
paths link to the exact full head SHA. Risk states highest retained finding
severity without inferring repository-wide risk or a runtime dependency graph.
Verification separates workflow stages and publication receipts from exact-head
provider CI; missing, stale and partial observations are not called passing.
The top-level Checks count now excludes publication receipts and matches its
subview, with a regression test for mismatched provider revisions. The demo
fixture now uses a full commit SHA so file links exercise the production URL
rule; it remains explicitly preview-only data. Five focused overview/check
tests were added, the 99-test Web library suite, TypeScript, scoped ESLint,
optimized Next build and diff check passed. An isolated localhost browser
exercised the five subview URLs, real rendered content, exact-SHA file links,
consistent Checks counts and light/dark layout. The scoped Console image
`open-review-platform-console:review-overview-20260927`
(`sha256:f1f9093b4d3a058c5c335bf02f767f1b12a102198f30876cf5ddf842d8007d29`)
replaced only Console. Local health returned 200, the unauthenticated
Overview URL retained its exact subview through sign-in `next`, and RabbitMQ
reported no alarms. A Casdoor-authenticated public-browser run and fresh
GitHub/GitLab provider CI observation remain separate, unaccepted checks;
the previous `checks-evidence-20260927` image remains available for rollback.

2026-09-27 Review Findings follow-up: the detail page now renders a
URL-backed severity/status/category/file/text filter workspace with a
two-pane finding list and selected evidence inspector. `blocking` is derived
only from the retained enabled immutable merge-gate threshold; a mismatch
with the gate's stored count is called out instead of silently reconciled.
Exact-head file links require a full provider commit identity. A selected
finding remains visible through a deep link even when outside the current
filters, and its collapsed prompt can be copied. The inspector explicitly
states that source excerpts and applyable patches are not retained in this
review record. Three focused tests and the 102-test Web library suite passed,
as did TypeScript, scoped ESLint, an optimized Next build and diff checking.
An isolated localhost browser confirmed rendering, URL filter navigation,
zero-result behavior, out-of-filter deep links, exact-SHA source links and
copy feedback using preview-only data. The scoped Console image
`open-review-platform-console:review-findings-20260927`
(`sha256:7e7e98a30ac292b76e73eeea93fc9c1a49be55c0551a1224d20de1ce9d55716a`)
replaced only Console; local and public health returned 200, unauthenticated
deep-link navigation retained its destination through sign-in, and RabbitMQ
reported no alarms. A Casdoor-authenticated provider-data visual run and
retained code/patch evidence remain separate acceptance or implementation
gates. The previous `review-overview-20260927` image is retained for rollback.

2026-09-27 Finding publication evidence follow-up: each retained finding is
matched to an `inline_finding` publication receipt only by its exact provider
marker. A timestamped matching receipt is confirmed; a retained error is
failed; a receipt without a timestamp is pending; a missing receipt or legacy
missing marker is explicitly unconfirmed/untracked. The Findings page shows
the confirmed-inline count and links each item's state to Checks receipts;
neither a completed run nor a published summary is presented as proof that
the inline comment reached the provider. Four focused tests, the 103-test Web
library suite, TypeScript, scoped ESLint and the optimized Next build passed.
An isolated localhost preview visibly showed one unconfirmed finding and
navigated to its Open Review receipts. The scoped Console image
`open-review-platform-console:review-publication-20260927`
(`sha256:4153d0fa04c05e2e8506b009542ee01b2fa5014c356eb15290369086efcbb33e`)
replaced only Console; `review-findings-20260927` remains available for
rollback. This is a retained-receipt UI contract, not a new live provider
publication or Casdoor-authenticated acceptance run.

2026-09-27 PR command timeline reduction: `@openreview review` and `retry`
now acknowledge the source comment with 👀 only. The provider reaction remains
the durable admission/release barrier; a failed reaction cannot start the run.
Comment-triggered runs no longer publish start/progress comments, and terminal
interaction receipts no longer add a second completion comment. The canonical
evidence report and inline findings remain authoritative; failed/cancelled
runs still use the terminal reporter. Parser, GitHub/GitLab publisher, retry
recovery, and an isolated migrated PostgreSQL 16 admission/recovery test passed,
as did `go test ./...` and focused vet. The local compact worker and Control API
were refreshed from tagged candidate images. A real [PR #6 command](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5853627289)
received exactly one 👀 reaction and zero Bot comments while analyzing; after
the run completed, exactly one Bot [evidence report](https://github.com/RainLib/open-review-platform/pull/6#issuecomment-5853639171)
appeared, with zero actionable findings and a passing high-threshold gate.
This validates this PR command path, not every provider/terminal branch.

Agent coding-loop status at the same local snapshot: two repository policies
are `disabled` with auto admission off, the three retained Issue tasks are
cancelled, there are zero attempts, and no agent-task runner or coding adapter
container is running. The code has bounded Issue `implement` admission,
Jev classification, source freeze, plan/approval, signed execution handoff,
Draft PR/MR publication, and `revise` feedback only for a Draft produced by
an earlier Agent task. There is no live Issue→code→Draft PR/MR→review→revision
acceptance, and arbitrary human-created PRs are not eligible for the `revise`
loop. Do not present this as an operationally closed Agent workflow until a
policy, isolated adapter/credential broker, provider sandbox, and complete
provider-backed acceptance run are enabled and observed.

2026-09-27 Agent Work readiness follow-up: the Console now checks the selected
task's exact provider/API host/repository policy through the unbounded exact
lookup rather than treating another repository's Manual policy in the 100-row
overview as applicable. It also reads effective General and Filters review
settings for that same repository and distinguishes connection-level automatic
reviews/author scope, Draft admission, push re-review and required labels.
The UI says “Core policy configured” only for those known settings and still
does not claim a delivered webhook or completed feedback cycle. Twenty focused
Agent data tests, TypeScript, scoped lint, optimized Next build and local
Console health passed; the local Console image is
`open-review-platform-console:agent-exact-policy-20260927`. The current `.env`
contains a Jev decision key but no coding adapter/model route or signed adapter
handoff settings; the database still has zero Agent attempts and no Manual
repository policy. Real Issue→Agent→Draft→review→revise acceptance remains open.

2026-09-27 Agent feedback UX follow-up: the Agent Work inspector now treats an
Issue implementation task and a Draft PR/MR `revise` child task as distinct
origins. A failed source check, rejected classification, or blocked plan on the
feedback child points to the current Agent-created Draft and a new
`@openreview revise <feedback>` comment, not the unrelated Issue
`@openreview implement` command. Its missing-review link opens the exact
repository/provider/host General review settings. Twenty-one focused Agent
data tests, TypeScript, scoped lint and optimized Next build passed. The
local Console image `open-review-platform-console:agent-feedback-copy-20260927`
was deployed. Browser navigation was checked only through unauthenticated
local and public sign-in boundaries; no signed-in Agent feedback visual test
has been completed or real provider Issue→Draft→review→revise cycle has passed.

2026-09-27 Agent Draft filter checkpoint: the handoff readiness check now
compares the selected task's captured base branch with literal repository
target-branch filters and flags unknown/wildcard branch, author exclusions or
required labels for explicit inspection in the repository Filters page. It
cannot certify provider webhook delivery, a model-backed execution attempt,
or a completed review/feedback cycle. Twenty-one focused tests, TypeScript,
scoped lint, optimized Next build and local image build passed. Signed-in
visual acceptance and real Issue→Agent→Draft→review→revise remain open.

2026-09-27 Agent feedback target-branch correction: the Agent task detail API
now exposes a `target_branch` derived from the frozen Issue source or the
tenant/provider/repository-matched parent lineage for a Draft feedback task.
The Console uses this branch for review-admission filter checks instead of
misreading the feedback task's `source_base_ref` (its Draft head branch) as
the review target. The isolated PostgreSQL Issue→feedback task test passed
against a fresh migrated temporary database, which was then removed. Full Go
tests, Agent data tests, TypeScript, scoped lint and optimized Next build
passed. Local API/Console images were deployed, both returned HTTP 200 on
their health endpoints, and the Console container reached healthy with zero
restarts; this is not evidence of a live
coding executor, provider Draft publication or completed feedback cycle.

2026-09-27 sparse Filters admission parity: Agent Work now interprets an
omitted `target_branches` field using the backend's default `["main"]`, while
an explicitly saved empty list still excludes every target branch. This
prevents an otherwise eligible Agent Draft from being mislabeled as excluded
when a workspace saved only other Filters fields. Focused Agent tests,
TypeScript, scoped lint and optimized Next build passed. This changes the
Console's readiness interpretation only; provider webhook admission remains
owned by the backend and has not been exercised by a real Agent Draft. The
updated local Console image reached healthy with zero restarts and HTTP 200;
the public Agent Work URL still redirected to organization sign-in, so
 authenticated visual acceptance was not obtained.

2026-09-27 terminal failure publication recovery: a failed review's durable
terminal reporter now reconciles the same stable summary marker as the runner,
instead of closing only the provider Check. It preserves the specific timeout
or model-context-exhaustion explanation from the retained typed job failure;
unrelated error text receives the generic safe failure message. Focused tests,
`go test ./...`, `go vet ./...` and diff checking passed. A freshly migrated,
temporary PostgreSQL 16 database plus an HTTP GitHub fixture exercised both
cancelled and timed-out failed runs: the fixture accepted a Check and comment
before dropping each connection, and durable retries recovered the same Check
and the same comment without a second POST. The test database was dropped and
the local runtime remains at five persistent services. This is provider
transport recovery evidence, not a new real GitHub failure run or Agent coding
end-to-end acceptance. Only the compact review worker was refreshed locally to
`open-review-platform-compact-review:terminal-failure-recovery-20260927`
(`sha256:ae5a179a3f1202d5b2b7c3ce7f9cc62c3a2bded36a20a67c954097ec2acdf523`);
its terminal-reporter child started, the container had zero restarts, and the
five-service development stack was unchanged. The previous image remains
available for rollback.

2026-09-27 onboarding route and baseline split: the setup wizard now has
refreshable routes for provider selection, provider-specific installation,
repositories, review scope, learning, publication severity and verification.
The existing GitHub App callback still takes precedence over the GitHub setup
page. A URL only selects a previously reached browser stage; the signed,
short-lived provider receipt remains mandatory for installation. After the
read-only provider probe, synchronized repository selection and Draft/push
review behavior are separate pages. An unsaved repository-scope edit disables
continuation, and durable setup checkpoints still govern readiness. Eight
focused draft/route tests, TypeScript, scoped ESLint and the optimized Next
build passed; a local HTTP smoke check confirmed valid setup paths redirect
unauthenticated users to sign-in while unknown paths return 404. The local
Console image `open-review-platform-console:setup-route-20260927` was built
and deployed with only the Console container recreated. Its final image is
`sha256:165ea98d232cf887b90862248f957cbccd23874ad7926c5cf4a807c0d7c75cff`;
the route draft retains the furthest visited page so browser back/forward
cannot display a different panel than the URL. `/api/health`
returned 200 and the container had zero restarts at the first check. This
does not establish signed-in visual acceptance or a complete GitHub/GitLab
authorization-to-first-review walkthrough. The pre-verification repository
boundary is still an explicit scope entry; searchable provider inventory is
available only after the worker verifies the connection.

2026-09-27 Agent execution-worker activation: the current-source
`agent-task-runner` image was built as
`open-review-platform-agent-task-runner:core-loop-20260927`
(`sha256:082e4892229244cfa96db03fd4817087e2c10cafa691c166a4f4cc6ae5e04afe`).
The prior `latest` image remains tagged `pre-core-loop-20260927` for rollback.
Before activation, both repository Agent policies were disabled, execution and
cancel queues had no ready or unacknowledged deliveries, and there were zero
Agent attempts. The runner is now a separate live Compose service with zero
restarts; its database heartbeat is unexpired and explicitly reports
`adapter_configured=false`. RabbitMQ shows one consumer on each Agent execute
and cancel queue, with zero ready or unacknowledged messages. Source-admitter
remains live in the compact bundle. No task was executed or provider branch/
Draft PR was created. The missing coding model, signed adapter endpoint and
write-credential boundary still prevent Issue-to-code acceptance.
The `rainlib-open-review` workspace also has `review_drafts=false`; even after
an adapter publishes a Draft PR, ordinary automatic review will be skipped
until that explicit review policy is enabled or an authorized comment/CLI
request starts the review. Agent coding policy and Draft review policy remain
separate opt-ins.

The compact supervisor regression test was also cross-compiled and run inside
its Linux/amd64 image. Worker-exit, descendant process-group cleanup and
cancellation tests passed; the descendant check treats a Linux zombie as
stopped rather than mistaking an unreaped PID for a running process. Focused
host tests for the supervisor, runner, interaction responder and publisher
passed after that adjustment. This is local process and publication evidence,
not a production high-availability or Agent coding result.

2026-09-27 Agent coding-loop recheck: the current compact review container
already runs `agent-task-source-admitter` with `OPEN_REVIEW_COMPACT_AGENT_SOURCE=true`
and a configured Jev key, so a seventh persistent source-worker container is
not needed. The separate Agent task runner is live, but its adapter connection
is not configured. The local database has zero Manual Agent repository policies,
three cancelled tasks and zero completed tasks; the local `.env` has no coding
model route, signed adapter URL/secret or coding-credential broker settings.
The authenticated Agent Work page remains unverified: local access correctly
redirects to a sign-in page that disallows an unregistered localhost callback,
and the public Console currently has no signed-in browser session. GitHub
Developer Settings currently lists the review App but no separate Coding App.
No GitHub permissions were changed and no provider write was attempted.

Against a fresh migrated, disposable PostgreSQL 16 database, all 19
`internal/store` Agent-named tests and the complete `internal/agentcredentials`
package passed inside the Linux test image. This covers frozen feedback
budgets, plan/approval gates, exact-scope GitHub issuance, GitLab project-bot
identity/scope rejection and the signed broker's database grant. The test
database and containers were removed after verification. These tests do not
replace a real Issue → coding Agent → Draft PR/MR → review → feedback cycle.

2026-09-27 isolated Issue-to-Draft publication fixture: the current
`internal/agentadapter` Linux test binary ran inside the existing
`adapter-codex` image against an immutable local Codex sandbox image on a
disposable internal-only Docker network and empty workspace volume. Both
`TestPipelineExecutePublishesGitHubDraftOnlyForCurrentIssue/isolated_codex_draft`
and `.../isolated_codex_stale_issue` passed. The first fixture exercised a
two-turn Responses-compatible model broker, sandboxed code edit, exact
repository credential refresh, guarded branch push, and a simulated GitHub
Draft PR response. The second changed the Issue between execution and
publication and verified that no Draft was created. No paid model or real
GitHub write credential was used. The temporary network and volume were
removed after checking for leftover child containers and workspace files;
the compiled test binary was moved to Trash. This closes a local adapter
publication-test gap, not the live Issue → Draft → review → revise acceptance
or the missing coding App/model/broker deployment.

2026-09-27 Draft feedback adapter acceptance: the GitHub fixture now keeps
the first simulated Draft open, serves its exact current head and a bound
`@openreview revise` comment, then submits a second approved feedback plan.
The adapter re-reads that comment and Draft head, starts from the frozen
Agent branch, pushes a new commit under the exact old-head lease and reads
the same Draft number 7 at its new head; the fixture asserts that the Draft
create count remains one and that a second publication checkpoint was
retained. Both the local fake-CLI run and the opt-in real Docker/Codex
sandbox run passed, alongside the stale-Issue rejection case. The full
`internal/agentadapter` test package and diff check passed. Only synthetic
model and provider endpoints were used; the disposable internal network and
workspace volume were removed after checking for leftover children/files.
This verifies the adapter's positive feedback publication path locally, not
GitHub webhook admission, control-plane owner approval, real provider
permissions or a live review finding-to-feedback cycle.

The same fixture also edits the admitted feedback comment after the coding
step but before branch publication. Host and real Docker/Codex variants both
stopped at the second origin check: no new checkpoint, push or Draft creation
occurred, and the existing Draft branch stayed at its original head. The
negative Docker run used another disposable internal network and empty volume,
both removed after confirming no child or workspace remained.

2026-09-27 Agent feedback terminal copy: the provider-bound terminal response
now distinguishes the original Issue-origin Draft creation from a feedback
child that updated that same Draft. The feedback completion comment names the
new revision, links the existing Draft, says re-review has not occurred, and
points out that automatic Draft review remains a separate repository setting.
It retains patch and verification evidence without implying a second PR/MR,
approval or merge. A focused store test, `go vet ./internal/store`, and
`go test ./... -count=1` passed. A host-cross-compiled Control API and
migrator were packaged over the previously running core image as
`open-review-platform-core:agent-feedback-copy-20260927`
(`sha256:dc1d618643f5b74808c6a9743526edd3a6b6447c6039873676b2349d91b355a9`),
then only the local Control API container was replaced. Its `/healthz`
returned 200, startup logs contained no errors, and the six-service stack
remained intact. The prior `agent-review-gate-20260927` image remains for
rollback. Historical provider comments were not rewritten; no real Agent
feedback task exists to exercise the new reply on GitHub or GitLab.

2026-09-27 workspace access recovery: the directory's denied/empty state now
accepts an authenticated access request without confirming whether a slug
exists. `000113_workspace_access_requests.sql` stores pending requests and
serializes duplicate/rate-limited admissions; an Owner can approve exactly one
revision to grant Viewer access or reject without changing membership. The
route and store tests cover hidden workspaces, role boundaries, stale decisions,
and concurrent requests. The integration case passed on an isolated PostgreSQL
database copied from the current schema; that disposable database was dropped.
`go test ./...`, `go vet ./...`, Web lint, and the production Web build passed.
The migration was then applied by the project migrator to the running local
database, and only the Control API and Console containers were replaced with
`workspace-access-20260927` images. Local API/Console health and the public
Console health returned 200; unauthenticated local and public access-request
POSTs returned 401. The six-service stack remains intact, with no extra
persistent container. The old `agent-feedback-copy-20260927` Control API and
`agent-review-gate-20260927` Console images remain tagged for rollback. A
signed-in request → Owner decision → refreshed directory browser acceptance
was not established by the public OIDC check; an anonymous sign-in page is
not acceptance evidence.

2026-09-27 isolated browser acceptance for workspace access: a schema-only
copy of the current PostgreSQL database held one synthetic ready workspace,
one Owner, and one legacy-verified installation. Two loopback Console
processes used explicit development subjects against a separate development
Control API, all connected only to that disposable database. In Chrome, the
requester signed in through the local-preview route, saw zero accessible
workspaces, submitted `access-e2e-20260927` with a note, and received the
existence-neutral acknowledgement. The database recorded exactly one pending
request. The Owner's Members page showed that request and approved Viewer;
the database then held an active Viewer membership, an approved revision-2
request, and `workspace_access.requested`/`workspace_access.decided` audit
events. After refresh, the requester directory showed one Ready workspace
and its card opened the live Cockpit. The standalone server initially omitted
CSS/client static assets, so the interactive pass used `next start` against
the existing build instead. Both temporary Console processes, the test API,
and the disposable database were removed after acceptance. This verifies the
browser → BFF → API → database → refreshed UI loop with synthetic identities;
public Casdoor/OIDC and a real organization Owner remain separate acceptance
gates.

2026-09-27 Claude executor acceptance: the adapter now has a separate
job-scoped Anthropic Messages broker and pinned Claude Code 2.1.144 Docker
profile. Broker tests prove fixed `aliyun/glm-5.3`, capped output and request
budgets, upstream-key separation, and rejection of hosted tools. An installed
Claude CLI reached the broker through its actual `?beta=true` Messages path;
the configured live gateway returned `BROKER_OK` through that broker. A real
child in immutable `agent-sandbox-claude` image
`sha256:b7bf05f46e23f4da638ef929f8f0aeaca6a83597914142823f684cb34b001f21`
ran on an internal-only Docker network with a host-backed disposable checkout
volume, wrote the approved test file, and was removed with ownership reclaimed.
The live model then completed a GitHub Issue → bounded patch → agent branch →
Draft PR → feedback update chain against a disposable bare Git repository and
synthetic GitHub API, with no external provider write. An isolated migrated
PostgreSQL test accepted a per-repository Claude policy while preserving the
already-admitted task's frozen Codex profile; Console now offers Claude in the
executor selector. The running `agent-task-runner` still has no adapter URL,
callback secret, repository-scoped write credential, or Claude model broker
configuration, so real GitHub webhook → approval → provider Draft PR and
human review are **not** live-accepted. The Docker VM had under 1 GiB free,
so the sandbox test used a dedicated host-backed volume rather than disabling
the disk guard or pruning unrelated images and caches.
