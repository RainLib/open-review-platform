import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

// Keep configuration history behind the same authenticated BFF boundary as the
// editor. The control plane intentionally returns provenance only, never the
// previous configuration content or any credential reference.
export async function GET(
  request: Request,
  context: { params: Promise<{ org: string; section: string }> },
) {
  const { org, section } = await context.params;
  const query = new URL(request.url).searchParams.toString();
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/review-config/${encodeURIComponent(section)}/history${query ? `?${query}` : ""}`,
    { method: "GET", signal: request.signal },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
  });
}
