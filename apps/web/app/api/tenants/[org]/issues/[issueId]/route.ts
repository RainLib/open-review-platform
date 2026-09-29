import { NextRequest } from "next/server";

import { forwardControlPlaneRequest } from "@/lib/control-plane-proxy";

export async function GET(request: NextRequest, context: { params: Promise<{ org: string; issueId: string }> }) {
  const { org, issueId } = await context.params;
  return forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/issues/${encodeURIComponent(issueId)}`,
    { signal: request.signal },
  );
}

export async function PATCH(request: NextRequest, context: { params: Promise<{ org: string; issueId: string }> }) {
  const { org, issueId } = await context.params;
  return forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/issues/${encodeURIComponent(issueId)}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: await request.text(),
      signal: request.signal,
    },
  );
}
