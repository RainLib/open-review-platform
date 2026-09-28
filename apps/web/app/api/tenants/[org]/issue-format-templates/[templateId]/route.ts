import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

async function forward(
  request: Request,
  context: { params: Promise<{ org: string; templateId: string }> },
  method: "PUT" | "DELETE",
) {
  const { org, templateId } = await context.params;
  const query = new URL(request.url).searchParams.toString();
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/issue-format-templates/${encodeURIComponent(templateId)}${query ? `?${query}` : ""}`,
    {
      method,
      body: method === "PUT" ? await request.text() : undefined,
      headers: method === "PUT" ? { "Content-Type": "application/json" } : undefined,
      signal: request.signal,
    },
  );
  if (!upstream) return unavailableControlPlaneResponse();
  if (upstream.status === 204) {
    return new Response(null, { status: 204, headers: { "Cache-Control": "no-store" } });
  }
  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
  });
}

export async function PUT(request: Request, context: { params: Promise<{ org: string; templateId: string }> }) {
  return forward(request, context, "PUT");
}

export async function DELETE(request: Request, context: { params: Promise<{ org: string; templateId: string }> }) {
  return forward(request, context, "DELETE");
}
