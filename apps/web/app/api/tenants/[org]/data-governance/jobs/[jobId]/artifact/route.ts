import { NextRequest } from "next/server";

import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export async function GET(request: NextRequest, context: { params: Promise<{ org: string; jobId: string }> }) {
  const { org, jobId } = await context.params;
  return (await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/data-governance/jobs/${encodeURIComponent(jobId)}/artifact`,
    { signal: request.signal },
  )) ?? unavailableControlPlaneResponse();
}
