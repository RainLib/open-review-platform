import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(request: Request, context: { params: Promise<{ org: string; exceptionId: string }> }) {
  const { org, exceptionId } = await context.params;
  const upstream = await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/rule-exceptions/${encodeURIComponent(exceptionId)}/revoke`, { method: "POST", signal: request.signal });
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), { status: upstream.status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store" } });
}
