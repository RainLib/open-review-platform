import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

async function forward(
  request: Request,
  context: { params: Promise<{ org: string; section: string }> },
  method: "GET" | "PUT" | "DELETE",
) {
  const { org, section } = await context.params;
  const query = new URL(request.url).searchParams.toString();
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/review-config/${encodeURIComponent(section)}${query ? `?${query}` : ""}`,
    {
      method,
      body: method === "PUT" ? await request.text() : undefined,
      headers: method === "PUT" ? { "Content-Type": "application/json" } : undefined,
      signal: request.signal,
    },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
  });
}

export async function GET(request: Request, context: { params: Promise<{ org: string; section: string }> }) {
  return forward(request, context, "GET");
}

export async function PUT(request: Request, context: { params: Promise<{ org: string; section: string }> }) {
  return forward(request, context, "PUT");
}

export async function DELETE(request: Request, context: { params: Promise<{ org: string; section: string }> }) {
  return forward(request, context, "DELETE");
}
