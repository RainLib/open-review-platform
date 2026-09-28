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
  let scheduledFor: unknown;
  try {
    ({ scheduled_for: scheduledFor } = await request.json());
  } catch {
    return Response.json({ error: "A scheduled time is required." }, { status: 400 });
  }
  if (typeof scheduledFor !== "string" || Number.isNaN(Date.parse(scheduledFor))) {
    return Response.json({ error: "The scheduled time is invalid." }, { status: 400 });
  }
  return (
    (await forwardControlPlaneRequest(
      `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runId)}/schedules`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ scheduled_for: scheduledFor }),
        signal: request.signal,
      },
    )) ?? unavailableControlPlaneResponse()
  );
}
