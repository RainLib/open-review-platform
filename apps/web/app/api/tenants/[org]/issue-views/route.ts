import { NextRequest } from "next/server";
import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function GET(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  const upstream = await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/issue-views`, { signal: request.signal });
  return upstream ?? unavailableControlPlaneResponse();
}

export async function POST(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") return Response.json({ error: "Preview saved views are read-only." }, { status: 409 });
  const { org } = await context.params;
  const upstream = await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/issue-views`, { method: "POST", body: await request.text(), headers: { "Content-Type": "application/json" }, signal: request.signal });
  return upstream ?? unavailableControlPlaneResponse();
}
