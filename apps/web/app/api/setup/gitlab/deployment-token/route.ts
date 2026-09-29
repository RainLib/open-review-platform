import { NextRequest, NextResponse } from "next/server";

import { applicationOrigin } from "@/lib/auth/oidc";
import {
  authCookieOptions,
  getConsoleSession,
  safeInternalPath,
} from "@/lib/auth/session";
import {
  encodeProviderAuthorization,
  newProviderAuthorization,
  PROVIDER_AUTHORIZATION_COOKIE,
  validWorkspaceSlug,
} from "@/lib/auth/provider-authorization";
import { getProviderProfiles, getWorkspaceDirectory } from "@/lib/control-api";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function setupLocation(
  request: NextRequest,
  tenant: string,
  next: string,
  notice: string,
  step: "provider" | "install" = "provider",
) {
  const location = new URL("/setup", applicationOrigin(request));
  if (tenant) location.searchParams.set("tenant", tenant);
  location.searchParams.set("next", safeInternalPath(next));
  location.searchParams.set("provider", "gitlab");
  location.searchParams.set("step", step);
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

// This route never receives a GitLab token. It only turns an authenticated
// administrator's selection of a deployment-owned credential slot into the
// same short-lived signed receipt used by provider OAuth callbacks.
export async function GET(request: NextRequest) {
  if (!(await getConsoleSession())) {
    return NextResponse.redirect(signInLocation(request));
  }
  const tenant = request.nextUrl.searchParams.get("tenant") ?? "";
  const requestedNext = request.nextUrl.searchParams.get("next") ?? "";
  if (!validWorkspaceSlug(tenant)) {
    return NextResponse.redirect(
      setupLocation(request, tenant, requestedNext, "gitlab_authorization_needs_workspace"),
    );
  }
  const directory = await getWorkspaceDirectory();
  const canManageWorkspace = directory.workspaces.some(
    (workspace) =>
      workspace.slug === tenant &&
      (workspace.role === "owner" || workspace.role === "admin"),
  );
  if (!canManageWorkspace) {
    return NextResponse.redirect(
      setupLocation(request, tenant, requestedNext, "gitlab_authorization_workspace_unavailable"),
    );
  }
  const profiles = await getProviderProfiles(tenant);
  const deploymentTokenAvailable =
    profiles.source === "live" &&
    profiles.profiles.some(
      (profile) =>
        profile.provider === "gitlab" && profile.deployment_token_available,
    );
  if (!deploymentTokenAvailable) {
    return NextResponse.redirect(
      setupLocation(request, tenant, requestedNext, "gitlab_deployment_token_unavailable"),
    );
  }
  // The durable GitLab installation identity is derived only after the user
  // selects a scope in the installation BFF. This opaque value solely binds
  // the signed receipt to this handoff and exposes no provider identity.
  const authorization = newProviderAuthorization(
    "gitlab",
    tenant,
    "gitlab-token:deployment",
    requestedNext,
  );
  const encodedAuthorization = authorization && encodeProviderAuthorization(authorization);
  if (!encodedAuthorization) {
    return NextResponse.redirect(
      setupLocation(request, tenant, requestedNext, "gitlab_deployment_token_unavailable"),
    );
  }
  const response = NextResponse.redirect(
    setupLocation(
      request,
      tenant,
      requestedNext,
      "gitlab_deployment_token_ready",
      "install",
    ),
  );
  response.cookies.set(
    PROVIDER_AUTHORIZATION_COOKIE,
    encodedAuthorization,
    authCookieOptions(30 * 60),
  );
  return response;
}
