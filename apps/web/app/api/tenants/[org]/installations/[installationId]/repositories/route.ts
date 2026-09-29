import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const installationIDPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export async function GET(
  request: Request,
  context: { params: Promise<{ org: string; installationId: string }> },
) {
  const { org, installationId } = await context.params;
  if (!installationIDPattern.test(installationId)) {
    return Response.json({ error: "Installation id is invalid." }, { status: 400 });
  }
  const query = new URL(request.url).searchParams.get("query")?.trim() ?? "";
  if (query.length > 120) {
    return Response.json({ error: "Repository search is too long." }, { status: 400 });
  }
  const search = new URLSearchParams({ limit: "500" });
  if (query) search.set("query", query);
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationId)}/repositories?${search.toString()}`,
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
