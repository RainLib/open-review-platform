import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(
  request: Request,
  context: { params: Promise<{ org: string; deliveryId: string }> },
) {
  const { org, deliveryId } = await context.params;
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/notification-deliveries/${encodeURIComponent(deliveryId)}/retry`,
    { method: "POST", signal: request.signal },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
  });
}
