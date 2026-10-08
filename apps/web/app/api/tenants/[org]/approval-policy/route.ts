import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

async function forward(request: Request, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  const upstream = await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/approval-policy`, {
    method: request.method,
    ...(request.method === "PUT" ? { body: await request.text(), headers: { "Content-Type": "application/json" } } : {}),
    signal: request.signal,
  });
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), { status: upstream.status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store" } });
}

export const GET = forward;
export const PUT = forward;
