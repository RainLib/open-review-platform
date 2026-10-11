import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";
type Context = { params: Promise<{ org: string; parts?: string[] }> };
async function forward(request: Request, context: Context, method: "GET" | "POST") {
  const { org, parts = [] } = await context.params;
  if (parts.length > 2 || (parts.length === 2 && !["approve", "actions", "report"].includes(parts[1]))) return Response.json({ error: "Unknown campaign operation" }, { status: 404 });
  const query = new URL(request.url).searchParams.toString();
  const upstream = await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/agent-campaigns${parts.map(part => `/${encodeURIComponent(part)}`).join("")}${query ? `?${query}` : ""}`, { method, body: method === "POST" ? await request.text() : undefined, headers: method === "POST" ? { "Content-Type": "application/json" } : undefined, signal: request.signal });
  if (!upstream) return unavailableControlPlaneResponse();
  const headers = new Headers({ "Content-Type": upstream.headers.get("Content-Type") ?? "application/json", "Cache-Control": "no-store" });
  const disposition = upstream.headers.get("Content-Disposition");
  if (disposition) headers.set("Content-Disposition", disposition);
  return new Response(upstream.body, { status: upstream.status, headers });
}
export const GET = (request: Request, context: Context) => forward(request, context, "GET");
export const POST = (request: Request, context: Context) => forward(request, context, "POST");
