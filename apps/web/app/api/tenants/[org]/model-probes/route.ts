import { NextRequest } from "next/server";

import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function GET(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  const query = new URL(request.url).searchParams.toString();
  return (await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/model-probes${query ? `?${query}` : ""}`, { method: "GET", signal: request.signal })) ?? unavailableControlPlaneResponse();
}

export async function POST(request: NextRequest, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  const body = await request.text();
  return (await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/model-probes`, { method: "POST", body, headers: { "Content-Type": "application/json" }, signal: request.signal })) ?? unavailableControlPlaneResponse();
}
