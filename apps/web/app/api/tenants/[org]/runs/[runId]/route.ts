import {
  forwardDevelopmentRequest,
  unavailableDevelopmentResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(
  request: Request,
  context: { params: Promise<{ org: string; runId: string }> },
) {
  const { org, runId } = await context.params;
  let revision: unknown;

  try {
    ({ revision } = await request.json());
  } catch {
    return Response.json(
      { error: "The run revision is required." },
      { status: 400 },
    );
  }

  if (
    typeof revision !== "number" ||
    !Number.isInteger(revision) ||
    revision < 1
  ) {
    return Response.json(
      { error: "The run revision is invalid." },
      { status: 400 },
    );
  }

  const upstream = await forwardDevelopmentRequest(
    `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runId)}/cancel`,
    {
      method: "POST",
      body: JSON.stringify({ revision }),
      headers: { "Content-Type": "application/json" },
      signal: request.signal,
    },
  );

  if (!upstream) return unavailableDevelopmentResponse();

  const body = await upstream.text();
  return new Response(body, {
    status: upstream.status,
    headers: {
      "Content-Type":
        upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
    },
  });
}
