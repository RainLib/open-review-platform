import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function value(input: unknown) {
  return typeof input === "string" ? input.trim() : "";
}

export async function POST(
  request: Request,
  context: { params: Promise<{ org: string; catalogId: string }> },
) {
  const [{ org, catalogId }, input] = await Promise.all([
    context.params,
    request.json().catch(() => undefined),
  ]);
  const version = value((input as { version?: unknown } | undefined)?.version);
  const contentSHA256 = value(
    (input as { content_sha256?: unknown } | undefined)?.content_sha256,
  ).toLowerCase();
  if (!version || !/^[a-f0-9]{64}$/.test(contentSHA256)) {
    return Response.json(
      { error: "A release version and its 64-character content digest are required." },
      { status: 400 },
    );
  }
  const upstream = await forwardControlPlaneRequest(
    `/v1/tenants/${encodeURIComponent(org)}/rule-catalog/${encodeURIComponent(catalogId)}/installations`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ version, content_sha256: contentSHA256 }),
      signal: request.signal,
    },
  );
  if (!upstream) return unavailableControlPlaneResponse();

  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: {
      "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
    },
  });
}
