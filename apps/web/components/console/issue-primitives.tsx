import { Circle, ExternalLink, FileCode2, TriangleAlert } from "lucide-react";

import { ProviderMark } from "@/components/providers/provider-icons";
import type { IssueDetail, IssueOccurrence, ReviewIssue } from "@/lib/control-api";
import { providerFileTarget, providerReviewTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";
import type { UiLanguage } from "@/lib/ui-language";

const severityStyles = {
  critical: "text-[var(--ls-critical-text)]",
  high: "text-[var(--ls-warning-text)]",
  medium: "text-[var(--ls-warning-text)]",
  low: "text-[var(--ls-success-text)]",
} satisfies Record<ReviewIssue["severity"], string>;

export function SeverityBadge({ severity, language = "en" }: { severity: ReviewIssue["severity"]; language?: UiLanguage }) {
  const label = language === "zh-CN" ? ({ critical: "严重", high: "高", medium: "中", low: "低" } as const)[severity] : severity;
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-xs font-medium capitalize", severityStyles[severity])}>
      <TriangleAlert aria-hidden="true" className="size-4" fill="currentColor" fillOpacity={0.12} />
      {label}
    </span>
  );
}

export function IssueStatusBadge({ status, language = "en" }: { status: ReviewIssue["status"]; language?: UiLanguage }) {
  const label = language === "zh-CN" ? ({ open: "待处理", regressed: "再次出现", resolved: "已解决", suppressed: "已忽略" } as const)[status] : status;
  const statusClass =
    status === "regressed"
      ? "text-[var(--ls-critical-text)]"
      : status === "resolved"
        ? "text-[var(--ls-success-text)]"
        : status === "suppressed"
          ? "text-[var(--ls-text-tertiary)]"
          : "text-[var(--ls-text-secondary)]";
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-xs font-medium capitalize", statusClass)}>
      <Circle aria-hidden="true" className="size-2" fill="currentColor" />
      {label}
    </span>
  );
}

export function ProviderReviewAnchor({
  issue,
  occurrence,
  compact = false,
}: {
  issue: Pick<ReviewIssue, "provider" | "api_base_url" | "repository">;
  occurrence: Pick<IssueOccurrence, "review_number">;
  compact?: boolean;
}) {
  const target = providerReviewTarget({
    ...issue,
    review_number: occurrence.review_number,
  });
  if (!target) return <span className="text-[var(--ls-text-tertiary)]">#{occurrence.review_number}</span>;
  return (
    <a
      className="luminous-focus inline-flex items-center gap-1.5 rounded text-[var(--ls-accent)] hover:underline"
      href={target.url}
      rel="noreferrer"
      target="_blank"
      title={`Open in ${target.label}`}
    >
      <ProviderMark className="size-3.5" provider={issue.provider} />
      #{occurrence.review_number}
      {compact ? null : <ExternalLink className="size-3" />}
    </a>
  );
}

export function ProviderFileAnchor({
  issue,
  occurrence,
}: {
  issue: Pick<ReviewIssue, "provider" | "api_base_url" | "repository">;
  occurrence: Pick<IssueOccurrence, "head_sha" | "path" | "start_line" | "end_line">;
}) {
  const target = providerFileTarget({ ...issue, ...occurrence });
  const label = `${occurrence.path}:${occurrence.start_line}${occurrence.end_line > occurrence.start_line ? `–${occurrence.end_line}` : ""}`;
  if (!target) {
    return (
      <span className="inline-flex min-w-0 items-center gap-1.5 font-mono text-xs text-[var(--ls-text-secondary)]">
        <FileCode2 className="size-3.5 shrink-0" />
        <span className="truncate">{label}</span>
      </span>
    );
  }
  return (
    <a
      className="luminous-focus inline-flex min-w-0 items-center gap-1.5 rounded font-mono text-xs text-[var(--ls-accent)] hover:underline"
      href={target.url}
      rel="noreferrer"
      target="_blank"
      title={`Open ${target.pathLabel} in ${target.label}`}
    >
      <FileCode2 className="size-3.5 shrink-0" />
      <span className="truncate">{label}</span>
      <ExternalLink className="size-3 shrink-0" />
    </a>
  );
}

export function issuePrompt(issue: IssueDetail, occurrence: IssueOccurrence) {
  const location = `${occurrence.path}:${occurrence.start_line}${occurrence.end_line > occurrence.start_line ? `-${occurrence.end_line}` : ""}`;
  return [
    `Review and fix the ${occurrence.severity} ${occurrence.category} finding in ${issue.repository}.`,
    `Location: ${location}`,
    `Revision: ${occurrence.head_sha}`,
    `Finding: ${occurrence.body}`,
    occurrence.suggestion ? `Recommended direction: ${occurrence.suggestion}` : undefined,
    "Preserve existing behavior outside this finding, add focused verification, and explain any trust-boundary or blast-radius changes.",
  ]
    .filter(Boolean)
    .join("\n\n");
}

export function formatIssueTime(value: string, language: UiLanguage = "en") {
  return new Intl.DateTimeFormat(language, {
    dateStyle: "medium",
    timeStyle: "short",
    timeZone: "UTC",
  }).format(new Date(value));
}

export function formatIssueAge(value: string, language: UiLanguage = "en") {
  const difference = new Date(value).getTime() - Date.now();
  const absolute = Math.abs(difference);
  const formatter = new Intl.RelativeTimeFormat(language, { numeric: "auto" });
  if (absolute < 60 * 60 * 1000) return formatter.format(Math.round(difference / (60 * 1000)), "minute");
  if (absolute < 24 * 60 * 60 * 1000) return formatter.format(Math.round(difference / (60 * 60 * 1000)), "hour");
  if (absolute < 30 * 24 * 60 * 60 * 1000) return formatter.format(Math.round(difference / (24 * 60 * 60 * 1000)), "day");
  return formatter.format(Math.round(difference / (30 * 24 * 60 * 60 * 1000)), "month");
}

export function shortIssueID(issueID: string) {
  return `ORP-${issueID.replaceAll("-", "").slice(0, 6).toUpperCase()}`;
}
