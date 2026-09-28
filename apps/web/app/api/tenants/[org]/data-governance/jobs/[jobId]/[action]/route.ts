import { NextRequest } from "next/server";

import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

const actions = new Set(["decisions", "cancel", "retry"]);

export async function POST(request: NextRequest, context: { params: Promise<{ org: string; jobId: string; action: string }> }) {
  const { org, jobId, action } = await context.params;
  if (!actions.has(action)) return Response.json({ error: "unsupported data governance action" }, { status: 404 });
  const body = await request.text();
  return (await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/data-governance/jobs/${encodeURIComponent(jobId)}/${action}`, { method: "POST", body, headers: { "Content-Type": "application/json" }, signal: request.signal })) ?? unavailableControlPlaneResponse();
}
