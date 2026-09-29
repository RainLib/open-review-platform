import type { ReviewEvidence } from "@/lib/control-api";

export type ReviewOverviewView = "overview" | "scope" | "risk" | "verification" | "evidence";

export function validReviewOverviewView(value?: string): ReviewOverviewView {
  return value === "scope" || value === "risk" || value === "verification" || value === "evidence" ? value : "overview";
}

export function observedSeverity(findings: ReviewEvidence["findings"]): string {
  for (const severity of ["critical", "high", "medium", "low"] as const) {
    if (findings.some((finding) => finding.severity === severity)) return severity;
  }
  return "none retained";
}

export function providerObservation(evidence: ReviewEvidence): "not observed" | "pending" | "failed" | "stale" | "partial" | "observed" {
  const checks = evidence.provider_checks;
  if (!checks || checks.head_sha.toLowerCase() !== evidence.run.head_sha.toLowerCase()) return "not observed";
  if (checks.state === "queued" || checks.state === "running") return "pending";
  if (checks.state === "failed") return "failed";
  if (checks.stale || !checks.observed_at) return "stale";
  return checks.truncated ? "partial" : "observed";
}

export function selectedScopeCount(evidence: ReviewEvidence): number | undefined {
  if (!evidence.execution_plan) return undefined;
  return evidence.execution_plan.selected_paths.length;
}
