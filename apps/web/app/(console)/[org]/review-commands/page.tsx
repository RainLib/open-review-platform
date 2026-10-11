import Link from "next/link";
import {
  ArrowUpRight,
  CircleHelp,
  GitPullRequest,
  Keyboard,
  RotateCcw,
  SearchCheck,
  ShieldCheck,
  SquareTerminal,
  XCircle,
} from "lucide-react";

import { CopyEvidenceButton } from "@/components/console/copy-evidence-button";
import { HelpHint } from "@/components/console/help-hint";

type CommandCardProps = {
  command: string;
  description: string;
  label: string;
};

export default async function ReviewCommandsPage({
  params,
}: {
  params: Promise<{ org: string }>;
}) {
  const { org } = await params;
  const workspacePath = `/${encodeURIComponent(org)}`;

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <header className="flex flex-col gap-5 border-b border-[var(--ls-line)] pb-6 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">
            Provider operations
          </p>
          <div className="mt-2 flex min-w-0 items-center gap-2"><h1 className="text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">
            Review commands & shortcuts
          </h1><HelpHint label="Review commands & shortcuts">Start and recover a review from a GitHub pull request or GitLab merge request. Every command is acknowledged first, then runs against the provider&apos;s current revision and the admitted policy snapshot.</HelpHint></div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Link
            className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-xs font-semibold text-[var(--ls-text)] shadow-[var(--ls-shadow-control)] hover:bg-[var(--ls-surface-muted)]"
            href={`${workspacePath}/reviews`}
          >
            <GitPullRequest className="size-3.5" /> Review runs
          </Link>
          <Link
            className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-3.5 text-xs font-semibold text-white shadow-[var(--ls-shadow-control)] hover:bg-[var(--ls-accent-hover)]"
            href={`${workspacePath}/cli-reviews?tab=quickstart`}
          >
            <SquareTerminal className="size-3.5" /> CLI quickstart
          </Link>
        </div>
      </header>

      <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
        <div className="flex items-start gap-3">
          <span className="grid size-9 shrink-0 place-items-center rounded-[11px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
            <GitPullRequest className="size-4" />
          </span>
          <div>
            <div className="flex min-w-0 items-center gap-2"><h2 className="text-base font-semibold text-[var(--ls-text)]">Start a provider review</h2><HelpHint label="Start a provider review">Post one command as a top-level comment on the target PR or MR. Open Review verifies repository scope and permissions before it creates a durable run; a command in quoted text never starts work.</HelpHint></div>
          </div>
        </div>
        <div className="mt-5 grid gap-3 lg:grid-cols-3">
          <CommandCard command="@openreview review --mode=standard" description="Balanced default for routine changes." label="Standard" />
          <CommandCard command="@openreview review --mode=deep" description="Broader dependency and behavior inspection for larger impact." label="Deep" />
          <CommandCard command="@openreview review --mode=security" description="Security-prioritized execution without relaxing evidence standards." label="Security" />
        </div>
        <div className="mt-4 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
          <strong className="text-[var(--ls-text)]">Need an intentional re-run?</strong>{" "}
          Use <code className="rounded bg-[var(--ls-surface)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--ls-text)]">@openreview review --force</code>. A newer provider revision still supersedes prior evidence.
        </div>
      </section>

      <div className="grid gap-5 lg:grid-cols-2">
        <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
          <div className="flex items-center gap-2">
            <SearchCheck className="size-4 text-[var(--ls-accent)]" />
            <h2 className="text-base font-semibold text-[var(--ls-text)]">Inspect and recover</h2>
          </div>
          <div className="mt-4 space-y-3">
            <CommandCard command="@openreview status [review-run-id]" description="Show the current review state. Omit the ID for the PR or MR&apos;s current run." label="Status" />
            <CommandCard command="@openreview retry [review-run-id]" description="Retry after the recorded failure or intervention is resolved." label="Retry" />
            <CommandCard command="@openreview cancel [review-run-id]" description="Request cancellation for an active run in the current PR or MR." label="Cancel" />
            <CommandCard command="@openreview explain <finding-id>" description="Ask for evidence about one published finding. A finding ID is required." label="Explain a finding" />
          </div>
        </section>

        <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
          <div className="flex items-center gap-2">
            <Keyboard className="size-4 text-[var(--ls-accent)]" />
            <h2 className="text-base font-semibold text-[var(--ls-text)]">Console shortcuts</h2>
          </div>
          <dl className="mt-4 divide-y divide-[var(--ls-line)] rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)]">
            <Shortcut icon={Keyboard} keys="⌘ K / Ctrl K" title="Open command palette">Jump to review runs, policies, integrations, and workspace actions.</Shortcut>
            <Shortcut icon={XCircle} keys="Esc" title="Close command palette">Returns focus to the element that opened the palette.</Shortcut>
          </dl>
          <div className="mt-5 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
            <div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><CircleHelp className="size-4 text-[var(--ls-accent)]" /> Need the command list in a PR or MR?</div>
            <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">Comment <code className="rounded bg-[var(--ls-surface)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--ls-text)]">@openreview help</code>. It is informational only and does not create a review.</p>
          </div>
          <div className="mt-5 flex flex-wrap gap-2">
            <Link className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs font-semibold text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" href={`${workspacePath}/tasks`}><RotateCcw className="size-3.5" /> Work queue</Link>
            <Link className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs font-semibold text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" href={`${workspacePath}/review-config/general`}><ShieldCheck className="size-3.5" /> Review settings <ArrowUpRight className="size-3" /></Link>
          </div>
        </section>
      </div>
    </div>
  );
}

function CommandCard({ command, description, label }: CommandCardProps) {
  return (
    <article className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3.5">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold text-[var(--ls-text)]">{label}</h3>
          <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{description}</p>
        </div>
        <CopyEvidenceButton label="Copy command" value={command} />
      </div>
      <code className="mt-3 block overflow-x-auto whitespace-nowrap rounded-[8px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-2.5 py-2 font-mono text-[11px] text-[var(--ls-text)]">{command}</code>
    </article>
  );
}

function Shortcut({ children, icon: Icon, keys, title }: { children: React.ReactNode; icon: typeof Keyboard; keys: string; title: string }) {
  return <div className="flex items-start gap-3 p-3.5"><span className="grid size-8 shrink-0 place-items-center rounded-[9px] bg-[var(--ls-surface)] text-[var(--ls-accent)]"><Icon className="size-3.5" /></span><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center justify-between gap-2"><dt className="text-sm font-semibold text-[var(--ls-text)]">{title}</dt><kbd className="rounded border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2 py-1 font-mono text-[11px] text-[var(--ls-text-secondary)]">{keys}</kbd></div><dd className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{children}</dd></div></div>;
}
