import { NextRequest, NextResponse } from "next/server";

import {
  GITHUB_INSTALL_INTENT_COOKIE,
  encodeGitHubInstallIntent,
  gitHubAppInstallURL,
  newGitHubInstallIntent,
  validWorkspaceSlug,
} from "@/lib/auth/github-install";
import { applicationOrigin } from "@/lib/auth/oidc";
import {
  authCookieOptions,
  getConsoleSession,
  safeInternalPath,
} from "@/lib/auth/session";
import { getWorkspaceDirectory } from "@/lib/control-api";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

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
    const location = new URL("/workspaces", applicationOrigin(request));
    location.searchParams.set("notice", "github_installation_needs_workspace");
    return NextResponse.redirect(location);
  }

  // The signed intent is the authority for the callback. Establishing it for
  // an arbitrary slug would let an authenticated user initiate an App install
  // against a workspace they cannot administer. The control plane scopes this
  // directory by membership, so fail closed when that proof is unavailable.
  const directory = await getWorkspaceDirectory();
  const canManageWorkspace = directory.workspaces.some(
    (workspace) =>
      workspace.slug === tenant &&
      (workspace.role === "owner" || workspace.role === "admin"),
  );
  if (!canManageWorkspace) {
    const location = new URL("/setup", applicationOrigin(request));
    location.searchParams.set("tenant", tenant);
    location.searchParams.set("provider", "github");
    location.searchParams.set("step", "provider");
    location.searchParams.set(
      "notice",
      "github_installation_workspace_unavailable",
    );
    return NextResponse.redirect(location);
  }

  const intent = newGitHubInstallIntent(
    tenant,
    request.nextUrl.searchParams.get("next"),
  );
  const installationURL = gitHubAppInstallURL();
  const encodedIntent = intent && encodeGitHubInstallIntent(intent);
  if (!intent || !installationURL || !encodedIntent) {
    const location = new URL("/setup", applicationOrigin(request));
    if (tenant) location.searchParams.set("tenant", tenant);
    location.searchParams.set("provider", "github");
    location.searchParams.set("step", "provider");
    location.searchParams.set(
      "notice",
      "github_handoff_unavailable",
    );
    return NextResponse.redirect(location);
  }

  installationURL.searchParams.set("state", intent.state);
  const response = NextResponse.redirect(installationURL);
  response.cookies.set(
    GITHUB_INSTALL_INTENT_COOKIE,
    encodedIntent,
    authCookieOptions(10 * 60),
  );
  return response;
}
