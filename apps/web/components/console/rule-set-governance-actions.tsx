"use client";

import { useWorkflowStatus, useWorkflowText } from "@/components/console/ui-language-context";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { ArrowRight, LoaderCircle, Send, UploadCloud } from "lucide-react";

import type { RuleSet } from "@/lib/control-api";

export function RuleSetGovernanceActions({
  enabled,
  org,
  ruleSet,
}: {
  enabled: boolean;
  org: string;
  ruleSet: RuleSet;
}) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  const router = useRouter();
  const [requiredApprovals, setRequiredApprovals] = useState(1);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();
  const version = ruleSet.latest_version;

  async function mutate(path: string, body?: Record<string, unknown>) {
    setPending(true);
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
      if (!response.ok) {
        throw new Error(payload.error ?? t("The governance action was rejected."));
      }
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error ? error.message : t("The action could not be completed."),
      );
    } finally {
      setPending(false);
    }
  }

  if (!version) return null;

  const versionPath = `/api/tenants/${encodeURIComponent(org)}/rule-sets/${encodeURIComponent(ruleSet.id)}/versions/${version.version}`;

  return (
    <div className="mt-4 border-t border-[var(--ls-line)] pt-4">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-[var(--ls-text-tertiary)]">
        <span className="rounded-full border border-[var(--ls-line-strong)] px-2 py-0.5 text-[var(--ls-text-secondary)]">
          v{version.version} · {status(version.state)}
        </span>
        <code title={version.content_sha256}>
          sha {version.content_sha256.slice(0, 12)}
        </code>
      </div>

      {version.state === "draft" ? (
        <div className="mt-3 flex flex-col gap-2 sm:flex-row sm:items-center">
          <label className="flex items-center gap-2 text-xs text-[var(--ls-text-secondary)]">
            {t(" Required approvals ")}<select
              className="luminous-focus h-8 rounded-[8px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2 text-xs text-[var(--ls-text)] outline-none"
              disabled={!enabled || pending}
              onChange={(event) => setRequiredApprovals(Number(event.target.value))}
              value={requiredApprovals}
            >
              {[1, 2, 3, 4, 5].map((count) => (
                <option key={count} value={count}>
                  {count}
                </option>
              ))}
            </select>
          </label>
          <button
            className="luminous-focus inline-flex h-8 items-center justify-center gap-1.5 rounded-[8px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white transition hover:bg-[var(--ls-accent-hover)] disabled:cursor-not-allowed disabled:opacity-45 sm:ml-auto"
            disabled={!enabled || pending}
            onClick={() =>
              mutate(`${versionPath}/approval-requests`, {
                required_approvals: requiredApprovals,
              })
            }
            type="button"
          >
            {pending ? <LoaderCircle className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}
            {t(" Submit for approval ")}</button>
        </div>
      ) : version.state === "approved" ? (
        <button
          className="luminous-focus mt-3 inline-flex h-8 items-center gap-1.5 rounded-[8px] bg-emerald-600 px-3 text-xs font-semibold text-white transition hover:bg-emerald-700 disabled:cursor-not-allowed disabled:opacity-45"
          disabled={!enabled || pending}
          onClick={() => mutate(`${versionPath}/publish`)}
          type="button"
        >
          {pending ? <LoaderCircle className="size-3.5 animate-spin" /> : <UploadCloud className="size-3.5" />}
          {t(" Publish approved version ")}</button>
      ) : version.state === "in_review" ? (
        <Link
          className="luminous-focus mt-3 inline-flex items-center gap-1.5 rounded text-xs font-medium text-[var(--ls-accent)] hover:text-[var(--ls-accent-hover)]"
          href={`/${encodeURIComponent(org)}/rules/approvals`}
        >
          {t(" Open approval queue ")}<ArrowRight className="size-3.5" />
        </Link>
      ) : (
        <p className="mt-3 text-xs text-[var(--ls-success-text)]">
          {t(" This immutable version is available for scoped bindings. ")}</p>
      )}

      {message ? (
        <p aria-live="polite" className="mt-2 text-xs leading-5 text-[var(--ls-critical-text)]">
          {message}
        </p>
      ) : null}
    </div>
  );
}
