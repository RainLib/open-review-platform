import {
  forwardControlPlaneRequest,
  unavailableControlPlaneResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(
  request: Request,
  context: { params: Promise<{ org: string; scheduleId: string }> },
) {
  const { org, scheduleId } = await context.params;
  let revision: unknown;
  try {
    ({ expected_revision: revision } = await request.json());
  } catch {
    return Response.json({ error: "The schedule revision is required." }, { status: 400 });
  }
  if (typeof revision !== "number" || !Number.isInteger(revision) || revision < 1) {
    return Response.json({ error: "The schedule revision is invalid." }, { status: 400 });
  }
  return (
    (await forwardControlPlaneRequest(
      `/v1/tenants/${encodeURIComponent(org)}/review-schedules/${encodeURIComponent(scheduleId)}/cancel`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: revision }),
        signal: request.signal,
      },
    )) ?? unavailableControlPlaneResponse()
  );
}
