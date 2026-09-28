import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";

import { safeInternalPath } from "@/lib/auth/session";
import { validGitHubInstallationID, validWorkspaceSlug } from "@/lib/auth/github-install";

export const GITHUB_USER_AUTHORIZATION_INTENT_COOKIE =
  "open_review_github_user_authorization_intent";

const intentLifetimeMilliseconds = 10 * 60 * 1000;

export type GitHubUserAuthorizationIntent = {
  installationID?: string;
  source: "existing" | "setup_callback";
  next: string;
  state: string;
  tenant: string;
};

type SignedGitHubUserAuthorizationIntent = GitHubUserAuthorizationIntent & {
  expiresAt: number;
  version: 1;
};

export type GitHubUserOAuthConfiguration = {
	appID?: string;
	authorizeURL: URL;
	clientID: string;
	clientSecret: string;
	installationsURL: URL;
	tokenURL: URL;
	userURL: URL;
};

function signingSecret() {
  const secret =
    process.env.OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET?.trim() ||
    process.env.OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET?.trim();
  return secret && secret.length >= 32 ? secret : undefined;
}

function trustedGitHubBaseURL() {
  const raw = process.env.GITHUB_OAUTH_BASE_URL?.trim() || "https://github.com";
  try {
    const url = new URL(raw);
    if (
      !url.hostname || url.username || url.password || url.search || url.hash ||
      (url.protocol !== "https:" && !(process.env.NODE_ENV !== "production" && url.protocol === "http:"))
    ) {
      return undefined;
    }
    url.pathname = url.pathname.replace(/\/+$/, "") || "/";
    return url;
  } catch {
    return undefined;
  }
}

function trustedGitHubAPIBaseURL() {
  const raw = process.env.GITHUB_API_URL?.trim() || "https://api.github.com";
  try {
    const url = new URL(raw);
    if (
      !url.hostname || url.username || url.password || url.search || url.hash ||
      (url.protocol !== "https:" && !(process.env.NODE_ENV !== "production" && url.protocol === "http:"))
    ) {
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

function destination(tenant: string, requestedNext: string) {
  const next = safeInternalPath(requestedNext);
  return next.startsWith(`/${tenant}/`) ? next : `/${tenant}/home`;
}

function signature(payload: string, secret: string) {
  return createHmac("sha256", secret).update(payload).digest("base64url");
}

function constantTimeEqual(left: string, right: string) {
  const leftBuffer = Buffer.from(left);
  const rightBuffer = Buffer.from(right);
  return leftBuffer.length === rightBuffer.length && timingSafeEqual(leftBuffer, rightBuffer);
}

export function getGitHubUserOAuthConfiguration(): GitHubUserOAuthConfiguration | undefined {
  const clientID =
    process.env.WEB_OAUTH_GITHUB_CLIENT_ID?.trim() ||
    process.env.GITHUB_OAUTH_CLIENT_ID?.trim() ||
    process.env.GITHUB_APP_CLIENT_ID?.trim();
  const clientSecret =
    process.env.WEB_OAUTH_GITHUB_CLIENT_SECRET?.trim() ||
    process.env.GITHUB_OAUTH_CLIENT_SECRET?.trim() ||
    process.env.GITHUB_APP_CLIENT_SECRET?.trim();
  const webBase = trustedGitHubBaseURL();
  const apiBase = trustedGitHubAPIBaseURL();
  if (!clientID || !clientSecret || !webBase || !apiBase || !signingSecret()) return undefined;
  return {
    appID: process.env.GITHUB_APP_ID?.trim() || undefined,
    authorizeURL: withPath(webBase, "login/oauth/authorize"),
    clientID,
    clientSecret,
		installationsURL: withPath(apiBase, "user/installations"),
		tokenURL: withPath(webBase, "login/oauth/access_token"),
		userURL: withPath(apiBase, "user"),
	};
}

export function githubExistingInstallationHandoffAvailable() {
  const configuration = getGitHubUserOAuthConfiguration();
  return Boolean(configuration && configuration.appID && /^[1-9][0-9]{0,18}$/.test(configuration.appID));
}

export function newGitHubUserAuthorizationIntent(
  tenant: string,
  installationID: string,
  requestedNext: string,
): GitHubUserAuthorizationIntent | undefined {
  if (!signingSecret() || !validWorkspaceSlug(tenant) || !validGitHubInstallationID(installationID)) {
    return undefined;
  }
  return {
    installationID,
    source: "setup_callback",
    next: destination(tenant, requestedNext),
    state: randomBytes(32).toString("base64url"),
    tenant,
  };
}

export function newExistingGitHubUserAuthorizationIntent(
  tenant: string,
  requestedNext: string,
): GitHubUserAuthorizationIntent | undefined {
  if (!signingSecret() || !validWorkspaceSlug(tenant)) return undefined;
  return {
    source: "existing",
    next: destination(tenant, requestedNext),
    state: randomBytes(32).toString("base64url"),
    tenant,
  };
}

export function encodeGitHubUserAuthorizationIntent(
  intent: GitHubUserAuthorizationIntent,
  now = Date.now(),
) {
  const secret = signingSecret();
  if (!secret || !validWorkspaceSlug(intent.tenant) || intent.state.length < 32 ||
    (intent.source === "setup_callback" && !validGitHubInstallationID(intent.installationID)) ||
    (intent.source === "existing" && intent.installationID !== undefined)) {
    return undefined;
  }
  const payload = Buffer.from(JSON.stringify({
    expiresAt: now + intentLifetimeMilliseconds,
    ...(intent.installationID ? { installationID: intent.installationID } : {}),
    next: destination(intent.tenant, intent.next),
    state: intent.state,
    source: intent.source,
    tenant: intent.tenant,
    version: 1,
  } satisfies SignedGitHubUserAuthorizationIntent)).toString("base64url");
  return `${payload}.${signature(payload, secret)}`;
}

export function decodeGitHubUserAuthorizationIntent(
  value: string | undefined,
  now = Date.now(),
): GitHubUserAuthorizationIntent | undefined {
  const secret = signingSecret();
  if (!secret || !value) return undefined;
  const [payload, receivedSignature, ...rest] = value.split(".");
  if (!payload || !receivedSignature || rest.length || !constantTimeEqual(signature(payload, secret), receivedSignature)) {
    return undefined;
  }
  try {
    const candidate = JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as Partial<SignedGitHubUserAuthorizationIntent>;
    if (
      candidate.version !== 1 ||
      typeof candidate.expiresAt !== "number" || candidate.expiresAt <= now ||
      (candidate.source !== "existing" && candidate.source !== "setup_callback") ||
      (candidate.source === "setup_callback" && (typeof candidate.installationID !== "string" || !validGitHubInstallationID(candidate.installationID))) ||
      (candidate.source === "existing" && candidate.installationID !== undefined) ||
      typeof candidate.state !== "string" || candidate.state.length < 32 ||
      typeof candidate.tenant !== "string" || !validWorkspaceSlug(candidate.tenant) ||
      typeof candidate.next !== "string"
    ) return undefined;
    return {
      ...(typeof candidate.installationID === "string" ? { installationID: candidate.installationID } : {}),
      next: destination(candidate.tenant, candidate.next),
      state: candidate.state,
      source: candidate.source,
      tenant: candidate.tenant,
    };
  } catch {
    return undefined;
  }
}

export function sameGitHubUserAuthorizationState(received: string | null | undefined, expected: string) {
  return Boolean(received && constantTimeEqual(received, expected));
}
