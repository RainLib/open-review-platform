import { createHash, createHmac, randomBytes, timingSafeEqual } from "node:crypto";

import { safeInternalPath } from "@/lib/auth/session";
import { validWorkspaceSlug } from "@/lib/auth/provider-authorization";

export const GITLAB_OAUTH_INTENT_COOKIE = "open_review_gitlab_oauth_intent";

const intentLifetimeMilliseconds = 10 * 60 * 1000;

export type GitLabOAuthIntent = {
  codeVerifier: string;
  next: string;
  state: string;
  tenant: string;
};

type SignedGitLabOAuthIntent = GitLabOAuthIntent & {
  expiresAt: number;
  version: 1;
};

export type GitLabOAuthConfiguration = {
  authorizeURL: URL;
  clientID: string;
  clientSecret: string;
  identityURL: URL;
  scopes: string;
  tokenURL: URL;
};

function signingSecret() {
  const secret =
    process.env.OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET?.trim() ||
    process.env.OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET?.trim();
  return secret && secret.length >= 32 ? secret : undefined;
}

// This key is intentionally separate from the OAuth user identity. GitLab
// webhooks identify projects, and a user may complete a new OAuth handoff
// after a browser or provider session expires. Deriving a stable, opaque key
// from the workspace means that handoff reuses its durable installation rather
// than creating another routing record. Deployments can rotate the short-lived
// authorization-state secret independently by setting this stable key first.
function installationIdentitySecret() {
  const secret = process.env.OPEN_REVIEW_GITLAB_INSTALLATION_IDENTITY_SECRET?.trim();
  return secret && secret.length >= 32 ? secret : signingSecret();
}

function allowsDevelopmentGitLabHTTP() {
  return (
    process.env.ENVIRONMENT === "development" &&
    process.env.GITLAB_ALLOW_HTTP === "true"
  );
}

function trustedBaseURL(value: string) {
  try {
    const url = new URL(value);
    if (!(
      Boolean(url.hostname) &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash &&
      (url.protocol === "https:" ||
        (allowsDevelopmentGitLabHTTP() && url.protocol === "http:"))
    )) {
      return undefined;
    }
    url.pathname = url.pathname.replace(/\/+$/, "") || "/";
    return url;
  } catch {
    return undefined;
  }
}

function withPath(base: URL, path: string) {
  const url = new URL(base);
  url.pathname = `${base.pathname.replace(/\/+$/, "")}/${path}`;
  return url;
}

function gitLabOAuthBaseURL() {
  const oauthBase = process.env.GITLAB_OAUTH_BASE_URL?.trim();
  if (oauthBase) return trustedBaseURL(oauthBase);

  // Match the control plane and Compose defaults for GitLab.com. A blank
  // dotenv placeholder must not silently disable OAuth when the client ID and
  // secret are otherwise configured; self-managed deployments still opt in by
  // setting GITLAB_API_URL or GITLAB_OAUTH_BASE_URL explicitly.
  const apiBase = trustedBaseURL(
    process.env.GITLAB_API_URL?.trim() || "https://gitlab.com/api/v4",
  );
  if (!apiBase || !/\/api\/v4$/.test(apiBase.pathname)) return undefined;
  apiBase.pathname = apiBase.pathname.replace(/\/api\/v4$/, "") || "/";
  return apiBase;
}

// Authorization redirects must use a browser-reachable origin, while a local
// Docker Console needs a bridge-reachable endpoint for its server-side code
// exchange and `/api/v4/user` lookup. Normal deployments leave this unset and
// use one public HTTPS origin for both directions.
function gitLabOAuthInternalBaseURL(publicBase: URL) {
  const internalBase = process.env.GITLAB_OAUTH_INTERNAL_BASE_URL?.trim();
  return internalBase ? trustedBaseURL(internalBase) : publicBase;
}

function safeWorkspaceDestination(tenant: string, requestedNext: string) {
  const next = safeInternalPath(requestedNext);
  return next.startsWith(`/${tenant}/`) ? next : `/${tenant}/home`;
}

function constantTimeEqual(left: string, right: string) {
  const leftBuffer = Buffer.from(left);
  const rightBuffer = Buffer.from(right);
  return (
    leftBuffer.length === rightBuffer.length &&
    timingSafeEqual(leftBuffer, rightBuffer)
  );
}

function signature(payload: string, secret: string) {
  return createHmac("sha256", secret).update(payload).digest("base64url");
}

export function getGitLabOAuthConfiguration(): GitLabOAuthConfiguration | undefined {
  const clientID = process.env.GITLAB_OAUTH_CLIENT_ID?.trim();
  const clientSecret = process.env.GITLAB_OAUTH_CLIENT_SECRET?.trim();
  const baseURL = gitLabOAuthBaseURL();
  const internalBaseURL = baseURL && gitLabOAuthInternalBaseURL(baseURL);
  if (!clientID || !clientSecret || !baseURL || !internalBaseURL || !signingSecret()) return undefined;
  return {
    authorizeURL: withPath(baseURL, "oauth/authorize"),
    clientID,
    clientSecret,
    identityURL: withPath(internalBaseURL, "api/v4/user"),
    scopes: process.env.GITLAB_OAUTH_SCOPES?.trim() || "api read_user",
    tokenURL: withPath(internalBaseURL, "oauth/token"),
  };
}

export function gitLabAuthorizationReceiptExternalID(tenant: string) {
  const secret = installationIdentitySecret();
  if (!secret || !validWorkspaceSlug(tenant)) return undefined;
  return `gitlab-oauth:${createHmac("sha256", secret)
    .update(`open-review/gitlab-authorization-receipt/v1/${tenant}`)
    .digest("base64url")}`;
}

// Repository scope becomes part of the durable key only after the signed
// callback has returned. This permits several non-overlapping GitLab scopes in
// one workspace while still making a deactivated scope idempotently reusable.
// It deliberately receives the scope from the BFF, never a provider identity
// from browser JavaScript.
export function gitLabInstallationExternalID(
  tenant: string,
  repositoryScope: string,
) {
  const secret = installationIdentitySecret();
  const scope = repositoryScope.trim();
  if (!secret || !validWorkspaceSlug(tenant) || !scope) return undefined;
  return `gitlab-scope:${createHmac("sha256", secret)
    .update(`open-review/gitlab-installation/v1/${tenant}\u0000${scope}`)
    .digest("base64url")}`;
}

export function newGitLabOAuthIntent(
  tenant: string,
  requestedNext: string | null | undefined,
): GitLabOAuthIntent | undefined {
  if (!validWorkspaceSlug(tenant) || !signingSecret()) return undefined;
  return {
    codeVerifier: randomBytes(48).toString("base64url"),
    next: safeWorkspaceDestination(tenant, requestedNext ?? ""),
    state: randomBytes(32).toString("base64url"),
    tenant,
  };
}

export function pkceChallenge(verifier: string) {
  return createHash("sha256").update(verifier).digest("base64url");
}

export function encodeGitLabOAuthIntent(
  intent: GitLabOAuthIntent,
  now = Date.now(),
) {
  const secret = signingSecret();
  if (!secret || !validWorkspaceSlug(intent.tenant) || !intent.state) return undefined;
  const payload = Buffer.from(
    JSON.stringify({
      codeVerifier: intent.codeVerifier,
      expiresAt: now + intentLifetimeMilliseconds,
      next: safeWorkspaceDestination(intent.tenant, intent.next),
      state: intent.state,
      tenant: intent.tenant,
      version: 1,
    } satisfies SignedGitLabOAuthIntent),
  ).toString("base64url");
  return `${payload}.${signature(payload, secret)}`;
}

export function decodeGitLabOAuthIntent(
  value: string | undefined,
  now = Date.now(),
): GitLabOAuthIntent | undefined {
  const secret = signingSecret();
  if (!secret || !value) return undefined;
  const [payload, receivedSignature, ...rest] = value.split(".");
  if (
    !payload ||
    !receivedSignature ||
    rest.length ||
    !constantTimeEqual(signature(payload, secret), receivedSignature)
  ) {
    return undefined;
  }
  try {
    const candidate = JSON.parse(
      Buffer.from(payload, "base64url").toString("utf8"),
    ) as Partial<SignedGitLabOAuthIntent>;
    if (
      candidate.version !== 1 ||
      typeof candidate.expiresAt !== "number" ||
      candidate.expiresAt <= now ||
      typeof candidate.codeVerifier !== "string" ||
      candidate.codeVerifier.length < 32 ||
      typeof candidate.state !== "string" ||
      candidate.state.length < 32 ||
      typeof candidate.tenant !== "string" ||
      !validWorkspaceSlug(candidate.tenant) ||
      typeof candidate.next !== "string"
    ) {
      return undefined;
    }
    return {
      codeVerifier: candidate.codeVerifier,
      next: safeWorkspaceDestination(candidate.tenant, candidate.next),
      state: candidate.state,
      tenant: candidate.tenant,
    };
  } catch {
    return undefined;
  }
}

export function sameGitLabOAuthState(
  received: string | null | undefined,
  expected: string,
) {
  return Boolean(received && constantTimeEqual(received, expected));
}
