import {
  forwardDevelopmentRequest,
  unavailableDevelopmentResponse,
} from "@/lib/control-plane-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

type RuleSetRequest = {
  name?: unknown;
  description?: unknown;
  rules?: unknown;
};

export async function POST(
  request: Request,
  context: { params: Promise<{ org: string }> },
) {
  const { org } = await context.params;
  let input: RuleSetRequest;

  try {
    input = (await request.json()) as RuleSetRequest;
  } catch {
    return Response.json(
      { error: "A rule-set payload is required." },
      { status: 400 },
    );
  }

  const name = typeof input.name === "string" ? input.name.trim() : "";
  const description =
    typeof input.description === "string" ? input.description.trim() : "";
  if (!name || !Array.isArray(input.rules) || input.rules.length === 0) {
    return Response.json(
      { error: "A name and at least one rule are required." },
      { status: 400 },
    );
  }

  const upstream = await forwardDevelopmentRequest(
    `/v1/tenants/${encodeURIComponent(org)}/rule-sets`,
    {
      method: "POST",
      body: JSON.stringify({ name, description, rules: input.rules }),
      headers: { "Content-Type": "application/json" },
      signal: request.signal,
    },
  );
  if (!upstream) return unavailableDevelopmentResponse();

  return new Response(await upstream.text(), {
    status: upstream.status,
    headers: {
      "Content-Type":
        upstream.headers.get("Content-Type") ?? "application/json",
      "Cache-Control": "no-store",
    },
  });
}
