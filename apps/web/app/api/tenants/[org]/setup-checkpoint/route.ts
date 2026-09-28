import { forwardControlPlaneRequest, unavailableControlPlaneResponse } from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

type CheckpointRequest = {
  current_step?: unknown;
  expected_revision?: unknown;
  learning_boundary?: unknown;
};
const steps = new Set(["connect", "review_scope", "learning", "severity", "rules", "complete"]);
const learningModes = new Set(["governed_policy", "skipped"]);

function learningBoundary(value: unknown) {
  if (value === undefined) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const candidate = value as { mode?: unknown; reviewer_exclusions?: unknown };
  if (typeof candidate.mode !== "string" || !learningModes.has(candidate.mode)) return null;
  if (!Array.isArray(candidate.reviewer_exclusions) || candidate.reviewer_exclusions.length > 100) return null;
  const reviewerExclusions = candidate.reviewer_exclusions.map((subject) => typeof subject === "string" ? subject.trim() : "");
  if (reviewerExclusions.some((subject) => !subject || subject.length > 256)) return null;
  return { mode: candidate.mode, reviewer_exclusions: reviewerExclusions };
}

export async function PUT(request: Request, context: { params: Promise<{ org: string }> }) {
  const { org } = await context.params;
  let input: CheckpointRequest;
  try { input = await request.json() as CheckpointRequest; } catch { return Response.json({ error: "A setup checkpoint payload is required." }, { status: 400 }); }
  const step = typeof input.current_step === "string" ? input.current_step.trim() : "";
  const revision = typeof input.expected_revision === "number" && Number.isInteger(input.expected_revision) ? input.expected_revision : -1;
  if (!steps.has(step) || revision < 0) return Response.json({ error: "Setup checkpoint values are invalid." }, { status: 400 });
  const boundary = learningBoundary(input.learning_boundary);
  if (boundary === null) return Response.json({ error: "The learning boundary is invalid." }, { status: 400 });
  const upstream = await forwardControlPlaneRequest(`/v1/tenants/${encodeURIComponent(org)}/setup-checkpoint`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ current_step: step, expected_revision: revision, ...(boundary ? { learning_boundary: boundary } : {}) }), signal: request.signal });
  if (!upstream) return unavailableControlPlaneResponse();
  return new Response(await upstream.text(), { status: upstream.status, headers: { "Content-Type": upstream.headers.get("Content-Type") ?? "application/json", "Cache-Control": "no-store" } });
}
