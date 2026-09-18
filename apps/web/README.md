# Open Review Console

The management console is a Next.js App Router application. It currently reads the control plane's durable review-run and rule-set APIs. It does not place provider credentials or GitHub App keys in browser state.

## Local development

```bash
cp .env.example .env.local
pnpm install
pnpm dev
```

For a local control-plane connection, configure `CONTROL_API_URL` and `CONTROL_API_DEVELOPMENT_SUBJECT` in `.env.local`. This corresponds to the Go server's development authenticator; it is not a production login mechanism.

To inspect the interface without a control plane, set `OPEN_REVIEW_CONSOLE_DEMO=true`. The console visibly labels fixture content as preview data.

## Deployment boundary

The image is standalone and exposes `GET /api/health`. A production deployment still needs a Casdoor authorization-code/session bridge before it can pass a user bearer token to the control plane. Until that exists, the console deliberately reports an unconnected state instead of bypassing the API's OIDC authentication.
