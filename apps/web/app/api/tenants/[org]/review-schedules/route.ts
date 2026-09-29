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
  const limit = new URL(request.url).searchParams.get("limit") ?? "50";
  return (
    (await forwardControlPlaneRequest(
      `/v1/tenants/${encodeURIComponent(org)}/review-schedules?limit=${encodeURIComponent(limit)}`,
      { signal: request.signal },
    )) ?? unavailableControlPlaneResponse()
  );
}
