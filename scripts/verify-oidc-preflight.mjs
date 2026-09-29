#!/usr/bin/env node

// Verify the public OIDC entry/callback boundary without submitting an account
// credential or an authorization code. Never print the state or cookie value.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";

const input = process.argv[2];
if (!input) {
  console.error("Usage: node scripts/verify-oidc-preflight.mjs https://review.example.com");
  process.exit(2);
}

let origin;
try {
  const url = new URL(input);
  assert.equal(url.protocol, "https:", "a public preflight must use HTTPS");
  assert.equal(url.pathname, "/", "pass only the Console origin");
  assert.equal(url.search, "", "pass only the Console origin");
  assert.equal(url.hash, "", "pass only the Console origin");
  assert.equal(url.username, "", "credentials must not appear in the URL");
  assert.equal(url.password, "", "credentials must not appear in the URL");
  origin = url.origin;
} catch (error) {
  console.error(`Invalid Console origin: ${error.message}`);
  process.exit(2);
}

async function request(path, options = {}) {
  return fetch(new URL(path, origin), {
    cache: "no-store",
    redirect: "manual",
    signal: AbortSignal.timeout(15_000),
    ...options,
  });
}

function redirectLocation(response) {
  assert.equal(response.status, 307, `expected a 307 redirect, received ${response.status}`);
  const location = response.headers.get("location");
  assert.ok(location, "redirect has no Location header");
  return new URL(location);
}

try {
  const signIn = await request("/sign-in?next=%2Fworkspaces");
  assert.equal(signIn.status, 200, `sign-in page returned ${signIn.status}`);

  // A rejected ID token may remain in the browser's HttpOnly cookie until a
  // replacement login succeeds. The renewal page must stay reachable instead
  // of redirecting that stale cookie back to the protected destination.
  const renewal = await request("/sign-in?error=session&next=%2Fworkspaces", {
    headers: { cookie: "open_review_id_token=invalid-fixture" },
  });
  assert.equal(renewal.status, 200, `session-renewal page returned ${renewal.status}`);

  const login = await request("/api/auth/login?next=%2Fworkspaces");
  const authorization = redirectLocation(login);
  assert.equal(authorization.protocol, "https:", "identity provider must use HTTPS");
  assert.equal(
    authorization.searchParams.get("redirect_uri"),
    `${origin}/api/auth/callback`,
    "identity provider callback does not match the Console origin",
  );
  assert.equal(authorization.searchParams.get("response_type"), "code");
  assert.equal(authorization.searchParams.get("code_challenge_method"), "S256");
  assert.ok(authorization.searchParams.get("code_challenge"), "PKCE challenge is missing");
  const state = authorization.searchParams.get("state");
  assert.match(state ?? "", /^[A-Za-z0-9_-]{43}$/, "OIDC state is missing or malformed");

  const cookieHeader = login.headers.get("set-cookie") ?? "";
  const cookieName = `open_review_oidc_attempt_${createHash("sha256").update(state).digest("hex").slice(0, 20)}`;
  assert.ok(cookieHeader.startsWith(`${cookieName}=`), "OIDC attempt cookie does not match state");
  assert.match(cookieHeader, /;\s*Path=\//i, "attempt cookie is not site-wide");
  assert.match(cookieHeader, /;\s*Secure(?:;|$)/i, "attempt cookie is not Secure");
  assert.match(cookieHeader, /;\s*HttpOnly(?:;|$)/i, "attempt cookie is not HttpOnly");
  assert.match(cookieHeader, /;\s*SameSite=Lax(?:;|$)/i, "attempt cookie is not SameSite=Lax");
  const cookie = cookieHeader.split(";", 1)[0];

  // No authorization code is sent. A matched attempt must reach the provider
  // error branch; without the cookie it must fail the state gate.
  const callbackPath = `/api/auth/callback?state=${encodeURIComponent(state)}`;
  const accepted = redirectLocation(await request(callbackPath, { headers: { cookie } }));
  assert.equal(accepted.origin, origin);
  assert.equal(accepted.pathname, "/sign-in");
  assert.equal(accepted.searchParams.get("error"), "provider");
  assert.equal(accepted.searchParams.get("next"), "/workspaces");

  const rejected = redirectLocation(await request(callbackPath));
  assert.equal(rejected.origin, origin);
  assert.equal(rejected.searchParams.get("error"), "state");

  console.log(JSON.stringify({
    status: "passed",
    console_origin: origin,
    identity_provider_origin: authorization.origin,
    callback_origin: accepted.origin,
    state_cookie: "scoped, Secure, HttpOnly, SameSite=Lax",
    matched_attempt: "provider error without code",
    missing_cookie: "state error",
    rejected_session: "renewal page remains reachable",
    authenticated_login: "not tested",
  }, null, 2));
} catch (error) {
  console.error(`OIDC preflight failed: ${error.message}`);
  process.exitCode = 1;
}
