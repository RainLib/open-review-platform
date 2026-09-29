import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";
import { authCookieOptions } from "@/lib/auth/session";
import {
  decodeProviderAuthorization,
  PROVIDER_AUTHORIZATION_COOKIE,
  PROVIDER_AUTHORIZATION_HEADER,
} from "@/lib/auth/provider-authorization";
import { gitLabInstallationExternalID } from "@/lib/auth/gitlab-oauth";
import { cookies } from "next/headers";
import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

type InstallationRequest = {
  provider?: unknown;
  repository_scope?: unknown;
  automatic_reviews?: unknown;
  author_scope?: unknown;
  minimum_severity?: unknown;
};

const severityValues = new Set(["low", "medium", "high", "critical"]);

export async function POST(
  request: Request,
  context: { params: Promise<{ org: string }> },
) {
  const { org } = await context.params;
  let input: InstallationRequest;

  try {
    input = (await request.json()) as InstallationRequest;
  } catch {
    return Response.json(
      { error: "A provider installation payload is required." },
      { status: 400 },
    );
  }

  const provider =
    typeof input.provider === "string" ? input.provider.trim().toLowerCase() : "";
  const repositoryScope =
    typeof input.repository_scope === "string"
      ? input.repository_scope.trim()
      : "";
  const automaticReviews =
    typeof input.automatic_reviews === "boolean"
      ? input.automatic_reviews
      : true;
  const authorScope = input.author_scope === "mine" ? "mine" :
    input.author_scope === undefined || input.author_scope === "all" ? "all" : "";
  const minimumSeverity =
    typeof input.minimum_severity === "string"
      ? input.minimum_severity.trim().toLowerCase()
      : "medium";
  const cookieStore = await cookies();
  const authorizationReceipt = cookieStore.get(
    PROVIDER_AUTHORIZATION_COOKIE,
  )?.value;
  const authorization = decodeProviderAuthorization(authorizationReceipt);
  const expectedCredentialRef = provider === "github"
    ? "github-app"
    : provider === "gitlab"
      ? authorization?.credentialRef ?? "gitlab-token"
      : "";

  if (
    !expectedCredentialRef ||
    !authorScope ||
    (authorScope === "mine" && !authorization?.actorExternalID) ||
    !repositoryScope ||
    !severityValues.has(minimumSeverity) ||
    !authorization ||
    authorization.tenant !== org ||
    authorization.provider !== provider
  ) {
    return Response.json(
      {
        error:
          "Return through the signed GitHub or GitLab authorization flow before saving this connection. Provider identifiers and secrets are never accepted from the browser.",
      },
      { status: 400 },
    );
  }

  // A GitLab OAuth callback proves authorization but intentionally does not
  // carry a durable provider/user identity. Derive the installation identity
  // from the tenant and the now-confirmed scope in this server-side BFF. That
  // makes the same scope safe to reconnect and permits multiple non-overlapping
  // scopes on one configured GitLab host.
  const externalID = provider === "gitlab"
    ? gitLabInstallationExternalID(org, repositoryScope)
    : authorization.externalID;
  if (!externalID) {
    return Response.json(
      { error: "The GitLab repository scope could not be bound to this authorized workspace." },
      { status: 400 },
    );
  }

  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/installations`,
    {
      method: "POST",
      body: JSON.stringify({
        provider,
        external_id: externalID,
        repository_scope: repositoryScope,
        automatic_reviews: automaticReviews,
        author_scope: authorScope,
        author_external_id: authorScope === "mine" ? authorization.actorExternalID : "",
        minimum_severity: minimumSeverity,
        credential_ref: expectedCredentialRef,
      }),
      headers: {
        "Content-Type": "application/json",
        [PROVIDER_AUTHORIZATION_HEADER]: authorizationReceipt ?? "",
      },
      signal: request.signal,
    },
  );
  if (!upstream) return unavailableControlPlaneResponse();

  const response = new NextResponse(await upstream.text(), {
    status: upstream.status,
    headers: {
      "Content-Type":
        upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
    },
  });
  if (upstream.ok || upstream.status === 409) {
    response.cookies.set(PROVIDER_AUTHORIZATION_COOKIE, "", {
      ...authCookieOptions(),
      expires: new Date(0),
    });
  }
  return response;
}
