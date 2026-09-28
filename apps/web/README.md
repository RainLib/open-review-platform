# Open Review Console

The management console is a Next.js App Router application. It currently reads the control plane's durable review-run and rule-set APIs. It does not place provider credentials or GitHub App keys in browser state.

## Local development

```bash
cp .env.example .env.local
pnpm install
pnpm dev
```

For a local control-plane connection, configure `CONTROL_API_URL`, `CONTROL_API_DEVELOPMENT_SUBJECT`, and the explicit `OPEN_REVIEW_LOCAL_PREVIEW=true` flag in `.env.local`, with `ENVIRONMENT=development`. This corresponds to the Go server's development authenticator; it is not a production login mechanism. Use the visible **Open local development preview** action on `/sign-in`; a console route no longer bypasses this explicit local-only session. The flag stays off by default, including in the production Next runtime.

To inspect the interface without a control plane, set `OPEN_REVIEW_CONSOLE_DEMO=true`. The console visibly labels fixture content as preview data. It defaults to a completed fixture so Console screens are reachable. To verify that direct workspace URLs return to onboarding, set `OPEN_REVIEW_CONSOLE_DEMO_SETUP_STATE=needs_connection`, `verifying_connection`, `connection_failed`, `needs_setup`, or `unavailable`; use `ready` to restore the completed fixture. This switch is preview-only and never affects a live control-plane decision.

## Sign-in and onboarding

The public landing page is `/`. The authenticated path is deliberately ordered:

1. `/sign-in` starts Casdoor Authorization Code + PKCE.
2. The callback at `/api/auth/callback` stores only the returned OIDC ID token in an HttpOnly, Secure (in production), SameSite=Lax cookie.
3. `/workspaces` is the post-sign-in directory. `/setup` requires an explicit workspace `tenant` selected there or created through `/workspaces/new`; a bare setup URL never falls back to a default workspace.
4. The workspace console is also session-protected. Its server-side BFF forwards the OIDC ID token to the Go control plane, which remains the authority that verifies the token and applies tenant RBAC.

Set `OPEN_REVIEW_APP_URL`, `CASDOOR_ISSUER`, `CASDOOR_CLIENT_ID`, and (for a confidential client) `CASDOOR_CLIENT_SECRET` in the web deployment. Register this exact callback with Casdoor:

```text
https://review.example.com/api/auth/callback
```

`OPEN_REVIEW_APP_URL` is the fixed callback origin, not a link inferred from
the browser Host header. Opening a local Console while it points to a public
origin now shows an actionable sign-in warning and does not start an OAuth
attempt that would return to the other deployment. For local HTTP testing use
the explicit development-only preview profile, a matching local
`OPEN_REVIEW_APP_URL`, and an exact callback registered with Casdoor; otherwise
open the configured public Console URL. A failed login preserves the requested
internal `next` path. Casdoor discovery must report the exact configured issuer;
its authorization and token endpoints must stay on that issuer's origin.
Discovery and code exchange are time-bounded and never follow HTTP redirects.

Set `OPEN_REVIEW_DEPLOYMENT_MODE` to `self_hosted` or `cloud` and provide a
non-sensitive `OPEN_REVIEW_DEPLOYMENT_REGION` label. The workspace creation
screen displays these deployment-owned facts together with the OIDC state. It
does not let a browser select a region, alter the hosting model, or replace the
identity provider.

`GITHUB_APP_INSTALL_URL` is the GitHub App installation URL. Configure the App setup URL as `https://review.example.com/api/setup/github/complete`, register the exact OAuth redirect URI `https://review.example.com/api/setup/github/authorize/complete`, and set `WEB_OAUTH_GITHUB_CLIENT_ID` plus `WEB_OAUTH_GITHUB_CLIENT_SECRET` in the Console secret manager. `GITHUB_OAUTH_CLIENT_*` and `GITHUB_APP_CLIENT_*` remain supported aliases for existing deployments. Set `GITHUB_APP_ID` as well: when an App is already installed, setup can obtain a short-lived GitHub user authorization and bind exactly one installation for that App instead of asking the browser for an installation ID. The browser adds signed, ten-minute state before opening GitHub. On return, the Console exchanges the authorization code server-side and confirms that the authorized GitHub user can enumerate the returned installation before it creates an HTTP-only signed authorization receipt. The setup form never displays or accepts an installation ID, user token, client secret, or App private key. A stale, direct, or unauthorized callback returns to setup without attaching a workspace. The App private key stays mounted only in worker-side deployment, and the worker separately verifies the exact declared repository scope with a read-only provider call before it enables admission.

GitLab.com and GitLab self-managed use an authorization-code + PKCE handoff. Configure `GITLAB_OAUTH_CLIENT_ID`, `GITLAB_OAUTH_CLIENT_SECRET`, and `OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET`; register `https://review.example.com/api/setup/gitlab/complete` as the GitLab application callback. Set `GITLAB_OAUTH_BASE_URL` when GitLab uses a separate public base or a relative URL installation such as `https://gitlab.example.com/gitlab`; authorization redirects always retain that browser-facing URL. When the Console or workers reach self-managed GitLab through a distinct trusted private address, set `GITLAB_OAUTH_INTERNAL_BASE_URL`; only token exchange, `/api/v4/user`, and refresh use it. The web server exchanges the callback code and makes one `GET /api/v4/user` identity check. It sends the OAuth material directly to the authenticated control plane, which encrypts it; the HTTP-only receipt, browser, audit log, and message bus carry only an opaque credential reference. Configure the base64 32-byte `PROVIDER_CREDENTIAL_ENCRYPTION_KEY` only on the control API and provider-call workers, not the Console. The setup BFF derives a non-PII opaque installation key from the workspace and confirmed repository scope, so a later authorization of the same scope reuses its record while non-overlapping scopes can coexist. Set the stable 32+ character `OPEN_REVIEW_GITLAB_INSTALLATION_IDENTITY_SECRET` before rotating the authorization-state secret; the state secret is used as a compatibility fallback when that key is omitted. GitLab webhooks are routed by the explicitly selected repository or `group/*` scope, not that OAuth identity; overlapping active GitLab scopes are rejected. A browser user never enters a GitLab token, identity, or endpoint. Workers decrypt this credential only at the provider side-effect boundary; `GITLAB_TOKEN` remains an explicit self-hosted fallback, not a replacement for OAuth-backed SaaS installations. To expose that fallback as **Use deployment token** in the authenticated setup and Connections pages, set the non-secret `GITLAB_DEPLOYMENT_TOKEN_CONFIGURED=true` only on control-api after mounting `GITLAB_TOKEN` on provider-call workers. The Console reads a tenant-authorized capability flag, then the server creates a short-lived receipt; it never reads the token and provider admission remains fail-closed until the worker probe succeeds.

An installation passing its provider probe is necessary but not sufficient for execution. Webhooks, mention commands, CLI reviews, console retry requests, and scheduled admission also require the tenant's durable setup checkpoint to be `complete`; before then, a valid webhook is retained as a skipped delivery with audit evidence, interactive commands return a durable remediation reply, HTTP/CLI submission returns `409`, and a due schedule becomes a durable blocked record. Existing verified installations with no checkpoint are treated as pre-checkpoint deployments for upgrade compatibility.

The first `@openreview review [--mode=standard|deep|security]` on a PR/MR is deliberately two-hop: Open Review first creates or updates a marker-keyed progress reply (and a GitHub 👀 reaction where supported); only after the provider accepts that reply does the interaction responder enqueue an admission worker. The worker resolves the deployment-owned credential, reads the provider's current base/head revision, and creates an acknowledged run from that exact revision. A second marker-keyed update is published before the run reaches the executor. This protects the visible acknowledgement-before-execution contract without trusting comment text for commit SHAs; retries are deduplicated by the interaction, delivery, outbox, and inbox keys.

## Deployment boundary

The image is standalone and exposes `GET /api/health`. The session bridge passes a short-lived OIDC token only on server-to-server control-plane requests; it does not place a provider credential, App private key, or GitLab token in browser state.
