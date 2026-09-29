import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";

import { safeInternalPath } from "@/lib/auth/session";

export const PROVIDER_AUTHORIZATION_COOKIE =
  "open_review_provider_authorization";

// Server-to-server only. The BFF forwards the opaque receipt to the control
// plane when it creates an installation; page JavaScript never reads it.
export const PROVIDER_AUTHORIZATION_HEADER =
  "X-Open-Review-Provider-Authorization";

export type ProviderAuthorizationProvider = "github" | "gitlab";

export type ProviderAuthorization = {
  actorExternalID?: string;
  credentialRef?: string;
  externalID: string;
  next: string;
  provider: ProviderAuthorizationProvider;
  tenant: string;
};

type SignedProviderAuthorization = ProviderAuthorization & {
  expiresAt: number;
  version: 1;
};

const authorizationLifetimeMilliseconds = 30 * 60 * 1000;
const gitLabOAuthCredentialReference =
  /^secret:\/\/provider\/gitlab-oauth\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const providerActorID = /^[1-9][0-9]{0,18}$/;

function signingSecret() {
  const secret =
    process.env.OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET?.trim() ||
    process.env.OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET?.trim();
  return secret && secret.length >= 32 ? secret : undefined;
}

export function validWorkspaceSlug(value: string | null | undefined) {
  return Boolean(value && /^[a-z0-9][a-z0-9-]{1,62}$/.test(value));
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

function validProvider(value: unknown): value is ProviderAuthorizationProvider {
  return value === "github" || value === "gitlab";
}

function validCredentialReference(value: string | undefined) {
  return !value || gitLabOAuthCredentialReference.test(value);
}

// An authorization receipt is an opaque browser cookie, not a provider token.
// It proves either a provider-managed redirect or an administrator's selection
// of a deployment-owned credential slot for a workspace. The control-plane API
// consumes it through a server BFF and never accepts an installation identity
// supplied by page JavaScript.
export function newProviderAuthorization(
  provider: ProviderAuthorizationProvider,
  tenant: string,
  externalID: string,
  next: string,
  credentialRef?: string,
  actorExternalID?: string,
): ProviderAuthorization | undefined {
  if (
    !signingSecret() ||
    !validWorkspaceSlug(tenant) ||
    !externalID.trim() ||
    !validProvider(provider) ||
    !validCredentialReference(credentialRef?.trim()) ||
    (actorExternalID !== undefined && !providerActorID.test(actorExternalID))
  ) {
    return undefined;
  }
  return {
    actorExternalID,
    credentialRef: credentialRef?.trim() || undefined,
    externalID: externalID.trim(),
    next: safeWorkspaceDestination(tenant, next),
    provider,
    tenant,
  };
}

export function encodeProviderAuthorization(
  authorization: ProviderAuthorization,
  now = Date.now(),
) {
  const secret = signingSecret();
  if (
    !secret ||
    !validWorkspaceSlug(authorization.tenant) ||
    !validProvider(authorization.provider) ||
    !authorization.externalID.trim() ||
    !validCredentialReference(authorization.credentialRef?.trim()) ||
    (authorization.actorExternalID !== undefined &&
      !providerActorID.test(authorization.actorExternalID))
  ) {
    return undefined;
  }
  const payload = Buffer.from(
    JSON.stringify({
      actorExternalID: authorization.actorExternalID,
      expiresAt: now + authorizationLifetimeMilliseconds,
      credentialRef: authorization.credentialRef?.trim() || undefined,
      externalID: authorization.externalID.trim(),
      next: safeWorkspaceDestination(authorization.tenant, authorization.next),
      nonce: randomBytes(16).toString("base64url"),
      provider: authorization.provider,
      tenant: authorization.tenant,
      version: 1,
    } satisfies SignedProviderAuthorization & { nonce: string }),
  ).toString("base64url");
  return `${payload}.${signature(payload, secret)}`;
}

export function decodeProviderAuthorization(
  value: string | undefined,
  now = Date.now(),
): ProviderAuthorization | undefined {
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
    ) as Partial<SignedProviderAuthorization>;
    if (
      candidate.version !== 1 ||
      typeof candidate.expiresAt !== "number" ||
      candidate.expiresAt <= now ||
      !validProvider(candidate.provider) ||
      typeof candidate.tenant !== "string" ||
      !validWorkspaceSlug(candidate.tenant) ||
      typeof candidate.externalID !== "string" ||
      !candidate.externalID.trim() ||
      (candidate.actorExternalID !== undefined &&
        (typeof candidate.actorExternalID !== "string" ||
          !providerActorID.test(candidate.actorExternalID))) ||
      typeof candidate.next !== "string"
    ) {
      return undefined;
    }
    const credentialRef =
      typeof candidate.credentialRef === "string" &&
      validCredentialReference(candidate.credentialRef.trim())
        ? candidate.credentialRef.trim()
        : undefined;
    if (typeof candidate.credentialRef !== "undefined" && !credentialRef) {
      return undefined;
    }
    return {
      actorExternalID: candidate.actorExternalID,
      credentialRef,
      externalID: candidate.externalID.trim(),
      next: safeWorkspaceDestination(candidate.tenant, candidate.next),
      provider: candidate.provider,
      tenant: candidate.tenant,
    };
  } catch {
    return undefined;
  }
}

export function providerAuthorizationAvailable() {
  return Boolean(signingSecret());
}
