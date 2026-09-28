import Link from "next/link";
import {
  ArrowLeft,
  ArrowUpRight,
  CircleAlert,
  Clock3,
  GitCommitHorizontal,
  GitPullRequest,
  Layers3,
  Radio,
  ShieldCheck,
} from "lucide-react";

import { ReviewFileEvidence } from "@/components/console/review-file-evidence";
import { ReviewActivityTimeline } from "@/components/console/review-activity-timeline";
import { ReviewChecksEvidence } from "@/components/console/review-checks-evidence";
import { ReviewFindingsEvidence } from "@/components/console/review-findings-evidence";
import { ReviewOverviewEvidence } from "@/components/console/review-overview-evidence";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { ProviderMark } from "@/components/providers/provider-icons";
import {
  displayReviewMode,
  getReviewEvidenceData,
  type ReviewEvidence,
  type ReviewRun,
} from "@/lib/control-api";
import { formatTime, shortSHA } from "@/lib/format";
import { reviewChecksCount, validReviewChecksView } from "@/lib/provider-checks";
import { validFindingSeverity, validFindingStatus } from "@/lib/review-findings";
import { validReviewOverviewView } from "@/lib/review-overview";
import { reviewFileRows, validReviewFileView } from "@/lib/review-file-analysis";
import { providerReviewTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";

type ReviewTab = "overview" | "findings" | "files" | "checks" | "activity";

export default async function ReviewDetailPage({ params, searchParams }: { params: Promise<{ org: string; reviewId: string }>; searchParams: Promise<{ tab?: string; finding?: string; view?: string; q?: string; scope?: string; change?: string; actor?: string; severity?: string; status?: string; category?: string; file?: string }> }) {
  const [{ org, reviewId }, query] = await Promise.all([params, searchParams]);
  const data = await getReviewEvidenceData(org, reviewId);
  const evidence = data.evidence;

  if (!evidence) {
    return <div className="space-y-5"><BackLink org={org} /><section className="grid min-h-[420px] place-items-center rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-8 text-center"><div><CircleAlert className="mx-auto size-7 text-[var(--ls-text-tertiary)]" /><h1 className="mt-4 text-lg font-semibold text-[var(--ls-text)]">Review evidence unavailable</h1><p className="mx-auto mt-2 max-w-lg text-sm leading-6 text-[var(--ls-text-secondary)]">{data.detail ?? "The run may have been removed or you may not have workspace access."}</p></div></section></div>;
  }

  const run = evidence.run;
  const focusedFinding = evidence.findings.some((finding) => finding.id === query.finding) ? query.finding : undefined;
  const tab = focusedFinding ? "findings" : validTab(query.tab);
  const provider = providerReviewTarget(run);
  const files = reviewFileRows(evidence);
  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-4"><BackLink org={org} /><span className="flex items-center gap-2 text-xs text-[var(--ls-text-tertiary)]"><span className={cn("size-2 rounded-full", data.source === "live" ? "bg-[var(--ls-success)]" : "bg-[var(--ls-warning)]")} />{data.source === "live" ? "Immutable live evidence" : data.source}</span></div>
      <header className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_auto] xl:items-end">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2 text-xs font-medium text-[var(--ls-accent)]"><ProviderMark className="size-4" provider={run.provider} /><span>{run.repository}</span><span className="text-[var(--ls-text-tertiary)]">/</span><span>Pull request #{run.review_number}</span></div>
          <h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">{run.title || "Review evidence"}</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">{run.author ? <>Opened by <span className="font-medium text-[var(--ls-text)]">{run.author}</span> · </> : null}Exact head <span className="font-mono text-[var(--ls-text)]">{shortSHA(run.head_sha)}</span>, durable run <span className="font-mono text-[var(--ls-text)]">{run.id.slice(0, 8)}</span>, and all retained publication evidence.</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Link className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-medium text-[var(--ls-text-secondary)] shadow-[var(--ls-shadow-control)] hover:text-[var(--ls-text)]" href={`/${org}/tasks/${run.id}`}><Radio className="size-4" />Run detail</Link>
          {provider ? <a className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-medium text-white shadow-[var(--ls-shadow-control)]" href={provider.url} rel="noreferrer" target="_blank">Open in {provider.label}<ArrowUpRight className="size-4" /></a> : null}
        </div>
      </header>

      <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="Review evidence views">
        <TabLink href={`/${org}/reviews/${run.id}?tab=overview`} label="Overview" selected={tab === "overview"} />
        <TabLink count={evidence.findings.length} href={`/${org}/reviews/${run.id}?tab=findings`} label="Findings" selected={tab === "findings"} />
        <TabLink count={files.length} href={`/${org}/reviews/${run.id}?tab=files`} label="Files" selected={tab === "files"} />
        <TabLink count={reviewChecksCount(evidence).all} href={`/${org}/reviews/${run.id}?tab=checks`} label="Checks" selected={tab === "checks"} />
        <TabLink count={evidence.events.length} href={`/${org}/reviews/${run.id}?tab=activity`} label="Activity" selected={tab === "activity"} />
      </TabStateRouter>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
        <section aria-label="Selected review evidence" className="min-w-0 rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 sm:p-6">
          {tab === "overview" ? <ReviewOverviewEvidence evidence={evidence} hasRuleSnapshot={Boolean(data.ruleSnapshot)} org={org} view={validReviewOverviewView(query.view)} /> : null}
          {tab === "findings" ? <ReviewFindingsEvidence evidence={evidence} focusedFinding={focusedFinding} org={org} filters={{ severity: validFindingSeverity(query.severity), status: validFindingStatus(query.status), category: query.category ?? "", file: query.file ?? "", query: (query.q ?? "").slice(0, 200) }} /> : null}
          {tab === "files" ? <ReviewFileEvidence evidence={evidence} files={files} org={org} view={validReviewFileView(query.view)} query={query.q} scope={query.scope} change={query.change} /> : null}
          {tab === "checks" ? <ReviewChecksEvidence evidence={evidence} org={org} view={validReviewChecksView(query.view)} /> : null}
          {tab === "activity" ? <ReviewActivityTimeline initialEvents={evidence.events} org={org} runID={run.id} streamEnabled={data.source === "live"} query={query.q} actor={query.actor} /> : null}
        </section>
        <ReviewContext evidence={evidence} hasRuleSnapshot={Boolean(data.ruleSnapshot)} org={org} />
      </div>
    </div>
  );
}






function ReviewContext({ evidence, hasRuleSnapshot, org }: { evidence: ReviewEvidence; hasRuleSnapshot: boolean; org: string }) {
  const run = evidence.run;
  const recentRuns = evidence.related_runs.slice(0, 5);
  const olderRuns = evidence.related_runs.slice(5);
  return <aside className="luminous-frosted h-fit rounded-[24px] border border-[var(--ls-line-strong)] p-5 shadow-[var(--ls-shadow-float)] xl:sticky xl:top-20"><h2 className="text-lg font-semibold text-[var(--ls-text)]">Review context</h2><dl className="mt-4 space-y-3"><ContextRow icon={GitPullRequest} label="State" value={run.state.replaceAll("_", " ")} /><ContextRow icon={GitCommitHorizontal} label="Head" mono value={shortSHA(run.head_sha)} /><ContextRow icon={GitCommitHorizontal} label="Base" mono value={shortSHA(run.base_sha)} /><ContextRow icon={Layers3} label="Mode" value={displayReviewMode(run.review_mode)} /><ContextRow icon={ShieldCheck} label="Rules" value={hasRuleSnapshot ? "Immutable snapshot" : "Not attached"} /><ContextRow icon={Clock3} label="Started" value={formatTime(run.started_at ?? run.created_at)} /></dl><div className="mt-5 border-t border-[var(--ls-line)] pt-4"><p className="text-xs font-semibold text-[var(--ls-text)]">Run history</p><div className="mt-3 space-y-2">{recentRuns.map((related) => <RunHistoryLink currentID={run.id} key={related.id} org={org} run={related} />)}</div>{olderRuns.length ? <details className="mt-2"><summary className="luminous-focus cursor-pointer list-none rounded-[9px] px-2 py-2 text-xs font-medium text-[var(--ls-accent)] [&::-webkit-details-marker]:hidden">Show {olderRuns.length} older runs</summary><div className="mt-1 max-h-64 space-y-1 overflow-y-auto pr-1">{olderRuns.map((related) => <RunHistoryLink currentID={run.id} key={related.id} org={org} run={related} />)}</div></details> : null}</div><p className="mt-5 border-t border-[var(--ls-line)] pt-4 text-xs leading-5 text-[var(--ls-text-tertiary)]">A newer revision cannot reuse this result. Supersession preserves this page for audit while preventing stale publication.</p></aside>;
}

function RunHistoryLink({ currentID, org, run }: { currentID: string; org: string; run: ReviewRun }) { return <Link className={cn("luminous-focus flex items-center justify-between rounded-[9px] px-2 py-2 text-xs", run.id === currentID ? "bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(run.id)}`}><span className="font-mono">{shortSHA(run.head_sha)}</span><span className="capitalize">{run.state.replaceAll("_", " ")}</span></Link>; }

function ContextRow({ icon: Icon, label, mono = false, value }: { icon: typeof GitPullRequest; label: string; mono?: boolean; value: string }) { return <div className="grid grid-cols-[18px_72px_minmax(0,1fr)] items-start gap-2 text-xs"><Icon className="mt-0.5 size-3.5 text-[var(--ls-text-tertiary)]" /><dt className="text-[var(--ls-text-tertiary)]">{label}</dt><dd className={cn("min-w-0 break-all text-right capitalize text-[var(--ls-text)]", mono && "font-mono normal-case")}>{value}</dd></div>; }
function TabLink({ count, href, label, selected }: { count?: number; href: string; label: string; selected: boolean }) { return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative inline-flex h-11 shrink-0 items-center gap-2 rounded-t-[10px] px-4 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={href} role="tab" tabIndex={selected ? 0 : -1}>{label}{count !== undefined ? <span className="rounded-full bg-[var(--ls-surface-muted)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)]">{count}</span> : null}{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>; }
function BackLink({ org }: { org: string }) { return <Link className="luminous-focus inline-flex items-center gap-2 rounded text-sm text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]" href={`/${org}/reviews`}><ArrowLeft className="size-4" />Back to pull requests</Link>; }

function validTab(value?: string): ReviewTab { return value === "findings" || value === "files" || value === "checks" || value === "activity" ? value : "overview"; }
