import { NextRequest } from "next/server";

import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export async function GET(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  return (await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/platform-health`, { signal: request.signal })) ?? unavailableControlPlaneResponse();
}
