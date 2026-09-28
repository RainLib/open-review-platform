import { NextRequest, NextResponse } from "next/server";

import { validWorkspaceSlug } from "@/lib/auth/github-install";
import {
  encodeGitHubUserAuthorizationIntent,
  getGitHubUserOAuthConfiguration,
  GITHUB_USER_AUTHORIZATION_INTENT_COOKIE,
  githubExistingInstallationHandoffAvailable,
  newExistingGitHubUserAuthorizationIntent,
} from "@/lib/auth/github-user-oauth";
import { applicationOrigin } from "@/lib/auth/oidc";
import {
  authCookieOptions,
  getConsoleSession,
  safeInternalPath,
} from "@/lib/auth/session";
import { getWorkspaceDirectory } from "@/lib/control-api";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function setupLocation(request: NextRequest, tenant: string, next: string, notice: string) {
  const location = new URL("/setup", applicationOrigin(request));
  location.searchParams.set("tenant", tenant);
  location.searchParams.set("next", next);
  location.searchParams.set("provider", "github");
  location.searchParams.set("step", "provider");
  location.searchParams.set("notice", notice);
  return location;
}

function signInLocation(request: NextRequest) {
  const location = new URL("/sign-in", applicationOrigin(request));
  const next = new URL(request.nextUrl.pathname, applicationOrigin(request));
  const tenant = request.nextUrl.searchParams.get("tenant");
  const requestedNext = request.nextUrl.searchParams.get("next");
  if (tenant) next.searchParams.set("tenant", tenant);
  if (requestedNext) next.searchParams.set("next", safeInternalPath(requestedNext));
  location.searchParams.set("next", `${next.pathname}${next.search}`);
  return location;
}

export async function GET(request: NextRequest) {
  if (!(await getConsoleSession())) {
    return NextResponse.redirect(signInLocation(request));
  }
  const tenant = request.nextUrl.searchParams.get("tenant") ?? "";
  const requestedNext = request.nextUrl.searchParams.get("next") ?? "";
  const next = safeInternalPath(requestedNext);
  if (!validWorkspaceSlug(tenant)) {
    return NextResponse.redirect(new URL("/workspaces", applicationOrigin(request)));
  }
  const directory = await getWorkspaceDirectory();
  const canManageWorkspace = directory.workspaces.some(
    (workspace) => workspace.slug === tenant &&
      (workspace.role === "owner" || workspace.role === "admin"),
  );
  if (!canManageWorkspace) {
    return NextResponse.redirect(
      setupLocation(request, tenant, next, "github_existing_installation_workspace_unavailable"),
    );
  }
  const configuration = getGitHubUserOAuthConfiguration();
  const intent = githubExistingInstallationHandoffAvailable() &&
    newExistingGitHubUserAuthorizationIntent(tenant, next);
  const encodedIntent = intent && encodeGitHubUserAuthorizationIntent(intent);
  if (!configuration || !intent || !encodedIntent) {
    return NextResponse.redirect(
      setupLocation(request, tenant, next, "github_existing_installation_unavailable"),
    );
  }
  const callback = new URL(
    "/api/setup/github/authorize/complete",
    applicationOrigin(request),
  );
  const location = new URL(configuration.authorizeURL);
  location.searchParams.set("client_id", configuration.clientID);
  location.searchParams.set("redirect_uri", callback.toString());
  location.searchParams.set("state", intent.state);
  const response = NextResponse.redirect(location);
  response.cookies.set(
    GITHUB_USER_AUTHORIZATION_INTENT_COOKIE,
    encodedIntent,
    authCookieOptions(10 * 60),
  );
  return response;
}
