import { NextRequest } from "next/server";

import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export async function PUT(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  const body = await request.text();
  return (await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/sso/configuration`, { method: "PUT", body, headers: { "Content-Type": "application/json" }, signal: request.signal })) ?? unavailableControlPlaneResponse();
}
