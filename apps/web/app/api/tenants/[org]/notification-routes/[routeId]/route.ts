import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function PATCH(
  request: Request,
  context: { params: Promise<{ org: string; routeId: string }> },
) {
  const { org, routeId } = await context.params;
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/notification-routes/${encodeURIComponent(routeId)}`,
    {
      method: "PATCH",
      body: await request.text(),
      headers: { "Content-Type": "application/json" },
      signal: request.signal,
    },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
  });
}
