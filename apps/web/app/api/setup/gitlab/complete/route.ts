import { NextRequest, NextResponse } from "next/server";

import {
  decodeGitLabOAuthIntent,
  gitLabAuthorizationReceiptExternalID,
  getGitLabOAuthConfiguration,
  GITLAB_OAUTH_INTENT_COOKIE,
  sameGitLabOAuthState,
} from "@/lib/auth/gitlab-oauth";
import { applicationOrigin } from "@/lib/auth/oidc";
import {
  authCookieOptions,
  getConsoleSession,
} from "@/lib/auth/session";
import {
  encodeProviderAuthorization,
  newProviderAuthorization,
  PROVIDER_AUTHORIZATION_COOKIE,
} from "@/lib/auth/provider-authorization";
import { getWorkspaceDirectory } from "@/lib/control-api";
import { forwardControlPlaneRequest } from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function clearIntent(response: NextResponse) {
  response.cookies.set(GITLAB_OAUTH_INTENT_COOKIE, "", {
    ...authCookieOptions(),
    expires: new Date(0),
  });
  return response;
}

function signInLocation(request: NextRequest) {
  const location = new URL("/sign-in", applicationOrigin(request));
  const next = new URL(request.nextUrl.pathname, applicationOrigin(request));
  const code = request.nextUrl.searchParams.get("code");
  const state = request.nextUrl.searchParams.get("state");
  if (code) next.searchParams.set("code", code);
  if (state) next.searchParams.set("state", state);
  location.searchParams.set("next", `${next.pathname}${next.search}`);
  return location;
}

function setupLocation(request: NextRequest, tenant: string, next: string, notice: string) {
  const location = new URL("/setup", applicationOrigin(request));
  location.searchParams.set("tenant", tenant);
  location.searchParams.set("next", next);
  location.searchParams.set("provider", "gitlab");
  location.searchParams.set("step", "provider");
  location.searchParams.set("notice", notice);
  return location;
}

async function exchangeAuthorizationCode(
  request: NextRequest,
  code: string,
  intent: NonNullable<ReturnType<typeof decodeGitLabOAuthIntent>>,
) {
  const configuration = getGitLabOAuthConfiguration();
  if (!configuration) return undefined;
  const redirectURI = new URL("/api/setup/gitlab/complete", applicationOrigin(request));
  const body = new URLSearchParams({
    client_id: configuration.clientID,
    client_secret: configuration.clientSecret,
    code,
    code_verifier: intent.codeVerifier,
    grant_type: "authorization_code",
    redirect_uri: redirectURI.toString(),
  });
  const response = await fetch(configuration.tokenURL, {
    body,
    cache: "no-store",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    method: "POST",
    redirect: "error",
  });
  if (!response.ok) return undefined;
  const payload = (await response.json()) as {
    access_token?: unknown;
    expires_in?: unknown;
    refresh_token?: unknown;
  };
  const expiresIn = typeof payload.expires_in === "number" &&
    Number.isSafeInteger(payload.expires_in) && payload.expires_in > 0 &&
    payload.expires_in <= 366 * 24 * 60 * 60
    ? payload.expires_in
    : undefined;
  return typeof payload.access_token === "string" && payload.access_token
    ? {
      configuration,
      expiresAt: expiresIn ? new Date(Date.now() + expiresIn * 1000).toISOString() : undefined,
      refreshToken: typeof payload.refresh_token === "string" ? payload.refresh_token : undefined,
      token: payload.access_token,
    }
    : undefined;
}

async function storeGitLabOAuthCredential(
  tenant: string,
  exchange: NonNullable<Awaited<ReturnType<typeof exchangeAuthorizationCode>>>,
) {
  const response = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(tenant)}/provider-credentials/gitlab-oauth`,
    {
      body: JSON.stringify({
        access_token: exchange.token,
        expires_at: exchange.expiresAt,
        refresh_token: exchange.refreshToken,
      }),
      headers: { "Content-Type": "application/json" },
      method: "POST",
    },
  );
  if (!response?.ok) return undefined;
  const payload = (await response.json()) as { credential_ref?: unknown };
  return typeof payload.credential_ref === "string" &&
    /^secret:\/\/provider\/gitlab-oauth\/[0-9a-f-]{36}$/i.test(payload.credential_ref)
    ? payload.credential_ref
    : undefined;
}

async function resolveGitLabIdentity(
  configuration: NonNullable<ReturnType<typeof getGitLabOAuthConfiguration>>,
  token: string,
) {
  const response = await fetch(configuration.identityURL, {
    cache: "no-store",
    headers: { Accept: "application/json", Authorization: `Bearer ${token}` },
    redirect: "error",
  });
  if (!response.ok) return undefined;
  const payload = (await response.json()) as { id?: unknown };
  if (typeof payload.id === "number" && Number.isSafeInteger(payload.id) && payload.id > 0) {
    return String(payload.id);
  }
  if (typeof payload.id === "string" && /^[1-9][0-9]{0,18}$/.test(payload.id)) {
    return payload.id;
  }
  return undefined;
}

// Bind the GitLab account that completed OAuth to the currently authenticated
// Console member. Webhook command authorization is intentionally actor-based;
// accepting a repository-scoped deployment credential must not grant every
// provider user permission to issue @openreview commands.
async function bindAuthorizedGitLabActor(tenant: string, actorID: string) {
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(tenant)}/provider-identities/gitlab/${encodeURIComponent(actorID)}/self`,
    { method: "PUT" },
  );
  return Boolean(upstream?.ok);
}

export async function GET(request: NextRequest) {
  if (!(await getConsoleSession())) {
    return NextResponse.redirect(signInLocation(request));
  }
  const intent = decodeGitLabOAuthIntent(
    request.cookies.get(GITLAB_OAUTH_INTENT_COOKIE)?.value,
  );
  const code = request.nextUrl.searchParams.get("code");
  const state = request.nextUrl.searchParams.get("state");
  if (!intent || !sameGitLabOAuthState(state, intent.state)) {
    return clearIntent(
      NextResponse.redirect(new URL("/workspaces", applicationOrigin(request))),
    );
  }
  if (!code) {
    return clearIntent(
      NextResponse.redirect(
        setupLocation(request, intent.tenant, intent.next, "gitlab_authorization_failed"),
      ),
    );
  }
  // The callback state binds this response to the original browser, but it
  // cannot establish that the caller is still an administrator. Make the same
  // live membership check used before the OAuth redirect before exchanging a
  // code or persisting a credential receipt.
  const directory = await getWorkspaceDirectory();
  const canManageWorkspace = directory.workspaces.some(
    (workspace) =>
      workspace.slug === intent.tenant &&
      (workspace.role === "owner" || workspace.role === "admin"),
  );
  if (!canManageWorkspace) {
    return clearIntent(
      NextResponse.redirect(
        setupLocation(
          request,
          intent.tenant,
          intent.next,
          "gitlab_authorization_workspace_unavailable",
        ),
      ),
    );
  }
  try {
    const exchange = await exchangeAuthorizationCode(request, code, intent);
    const identity =
      exchange &&
      (await resolveGitLabIdentity(exchange.configuration, exchange.token));
    const credentialRef = exchange && identity
      ? await storeGitLabOAuthCredential(intent.tenant, exchange)
      : undefined;
    const identityBound = identity && credentialRef
      ? await bindAuthorizedGitLabActor(intent.tenant, identity)
      : false;
    const authorization =
      identity && credentialRef && identityBound &&
      newProviderAuthorization(
        "gitlab",
        intent.tenant,
        // GitLab webhooks identify projects, while command authorization uses
        // the bound OAuth user above. The installation BFF derives the durable
        // opaque project identity after repository scope selection.
        gitLabAuthorizationReceiptExternalID(intent.tenant) ?? "",
        intent.next,
        credentialRef,
        identity,
      );
    const encodedAuthorization =
      authorization && encodeProviderAuthorization(authorization);
    if (!encodedAuthorization) {
      return clearIntent(
        NextResponse.redirect(
          setupLocation(request, intent.tenant, intent.next, "gitlab_authorization_failed"),
        ),
      );
    }
    const location = new URL("/setup", applicationOrigin(request));
    location.searchParams.set("tenant", intent.tenant);
    location.searchParams.set("next", intent.next);
    location.searchParams.set("provider", "gitlab");
    location.searchParams.set("step", "install");
    location.searchParams.set("notice", "gitlab_authorization_returned");
    const response = clearIntent(NextResponse.redirect(location));
    response.cookies.set(
      PROVIDER_AUTHORIZATION_COOKIE,
      encodedAuthorization,
      authCookieOptions(30 * 60),
    );
    return response;
  } catch {
    return clearIntent(
      NextResponse.redirect(
        setupLocation(request, intent.tenant, intent.next, "gitlab_authorization_failed"),
      ),
    );
  }
}
