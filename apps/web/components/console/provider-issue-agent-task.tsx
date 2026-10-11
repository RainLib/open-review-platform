"use client";

import { useWorkflowStatus, useWorkflowText } from "@/components/console/ui-language-context";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";
import { Bot } from "lucide-react";
import type { ProviderIssueAnalysisDetail } from "@/lib/control-api";
import { HelpHint } from "@/components/console/help-hint";

export function ProviderIssueAgentTask({
  admission,
  analysisID,
  enabled,
  expectedRevision,
  org,
}: {
  admission?: ProviderIssueAnalysisDetail["agent_admission"];
  analysisID: string;
  enabled: boolean;
  expectedRevision: number;
  org: string;
}) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [error, setError] = useState<string>();
  const existingTaskID = admission?.existing_task_id;
  const conflict = admission?.existing_task_conflict === true;
  const conflictingTasks = conflict ? admission?.existing_tasks ?? [] : [];
  const canRequest = enabled && admission?.can_request === true && !conflict;

  const requestTask = () => startTransition(async () => {
    setError(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/provider-issues/${encodeURIComponent(analysisID)}/agent-task`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ expected_revision: expectedRevision }),
        },
      );
      const result = await response.json() as { id?: string; error?: string };
      if (!response.ok || !result.id) {
        setError(result.error ?? t("The Agent task could not be created. Refresh the Issue and retry."));
        if (response.status === 409) router.refresh();
        return;
      }
      router.push(`/${encodeURIComponent(org)}/agent-work?task=${encodeURIComponent(result.id)}`);
    } catch {
      setError(t("The Agent task could not be created. Check the connection and retry."));
    }
  });

  return (
    <section className="mt-4 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3" aria-label={t("Agent work from this Issue")}>
      <div className="flex items-start gap-2">
        <Bot className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2"><h3 className="text-sm font-semibold text-[var(--ls-text)]">{t("Turn this Issue into Agent work")}</h3><HelpHint label={t("Turn this Issue into Agent work")}>{t(" Creates a candidate from this exact Issue revision. A manual repository policy, source recheck, bounded plan, and owner/admin approval are required before coding can start. ")}</HelpHint></div>
        </div>
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        {existingTaskID ? (
          <Link className="luminous-focus inline-flex h-9 items-center rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white" href={`/${encodeURIComponent(org)}/agent-work?task=${encodeURIComponent(existingTaskID)}`}>
            {t(" Open Agent task · ")}{status(admission?.existing_task_state ?? "received")}
          </Link>
        ) : canRequest ? (
          <button
          className="luminous-focus inline-flex h-9 items-center rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white disabled:cursor-not-allowed disabled:opacity-50"
          disabled={pending}
          onClick={requestTask}
          type="button"
          >
            {pending ? t("Requesting…") : t("Request governed Agent task")}
          </button>
        ) : null}
        <Link className="luminous-focus inline-flex h-9 items-center rounded-[9px] px-2 text-xs font-medium text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/agent-work`}>
          {conflict ? "Open Agent work" : t("View repository policy")}
        </Link>
      </div>
      {!existingTaskID && !canRequest ? (
        <p className="mt-2 text-xs leading-5 text-[var(--ls-text-tertiary)]">
          {conflict
            ? t("This Issue snapshot has multiple historical Agent tasks. No further task can be requested for this revision; inspect both tasks, or edit the Issue to create a new revision for new work.")
            : !enabled || !admission
            ? t("Agent admission status is unavailable in preview or while the control plane is disconnected.")
            : admission.policy_mode !== "manual"
              ? t("Repository Agent policy is {mode}; enable Manual before requesting work.", { mode: t(admission.policy_mode) })
              : !admission.installation_ready
                ? t("This Issue's original provider installation no longer uniquely owns the repository. Reconnect it before requesting work.")
              : t("This account cannot request an Agent task for this Issue. Check your workspace role and provider installation.")}
        </p>
      ) : null}
      {conflictingTasks.length ? (
        <ul className="mt-2 flex flex-wrap gap-2" aria-label={t("Agent tasks for this Issue snapshot")}>
          {conflictingTasks.map((task) => (
            <li key={task.id}>
              <Link className="luminous-focus text-xs font-medium text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/agent-work?task=${encodeURIComponent(task.id)}`}>
                {t(" Task ")}{task.id.slice(0, 8)} · {status(task.state)}
              </Link>
            </li>
          ))}
        </ul>
      ) : null}
      {error ? <p className="mt-2 text-xs leading-5 text-[var(--ls-danger-text)]" role="alert">{error}</p> : null}
    </section>
  );
}
