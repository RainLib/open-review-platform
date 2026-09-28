import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

type CreateWorkspaceRequest = {
  name?: unknown;
  slug?: unknown;
};

export async function POST(request: Request) {
  let input: CreateWorkspaceRequest;
  try {
    input = (await request.json()) as CreateWorkspaceRequest;
  } catch {
    return Response.json(
      { error: "A workspace name and slug are required." },
      { status: 400 },
    );
  }

  const name = typeof input.name === "string" ? input.name.trim() : "";
  const slug = typeof input.slug === "string" ? input.slug.trim() : "";
  if (!name || !/^[a-z0-9][a-z0-9-]{1,62}$/.test(slug)) {
    return Response.json(
      { error: "Use a workspace name and a lowercase slug from 2 to 63 characters." },
      { status: 400 },
    );
  }

  const upstream = await forwardControlPlaneRequest("/v1/tenants", {
    body: JSON.stringify({ name, slug }),
    headers: { "Content-Type": "application/json" },
    method: "POST",
    signal: request.signal,
  });
  if (!upstream) return unavailableControlPlaneResponse();

  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: {
      "Cache-Control": "no-store",
      "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
    },
  });
}
