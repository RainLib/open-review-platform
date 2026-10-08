"use client";

import { useUiLanguage, useWorkflowStatus, useWorkflowText } from "@/components/console/ui-language-context";

import { useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import {
  Check,
  CheckCircle2,
  Clock3,
  FileKey2,
  LoaderCircle,
  ShieldAlert,
  UploadCloud,
  X,
  XCircle,
} from "lucide-react";

import type { RuleApproval } from "@/lib/control-api";
import { cn } from "@/lib/utils";

type Filter = "all" | RuleApproval["state"];

const statePresentation = {
  pending: {
    label: "Pending",
    icon: Clock3,
    className: "bg-[color:color-mix(in_srgb,var(--ls-warning)_12%,transparent)] text-[var(--ls-warning)]",
  },
  approved: {
    label: "Approved",
    icon: CheckCircle2,
    className: "bg-[color:color-mix(in_srgb,var(--ls-success)_12%,transparent)] text-[var(--ls-success)]",
  },
  rejected: {
    label: "Rejected",
    icon: XCircle,
    className: "bg-red-500/10 text-[var(--ls-critical-text)]",
  },
  cancelled: {
    label: "Cancelled",
    icon: XCircle,
    className: "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]",
  },
} as const;



export function RuleApprovalManager({
  approvals,
  enabled,
  org,
}: {
  approvals: RuleApproval[];
  enabled: boolean;
  org: string;
}) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  const language = useUiLanguage();
  const dateFormatter = new Intl.DateTimeFormat(language, {dateStyle:"medium",timeStyle:"short",timeZone:"UTC"});
  const router = useRouter();
  const [filter, setFilter] = useState<Filter>("all");
  const [busy, setBusy] = useState<string>();
  const [message, setMessage] = useState<string>();
  const filtered = useMemo(
    () =>
      filter === "all"
        ? approvals
        : approvals.filter((approval) => approval.state === filter),
    [approvals, filter],
  );
  const counts = useMemo(
    () => ({
      pending: approvals.filter((item) => item.state === "pending").length,
      approved: approvals.filter((item) => item.state === "approved").length,
      rejected: approvals.filter((item) => item.state === "rejected").length,
    }),
    [approvals],
  );

  async function post(path: string, body?: Record<string, unknown>) {
    setBusy(path);
    setMessage(undefined);
    try {
      const response = await fetch(path, {
        method: "POST",
        headers: body ? { "Content-Type": "application/json" } : undefined,
        body: body ? JSON.stringify(body) : undefined,
      });
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      if (!response.ok) throw new Error(payload.error ?? t("The action was rejected."));
      router.refresh();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : t("The action failed."));
    } finally {
      setBusy(undefined);
    }
  }

  function decide(approval: RuleApproval, decision: "approved" | "rejected") {
    const input = document.getElementById(
      `approval-comment-${approval.id}`,
    ) as HTMLTextAreaElement | null;
    return post(
      `/api/tenants/${encodeURIComponent(org)}/rule-approval-requests/${encodeURIComponent(approval.id)}/decisions`,
      { decision, comment: input?.value.trim() ?? "" },
    );
  }

  function publish(approval: RuleApproval) {
    return post(
      `/api/tenants/${encodeURIComponent(org)}/rule-sets/${encodeURIComponent(approval.rule_set_id)}/versions/${approval.version}/publish`,
    );
  }

  return (
    <div className="space-y-5">
      <div className="grid gap-3 sm:grid-cols-3">
        {[
          [t("Awaiting decision"), counts.pending, Clock3, "text-[var(--ls-warning)]"],
          [t("Ready to publish"), counts.approved, CheckCircle2, "text-[var(--ls-success)]"],
          [t("Returned to draft"), counts.rejected, XCircle, "text-[var(--ls-critical-text)]"],
        ].map(([label, value, Icon, tone]) => {
          const StatusIcon = Icon as typeof Clock3;
          return (
            <div className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 shadow-[var(--ls-shadow-control)]" key={String(label)}>
              <div className="flex items-center justify-between text-xs text-[var(--ls-text-secondary)]">
                {label as string}
                <StatusIcon className={cn("size-4", tone as string)} />
              </div>
              <p className="mt-3 text-2xl font-semibold text-[var(--ls-text)]">{value as number}</p>
            </div>
          );
        })}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {(["all", "pending", "approved", "rejected", "cancelled"] as Filter[]).map(
          (value) => (
            <button
              className={cn(
                "luminous-focus rounded-full px-3 py-1.5 text-xs font-medium transition",
                filter === value
                  ? "bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"
                  : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]",
              )}
              key={value}
              onClick={() => setFilter(value)}
              type="button"
            >
              {value === "all" ? t("All") : status(value)}
            </button>
          ),
        )}
      </div>

      {message ? (
        <div className="flex items-start gap-2 rounded-[12px] border border-red-500/25 bg-red-500/[0.07] px-4 py-3 text-sm text-[var(--ls-critical-text)]">
          <ShieldAlert className="mt-0.5 size-4 shrink-0" /> {message}
        </div>
      ) : null}

      {filtered.length === 0 ? (
        <div className="grid min-h-48 place-items-center rounded-[18px] border border-dashed border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] p-8 text-center">
          <div>
            <FileKey2 className="mx-auto size-5 text-[var(--ls-text-tertiary)]" />
            <h2 className="mt-3 text-sm font-medium text-[var(--ls-text)]">{t("No matching approval requests")}</h2>
            <p className="mt-2 text-sm text-[var(--ls-text-secondary)]">{t("Submit a policy draft from the library to start governed approval.")}</p>
          </div>
        </div>
      ) : (
        <div className="space-y-3">
          {filtered.map((approval) => {
            const presentation = statePresentation[approval.state];
            const StateIcon = presentation.icon;
            const decisionPath = `/api/tenants/${encodeURIComponent(org)}/rule-approval-requests/${encodeURIComponent(approval.id)}/decisions`;
            const publishPath = `/api/tenants/${encodeURIComponent(org)}/rule-sets/${encodeURIComponent(approval.rule_set_id)}/versions/${approval.version}/publish`;
            return (
              <article className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]" key={approval.id}>
                <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-start">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <h2 className="truncate text-sm font-semibold text-[var(--ls-text)]">{approval.rule_set_name}</h2>
                      <span className={cn("inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium", presentation.className)}>
                        <StateIcon className="size-3" /> {status(approval.state)}
                      </span>
                    </div>
                    <p className="mt-2 text-xs text-[var(--ls-text-secondary)]">
                      {t(" Version ")}{approval.version} {t(" requested by ")}<span className="text-[var(--ls-text)]">{approval.requested_by}</span> · {dateFormatter.format(new Date(approval.created_at))} UTC
                    </p>
                  </div>
                  <div className="shrink-0 text-left sm:text-right">
                    <p className="text-sm font-semibold text-[var(--ls-text)]">
                      {approval.approval_count}/{approval.required_approvals}
                    </p>
                    <p className="text-[11px] text-[var(--ls-text-tertiary)]">{t("distinct approver votes")}</p>
                  </div>
                </div>

                <div className="mt-4 h-1.5 overflow-hidden rounded-full bg-[var(--ls-surface-muted)]">
                  <div
                    className="h-full rounded-full bg-gradient-to-r from-[var(--ls-accent)] to-[var(--ls-success)] transition-all"
                    style={{ width: `${Math.min(100, (approval.approval_count / approval.required_approvals) * 100)}%` }}
                  />
                </div>

                <details className="group mt-4 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] open:bg-[var(--ls-surface)]">
                  <summary className="cursor-pointer list-none px-3.5 py-2.5 text-xs font-medium text-[var(--ls-text-secondary)] marker:hidden hover:text-[var(--ls-text)]">
                    {t(" Evidence and decision controls ")}</summary>
                  <div className="border-t border-[var(--ls-line)] px-3.5 py-3">
                    <dl className="grid gap-3 text-xs sm:grid-cols-2">
                      <div>
                        <dt className="text-[var(--ls-text-tertiary)]">{t("Immutable content SHA-256")}</dt>
                        <dd className="mt-1 break-all font-mono text-[var(--ls-text-secondary)]">{approval.content_sha256}</dd>
                      </div>
                      <div>
                        <dt className="text-[var(--ls-text-tertiary)]">{t("Version state")}</dt>
                        <dd className="mt-1 text-[var(--ls-text-secondary)]">{status(approval.version_state)}</dd>
                      </div>
                    </dl>

                    {approval.state === "pending" ? (
                      approval.can_decide ? (
                        <div className="mt-4">
                          <label className="text-xs font-medium text-[var(--ls-text-secondary)]" htmlFor={`approval-comment-${approval.id}`}>
                            {t(" Review note ")}<span className="font-normal text-[var(--ls-text-tertiary)]">{t("(optional)")}</span>
                          </label>
                          <textarea
                            className="luminous-focus mt-2 min-h-20 w-full resize-y rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-2 text-sm leading-6 text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)]"
                            disabled={!enabled || busy !== undefined}
                            id={`approval-comment-${approval.id}`}
                            maxLength={2000}
                            placeholder={t("Record the evidence behind this decision.")}
                          />
                          <div className="mt-3 flex flex-wrap gap-2">
                            <button
                              className="luminous-focus inline-flex h-8 items-center gap-1.5 rounded-[8px] bg-[var(--ls-success)] px-3 text-xs font-semibold text-white hover:opacity-90 disabled:opacity-45"
                              disabled={!enabled || busy !== undefined}
                              onClick={() => decide(approval, "approved")}
                              type="button"
                            >
                              {busy === decisionPath ? <LoaderCircle className="size-3.5 animate-spin" /> : <Check className="size-3.5" />} {t(" Approve exact version ")}</button>
                            <button
                              className="luminous-focus inline-flex h-8 items-center gap-1.5 rounded-[8px] bg-red-500/10 px-3 text-xs font-semibold text-[var(--ls-critical-text)] hover:bg-red-500/15 disabled:opacity-45"
                              disabled={!enabled || busy !== undefined}
                              onClick={() => decide(approval, "rejected")}
                              type="button"
                            >
                              <X className="size-3.5" /> {t(" Reject to draft ")}</button>
                          </div>
                        </div>
                      ) : (
                        <p className="mt-4 rounded-[8px] border border-[var(--ls-line)] px-3 py-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
                          {t(" Decision unavailable: rule approval permission is required and each reviewer votes once. Requester decisions require the workspace owner to enable rule self-approval in Settings → Approvals. ")}</p>
                      )
                    ) : null}

                    {approval.actor_decision ? (
                      <p className="mt-4 text-xs text-[var(--ls-text-secondary)]">
                        {t(" Your recorded decision: ")}<span className="font-medium text-[var(--ls-text)]">{approval.actor_decision}</span>.
                      </p>
                    ) : null}

                    {approval.can_publish ? (
                      <button
                        className="luminous-focus mt-4 inline-flex h-8 items-center gap-1.5 rounded-[8px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white hover:bg-[var(--ls-accent-hover)] disabled:opacity-45"
                        disabled={!enabled || busy !== undefined}
                        onClick={() => publish(approval)}
                        type="button"
                      >
                        {busy === publishPath ? <LoaderCircle className="size-3.5 animate-spin" /> : <UploadCloud className="size-3.5" />} {t(" Publish approved version ")}</button>
                    ) : null}
                  </div>
                </details>
              </article>
            );
          })}
        </div>
      )}
    </div>
  );
}
