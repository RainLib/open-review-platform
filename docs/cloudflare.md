# Cloudflare public ingress

The control plane stays a normal stateful Go deployment. Cloudflare provides
the public HTTPS edge for Git provider webhooks; PostgreSQL, RabbitMQ, the
outbox relay, and the OCR runner stay on the private application network.

## Provisioned verification endpoint

The verification tunnel is named `open-review-platform-verify` and serves:

```text
https://review.rainlib.com/v1/webhooks/github
https://review.rainlib.com/v1/webhooks/gitlab
```

The tunnel credential JSON is intentionally local-only. Do not commit it or
place it in a GitHub Actions secret. Store it in the deployment platform's
secret manager and mount it read-only to the Cloudflared service.

## Cloudflared configuration

Create the following configuration on the runtime host, replacing the ID and
credential path with the values created in that account:

```yaml
tunnel: REPLACE_WITH_TUNNEL_ID
credentials-file: /run/secrets/cloudflared-tunnel.json

ingress:
  - hostname: review.rainlib.com
    service: http://control-api:8080
  - service: http_status:404
```

Run Cloudflared in the same private network as `control-api`. It must be the
only public-facing workload. The runner, relay, PostgreSQL, and RabbitMQ must
not receive public ports.

## GitHub App boundary

Use `https://review.rainlib.com/v1/webhooks/github` as the GitHub App webhook
URL. Enable only **Pull requests** and **Issue comments** events. The App
requires read access to repository contents and read/write access to pull
requests and issues so it can publish review findings and command responses.

Set `GITHUB_APP_ID`, `GITHUB_APP_PRIVATE_KEY_PATH`, and
`GITHUB_WEBHOOK_SECRET` on `control-api` and `runner`; use
`credential_ref=github-app` when registering the provider installation.

## Acceptance checks

1. `curl -fsS https://review.rainlib.com/healthz` returns `{"status":"ok"}`.
2. GitHub's webhook test delivery returns HTTP 202 only after the matching App
   installation is registered.
3. A pull-request delivery creates one run and one durable outbox message.
4. A repeated delivery does not create a second job.
5. A command-triggered review creates a fresh queued `review_jobs` row linked
   to its new review run, which the runner can claim independently.
