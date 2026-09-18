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

type NormalizedRule = {
  key: string;
  enforcement: "mandatory" | "advisory";
  merge_behavior: "replace" | "append" | "deny_override";
  severity: "low" | "medium" | "high" | "critical";
  content: { prompt: string };
};

const enforcementValues = new Set(["mandatory", "advisory"]);
const mergeBehaviorValues = new Set(["replace", "append", "deny_override"]);
const severityValues = new Set(["low", "medium", "high", "critical"]);

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function normalizeRules(value: unknown): NormalizedRule[] | undefined {
  if (!Array.isArray(value) || value.length === 0) return undefined;

  const rules: NormalizedRule[] = [];
  for (const candidate of value) {
    if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) {
      return undefined;
    }
    const input = candidate as Record<string, unknown>;
    const key = stringValue(input.key);
    const enforcement = stringValue(input.enforcement).toLowerCase();
    const mergeBehavior = stringValue(input.merge_behavior).toLowerCase();
    const severity = stringValue(input.severity).toLowerCase();
    const content = input.content;
    const prompt =
      content && typeof content === "object" && !Array.isArray(content)
        ? stringValue((content as Record<string, unknown>).prompt)
        : "";

    if (
      !key ||
      !enforcementValues.has(enforcement) ||
      !mergeBehaviorValues.has(mergeBehavior) ||
      !severityValues.has(severity) ||
      !prompt ||
      (mergeBehavior === "deny_override" && enforcement !== "mandatory")
    ) {
      return undefined;
    }

    rules.push({
      key,
      enforcement: enforcement as NormalizedRule["enforcement"],
      merge_behavior: mergeBehavior as NormalizedRule["merge_behavior"],
      severity: severity as NormalizedRule["severity"],
      content: { prompt },
    });
  }
  return rules;
}

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
  const rules = normalizeRules(input.rules);
  if (!name || !rules) {
    return Response.json(
      {
        error:
          "A name and at least one complete rule with a valid severity, enforcement, and review instruction are required.",
      },
      { status: 400 },
    );
  }

  const upstream = await forwardDevelopmentRequest(
    `/v1/tenants/${encodeURIComponent(org)}/rule-sets`,
    {
      method: "POST",
      body: JSON.stringify({ name, description, rules }),
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
