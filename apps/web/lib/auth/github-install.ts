import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";

import { safeInternalPath } from "@/lib/auth/session";

export const GITHUB_INSTALL_INTENT_COOKIE =
  "open_review_github_install_intent";

const intentLifetimeMilliseconds = 10 * 60 * 1000;

export type GitHubInstallIntent = {
  next: string;
  state: string;
  tenant: string;
};

type SignedGitHubInstallIntent = GitHubInstallIntent & {
  expiresAt: number;
  version: 1;
};

function intentSigningSecret() {
  // Keep existing dedicated GitHub state keys preferred, but allow the
  // deployment-wide provider receipt secret documented for a unified GitHub /
  // GitLab setup. The callback and the control plane verify the same HMAC
  // receipt; browser JavaScript never receives either secret.
  const secret =
    process.env.OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET?.trim() ||
    process.env.OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET?.trim();
  return secret && secret.length >= 32 ? secret : undefined;
}

export function validWorkspaceSlug(value: string | null | undefined) {
  return Boolean(value && /^[a-z0-9][a-z0-9-]{1,62}$/.test(value));
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

export function gitHubAppInstallURL() {
  const raw = process.env.GITHUB_APP_INSTALL_URL?.trim();
  if (!raw) return undefined;
  try {
    const url = new URL(raw);
    if (
      !url.hostname ||
      url.username ||
      url.password ||
      url.hash ||
      (url.protocol !== "https:" &&
        !(process.env.NODE_ENV !== "production" && url.protocol === "http:"))
    ) {
      return undefined;
    }
    return url;
  } catch {
    return undefined;
  }
}

export function githubInstallHandoffAvailable() {
  return Boolean(gitHubAppInstallURL() && intentSigningSecret());
}

export function workspaceDestination(
  tenant: string,
  requestedNext: string | null | undefined,
) {
  const fallback = `/${tenant}/home`;
  const next = safeInternalPath(requestedNext);
  return next.startsWith(`/${tenant}/`) ? next : fallback;
}

export function newGitHubInstallIntent(
  tenant: string,
  requestedNext: string | null | undefined,
): GitHubInstallIntent | undefined {
  if (!validWorkspaceSlug(tenant) || !intentSigningSecret()) return undefined;
  return {
    next: workspaceDestination(tenant, requestedNext),
    state: randomBytes(32).toString("base64url"),
    tenant,
  };
}

export function encodeGitHubInstallIntent(
  intent: GitHubInstallIntent,
  now = Date.now(),
) {
  const secret = intentSigningSecret();
  if (!secret || !validWorkspaceSlug(intent.tenant) || !intent.state) return undefined;
  const payload = Buffer.from(
    JSON.stringify({
      expiresAt: now + intentLifetimeMilliseconds,
      next: workspaceDestination(intent.tenant, intent.next),
      state: intent.state,
      tenant: intent.tenant,
      version: 1,
    } satisfies SignedGitHubInstallIntent),
  ).toString("base64url");
  return `${payload}.${signature(payload, secret)}`;
}

export function decodeGitHubInstallIntent(
  value: string | undefined,
  now = Date.now(),
): GitHubInstallIntent | undefined {
  const secret = intentSigningSecret();
  if (!secret || !value) return undefined;
  const [payload, receivedSignature, ...rest] = value.split(".");
  if (!payload || !receivedSignature || rest.length || !constantTimeEqual(
    signature(payload, secret),
    receivedSignature,
  )) {
    return undefined;
  }

  try {
    const candidate = JSON.parse(
      Buffer.from(payload, "base64url").toString("utf8"),
    ) as Partial<SignedGitHubInstallIntent>;
    if (
      candidate.version !== 1 ||
      typeof candidate.expiresAt !== "number" ||
      candidate.expiresAt <= now ||
      typeof candidate.state !== "string" ||
      candidate.state.length < 32 ||
      typeof candidate.tenant !== "string" ||
      !validWorkspaceSlug(candidate.tenant) ||
      typeof candidate.next !== "string"
    ) {
      return undefined;
    }
    return {
      next: workspaceDestination(candidate.tenant, candidate.next),
      state: candidate.state,
      tenant: candidate.tenant,
    };
  } catch {
    return undefined;
  }
}

export function validGitHubInstallationID(value: string | null | undefined) {
  return Boolean(value && /^[1-9][0-9]{0,18}$/.test(value));
}

export function sameInstallState(
  received: string | null | undefined,
  expected: string,
) {
  return Boolean(received && constantTimeEqual(received, expected));
}
