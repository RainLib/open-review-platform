import { NextRequest } from "next/server";

import { forwardControlPlaneRequest } from "@/lib/control-plane-proxy";

export async function GET(
  request: NextRequest,
  context: { params: Promise<{ org: string; runId: string }> },
) {
  const { org, runId } = await context.params;
  return forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runId)}/evidence`,
    { signal: request.signal },
  );
}
