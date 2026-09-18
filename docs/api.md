# Control API bootstrap surface

All `/v1` management endpoints require a Casdoor OIDC bearer token in
production. For a local-only `AUTH_MODE=development` run, send an
`X-Development-Subject` header; do not expose that mode outside a developer
machine.

Create the first tenant (the caller becomes its owner):

```bash
curl -X POST http://localhost:8080/v1/tenants \
  -H 'Content-Type: application/json' \
  -H 'X-Development-Subject: casdoor-user-id' \
  --data '{"slug":"acme","name":"Acme"}'
```

Grant a teammate a role. Only an owner can assign roles; `admin` can manage
provider installations but cannot grant roles.

```bash
curl -X PUT http://localhost:8080/v1/tenants/acme/members/casdoor-user-id-2 \
  -H 'Content-Type: application/json' \
  -H 'X-Development-Subject: casdoor-user-id' \
  --data '{"role":"admin"}'
```

Register a provider installation. The current built-in resolver supports
`credential_ref=github-app` for GitHub: the runner exchanges the
deployment-mounted App private key for a short-lived installation token. For
GitLab, `credential_ref=gitlab-token` selects the separately configured
deployment token. Neither value is an access token or private key, and the
management API does not serialize it in installation responses.

```bash
curl -X POST http://localhost:8080/v1/tenants/acme/installations \
  -H 'Content-Type: application/json' \
  -H 'X-Development-Subject: casdoor-user-id' \
  --data '{
    "provider":"github",
    "external_id":"12345678",
    "repository_scope":"acme/*",
    "credential_ref":"github-app"
  }'
```

The control API derives `api_base_url` from its trusted `GITHUB_API_URL` or
`GITLAB_API_URL` deployment configuration; browser and API callers cannot
choose it. For GitHub Enterprise configure
`https://github.example.com/api/v3`; for GitLab self-managed configure
`https://gitlab.example.com/api/v4`. That configured endpoint participates in
installation matching, preventing identically numbered installations from
different provider instances being mixed.

List credential-safe installation summaries for an authorized tenant:

    GET /v1/tenants/acme/installations?limit=25
