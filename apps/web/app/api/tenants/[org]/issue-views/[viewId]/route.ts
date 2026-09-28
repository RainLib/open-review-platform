import { NextRequest } from "next/server";
import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";
type Context = { params: Promise<{ org: string; viewId: string }> };

export async function PUT(request: NextRequest, context: Context) {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") return Response.json({ error: "Preview saved views are read-only." }, { status: 409 });
  const { org, viewId } = await context.params;
  const upstream = await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/issue-views/${encodeURIComponent(viewId)}`, { method: "PUT", body: await request.text(), headers: { "Content-Type": "application/json" }, signal: request.signal });
  return upstream ?? unavailableControlPlaneResponse();
}

export async function DELETE(request: NextRequest, context: Context) {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") return Response.json({ error: "Preview saved views are read-only." }, { status: 409 });
  const { org, viewId } = await context.params;
  const revision = new URLSearchParams({ revision: request.nextUrl.searchParams.get("revision") ?? "" });
  const upstream = await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/issue-views/${encodeURIComponent(viewId)}?${revision}`, { method: "DELETE", signal: request.signal });
  return upstream ?? unavailableControlPlaneResponse();
}
