# Provider integration contract

## GitHub

Configure a GitHub App with pull-request read/write, issues read/write, contents
read, metadata read, and webhook events for `pull_request` and
`issue_comment`. The control API checks
`X-Hub-Signature-256` with constant-time HMAC-SHA256 comparison and uses
`X-GitHub-Delivery` as the idempotency key. Only `opened`, `reopened`, and
`synchronize` events become review jobs.

Production publishing obtains a short-lived installation token from the
GitHub App private key mounted into the runner and interaction responder. Configure `GITHUB_APP_ID` and
`GITHUB_APP_PRIVATE_KEY_PATH`, then save `credential_ref=github-app` for the
installation. The runner and responder exchange an App JWT immediately before
a provider call; the token never enters PostgreSQL, RabbitMQ, logs, or traces. The
publisher uses the PR review API; low-confidence/unpositioned findings go to a
summary, never a guessed line. The interaction responder uses the same
short-lived installation identity to post a marker-keyed command response
before the queued review executes. For a command-triggered run, the response
consumer is also the execution barrier: it does not release the durable
acknowledgement event until the provider accepted that response. Once that
response is durably published, it also adds an idempotent reaction to the
source command comment: `eyes` for an
accepted command and `confused` for a rejected command. This acknowledgement
uses the GitHub issue-comment reactions API and is retried through the same
inbox fence; it does not alter review authorization or merge policy.

## GitLab

Configure a GitLab application or project/group webhook with merge-request and
note events plus a random secret token. The control API validates `X-Gitlab-Token`
with constant-time comparison and uses `X-Gitlab-Event-UUID` (or a computed
content ID only where the provider does not supply one) for idempotency. Only
open/update merge-request actions become jobs.

The publisher creates discussions with a diff position only when it can prove
the base/head SHA and changed-side line. Otherwise it produces a normal MR
note; it must not fabricate an inline location. `Note Hook` commands are only
accepted for merge-request notes and receive a marker-keyed MR note response;
other noteable types are ignored.

## Normalized event

Every adapter produces the same fields: provider, provider API base URL,
external installation ID, repository slug/clone URL, review number, base
ref/SHA, head ref/SHA, and delivery ID. This is the extension point for GitHub Enterprise, GitLab
self-managed, and a future Kodus ingress without forking the review engine.
