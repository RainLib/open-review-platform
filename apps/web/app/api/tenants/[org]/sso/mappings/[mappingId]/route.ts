import { NextRequest } from "next/server";

import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export async function DELETE(request: NextRequest, context: { params: Promise<{ org: string; mappingId: string }> }) {
  const { org, mappingId } = await context.params;
  const revision = request.nextUrl.searchParams.get("revision") ?? "";
  return (await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/sso/mappings/${encodeURIComponent(mappingId)}?revision=${encodeURIComponent(revision)}`, { method: "DELETE", signal: request.signal })) ?? unavailableControlPlaneResponse();
}
