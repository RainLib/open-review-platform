import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(request: Request) {
  const upstream = await forwardControlPlaneRequest("/v1/workspace-access-requests", {
    method: "POST",
    body: await request.text(),
    headers: { "Content-Type": "application/json" },
    signal: request.signal,
  });
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
  });
}
