import {
  forwardDevelopmentRequest,
  unavailableDevelopmentResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

type InstallationRequest = {
  provider?: unknown;
  external_id?: unknown;
  repository_scope?: unknown;
  api_base_url?: unknown;
  credential_ref?: unknown;
};

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

  const provider = typeof input.provider === "string" ? input.provider : "";
  const externalID =
    typeof input.external_id === "string" ? input.external_id.trim() : "";
  const repositoryScope =
    typeof input.repository_scope === "string"
      ? input.repository_scope.trim()
      : "";
  const apiBaseURL =
    typeof input.api_base_url === "string" ? input.api_base_url.trim() : "";
  const credentialRef =
    typeof input.credential_ref === "string"
      ? input.credential_ref.trim()
      : "";
  const expectedCredentialRef =
    provider === "github"
      ? "github-app"
      : provider === "gitlab"
        ? "gitlab-token"
        : "";

  if (
    !expectedCredentialRef ||
    !externalID ||
    !repositoryScope ||
    !apiBaseURL ||
    credentialRef !== expectedCredentialRef
  ) {
    return Response.json(
      {
        error:
          "Use a configured GitHub App or GitLab deployment credential; never send a provider secret from the browser.",
      },
      { status: 400 },
    );
  }

  const upstream = await forwardDevelopmentRequest(
    `/v1/tenants/${encodeURIComponent(org)}/installations`,
    {
      method: "POST",
      body: JSON.stringify({
        provider,
        external_id: externalID,
        repository_scope: repositoryScope,
        api_base_url: apiBaseURL,
        credential_ref: credentialRef,
      }),
      headers: { "Content-Type": "application/json" },
      signal: request.signal,
    },
  );
  if (!upstream) return unavailableDevelopmentResponse();

  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: {
      "Content-Type":
        upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
    },
  });
}
