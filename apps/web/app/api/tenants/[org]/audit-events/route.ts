import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function GET(
  request: Request,
  context: { params: Promise<{ org: string }> },
) {
  const { org } = await context.params;
  const input = new URL(request.url).searchParams;
  const query = new URLSearchParams();
  for (const key of ["actor", "action", "target", "from", "until", "limit", "before"]) {
    const value = input.get(key);
    if (value) query.set(key, value);
  }
  const suffix = query.size > 0 ? `?${query.toString()}` : "";
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/audit-events${suffix}`,
    { signal: request.signal },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
  });
}
