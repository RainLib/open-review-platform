"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  Ban,
  CheckCircle2,
  CircleAlert,
  ExternalLink,
  Layers3,
  LoaderCircle,
  MessageSquareMore,
  ShieldAlert,
  ThumbsDown,
  ThumbsUp,
} from "lucide-react";

import type { FindingFeedbackDashboard } from "@/lib/control-api";
import { findingEvidenceURL } from "@/lib/finding-evidence-url";
import { findingPreviewText } from "@/lib/finding-format";
import { providerFileTarget, providerReviewTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";

function percentage(value: number, total: number) {
  return total > 0 ? Math.round((value / total) * 100) : 0;
}

export function FindingFeedbackDashboardView({
  data,
  enabled,
  findingHeading = "Recent findings",
  findings,
  findingsError,
  org,
}: {
  data: FindingFeedbackDashboard;
  enabled: boolean;
  findingHeading?: string;
  findings?: FindingFeedbackDashboard["recent_findings"];
  findingsError?: string;
  org: string;
}) {
  const router = useRouter();
  const [busy, setBusy] = useState<string>();
  const [message, setMessage] = useState<string>();
  const [messageTone, setMessageTone] = useState<"success" | "error">();
  const responseRate = percentage(data.feedback_count, data.finding_count);
  const usefulRate = percentage(
    data.useful_finding_count,
    data.useful_finding_count + data.false_positive_count,
  );
  const visibleFindings = findings ?? data.recent_findings;

  async function setDisposition(
    findingID: string,
    kind: "resolved" | "wont_fix" | "clear",
  ) {
    if (!enabled || busy) return;
    setBusy(findingID);
    setMessage(undefined);
    setMessageTone(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/findings/${encodeURIComponent(findingID)}/disposition`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ kind }),
        },
      );
      const body = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      if (!response.ok) {
        setMessage(body.error ?? "The finding disposition could not be updated.");
        setMessageTone("error");
        return;
      }
      setMessage(
        `Finding disposition updated to ${kind === "clear" ? "unresolved" : kind.replace("_", " ")}.`,
      );
      setMessageTone("success");
      router.refresh();
    } catch {
      setMessage("The finding disposition could not reach the control plane.");
      setMessageTone("error");
    } finally {
      setBusy(undefined);
    }
  }

  const metrics = [
    {
      icon: MessageSquareMore,
      label: "Published findings",
      tone: "text-[var(--ls-accent)]",
      value: data.finding_count,
    },
    {
      icon: CircleAlert,
      label: "Feedback events",
      tone: "text-violet-600 dark:text-violet-300",
      value: data.feedback_count,
    },
    {
      icon: Layers3,
      label: "Response coverage",
      tone: "text-[var(--ls-warning-text)]",
      value: `${responseRate}%`,
    },
    {
      icon: ThumbsUp,
      label: "Useful among rated",
      tone: "text-[var(--ls-success-text)]",
      value: `${usefulRate}%`,
    },
  ];

  return (
    <div className="space-y-5">
      {message ? (
        <div
          aria-live="polite"
          className={cn(
            "flex items-start gap-2 rounded-[14px] border px-4 py-3 text-sm leading-6",
            messageTone === "success"
              ? "border-[color:color-mix(in_srgb,var(--ls-success)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-success)_8%,transparent)] text-[var(--ls-success-text)]"
              : "border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,transparent)] text-[var(--ls-warning-text)]",
          )}
        >
          <CircleAlert className="mt-0.5 size-4 shrink-0" />
          {message}
        </div>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {metrics.map((metric) => {
          const Icon = metric.icon;
          return (
            <article
              className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 shadow-[var(--ls-shadow-control)]"
              key={metric.label}
            >
              <div className="flex items-center justify-between text-xs text-[var(--ls-text-tertiary)]">
                {metric.label}
                <Icon className={cn("size-4", metric.tone)} />
              </div>
              <p className="mt-3 text-2xl font-semibold tracking-[-0.04em] text-[var(--ls-text)]">
                {metric.value}
              </p>
            </article>
          );
        })}
      </div>

      {data.attribution_warning ? (
        <div className="flex items-start gap-2 rounded-[14px] border border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,transparent)] px-4 py-3 text-xs leading-5 text-[var(--ls-warning-text)]">
          <ShieldAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-warning)]" />
          {data.attribution_warning}
        </div>
      ) : null}

      <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
        <div className="border-b border-[var(--ls-line)] px-5 py-4">
          <h2 className="text-sm font-semibold text-[var(--ls-text)]">Repository evidence</h2>
          <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">
            Only active feedback is counted. Deleted reactions remain auditable
            but no longer affect these measurements.
          </p>
        </div>
        {data.repositories.length ? (
          <div className="divide-y divide-[var(--ls-line)]">
            {data.repositories.map((item) => {
              const rated =
                item.useful_finding_count + item.false_positive_count;
              return (
                <article className="px-5 py-4" key={item.repository}>
                  <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
                    <div>
                      <h3 className="text-sm font-medium text-[var(--ls-text)]">
                        {item.repository}
                      </h3>
                      <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
                        {item.distinct_snapshot_count} immutable snapshot(s) ·{" "}
                        {item.high_risk_finding_count} high-risk finding(s)
                      </p>
                    </div>
                    <div className="flex flex-wrap gap-2 text-[11px]">
                      <MetricPill>{item.finding_count} findings</MetricPill>
                      <MetricPill tone="success">
                        <ThumbsUp className="size-3" />
                        {item.useful_finding_count}
                      </MetricPill>
                      <MetricPill tone="critical">
                        <ThumbsDown className="size-3" />
                        {item.false_positive_count}
                      </MetricPill>
                    </div>
                  </div>
                  <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-[var(--ls-surface-muted)]">
                    <div
                      className="h-full rounded-full bg-[var(--ls-success)]"
                      style={{
                        width: `${percentage(item.useful_finding_count, rated)}%`,
                      }}
                    />
                  </div>
                </article>
              );
            })}
          </div>
        ) : (
          <EmptyFeedback
            detail="Reactions on future inline findings will appear here."
            title="No finding feedback evidence yet"
          />
        )}
      </section>

      <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
        <div className="border-b border-[var(--ls-line)] px-5 py-4">
          <h2 className="text-sm font-semibold text-[var(--ls-text)]">{findingHeading}</h2>
          <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">
            Reviewer dispositions are actor-scoped and auditable; they never
            rewrite the original AI result.
          </p>
        </div>
        {findingsError ? (
          <EmptyFeedback detail={findingsError} title="Finding results are unavailable" />
        ) : visibleFindings.length ? (
          <div className="divide-y divide-[var(--ls-line)]">
            {visibleFindings.map((finding) => {
              const target = providerReviewTarget(finding);
              const fileTarget = providerFileTarget({ ...finding, head_sha: finding.head_sha ?? "" });
              const evidenceURL = findingEvidenceURL(org, finding.run_id, finding.id);
              const isBusy = busy === finding.id;
              return (
                <article className="px-5 py-4" key={finding.id}>
                  <div className="flex flex-col justify-between gap-3 lg:flex-row lg:items-start">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <MetricPill tone={severityTone(finding.severity)}>
                          {finding.severity}
                        </MetricPill>
                        <span className="text-xs text-[var(--ls-text-secondary)]">
                          {finding.category}
                        </span>
                        {finding.actor_disposition ? (
                          <MetricPill tone="accent">
                            {finding.actor_disposition.replace("_", " ")}
                          </MetricPill>
                        ) : null}
                      </div>
                      <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">
                        {findingPreviewText(finding.body_preview)}
                      </p>
                      <p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">
                        {finding.repository} #{finding.review_number} ·{" "}
                        {fileTarget ? (
                          <a className="luminous-focus rounded font-mono text-[var(--ls-accent)] hover:underline" href={fileTarget.url} rel="noreferrer" target="_blank" title={`Open exact revision in ${fileTarget.label}`}>
                            {finding.path}:{finding.start_line}
                          </a>
                        ) : (
                          <code>{finding.path}:{finding.start_line}</code>
                        )}{" "}
                        · 👍 {finding.useful_count} · 👎 {finding.false_positive_count}
                      </p>
                    </div>
                    <div className="flex shrink-0 flex-wrap gap-2">
                      {evidenceURL ? (
                        <Link className="luminous-focus inline-flex h-8 items-center rounded-[9px] border border-[var(--ls-line-strong)] px-2.5 text-xs text-[var(--ls-accent)] transition hover:bg-[var(--ls-accent-soft)]" href={evidenceURL} prefetch={false}>
                          Review evidence
                        </Link>
                      ) : null}
                      {target ? (
                        <a
                          className="luminous-focus inline-flex h-8 items-center gap-1.5 rounded-[9px] border border-[var(--ls-line-strong)] px-2.5 text-xs text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
                          href={target.url}
                          rel="noreferrer"
                          target="_blank"
                        >
                          Open {finding.provider === "gitlab" ? "MR" : "PR"} <ExternalLink className="size-3" />
                        </a>
                      ) : null}
                      <FeedbackButton
                        busy={isBusy}
                        disabled={!enabled || Boolean(busy)}
                        icon={CheckCircle2}
                        label="Resolved"
                        onClick={() =>
                          setDisposition(
                            finding.id,
                            finding.actor_disposition === "resolved"
                              ? "clear"
                              : "resolved",
                          )
                        }
                        tone="success"
                      />
                      <FeedbackButton
                        busy={isBusy}
                        disabled={!enabled || Boolean(busy)}
                        icon={Ban}
                        label="Won&apos;t fix"
                        onClick={() =>
                          setDisposition(
                            finding.id,
                            finding.actor_disposition === "wont_fix"
                              ? "clear"
                              : "wont_fix",
                          )
                        }
                        tone="warning"
                      />
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        ) : (
          <EmptyFeedback
            detail="Adjust the view or search filters to inspect other findings."
            title="No findings match this view"
          />
        )}
      </section>
    </div>
  );
}

function FeedbackButton({
  busy,
  disabled,
  icon: Icon,
  label,
  onClick,
  tone,
}: {
  busy: boolean;
  disabled: boolean;
  icon: typeof Ban;
  label: string;
  onClick: () => void;
  tone: "success" | "warning";
}) {
  return (
    <button
      className={cn(
        "luminous-focus inline-flex h-8 items-center gap-1.5 rounded-[9px] border px-2.5 text-xs font-medium disabled:cursor-not-allowed disabled:opacity-45",
        tone === "success"
          ? "border-[color:color-mix(in_srgb,var(--ls-success)_28%,transparent)] text-[var(--ls-success-text)]"
          : "border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] text-[var(--ls-warning-text)]",
      )}
      disabled={disabled}
      onClick={onClick}
      type="button"
    >
      {busy ? <LoaderCircle className="size-3 animate-spin" /> : <Icon className="size-3" />}
      {label}
    </button>
  );
}

function MetricPill({
  children,
  tone = "default",
}: {
  children: React.ReactNode;
  tone?: "accent" | "critical" | "default" | "high" | "success" | "warning";
}) {
  const tones = {
    accent:
      "border-[color:color-mix(in_srgb,var(--ls-accent)_25%,transparent)] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]",
    critical:
      "border-[color:color-mix(in_srgb,var(--ls-critical)_25%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-critical)_8%,transparent)] text-[var(--ls-critical-text)]",
    default: "border-[var(--ls-line)] text-[var(--ls-text-secondary)]",
    high:
      "border-[color:color-mix(in_srgb,var(--ls-warning)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,transparent)] text-[var(--ls-warning-text)]",
    success:
      "border-[color:color-mix(in_srgb,var(--ls-success)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-success)_8%,transparent)] text-[var(--ls-success-text)]",
    warning:
      "border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,transparent)] text-[var(--ls-warning-text)]",
  };
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-2 py-1 text-[11px] font-medium capitalize",
        tones[tone],
      )}
    >
      {children}
    </span>
  );
}

function EmptyFeedback({ detail, title }: { detail: string; title: string }) {
  return (
    <div className="grid min-h-44 place-items-center p-8 text-center">
      <div>
        <MessageSquareMore className="mx-auto size-5 text-[var(--ls-text-tertiary)]" />
        <p className="mt-3 text-sm font-medium text-[var(--ls-text-secondary)]">{title}</p>
        <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">{detail}</p>
      </div>
    </div>
  );
}

function severityTone(severity: string) {
  if (severity === "critical") return "critical";
  if (severity === "high" || severity === "medium") return "high";
  return "success";
}
