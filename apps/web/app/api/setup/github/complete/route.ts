import { NextRequest, NextResponse } from "next/server";

import {
  decodeGitHubInstallIntent,
  GITHUB_INSTALL_INTENT_COOKIE,
  sameInstallState,
  validGitHubInstallationID,
} from "@/lib/auth/github-install";
import { applicationOrigin } from "@/lib/auth/oidc";
import {
  authCookieOptions,
  getConsoleSession,
} from "@/lib/auth/session";
import {
  encodeGitHubUserAuthorizationIntent,
  getGitHubUserOAuthConfiguration,
  GITHUB_USER_AUTHORIZATION_INTENT_COOKIE,
  newGitHubUserAuthorizationIntent,
} from "@/lib/auth/github-user-oauth";
import { getWorkspaceDirectory } from "@/lib/control-api";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function clearIntent(response: NextResponse) {
  response.cookies.set(GITHUB_INSTALL_INTENT_COOKIE, "", {
    ...authCookieOptions(),
    expires: new Date(0),
  });
  return response;
}

function signInLocation(request: NextRequest) {
  const location = new URL("/sign-in", applicationOrigin(request));
  const next = new URL(request.nextUrl.pathname, applicationOrigin(request));
  const installationID = request.nextUrl.searchParams.get("installation_id");
  const state = request.nextUrl.searchParams.get("state");
  if (installationID) next.searchParams.set("installation_id", installationID);
  if (state) next.searchParams.set("state", state);
  location.searchParams.set("next", `${next.pathname}${next.search}`);
  return location;
}

export async function GET(request: NextRequest) {
  if (!(await getConsoleSession())) {
    return NextResponse.redirect(signInLocation(request));
  }

  const installationID = request.nextUrl.searchParams.get("installation_id");
  const state = request.nextUrl.searchParams.get("state");
  const intent = decodeGitHubInstallIntent(
    request.cookies.get(GITHUB_INSTALL_INTENT_COOKIE)?.value,
  );

  if (
    intent &&
    validGitHubInstallationID(installationID) &&
    sameInstallState(state, intent.state)
  ) {
    // An administrator may lose access while GitHub is showing its App
    // installation UI. Re-check the tenant boundary before minting a receipt;
    // the signed state proves continuity, not ongoing authorization.
    const directory = await getWorkspaceDirectory();
    const canManageWorkspace = directory.workspaces.some(
      (workspace) =>
        workspace.slug === intent.tenant &&
        (workspace.role === "owner" || workspace.role === "admin"),
    );
    if (!canManageWorkspace) {
      const location = new URL("/setup", applicationOrigin(request));
      location.searchParams.set("tenant", intent.tenant);
      location.searchParams.set("provider", "github");
      location.searchParams.set("step", "provider");
      location.searchParams.set(
        "notice",
        "github_installation_workspace_unavailable",
      );
      return clearIntent(NextResponse.redirect(location));
    }
    // GitHub only guarantees an installation_id at the setup URL. Do not
    // attach that identifier based solely on a browser cookie: GitHub warns
    // it may be spoofed. Prove that the currently authenticated GitHub user
    // can access this installation before minting the control-plane receipt.
    const configuration = getGitHubUserOAuthConfiguration();
    const authorizationIntent = configuration && newGitHubUserAuthorizationIntent(
      intent.tenant,
      installationID!,
      intent.next,
    );
    const encodedAuthorizationIntent = authorizationIntent &&
      encodeGitHubUserAuthorizationIntent(authorizationIntent);
    if (!configuration || !authorizationIntent || !encodedAuthorizationIntent) {
      const location = new URL("/setup", applicationOrigin(request));
      location.searchParams.set("tenant", intent.tenant);
      location.searchParams.set("provider", "github");
      location.searchParams.set("step", "provider");
      location.searchParams.set("notice", "github_user_authorization_unavailable");
      return clearIntent(NextResponse.redirect(location));
    }
    const callback = new URL("/api/setup/github/authorize/complete", applicationOrigin(request));
    const location = new URL(configuration.authorizeURL);
    location.searchParams.set("client_id", configuration.clientID);
    location.searchParams.set("redirect_uri", callback.toString());
    location.searchParams.set("state", authorizationIntent.state);
    const response = clearIntent(NextResponse.redirect(location));
    response.cookies.set(
      GITHUB_USER_AUTHORIZATION_INTENT_COOKIE,
      encodedAuthorizationIntent,
      authCookieOptions(10 * 60),
    );
    return response;
  }

  const location = new URL("/workspaces", applicationOrigin(request));
  location.searchParams.set("notice", "github_installation_needs_workspace");
  return clearIntent(NextResponse.redirect(location));
}
