import Link from "next/link";
import {
  ArrowLeft,
  Braces,
  CalendarClock,
  CircleAlert,
  GitBranch,
  GitCommitHorizontal,
  History,
  Layers3,
  ShieldCheck,
} from "lucide-react";

import { CopyEvidenceButton } from "@/components/console/copy-evidence-button";
import { FindingNarrative } from "@/components/console/finding-narrative";
import { IssueActions } from "@/components/console/issue-actions";
import { TabStateRouter } from "@/components/console/tab-state-router";
import {
  IssueStatusBadge,
  ProviderFileAnchor,
  ProviderReviewAnchor,
  SeverityBadge,
  formatIssueAge,
  formatIssueTime,
  issuePrompt,
  shortIssueID,
} from "@/components/console/issue-primitives";
import { getIssueDetailData, getMemberData, type IssueDetail, type IssueOccurrence } from "@/lib/control-api";
import { issueDisplaySummary, issueDisplayTitle } from "@/lib/issue-filters";
import { cn } from "@/lib/utils";

type DetailTab = "evidence" | "occurrences" | "pull-requests" | "timeline";

export default async function IssueDetailPage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string; issueId: string }>;
  searchParams: Promise<{ tab?: string }>;
}) {
  const [{ org, issueId }, query] = await Promise.all([params, searchParams]);
  const [data, memberData] = await Promise.all([
    getIssueDetailData(org, issueId),
    getMemberData(org),
  ]);
  const issue = data.issue;
  const tab = validTab(query.tab);

  if (!issue) {
    return (
      <div className="space-y-5">
        <BackLink org={org} />
        <section className="grid min-h-[420px] place-items-center rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-8 text-center">
          <div>
            <CircleAlert className="mx-auto size-7 text-[var(--ls-text-tertiary)]" />
            <h1 className="mt-4 text-lg font-semibold text-[var(--ls-text)]">Issue evidence unavailable</h1>
            <p className="mx-auto mt-2 max-w-lg text-sm leading-6 text-[var(--ls-text-secondary)]">{data.detail ?? "The issue may have been removed or you may not have workspace access."}</p>
          </div>
        </section>
      </div>
    );
  }

  const latest = issue.occurrences[0];
  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between"><BackLink org={org} /><span className="text-xs text-[var(--ls-text-tertiary)]">{data.source === "live" ? "Live evidence" : data.source}</span></div>
      <header>
        <div className="flex flex-col gap-5 xl:flex-row xl:items-start xl:justify-between">
          <div className="min-w-0">
            <p className="text-xs font-medium text-[var(--ls-accent)]">Issues / {shortIssueID(issue.id)}</p>
            <h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">{issueDisplayTitle(issue.body_preview)}</h1>
            <p className="mt-1 max-w-4xl text-sm leading-6 text-[var(--ls-text-secondary)]">{issueDisplaySummary(latest?.body ?? issue.body_preview)}</p>
            <div className="mt-3 flex flex-wrap items-center gap-3"><SeverityBadge severity={issue.severity} /><span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">{issue.category}</span><IssueStatusBadge status={issue.status} /><span className="font-mono text-xs text-[var(--ls-accent)]">{issue.repository}</span></div>
          </div>
          <div className="flex w-full max-w-xl flex-col items-stretch gap-2 xl:items-end">
            {data.source === "live" ? <IssueActions key={issue.revision} issue={issue} members={memberData.members} org={org} /> : <p className="w-full rounded-[12px] border border-sky-500/20 bg-sky-500/[0.06] px-3 py-2.5 text-xs leading-5 text-[var(--ls-text-secondary)]">Read-only preview: assignment, disposition, exception requests, and provider Issue automation are available only from a live workspace.</p>}
            {latest ? <ProviderReviewAnchor issue={issue} occurrence={latest} /> : null}
          </div>
        </div>
      </header>

      <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="Issue detail views">
        <DetailTabLink href={`/${org}/issues/${issue.id}?tab=evidence`} label="Evidence" selected={tab === "evidence"} />
        <DetailTabLink count={issue.occurrence_count} href={`/${org}/issues/${issue.id}?tab=occurrences`} label="Occurrences" selected={tab === "occurrences"} />
        <DetailTabLink count={issue.pull_request_count} href={`/${org}/issues/${issue.id}?tab=pull-requests`} label="Pull requests" selected={tab === "pull-requests"} />
        <DetailTabLink href={`/${org}/issues/${issue.id}?tab=timeline`} label="Timeline" selected={tab === "timeline"} />
      </TabStateRouter>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_360px]">
        <section aria-label="Selected issue detail" className="min-w-0 rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 sm:p-6">
          {tab === "evidence" ? <EvidenceTab issue={issue} /> : null}
          {tab === "occurrences" ? <OccurrencesTab issue={issue} /> : null}
          {tab === "pull-requests" ? <PullRequestsTab issue={issue} /> : null}
          {tab === "timeline" ? <TimelineTab issue={issue} /> : null}
        </section>
        <div className="space-y-4">
          <IssueContext issue={issue} />
          {latest?.rule_attributions?.length ? <RuleProvenance org={org} attributions={latest.rule_attributions} /> : null}
        </div>
      </div>
    </div>
  );
}

function EvidenceTab({ issue }: { issue: IssueDetail }) {
  const latest = issue.occurrences[0];
  if (!latest) return <EmptyTab detail="No occurrence evidence is attached to this issue." title="No evidence" />;
  const prompt = issuePrompt(issue, latest);
  return (
    <div className="space-y-6">
      <div className="grid gap-5 border-b border-[var(--ls-line)] pb-5 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div><h2 className="text-sm font-semibold text-[var(--ls-text)]">Summary</h2><p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">{issueDisplaySummary(latest.body)}</p></div>
        <div className="border-l-0 border-[var(--ls-line)] lg:border-l lg:pl-5"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Current impact</h2><ul className="mt-2 space-y-2 text-sm text-[var(--ls-text-secondary)]"><li>{issue.active_occurrence_count} active of {issue.occurrence_count} occurrences</li><li>{issue.pull_request_count} affected pull requests</li><li className="capitalize">Highest severity: {issue.severity}</li></ul></div>
      </div>
      <section className="overflow-hidden rounded-[12px] border border-[var(--ls-line-strong)]">
        <div className="flex items-center justify-between gap-3 border-b border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2.5"><ProviderFileAnchor issue={issue} occurrence={latest} /><CopyEvidenceButton label="Copy path" value={`${latest.path}:${latest.start_line}${latest.end_line > latest.start_line ? `-${latest.end_line}` : ""}`} /></div>
        <div className="p-4 sm:p-5"><p className="text-xs font-semibold uppercase tracking-[0.12em] text-[var(--ls-text-tertiary)]">Finding evidence</p><FindingNarrative className="mt-3 text-[var(--ls-text)]" text={latest.body} /></div>
      </section>
      <section><h2 className="text-sm font-semibold text-[var(--ls-text)]">Recommended remediation</h2>{latest.suggestion ? <FindingNarrative className="mt-2 text-[var(--ls-text-secondary)]" text={latest.suggestion} /> : <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">No suggested patch was supplied by the review engine. Use the evidence and repository context to prepare a focused correction.</p>}</section>
      <details className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)]"><summary className="luminous-focus flex cursor-pointer list-none items-center gap-2 rounded-[12px] px-4 py-3 text-sm font-medium text-[var(--ls-text)] [&::-webkit-details-marker]:hidden"><Braces className="size-4 text-[var(--ls-accent)]" />Prompt for LLM<span className="ml-auto text-xs text-[var(--ls-text-tertiary)]">Derived from retained evidence</span></summary><div className="border-t border-[var(--ls-line)] p-4"><div className="flex justify-end"><CopyEvidenceButton value={prompt} /></div><pre className="mt-3 overflow-x-auto whitespace-pre-wrap font-mono text-xs leading-6 text-[var(--ls-text-secondary)]">{prompt}</pre></div></details>
      <p className="text-xs leading-5 text-[var(--ls-text-tertiary)]">Private chain-of-thought is not stored or shown. This view contains only retained finding evidence and a deterministic, copyable task prompt.</p>
    </div>
  );
}

function OccurrencesTab({ issue }: { issue: IssueDetail }) {
  if (!issue.occurrences.length) return <EmptyTab detail="No occurrence records are attached to this issue." title="No occurrences" />;
  return <div><h2 className="text-lg font-semibold text-[var(--ls-text)]">Occurrences</h2><p className="mt-1 text-sm text-[var(--ls-text-secondary)]">Chronological evidence for this stable fingerprint.</p><div className="mt-5 overflow-hidden rounded-[12px] border border-[var(--ls-line)]"><div className="hidden grid-cols-[54px_170px_110px_minmax(180px,1fr)_100px] gap-3 border-b border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-4 py-3 text-xs font-medium text-[var(--ls-text-tertiary)] md:grid"><span>#</span><span>Detected</span><span>Revision</span><span>Pull request / file</span><span>State</span></div>{issue.occurrences.map((occurrence, index) => <div className="grid gap-2 border-b border-[var(--ls-line)] px-4 py-4 last:border-0 md:grid-cols-[54px_170px_110px_minmax(180px,1fr)_100px] md:items-center md:gap-3" key={occurrence.id}><span className="text-xs tabular-nums text-[var(--ls-text-tertiary)]">{index + 1}</span><time className="text-xs text-[var(--ls-text-secondary)]" dateTime={occurrence.created_at}>{formatIssueTime(occurrence.created_at)}</time><span className="font-mono text-xs text-[var(--ls-accent)]">{occurrence.head_sha.slice(0, 8)}</span><div className="min-w-0 space-y-1"><ProviderReviewAnchor compact issue={issue} occurrence={occurrence} /><div><ProviderFileAnchor issue={issue} occurrence={occurrence} /></div></div><span className={cn("w-fit rounded-full px-2 py-1 text-xs font-medium", occurrence.active ? "bg-violet-500/10 text-[var(--ls-accent)]" : "bg-emerald-500/10 text-[var(--ls-success-text)]")}>{occurrence.active ? "Active" : "Resolved"}</span></div>)}</div></div>;
}

function PullRequestsTab({ issue }: { issue: IssueDetail }) {
  const pullRequests = groupPullRequests(issue.occurrences);
  if (!pullRequests.length) return <EmptyTab detail="No pull request references are attached to this issue." title="No pull requests" />;
  return <div><h2 className="text-lg font-semibold text-[var(--ls-text)]">Affected pull requests</h2><p className="mt-1 text-sm text-[var(--ls-text-secondary)]">Provider links and exact revisions where this issue was detected.</p><div className="mt-5 divide-y divide-[var(--ls-line)] overflow-hidden rounded-[12px] border border-[var(--ls-line)]">{pullRequests.map((occurrence) => <article className="grid gap-4 p-4 sm:grid-cols-[minmax(0,1fr)_120px_120px] sm:items-center" key={occurrence.review_number}><div><ProviderReviewAnchor issue={issue} occurrence={occurrence} /><div className="mt-2"><ProviderFileAnchor issue={issue} occurrence={occurrence} /></div></div><div><p className="text-[11px] text-[var(--ls-text-tertiary)]">Revision</p><p className="mt-1 font-mono text-xs text-[var(--ls-text)]">{occurrence.head_sha.slice(0, 10)}</p></div><div><p className="text-[11px] text-[var(--ls-text-tertiary)]">Latest detection</p><p className="mt-1 text-xs text-[var(--ls-text)]">{formatIssueAge(occurrence.created_at)}</p></div></article>)}</div></div>;
}

function TimelineTab({ issue }: { issue: IssueDetail }) {
  const items = [
    ...issue.events.map((event) => ({
      id: `workflow-${event.id}`,
      at: event.created_at,
      tone: "workflow" as const,
      title: workflowEventTitle(event.action),
      detail: workflowEventDetail(event),
      actor: event.actor_subject,
    })),
    ...issue.occurrences.map((occurrence) => ({
      id: `occurrence-${occurrence.id}`,
      at: occurrence.created_at,
      tone: occurrence.active ? ("active" as const) : ("resolved" as const),
      title: `${occurrence.active ? "Detected" : "Previously detected"} in pull request #${occurrence.review_number}`,
      detail: occurrence.body,
      occurrence,
    })),
  ].sort((left, right) => Date.parse(right.at) - Date.parse(left.at));
  if (!items.length) return <EmptyTab detail="No auditable occurrence or workflow events are attached to this issue." title="No timeline events" />;
  return <div><h2 className="text-lg font-semibold text-[var(--ls-text)]">Issue timeline</h2><p className="mt-1 text-sm text-[var(--ls-text-secondary)]">Detection, assignment, resolution and disposition decisions are retained as immutable evidence.</p><ol className="mt-6 border-l border-[var(--ls-line-strong)] pl-6">{items.map((item, index) => <li className="relative border-b border-[var(--ls-line)] py-5 first:pt-0 last:border-0" key={item.id}><span className={cn("absolute -left-[29px] top-6 size-2.5 rounded-full ring-4 ring-[var(--ls-surface)]", item.tone === "workflow" ? "bg-sky-500" : item.tone === "active" ? "bg-[var(--ls-accent)]" : "bg-[var(--ls-success)]", index === 0 && "top-1")} /><div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between"><div><p className="text-sm font-semibold text-[var(--ls-text)]">{item.title}</p><p className="mt-1 text-sm text-[var(--ls-text-secondary)]">{item.detail}</p>{"actor" in item ? <p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">Actor: <span className="font-mono text-[var(--ls-text-secondary)]">{item.actor}</span></p> : null}{"occurrence" in item ? <div className="mt-2"><ProviderFileAnchor issue={issue} occurrence={item.occurrence} /></div> : null}</div><time className="shrink-0 text-xs text-[var(--ls-text-tertiary)]" dateTime={item.at}>{formatIssueTime(item.at)}</time></div></li>)}</ol></div>;
}

function IssueContext({ issue }: { issue: IssueDetail }) {
  const latest = issue.occurrences[0];
  const external = issue.external_issue;
  return <aside className="luminous-frosted h-fit rounded-[24px] border border-[var(--ls-line-strong)] p-5 shadow-[var(--ls-shadow-float)] xl:sticky xl:top-20"><h2 className="text-lg font-semibold text-[var(--ls-text)]">Issue context</h2><dl className="mt-4 space-y-3"><ContextRow icon={Layers3} label="Repository" value={issue.repository} mono /><ContextRow icon={GitBranch} label="File" value={issue.path} mono /><ContextRow icon={CalendarClock} label="First seen" value={formatIssueTime(issue.first_seen_at)} /><ContextRow icon={History} label="Last seen" value={formatIssueTime(issue.last_seen_at)} /><ContextRow icon={ActivityIcon} label="Occurrences" value={`${issue.occurrence_count} total · ${issue.active_occurrence_count} active`} /><ContextRow icon={ActivityIcon} label="Assignee" value={issue.assignee_subject || "Unassigned"} mono={Boolean(issue.assignee_subject)} /><ContextRow icon={ActivityIcon} label="Revision" value={String(issue.revision)} mono />{issue.disposition_kind ? <ContextRow icon={ShieldCheck} label="Disposition" value={issue.disposition_kind.replaceAll("_", " ")} /> : null}{issue.exception_request ? <ContextRow icon={ShieldCheck} label="Exception" value={`${issue.exception_request.effective_state} · ${issue.exception_request.rule_key}`} /> : null}<ContextRow compact copy icon={ShieldCheck} label="Fingerprint" value={issue.fingerprint} mono /></dl>{external ? <section className="mt-4 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3"><div className="flex items-center justify-between gap-3"><p className="text-[11px] font-semibold uppercase tracking-[0.1em] text-[var(--ls-text-tertiary)]">Provider Issue</p><span className={cn("rounded-full px-2 py-1 text-[10px] font-medium", external.state === "created" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : external.state === "failed" ? "bg-rose-500/10 text-[var(--ls-critical-text)]" : external.state === "cancelled" ? "bg-[var(--ls-surface)] text-[var(--ls-text-secondary)]" : "bg-violet-500/10 text-[var(--ls-accent)]")}>{external.state}</span></div>{external.external_url ? <a className="mt-2 block truncate text-xs font-medium text-[var(--ls-accent)] hover:underline" href={external.external_url} rel="noreferrer" target="_blank">Open {external.provider} Issue #{external.external_id}</a> : <p className="mt-2 text-xs text-[var(--ls-text-secondary)]">{external.state === "failed" ? "Publication needs attention" : external.state === "cancelled" ? "No provider Issue was created" : "Queued for provider publication"}</p>}<p className="mt-2 text-[11px] text-[var(--ls-text-tertiary)]">Trigger: {external.trigger.replaceAll("_", " ")} · {external.attempts} attempt{external.attempts === 1 ? "" : "s"}</p>{external.last_error ? <p className="mt-2 text-[11px] leading-4 text-[var(--ls-warning-text)]">{external.last_error}</p> : null}</section> : null}{issue.disposition_reason ? <div className="mt-4 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3"><p className="text-[11px] font-semibold uppercase tracking-[0.1em] text-[var(--ls-text-tertiary)]">Decision evidence</p><p className="mt-1.5 text-xs leading-5 text-[var(--ls-text-secondary)]">{issue.disposition_reason}</p></div> : null}{latest ? <div className="mt-5 border-t border-[var(--ls-line)] pt-4"><p className="text-xs font-semibold text-[var(--ls-text)]">Latest evidence</p><div className="mt-2"><ProviderFileAnchor issue={issue} occurrence={latest} /></div><div className="mt-2 flex items-center gap-2 text-xs text-[var(--ls-text-secondary)]"><GitCommitHorizontal className="size-3.5" /><span className="font-mono">{latest.head_sha.slice(0, 12)}</span></div></div> : null}</aside>;
}

function RuleProvenance({ org, attributions }: { org: string; attributions: NonNullable<IssueOccurrence["rule_attributions"]> }) {
  return (
    <section aria-label="Rule provenance" className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5">
      <h2 className="text-sm font-semibold text-[var(--ls-text)]">Rule provenance</h2>
      <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Immutable rule versions attributed to this occurrence. The current published version may differ.</p>
      <ul className="mt-4 space-y-3">
        {attributions.map((attribution) => (
          <li className="rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3" key={`${attribution.rule_key}:${attribution.rule_version_id}`}>
            <Link className="luminous-focus text-xs font-semibold text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/rules/${encodeURIComponent(attribution.rule_set_id)}?tab=provenance`}>
              {attribution.rule_set_name}
            </Link>
            <p className="mt-1 break-all font-mono text-xs text-[var(--ls-text)]">{attribution.rule_key} · v{attribution.version}</p>
            <div className="mt-2 flex items-center justify-between gap-2 text-[11px] text-[var(--ls-text-tertiary)]">
              <span title={attribution.rule_version_id}>Version {attribution.rule_version_id.slice(0, 8)}</span>
              <CopyEvidenceButton label="Copy exact rule version ID" value={attribution.rule_version_id} />
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}

const ActivityIcon = History;
function ContextRow({ icon: Icon, label, mono = false, value, compact = false, copy = false }: { icon: typeof Layers3; label: string; mono?: boolean; value: string; compact?: boolean; copy?: boolean }) { return <div className="grid grid-cols-[18px_92px_minmax(0,1fr)] items-start gap-2 text-xs"><Icon className="mt-0.5 size-3.5 text-[var(--ls-text-tertiary)]" /><dt className="text-[var(--ls-text-tertiary)]">{label}</dt><dd className={cn("flex min-w-0 items-start gap-1 text-[var(--ls-text)]", mono && "font-mono")}><span className={cn("min-w-0 break-all", compact && "truncate")} title={compact ? value : undefined}>{compact && value.length > 28 ? `${value.slice(0, 15)}…${value.slice(-8)}` : value}</span>{copy ? <CopyEvidenceButton label={`Copy ${label.toLowerCase()}`} value={value} /> : null}</dd></div>; }
function DetailTabLink({ count, href, label, selected }: { count?: number; href: string; label: string; selected: boolean }) { return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative inline-flex h-11 shrink-0 items-center gap-2 rounded-t-[10px] px-4 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={href} role="tab" tabIndex={selected ? 0 : -1}>{label}{count !== undefined ? <span className="rounded-full bg-[var(--ls-surface-muted)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)]">{count}</span> : null}{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>; }
function EmptyTab({ detail, title }: { detail: string; title: string }) { return <div className="grid min-h-80 place-items-center text-center"><div><CircleAlert className="mx-auto size-6 text-[var(--ls-text-tertiary)]" /><h2 className="mt-3 text-sm font-semibold text-[var(--ls-text)]">{title}</h2><p className="mt-2 text-sm text-[var(--ls-text-secondary)]">{detail}</p></div></div>; }
function BackLink({ org }: { org: string }) { return <Link className="luminous-focus inline-flex items-center gap-2 rounded text-sm text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]" href={`/${org}/issues`}><ArrowLeft className="size-4" />Back to issue inbox</Link>; }
function validTab(value?: string): DetailTab { return value === "occurrences" || value === "pull-requests" || value === "timeline" ? value : "evidence"; }
function groupPullRequests(occurrences: IssueOccurrence[]) { const byNumber = new Map<number, IssueOccurrence>(); for (const occurrence of occurrences) if (!byNumber.has(occurrence.review_number)) byNumber.set(occurrence.review_number, occurrence); return [...byNumber.values()]; }
function workflowEventTitle(action: IssueDetail["events"][number]["action"]) { return ({ assigned: "Issue assigned", unassigned: "Issue unassigned", resolved: "Issue resolved", reopened: "Issue reopened", false_positive: "Marked as false positive", suppression_cleared: "Suppression cleared", exception_approved: "Rule exception approved", exception_revoked: "Rule exception revoked", exception_expired: "Rule exception expired" } satisfies Record<typeof action, string>)[action]; }
function workflowEventDetail(event: IssueDetail["events"][number]) { if (event.reason) return event.reason; if (event.action === "assigned") return `Assigned to ${event.next_assignee}.`; if (event.action === "unassigned") return `Removed ${event.previous_assignee} as assignee.`; return `Status changed from ${event.previous_status} to ${event.next_status}.`; }
