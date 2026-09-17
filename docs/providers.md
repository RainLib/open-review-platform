# Provider integration contract

## GitHub

Configure a GitHub App with pull-request read/write, contents read, metadata
read, and webhook events for `pull_request`. The control API checks
`X-Hub-Signature-256` with constant-time HMAC-SHA256 comparison and uses
`X-GitHub-Delivery` as the idempotency key. Only `opened`, `reopened`, and
`synchronize` events become review jobs.

Production publishing obtains a short-lived installation token from the
GitHub App private key mounted into the runner. Configure `GITHUB_APP_ID` and
`GITHUB_APP_PRIVATE_KEY_PATH`, then save `credential_ref=github-app` for the
installation. The runner exchanges an App JWT immediately before clone or
publish; the token never enters PostgreSQL, RabbitMQ, logs, or traces. The
publisher uses the PR review API; low-confidence/unpositioned findings go to a
summary, never a guessed line.

## GitLab

Configure a GitLab application or project/group webhook with merge-request
events and a random secret token. The control API validates `X-Gitlab-Token`
with constant-time comparison and uses `X-Gitlab-Event-UUID` (or a computed
content ID only where the provider does not supply one) for idempotency. Only
open/update merge-request actions become jobs.

The publisher creates discussions with a diff position only when it can prove
the base/head SHA and changed-side line. Otherwise it produces a normal MR
note; it must not fabricate an inline location.

## Normalized event

Every adapter produces the same fields: provider, provider API base URL,
external installation ID, repository slug/clone URL, review number, base
ref/SHA, head ref/SHA, and delivery ID. This is the extension point for GitHub Enterprise, GitLab
self-managed, and a future Kodus ingress without forking the review engine.
