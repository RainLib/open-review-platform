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
  const query = new URL(request.url).searchParams;
  const limit = query.get("limit") ?? "50";
  const activeOnly = query.get("active_only") ?? "true";
  const upstreamQuery = new URLSearchParams({ limit, active_only: activeOnly });
  const runID = query.get("run_id");
  if (runID) upstreamQuery.set("run_id", runID);
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/review-interventions?${upstreamQuery.toString()}`,
    { method: "GET", signal: request.signal },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: {
      "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
    },
  });
}
