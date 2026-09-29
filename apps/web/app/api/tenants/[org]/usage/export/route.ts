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
  const periodStart = new URL(request.url).searchParams.get("period_start") ?? "";
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/usage/export?period_start=${encodeURIComponent(periodStart)}`,
    { signal: request.signal },
  );
  return upstream ?? unavailableControlPlaneResponse();
}
