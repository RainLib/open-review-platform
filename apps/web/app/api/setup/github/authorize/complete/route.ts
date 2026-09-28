import { NextRequest, NextResponse } from "next/server";

import { applicationOrigin } from "@/lib/auth/oidc";
import {
  authCookieOptions,
  getConsoleSession,
} from "@/lib/auth/session";
import {
  decodeGitHubUserAuthorizationIntent,
  getGitHubUserOAuthConfiguration,
  GITHUB_USER_AUTHORIZATION_INTENT_COOKIE,
  sameGitHubUserAuthorizationState,
} from "@/lib/auth/github-user-oauth";
import {
  encodeProviderAuthorization,
  newProviderAuthorization,
  PROVIDER_AUTHORIZATION_COOKIE,
} from "@/lib/auth/provider-authorization";
import { forwardControlPlaneRequest } from "@/lib/control-plane-proxy";
import { getWorkspaceDirectory } from "@/lib/control-api";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function clearIntent(response: NextResponse) {
  response.cookies.set(GITHUB_USER_AUTHORIZATION_INTENT_COOKIE, "", {
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
  location.searchParams.set("provider", "github");
  location.searchParams.set("step", "provider");
  location.searchParams.set("notice", notice);
  return location;
}

async function exchangeAuthorizationCode(request: NextRequest, code: string) {
  const configuration = getGitHubUserOAuthConfiguration();
  if (!configuration) return undefined;
  const callback = new URL("/api/setup/github/authorize/complete", applicationOrigin(request));
  const response = await fetch(configuration.tokenURL, {
    body: new URLSearchParams({
      client_id: configuration.clientID,
      client_secret: configuration.clientSecret,
      code,
      redirect_uri: callback.toString(),
    }),
    cache: "no-store",
    headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
    method: "POST",
    redirect: "error",
  });
  if (!response.ok) return undefined;
  const payload = (await response.json()) as { access_token?: unknown };
  return typeof payload.access_token === "string" && payload.access_token ? {
    configuration,
    token: payload.access_token,
  } : undefined;
}

type GitHubInstallation = {
  app_id?: unknown;
  id?: unknown;
};

function validGitHubNumericID(value: unknown) {
  return /^[1-9][0-9]{0,18}$/.test(String(value));
}

async function authorizedInstallationID(
  configuration: NonNullable<ReturnType<typeof getGitHubUserOAuthConfiguration>>,
  token: string,
  intent: ReturnType<typeof decodeGitHubUserAuthorizationIntent>,
) {
  if (!intent) return undefined;
  const endpoint = new URL(configuration.installationsURL);
  endpoint.searchParams.set("per_page", "100");
  const response = await fetch(endpoint, {
    cache: "no-store",
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "X-GitHub-Api-Version": "2022-11-28",
    },
    redirect: "error",
  });
  if (!response.ok) return undefined;
  const payload = (await response.json()) as { installations?: unknown };
  if (!Array.isArray(payload.installations)) return undefined;
  const installations = payload.installations.filter(
    (installation): installation is GitHubInstallation =>
      Boolean(installation && typeof installation === "object" &&
        /^[1-9][0-9]{0,18}$/.test(String((installation as GitHubInstallation).id))),
  );
  if (intent.source === "setup_callback") {
    return installations.some((installation) => String(installation.id) === intent.installationID)
      ? intent.installationID
      : undefined;
  }
  if (!configuration.appID || !/^[1-9][0-9]{0,18}$/.test(configuration.appID)) {
    return undefined;
  }
  const matchingInstallations = installations.filter(
    (installation) => String(installation.app_id) === configuration.appID,
  );
  // A user with multiple App installations needs to select the desired
  // organization in GitHub first. Guessing would bind the wrong workspace.
  return matchingInstallations.length === 1
    ? String(matchingInstallations[0].id)
    : undefined;
}

async function authorizedGitHubActorID(
  configuration: NonNullable<ReturnType<typeof getGitHubUserOAuthConfiguration>>,
  token: string,
) {
  const response = await fetch(configuration.userURL, {
    cache: "no-store",
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "X-GitHub-Api-Version": "2022-11-28",
    },
    redirect: "error",
  });
  if (!response.ok) return undefined;
  const payload = (await response.json()) as { id?: unknown };
  return validGitHubNumericID(payload.id) ? String(payload.id) : undefined;
}

async function bindAuthorizedGitHubActor(tenant: string, actorID: string) {
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(tenant)}/provider-identities/github/${encodeURIComponent(actorID)}/self`,
    { method: "PUT" },
  );
  return Boolean(upstream?.ok);
}

export async function GET(request: NextRequest) {
  if (!(await getConsoleSession())) {
    return NextResponse.redirect(signInLocation(request));
  }
  const intent = decodeGitHubUserAuthorizationIntent(
    request.cookies.get(GITHUB_USER_AUTHORIZATION_INTENT_COOKIE)?.value,
  );
  const code = request.nextUrl.searchParams.get("code");
  const state = request.nextUrl.searchParams.get("state");
  if (!intent || !sameGitHubUserAuthorizationState(state, intent.state) || !code) {
    return clearIntent(NextResponse.redirect(new URL("/workspaces", applicationOrigin(request))));
  }
  const directory = await getWorkspaceDirectory();
  if (!directory.workspaces.some(
    (workspace) => workspace.slug === intent.tenant && (workspace.role === "owner" || workspace.role === "admin"),
  )) {
    return clearIntent(NextResponse.redirect(
      setupLocation(request, intent.tenant, intent.next, "github_user_authorization_workspace_unavailable"),
    ));
  }
  try {
    const exchange = await exchangeAuthorizationCode(request, code);
    const installationID = exchange && await authorizedInstallationID(
      exchange.configuration,
      exchange.token,
      intent,
    );
    const actorID = exchange && installationID && await authorizedGitHubActorID(
      exchange.configuration,
      exchange.token,
    );
    const identityBound = actorID && await bindAuthorizedGitHubActor(intent.tenant, actorID);
    const authorization = installationID && identityBound && newProviderAuthorization(
      "github",
      intent.tenant,
      installationID,
      intent.next,
      undefined,
      actorID,
    );
    const encodedAuthorization = authorization && encodeProviderAuthorization(authorization);
    if (!encodedAuthorization) {
      return clearIntent(NextResponse.redirect(
        setupLocation(
          request,
          intent.tenant,
          intent.next,
          intent.source === "existing"
            ? "github_existing_installation_identity_unavailable"
            : "github_user_authorization_not_authorized",
        ),
      ));
    }
    const location = new URL("/setup", applicationOrigin(request));
    location.searchParams.set("tenant", intent.tenant);
    location.searchParams.set("next", intent.next);
    location.searchParams.set("provider", "github");
    location.searchParams.set("step", "install");
    location.searchParams.set(
      "notice",
      intent.source === "existing"
        ? "github_existing_installation_returned"
        : "github_installation_returned",
    );
    const response = clearIntent(NextResponse.redirect(location));
    response.cookies.set(PROVIDER_AUTHORIZATION_COOKIE, encodedAuthorization, authCookieOptions(30 * 60));
    return response;
  } catch {
    return clearIntent(NextResponse.redirect(
      setupLocation(request, intent.tenant, intent.next, "github_user_authorization_failed"),
    ));
  }
}
