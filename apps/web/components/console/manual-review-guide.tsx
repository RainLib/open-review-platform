"use client";

import Link from "next/link";
import { ChevronDown, GitPullRequest, ShieldCheck, SquareTerminal, Zap } from "lucide-react";
import type { ReactNode } from "react";

import { CopyEvidenceButton } from "@/components/console/copy-evidence-button";

export function ManualReviewGuide({ org }: { org: string }) {
  return (
    <details className="group overflow-hidden rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]" id="manual-review-guide">
      <summary className="luminous-focus flex cursor-pointer list-none items-center justify-between gap-4 px-4 py-3.5 marker:content-none hover:bg-[var(--ls-surface-muted)]">
        <span className="flex min-w-0 items-center gap-3">
          <span className="grid size-8 shrink-0 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
            <GitPullRequest className="size-4" />
          </span>
          <span className="min-w-0">
            <span className="block text-sm font-semibold text-[var(--ls-text)]">Start a manual review</span>
            <span className="mt-0.5 block text-xs text-[var(--ls-text-secondary)]">Use a verified GitHub pull request or GitLab merge request. No clone URL or commit can be entered here.</span>
          </span>
        </span>
        <ChevronDown className="size-4 shrink-0 text-[var(--ls-text-tertiary)] transition group-open:rotate-180" />
      </summary>
      <div className="border-t border-[var(--ls-line)] px-4 py-4">
        <div className="flex items-center gap-2 text-xs font-medium text-[var(--ls-text)]"><ShieldCheck className="size-3.5 text-[var(--ls-success-text)]" /> GitHub and GitLab</div>
        <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Open the existing PR or MR, add one command as a top-level comment, then Open Review acknowledges it before reading the current provider revision.</p>
        <div className="mt-3 grid gap-3 md:grid-cols-3">
          <ReviewModeCommand
            command="@openreview review --mode=standard"
            description="Balanced default for routine pull requests."
            label="Standard"
          />
          <ReviewModeCommand
            command="@openreview review --mode=deep"
            description="Broader dependency and behavior inspection when the change has a larger blast radius."
            label="Deep"
          />
          <ReviewModeCommand
            command="@openreview review --mode=security"
            description="Security-focused scope. Its admitted execution is high-priority ahead of normal recovery work."
            icon={<Zap className="size-3.5" />}
            label="Security priority"
          />
        </div>
        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-[var(--ls-line)] pt-3">
          <p className="max-w-2xl text-[11px] leading-5 text-[var(--ls-text-tertiary)]">Priority changes scheduling, not evidence standards: each run still uses its immutable revision, policy snapshot, provider verification, and merge-gate conclusion.</p>
          <Link className="luminous-focus inline-flex h-8 items-center gap-1.5 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2.5 text-xs font-medium text-[var(--ls-text-secondary)] shadow-[var(--ls-shadow-control)] hover:text-[var(--ls-text)]" href={`/${encodeURIComponent(org)}/cli-reviews?tab=quickstart`}><SquareTerminal className="size-3.5" /> CLI quickstart</Link>
        </div>
      </div>
    </details>
  );
}

function ReviewModeCommand({
  command,
  description,
  icon,
  label,
}: {
  command: string;
  description: string;
  icon?: ReactNode;
  label: string;
}) {
  return (
    <article className="rounded-[11px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3.5 py-3">
      <div className="flex items-center gap-1.5 text-xs font-semibold text-[var(--ls-text)]">
        {icon}
        {label}
      </div>
      <p className="mt-1 min-h-10 text-[11px] leading-5 text-[var(--ls-text-secondary)]">{description}</p>
      <code className="mt-2 block overflow-x-auto whitespace-nowrap rounded-[8px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-2.5 py-2 font-mono text-[11px] text-[var(--ls-text)]">{command}</code>
      <div className="mt-2">
        <CopyEvidenceButton label="Copy command" value={command} />
      </div>
    </article>
  );
}
