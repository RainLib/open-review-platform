import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const installationIDPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

type RepositoryScopeRequest = {
  repository_scope?: unknown;
  expected_repository_scope?: unknown;
};

export async function GET(
  request: Request,
  context: { params: Promise<{ org: string; installationId: string }> },
) {
  const { org, installationId } = await context.params;
  if (!installationIDPattern.test(installationId)) {
    return Response.json({ error: "Installation id is invalid." }, { status: 400 });
  }
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationId)}`,
    { method: "GET", signal: request.signal },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: {
      "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
    },
  });
}

export async function PATCH(
  request: Request,
  context: { params: Promise<{ org: string; installationId: string }> },
) {
  const { org, installationId } = await context.params;
  if (!installationIDPattern.test(installationId)) {
    return Response.json(
      { error: "Installation id is invalid." },
      { status: 400 },
    );
  }
  let input: RepositoryScopeRequest;
  try {
    input = (await request.json()) as RepositoryScopeRequest;
  } catch {
    return Response.json(
      { error: "Repository scope update is required." },
      { status: 400 },
    );
  }
  const repositoryScope =
    typeof input.repository_scope === "string" ? input.repository_scope.trim() : "";
  const expectedRepositoryScope =
    typeof input.expected_repository_scope === "string"
      ? input.expected_repository_scope.trim()
      : "";
  if (!repositoryScope || !expectedRepositoryScope) {
    return Response.json(
      { error: "Repository scope and its expected current value are required." },
      { status: 400 },
    );
  }
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationId)}`,
    {
      method: "PATCH",
      body: JSON.stringify({
        repository_scope: repositoryScope,
        expected_repository_scope: expectedRepositoryScope,
      }),
      headers: { "Content-Type": "application/json" },
      signal: request.signal,
    },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: {
      "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
    },
  });
}

export async function DELETE(
  request: Request,
  context: { params: Promise<{ org: string; installationId: string }> },
) {
  const { org, installationId } = await context.params;
  if (!installationIDPattern.test(installationId)) {
    return Response.json(
      { error: "Installation id is invalid." },
      { status: 400 },
    );
  }
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationId)}`,
    { method: "DELETE", signal: request.signal },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: {
      "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
    },
  });
}
