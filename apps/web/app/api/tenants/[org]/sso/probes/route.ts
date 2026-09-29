import { NextRequest } from "next/server";

import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export async function POST(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  const body = await request.text();
  return (await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/sso/probes`, { method: "POST", body, headers: { "Content-Type": "application/json" }, signal: request.signal })) ?? unavailableControlPlaneResponse();
}
