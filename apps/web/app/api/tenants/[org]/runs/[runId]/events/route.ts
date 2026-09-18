import {
  forwardDevelopmentRequest,
  unavailableDevelopmentResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function GET(
  request: Request,
  context: { params: Promise<{ org: string; runId: string }> },
) {
  const { org, runId } = await context.params;
  const afterRevision =
    new URL(request.url).searchParams.get("afterRevision") ?? "0";
  const upstream = await forwardDevelopmentRequest(
    `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runId)}/events?after_revision=${encodeURIComponent(afterRevision)}`,
    { headers: { Accept: "text/event-stream" }, signal: request.signal },
  );

  if (!upstream) return unavailableDevelopmentResponse();
  if (!upstream.ok || !upstream.body) {
    return new Response(await upstream.text(), {
      status: upstream.status,
      headers: {
        "Content-Type":
          upstream.headers.get("Content-Type") ?? "application/json",
        "Cache-Control": "no-store",
      },
    });
  }

  return new Response(upstream.body, {
    status: 200,
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache, no-transform",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    },
  });
}
