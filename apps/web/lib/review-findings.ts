import type { ReviewEvidence, ReviewFindingEvidence } from "@/lib/control-api";

export type FindingSeverityView = "all" | "blocking" | "critical" | "high" | "medium" | "low";
export type FindingStatusView = "all" | "open" | "resolved" | "wont_fix";
export type FindingFilters = { severity: FindingSeverityView; status: FindingStatusView; category: string; file: string; query: string };
export type FindingPublicationState = "published" | "failed" | "pending" | "unconfirmed" | "untracked";

export function validFindingSeverity(value?: string): FindingSeverityView {
  return value === "blocking" || value === "critical" || value === "high" || value === "medium" || value === "low" ? value : "all";
}

export function validFindingStatus(value?: string): FindingStatusView {
  return value === "open" || value === "resolved" || value === "wont_fix" ? value : "all";
}

const severityRank: Record<ReviewFindingEvidence["severity"], number> = { low: 1, medium: 2, high: 3, critical: 4 };

export function blockingFindings(evidence: Pick<ReviewEvidence, "findings" | "merge_gate">): ReviewFindingEvidence[] | undefined {
  const gate = evidence.merge_gate;
  if (!gate?.enabled || gate.threshold === "off") return undefined;
  const threshold = severityRank[gate.threshold];
  return evidence.findings.filter((finding) => severityRank[finding.severity] >= threshold);
}

export function filterReviewFindings(findings: ReviewFindingEvidence[], filters: FindingFilters, blocking?: ReviewFindingEvidence[]) {
  const blockingIDs = new Set(blocking?.map((finding) => finding.id) ?? []);
  const query = filters.query.trim().toLocaleLowerCase();
  return findings.filter((finding) =>
    (filters.severity === "all" || (filters.severity === "blocking" ? Boolean(blocking && blockingIDs.has(finding.id)) : finding.severity === filters.severity))
    && (filters.status === "all" || (filters.status === "open" ? !finding.disposition : finding.disposition === filters.status))
    && (!filters.category || finding.category === filters.category)
    && (!filters.file || finding.path === filters.file)
    && (!query || `${finding.path} ${finding.category} ${finding.body} ${finding.suggestion ?? ""}`.toLocaleLowerCase().includes(query))
  );
}

export function findingPublicationState(
  finding: Pick<ReviewFindingEvidence, "provider_marker">,
  receipts: ReviewEvidence["receipts"],
): FindingPublicationState {
  if (!finding.provider_marker) return "untracked";
  const receipt = receipts.find((item) =>
    item.receipt_kind === "inline_finding" && item.stable_marker === finding.provider_marker
  );
  if (!receipt) return "unconfirmed";
  if (receipt.published_at) return "published";
  return receipt.last_error ? "failed" : "pending";
}

export function findingPrompt(repository: string, headSHA: string, finding: ReviewFindingEvidence) {
  return [
    `Review and fix the ${finding.severity} ${finding.category} finding in ${repository}.`,
    `Location: ${finding.path}:${finding.start_line}${finding.end_line > finding.start_line ? `-${finding.end_line}` : ""}`,
    `Revision: ${headSHA}`,
    `Finding: ${finding.body}`,
    finding.suggestion ? `Recommended direction: ${finding.suggestion}` : undefined,
    "Preserve unrelated behavior, add focused verification, and explain trust-boundary or blast-radius changes.",
  ].filter(Boolean).join("\n\n");
}
