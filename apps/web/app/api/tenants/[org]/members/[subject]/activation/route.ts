import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function PATCH(
  request: Request,
  context: { params: Promise<{ org: string; subject: string }> },
) {
  const { org, subject } = await context.params;
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/members/${encodeURIComponent(subject)}/activation`,
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
