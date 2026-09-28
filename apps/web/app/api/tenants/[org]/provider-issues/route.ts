import { NextRequest } from "next/server";

import { forwardControlPlaneRequest } from "@/lib/control-plane-proxy";

export async function GET(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  const query = request.nextUrl.searchParams.toString();
  return forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/provider-issues${query ? `?${query}` : ""}`,
    { signal: request.signal },
  );
}
