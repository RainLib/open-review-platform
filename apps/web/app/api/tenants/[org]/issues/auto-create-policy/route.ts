import { NextRequest } from "next/server";

import { forwardControlPlaneRequest } from "@/lib/control-plane-proxy";

export async function GET(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  return forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/issues/auto-create-policy`, { signal: request.signal });
}

export async function PUT(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  return forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/issues/auto-create-policy`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: await request.text(),
    signal: request.signal,
  });
}
