# Design mockup provenance

These mockups were generated with the built-in OpenAI image generation tool in `ui-mockup` mode for design-direction review. They are not implementation screenshots and contain placeholder content.

## Shared art direction

```text
Premium AI-native enterprise developer product; warm porcelain canvas, deep ink
text, compact floating midnight-violet navigation rail, restrained cobalt/violet
and mint accents, generous 22–28px radii, soft layered surfaces and hairline
borders. Forward-looking, elegant and operationally clear. Avoid a generic admin
dashboard, repetitive same-size cards, excessive glassmorphism, neon cyberpunk,
stock imagery, decorative illustration, third-party logos and watermark.
```

## dashboard-overview.png

```text
Create the main workspace for Open Review. Use an asymmetric editorial layout:
a calm top command bar; greeting and concise system summary; a dominant Review
pulse showing Received, Understanding, Reviewing and Publishing; a human-readable
Needs your attention feed with direct actions; differently sized In motion task
tiles for running, queued and completed reviews; and one slim health/usage capsule.
Prioritize next meaningful actions over KPI cards. Do not use a full traditional
admin sidebar, four-KPI grid or giant data table.
```

## review-run-detail.png

```text
Create a live AI review execution workspace. Header shows LIVE state, task title,
trigger, commit, elapsed time, cancel and provider actions. The dominant Reasoning
canvas shows a connected six-stage journey with Reviewing active at 68%, while a
Signals, not chain-of-thought stream exposes only verifiable operational events.
Use a Context/Rules/Usage inspector with trigger command, supersession behavior
and estimated usage. Include a secondary Ask about this review bar. Avoid a
traditional vertical timeline, KPI grid, dense table or private chain-of-thought.
```

## enterprise-rules.png

```text
Create an enterprise policy studio, not a CRUD table. Show an editorial rule-set
gallery; a selected Secure API Baseline with published/mandatory state; a visual
composition of Organization baseline, Team overlay and Repository additions; an
immutable-version governance callout; a subtle version constellation; and a
floating Before you publish impact dock with sample size, new critical findings,
false-positive risk, confidence, preview and create-draft actions. Keep each area
visually distinct while sharing the product design language.
```

The source-generation copies remain outside the repository in the local Codex generated-image directory. Only the selected final PNGs are committed here.

## Detailed tab-state boards

The following light-theme boards refine the approved Luminous Spatial direction into task-specific secondary pages and tab states. Their exact interaction and data contracts are documented in `../19-console-v2-detailed-tab-mockups.md`.

- `console-v2-pr-tabs-detailed.png`: Pull Requests plus Overview, Findings, Files, Checks, and Activity.
- `console-v2-operate-tabs-detailed.png`: Connections, installation/repository detail, notification destinations, routing, and deliveries.
- `console-v2-enterprise-tabs-detailed.png`: Members, SSO, Models & BYOK, API/CLI keys, Data governance, and Platform health.
- `console-v2-onboarding-tabs-detailed.png`: provider connection through rules sync and readiness.
- `console-v2-policy-governance-detailed.png`: policy library, discovery, approvals, bindings, exceptions, and insights.

These are design-review artifacts, not proof that the corresponding backend capability is implemented. Secret-like strings are placeholders; production UI must render secret references only.

## Remaining page and recovery boards

The following six `ui-mockup` prompts complete the page inventory. Each prompt requested a 3-by-2 board of complete desktop screens using the same Luminous Spatial language, realistic enterprise data, stable tabs, one primary action, and no payment UI.

- `console-v2-public-workspaces-detailed.png`: landing, sign-in, workspace selection, workspace creation, resumable setup, and no-access recovery.
- `console-v2-execution-center-detailed.png`: Cockpit, Work queue, Run Overview/Evidence, CLI Reviews, and Finding Explorer.
- `console-v2-rule-authoring-detailed.png`: Rule Overview/Content/Semantic diff/Validation and Test Lab static/replay modes.
- `console-v2-model-governance-tabs-detailed.png`: model Routes/Credentials/Budgets/History, connectivity receipt, and create-route sheet; credentials are references only.
- `console-v2-audit-usage-tabs-detailed.png`: Audit events/detail/exports and Usage overview/ledger/limits/reconciliation.
- `console-v2-state-recovery-detailed.png`: loading, filtered empty, first-use empty, provider partial, permission denied, and superseded revision states.

The exact route, URL-state, data, permission, security, and recovery contracts are documented in `../20-console-v2-remaining-pages-and-state-mockups.md`. These boards remain `DRAFT_FOR_OWNER_REVIEW` until explicitly approved.

## Security, health, and CLI secondary-tab boards

The final uncovered secondary-page families use the same `ui-mockup` direction but are separated because their state and security contracts are materially different:

- `console-v2-sso-data-governance-tabs.png`: SSO Overview/IdP/Domains & mapping plus Data residency/Retention/Export & deletion.
- `console-v2-platform-health-tabs.png`: Overview, Queues, Workers, Providers, Incidents, and Runbooks with observed/configured distinction.
- `console-v2-cli-reviews-api-keys-tabs.png`: CLI runs/Quickstart/Overview/Evidence plus Active and Revoked key states.

Their route, state-machine, permission, secret, evidence, and responsive contracts are documented in `../21-console-v2-security-health-cli-tab-mockups.md`. The generated source copies remain in the local Codex image directory; the selected project assets are the PNGs listed above.

## Luminous Apple tab boards

These five `ui-mockup` boards redraw the remaining secondary pages in the approved Luminous Apple visual language. Each board uses full page states rather than decorative navigation variants:

- `console-v2-review-config-tabs-v3-apple.png`: General, Categories, Review filters, Custom prompts, PR summary, and Custom messages.
- `console-v2-policy-governance-v3-apple.png`: Rule library/detail, Approvals, Bindings, Exceptions, and Insights.
- `console-v2-review-tabs-v3-apple.png`: Pull requests, Review Overview, Findings, Files, Checks, and Activity.
- `console-v2-operate-tabs-v3-apple.png`: Connections, Installation, Repositories, Destinations, Event routing, and Delivery logs.
- `console-v2-enterprise-controls-v3-apple.png`: Members, SSO, Data governance, Platform health, API/CLI keys, and Audit/export.

Their route, state, mutation, security, and implementation acceptance contracts are documented in `../22-console-v2-luminous-apple-tab-mockups.md`. Dark implementations use the same information architecture with the independent token system demonstrated in `console-v2-screen-system-v3-dark.png`; they must not be generated by color inversion.
