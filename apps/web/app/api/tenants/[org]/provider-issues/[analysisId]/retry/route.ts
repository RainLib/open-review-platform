import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(
  request: Request,
  context: { params: Promise<{ org: string; analysisId: string }> },
) {
  const { org, analysisId } = await context.params;
  let expectedRevision: unknown;
  let idempotencyKey: unknown;

  try {
    ({ expected_revision: expectedRevision, idempotency_key: idempotencyKey } =
      await request.json());
  } catch {
    return Response.json(
      { error: "The Issue revision and idempotency key are required." },
      { status: 400 },
    );
  }

  if (
    typeof expectedRevision !== "number" ||
    !Number.isInteger(expectedRevision) ||
    expectedRevision < 1 ||
    typeof idempotencyKey !== "string" ||
    !/^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$/.test(idempotencyKey)
  ) {
    return Response.json(
      { error: "The Issue analysis retry request is invalid." },
      { status: 400 },
    );
  }

  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/provider-issues/${encodeURIComponent(analysisId)}/retry`,
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
      "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
      ...(upstream.headers.get("Location")
        ? { Location: upstream.headers.get("Location")! }
        : {}),
    },
  });
}
