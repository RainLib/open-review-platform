import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

async function forward(
  request: Request,
  context: { params: Promise<{ org: string }> },
  method: "GET" | "POST",
) {
  const { org } = await context.params;
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/issue-format-templates`,
    {
      method,
      body: method === "POST" ? await request.text() : undefined,
      headers: method === "POST" ? { "Content-Type": "application/json" } : undefined,
      signal: request.signal,
    },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
  });
}

export async function GET(request: Request, context: { params: Promise<{ org: string }> }) {
  return forward(request, context, "GET");
}

export async function POST(request: Request, context: { params: Promise<{ org: string }> }) {
  return forward(request, context, "POST");
}
