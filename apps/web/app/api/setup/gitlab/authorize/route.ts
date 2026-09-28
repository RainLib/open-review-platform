import { NextRequest, NextResponse } from "next/server";

import {
  encodeGitLabOAuthIntent,
  getGitLabOAuthConfiguration,
  GITLAB_OAUTH_INTENT_COOKIE,
  newGitLabOAuthIntent,
  pkceChallenge,
} from "@/lib/auth/gitlab-oauth";
import { applicationOrigin } from "@/lib/auth/oidc";
import {
  authCookieOptions,
  getConsoleSession,
  safeInternalPath,
} from "@/lib/auth/session";
import { validWorkspaceSlug } from "@/lib/auth/provider-authorization";
import { getWorkspaceDirectory } from "@/lib/control-api";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function setupLocation(
  request: NextRequest,
  tenant: string,
  notice: string,
) {
  const location = new URL("/setup", applicationOrigin(request));
  if (tenant) location.searchParams.set("tenant", tenant);
  location.searchParams.set("provider", "gitlab");
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
  if (!validWorkspaceSlug(tenant)) {
    return NextResponse.redirect(
      setupLocation(request, tenant, "gitlab_authorization_needs_workspace"),
    );
  }
  const directory = await getWorkspaceDirectory();
  if (!directory.workspaces.some(
    (workspace) =>
      workspace.slug === tenant &&
      (workspace.role === "owner" || workspace.role === "admin"),
  )) {
    return NextResponse.redirect(
      setupLocation(request, tenant, "gitlab_authorization_workspace_unavailable"),
    );
  }
  const configuration = getGitLabOAuthConfiguration();
  const intent = newGitLabOAuthIntent(
    tenant,
    request.nextUrl.searchParams.get("next"),
  );
  const encodedIntent = intent && encodeGitLabOAuthIntent(intent);
  if (!configuration || !intent || !encodedIntent) {
    return NextResponse.redirect(
      setupLocation(request, tenant, "gitlab_authorization_unavailable"),
    );
  }

  const redirectURI = new URL("/api/setup/gitlab/complete", applicationOrigin(request));
  const location = new URL(configuration.authorizeURL);
  location.searchParams.set("client_id", configuration.clientID);
  location.searchParams.set("code_challenge", pkceChallenge(intent.codeVerifier));
  location.searchParams.set("code_challenge_method", "S256");
  location.searchParams.set("redirect_uri", redirectURI.toString());
  location.searchParams.set("response_type", "code");
  location.searchParams.set("scope", configuration.scopes);
  location.searchParams.set("state", intent.state);
  const response = NextResponse.redirect(location);
  response.cookies.set(
    GITLAB_OAUTH_INTENT_COOKIE,
    encodedIntent,
    authCookieOptions(10 * 60),
  );
  return response;
}
