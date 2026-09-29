import { NextRequest } from "next/server";

import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  const query = request.nextUrl.searchParams.toString();
  const upstream = await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/issues${query ? `?${query}` : ""}`, {
    signal: request.signal,
  });
  return upstream ?? unavailableControlPlaneResponse();
}
