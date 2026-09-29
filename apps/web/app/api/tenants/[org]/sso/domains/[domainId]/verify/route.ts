import { NextRequest } from "next/server";

import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export async function POST(request: NextRequest, context: { params: Promise<{ org: string; domainId: string }> }) {
  const { org, domainId } = await context.params;
  return (await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/sso/domains/${encodeURIComponent(domainId)}/verify`, { method: "POST", signal: request.signal })) ?? unavailableControlPlaneResponse();
}
