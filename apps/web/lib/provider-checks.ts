export type ProviderCheckWithOrigin = {
  origin?: "independent" | "open_review" | "unclassified";
  state?: string;
};

export type ReviewChecksView = "all" | "open-review" | "provider-ci";

export function validReviewChecksView(value?: string): ReviewChecksView {
  return value === "open-review" || value === "provider-ci" ? value : "all";
}

export function providerCheckState(value: string): "success" | "failed" | "cancelled" | "running" | "queued" | "skipped" | "unknown" {
  const state = value.toLowerCase();
  if (state === "success" || state === "passed" || state === "succeeded") return "success";
  if (["failure", "failed", "error", "timed_out", "action_required"].includes(state)) return "failed";
  if (state === "cancelled" || state === "canceled") return "cancelled";
  if (state === "running" || state === "in_progress") return "running";
  if (["pending", "queued", "waiting_for_resource"].includes(state)) return "queued";
  if (state === "skipped") return "skipped";
  return "unknown";
}

export function safeProviderCheckURL(value?: string): string | undefined {
  if (!value || value.length > 2048) return undefined;
  try {
    const parsed = new URL(value);
    if ((parsed.protocol !== "https:" && parsed.protocol !== "http:") || !parsed.hostname || parsed.username || parsed.password) return undefined;
    return parsed.toString();
  } catch {
    return undefined;
  }
}

export function reviewChecksCount(evidence: ReviewEvidence) {
  const checks = evidence.provider_checks?.head_sha.toLowerCase() === evidence.run.head_sha.toLowerCase()
    ? evidence.provider_checks.checks : [];
  const own = checks.filter((check) => check.origin === "open_review").length;
  const independent = checks.filter((check) => check.origin === "independent").length;
  const unclassified = checks.length - own - independent;
  const openReview = evidence.stages.length + own + (evidence.merge_gate ? 1 : 0);
  return { all: openReview + independent + unclassified, openReview, independent, unclassified };
}

// Older read models have no origin. Do not silently promote their checks to
// independent CI evidence just because a provider reported "success".
export function groupProviderChecks<T extends ProviderCheckWithOrigin>(checks: T[]) {
  return {
    independent: checks.filter((check) => check.origin === "independent"),
    other: checks.filter((check) => check.origin !== "independent"),
  };
}

export function prioritizeProviderChecks<T extends ProviderCheckWithOrigin>(checks: T[]): T[] {
  const rank = (state: string | undefined) => {
    const normalized = providerCheckState(state ?? "");
    if (normalized === "failed") return 0;
    if (normalized === "queued" || normalized === "running") return 1;
    if (normalized === "success") return 3;
    return 2;
  };
  return checks.map((check, index) => ({ check, index }))
    .sort((left, right) => rank(left.check.state) - rank(right.check.state) || left.index - right.index)
    .map(({ check }) => check);
}
import type { ReviewEvidence } from "@/lib/control-api";
