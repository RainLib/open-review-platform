import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(
  request: Request,
  context: { params: Promise<{ org: string; runId: string }> },
) {
  const { org, runId } = await context.params;
  let expectedRevision: unknown;
  let idempotencyKey: unknown;

  try {
    ({ expected_revision: expectedRevision, idempotency_key: idempotencyKey } =
      await request.json());
  } catch {
    return Response.json(
      { error: "The run revision and idempotency key are required." },
      { status: 400 },
    );
  }

  if (
    typeof expectedRevision !== "number" ||
    !Number.isInteger(expectedRevision) ||
    expectedRevision < 1 ||
    typeof idempotencyKey !== "string" ||
    idempotencyKey.length < 8 ||
    idempotencyKey.length > 128
  ) {
    return Response.json(
      { error: "The retry request is invalid." },
      { status: 400 },
    );
  }

  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runId)}/retry`,
    {
      method: "POST",
      body: JSON.stringify({
        expected_revision: expectedRevision,
        idempotency_key: idempotencyKey,
      }),
      headers: { "Content-Type": "application/json" },
      signal: request.signal,
    },
  );
  if (!upstream) return unavailableControlPlaneResponse();

  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: {
      "Content-Type":
        upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
    },
  });
}
