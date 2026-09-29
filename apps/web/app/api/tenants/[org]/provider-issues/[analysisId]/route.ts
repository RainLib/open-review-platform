import { NextRequest } from "next/server";

import { forwardControlPlaneRequest } from "@/lib/control-plane-proxy";

export async function GET(
  request: NextRequest,
  context: { params: Promise<{ org: string; analysisId: string }> },
) {
  const { org, analysisId } = await context.params;
  return forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/provider-issues/${encodeURIComponent(analysisId)}`,
    { signal: request.signal },
  );
}
