import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

async function forward(request: Request, context: { params: Promise<{ org: string }> }, method: "GET" | "PUT") {
  const { org } = await context.params;
  const query = new URL(request.url).searchParams.toString();
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/agent-task-policies${query ? `?${query}` : ""}`,
    { method, body: method === "PUT" ? await request.text() : undefined, headers: method === "PUT" ? { "Content-Type": "application/json" } : undefined, signal: request.signal },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), { status: upstream.status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store" } });
}

export const GET = (request: Request, context: { params: Promise<{ org: string }> }) => forward(request, context, "GET");
export const PUT = (request: Request, context: { params: Promise<{ org: string }> }) => forward(request, context, "PUT");
