import { NextRequest } from "next/server";

import { forwardControlPlaneRequest } from "@/lib/control-plane-proxy";

export async function POST(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  return forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/issues/auto-create-policy/preview`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: await request.text(),
    signal: request.signal,
  });
}
