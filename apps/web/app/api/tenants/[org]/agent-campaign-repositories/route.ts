import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";
export const dynamic = "force-dynamic";
export const runtime = "nodejs";
export async function GET(request: Request, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  const upstream = await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/agent-campaign-repositories?${new URL(request.url).searchParams.toString()}`, { signal: request.signal });
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(upstream.body, { status: upstream.status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store" } });
}
