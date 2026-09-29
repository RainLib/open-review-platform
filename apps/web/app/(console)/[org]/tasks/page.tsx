import Link from "next/link";
import { ArrowLeft, ArrowRight, CalendarClock, CircleAlert, Clock3, ListTodo, Search, SlidersHorizontal } from "lucide-react";

import { ProviderReviewLink } from "@/components/console/provider-review-link";
import { DataFreshness, PageState, RecoveryAction } from "@/components/console/page-state";
import { StatusBadge } from "@/components/console/status-badge";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { CancelReviewScheduleControl } from "@/components/console/review-schedule-controls";
import { ReviewInterventionControls } from "@/components/console/review-intervention-controls";
import { getReviewScheduleData, getWorkQueueData, type ReviewSchedule, type WorkQueueView } from "@/lib/control-api";
import { formatTime, isAttentionRun, shortSHA } from "@/lib/format";
import { cn } from "@/lib/utils";

type QueueTab = "running" | "needs-attention" | "scheduled";
type QueueQuery = {
  tab?: string;
  repository?: string;
  q?: string;
  cursor?: string;
  direction?: string;
};

export default async function TasksPage({ params, searchParams }: { params: Promise<{ org: string }>; searchParams: Promise<QueueQuery> }) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const tab = validTab(query.tab);
  const queueView: WorkQueueView = tab === "needs-attention" ? "needs_attention" : "running";
  const [data, scheduleData] = await Promise.all([
    getWorkQueueData(org, {
      view: queueView,
      repository: query.repository,
      query: query.q,
      cursor: query.cursor,
      direction: query.direction === "before" || query.direction === "after" ? query.direction : undefined,
    }),
    tab === "scheduled" ? getReviewScheduleData(org) : Promise.resolve(null),
  ]);
  const count = tab === "needs-attention" ? data.counts.needs_attention : data.counts.running;
  const displaySource = scheduleData ?? data;
  const queueCountsAvailable = data.source === "live" || data.source === "demo";

  return <div className="space-y-7">
    <header className="flex flex-col justify-between gap-5 lg:flex-row lg:items-end">
      <div><p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">Review operations</p><h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">Work queue</h1><p className="mt-2 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">Live operational runs only. Review evidence and completed history stay in Pull requests, so this queue remains useful when attention is scarce.</p></div>
      <DataFreshness detail={displaySource.detail} state={displaySource.source === "live" ? "live" : displaySource.source === "demo" ? "demo" : "unavailable"} />
    </header>

    <div className="flex flex-col gap-3 border-b border-[var(--ls-line)] xl:flex-row xl:items-end xl:justify-between">
      <TabStateRouter className="flex gap-1 overflow-x-auto" label="Work queue views">
        <QueueTabLink count={queueCountsAvailable ? data.counts.running : undefined} href={queueHref(org, "running", query)} label="Running" selected={tab === "running"} />
        <QueueTabLink count={queueCountsAvailable ? data.counts.needs_attention : undefined} href={queueHref(org, "needs-attention", query)} label="Needs attention" selected={tab === "needs-attention"} />
        <QueueTabLink href={queueHref(org, "scheduled", query)} label="Scheduled" selected={tab === "scheduled"} />
      </TabStateRouter>
      {tab !== "scheduled" ? <QueueFilterForm org={org} query={query} tab={tab} /> : null}
    </div>

    {tab === "scheduled" && scheduleData ? <ScheduledAdmissions data={scheduleData} org={org} /> : data.runs.length === 0 ? <EmptyQueue detail={data.detail} org={org} source={data.source} tab={tab} /> : <>
      <p className="text-xs text-[var(--ls-text-tertiary)]">Showing {data.runs.length} of {count} {tab === "running" ? "running reviews" : "runs that require attention"}.</p>
      <div className="hidden overflow-x-auto rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] md:block">
        <table className="w-full min-w-[920px] text-left text-sm">
          <thead className="border-b border-[var(--ls-line)] bg-[var(--ls-surface-muted)] text-xs text-[var(--ls-text-tertiary)]">
            <tr>
              <th className="px-5 py-3 font-medium" scope="col">Review</th>
              <th className="px-4 py-3 font-medium" scope="col">State</th>
              <th className="px-4 py-3 font-medium" scope="col">Mode</th>
              <th className="px-4 py-3 font-medium" scope="col">Started</th>
              <th className="px-5 py-3 text-right font-medium" scope="col">Next action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[var(--ls-line)]">
            {data.runs.map((run) => <QueueRunRow enabled={data.source === "live"} key={run.id} org={org} run={run} />)}
          </tbody>
        </table>
      </div>
      <div className="grid gap-3 md:hidden">{data.runs.map((run) => <QueueRun enabled={data.source === "live"} key={run.id} org={org} run={run} />)}</div>
      <QueuePager data={data} org={org} query={query} tab={tab} />
    </>}
  </div>;
}

function QueueRunRow({ enabled, org, run }: { enabled: boolean; org: string; run: Awaited<ReturnType<typeof getWorkQueueData>>["runs"][number] }) {
  const attention = Boolean(run.queue_block) || isAttentionRun(run.state);
  const label = run.title || `${run.repository} #${run.review_number}`;
  const href = `/${encodeURIComponent(org)}/tasks/${encodeURIComponent(run.id)}?tab=overview`;
  return <tr className="align-top transition hover:bg-[var(--ls-surface-muted)]">
    <td className="px-5 py-4">
      <div className="flex min-w-0 items-start gap-3">
        <ListTodo aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />
        <div className="min-w-0">
          <Link className="luminous-focus rounded font-semibold text-[var(--ls-text)] hover:text-[var(--ls-accent)]" href={href}>{label}</Link>
          <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">{run.repository} #{run.review_number}{run.author ? ` · ${run.author}` : ""}</p>
          {run.queue_block ? <QueueBlockNotice org={org} run={run} /> : attention ? <p className="mt-1 max-w-[36rem] text-xs leading-5 text-[var(--ls-warning-text)]">{run.failure_message || "Review evidence needs a human decision."}</p> : null}
          <ProviderReviewLink className="mt-1 inline-flex items-center gap-1.5 text-xs text-[var(--ls-accent)] hover:underline" run={run} />
        </div>
      </div>
    </td>
    <td className="px-4 py-4"><div className="flex flex-col items-start gap-1.5">{run.queue_block ? <QueueBlockBadge reason={run.queue_block.reason} /> : null}<StatusBadge state={run.state} /></div></td>
    <td className="px-4 py-4"><ReviewModeBadge mode={run.review_mode} /></td>
    <td className="whitespace-nowrap px-4 py-4 text-xs text-[var(--ls-text-secondary)]">{formatTime(run.started_at ?? run.created_at)}</td>
    <td className="px-5 py-4">
      <div className="flex flex-wrap items-start justify-end gap-2">
        <ReviewInterventionControls enabled={enabled} intervention={run.intervention} org={org} runID={run.id} />
        <Link className="luminous-focus inline-flex h-9 items-center gap-1 rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-semibold text-[var(--ls-text)] hover:border-[var(--ls-accent)] hover:text-[var(--ls-accent)]" href={href}>Open run<ArrowRight className="size-3.5" /></Link>
      </div>
    </td>
  </tr>;
}

function QueueRun({ enabled, org, run }: { enabled: boolean; org: string; run: Awaited<ReturnType<typeof getWorkQueueData>>["runs"][number] }) {
  const attention = Boolean(run.queue_block) || isAttentionRun(run.state);
  const label = run.title || `${run.repository} #${run.review_number}`;
  return <article className="group rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] transition hover:border-[var(--ls-line-strong)]"><div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between"><div className="min-w-0"><div className="flex flex-wrap items-center gap-x-3 gap-y-2"><ListTodo className="size-4 text-[var(--ls-accent)]" /><Link className="luminous-focus truncate rounded text-sm font-semibold text-[var(--ls-text)] hover:text-[var(--ls-accent)]" href={`/${encodeURIComponent(org)}/tasks/${encodeURIComponent(run.id)}?tab=overview`}>{label}</Link><ProviderReviewLink className="inline-flex items-center gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)] hover:text-[var(--ls-accent)]" run={run} /><ReviewModeBadge mode={run.review_mode} /></div><p className="mt-1.5 truncate text-xs text-[var(--ls-text-tertiary)]">{run.repository} #{run.review_number}{run.author ? ` · ${run.author}` : ""}</p>{run.queue_block ? <QueueBlockNotice org={org} run={run} /> : <p className="mt-2 max-w-2xl text-sm leading-6 text-[var(--ls-text-secondary)]">{attention ? run.failure_message || "This review stopped at a safe boundary and needs an explicit human decision." : "The control plane is processing this exact revision through its durable pipeline."}</p>}<div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-[var(--ls-text-tertiary)]"><span className="flex items-center gap-1.5"><Clock3 className="size-3.5" />{formatTime(run.started_at ?? run.created_at)}</span>{attention && !run.queue_block ? <span className="flex items-center gap-1.5 text-[var(--ls-warning-text)]"><CircleAlert className="size-3.5" />Open immutable evidence before retrying</span> : null}</div></div><div className="flex shrink-0 flex-wrap items-start justify-end gap-2"><ReviewInterventionControls compact enabled={enabled} intervention={run.intervention} org={org} runID={run.id} />{run.queue_block ? <QueueBlockBadge reason={run.queue_block.reason} /> : null}<StatusBadge state={run.state} /><Link aria-label={`Open run for ${label}`} className="luminous-focus grid size-8 place-items-center rounded-[8px] text-[var(--ls-text-tertiary)] hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-accent)]" href={`/${encodeURIComponent(org)}/tasks/${encodeURIComponent(run.id)}?tab=overview`}><ArrowRight className="size-4" /></Link></div></div></article>;
}

function QueueBlockBadge({ reason }: { reason: NonNullable<Awaited<ReturnType<typeof getWorkQueueData>>["runs"][number]["queue_block"]>["reason"] }) {
  return <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/10 px-2 py-0.5 text-xs font-medium text-[var(--ls-warning-text)]"><CircleAlert aria-hidden="true" className="size-3" />{reason === "acknowledgement_exhausted" ? "Reply needs attention" : "Connection blocked"}</span>;
}

function QueueBlockNotice({ org, run }: { org: string; run: Awaited<ReturnType<typeof getWorkQueueData>>["runs"][number] }) {
  if (!run.queue_block) return null;
  if (run.queue_block.reason === "acknowledgement_exhausted") {
    return <p className="mt-2 max-w-2xl text-xs leading-5 text-[var(--ls-warning-text)]">The provider progress reply exhausted delivery, so review execution is still held. <Link className="luminous-focus rounded font-semibold underline underline-offset-2" href={`/${encodeURIComponent(org)}/tasks/${encodeURIComponent(run.id)}?tab=overview`}>Open this run</Link> to inspect its reply evidence and available recovery or cancellation actions.</p>;
  }
  const detail = run.queue_block.reason === "installation_inactive"
    ? "This connection is inactive, so a worker cannot claim the review."
    : run.queue_block.verification_state === "failed"
      ? "Provider verification failed, so a worker cannot claim the review."
      : "Provider verification is not complete, so a worker cannot claim the review.";
  return <p className="mt-2 max-w-2xl text-xs leading-5 text-[var(--ls-warning-text)]">{detail} <Link className="luminous-focus rounded font-semibold underline underline-offset-2" href={`/${encodeURIComponent(org)}/connect/${encodeURIComponent(run.queue_block.installation_id)}`}>Inspect connection</Link> and this exact run before taking further action.</p>;
}

function ReviewModeBadge({ mode }: { mode?: string }) {
  const label = mode === "security" ? "Security priority" : mode === "deep" ? "Deep review" : "Standard review";
  const tone = mode === "security"
    ? "bg-rose-500/10 text-rose-700 dark:text-rose-300"
    : mode === "deep"
      ? "bg-violet-500/10 text-violet-700 dark:text-violet-300"
      : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]";
  return <span className={cn("inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-semibold", tone)}>{mode === "security" ? "⚡" : null}{label}</span>;
}

function QueueFilterForm({ org, query, tab }: { org: string; query: QueueQuery; tab: QueueTab }) {
  return <form className="mb-2 flex flex-col gap-2 sm:flex-row" method="get"><input name="tab" type="hidden" value={tab} /><input name="cursor" type="hidden" value="" /><input name="direction" type="hidden" value="" /><label className="flex h-9 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 shadow-[var(--ls-shadow-control)]"><Search aria-hidden="true" className="size-4 text-[var(--ls-text-tertiary)]" /><input aria-label="Search work queue" className="h-full min-w-0 bg-transparent text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)] sm:w-48" defaultValue={query.q} name="q" placeholder="Repository, PR, or failure" /></label><label className="flex h-9 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 shadow-[var(--ls-shadow-control)]"><SlidersHorizontal aria-hidden="true" className="size-4 text-[var(--ls-text-tertiary)]" /><input aria-label="Filter work queue by repository" className="h-full min-w-0 bg-transparent text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)] sm:w-44" defaultValue={query.repository} name="repository" placeholder="Exact repository" /></label><button className="luminous-focus h-9 rounded-[10px] bg-[var(--ls-surface-muted)] px-3 text-sm font-medium text-[var(--ls-text)] transition hover:bg-[var(--ls-accent-soft)]" type="submit">Apply</button><Link className="luminous-focus inline-flex h-9 items-center justify-center rounded-[10px] px-3 text-sm text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]" href={`/${encodeURIComponent(org)}/tasks?tab=${tab}`}>Reset</Link></form>;
}

function QueuePager({ data, org, query, tab }: { data: Awaited<ReturnType<typeof getWorkQueueData>>; org: string; query: QueueQuery; tab: QueueTab }) {
  if (!data.previousCursor && !data.nextCursor) return null;
  return <nav aria-label="Work queue pagination" className="flex items-center justify-between gap-3 border-t border-[var(--ls-line)] pt-4"><span className="text-xs text-[var(--ls-text-tertiary)]">Pages are anchored to durable run creation time.</span><div className="flex gap-2">{data.previousCursor ? <Link className="luminous-focus inline-flex h-9 items-center gap-1.5 rounded-[10px] border border-[var(--ls-line-strong)] px-3 text-sm font-medium text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" href={queueHref(org, tab, query, data.previousCursor, "before")}><ArrowLeft className="size-4" />Previous</Link> : null}{data.nextCursor ? <Link className="luminous-focus inline-flex h-9 items-center gap-1.5 rounded-[10px] border border-[var(--ls-line-strong)] px-3 text-sm font-medium text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" href={queueHref(org, tab, query, data.nextCursor, "after")}>Next<ArrowRight className="size-4" /></Link> : null}</div></nav>;
}

function ScheduledAdmissions({ data, org }: { data: Awaited<ReturnType<typeof getReviewScheduleData>>; org: string }) {
  if (data.schedules.length === 0) { const available = data.source === "live" || data.source === "demo"; return <PageState action={available ? <RecoveryAction href={`/${encodeURIComponent(org)}/reviews`}>Open pull requests</RecoveryAction> : <RecoveryAction href={`/${encodeURIComponent(org)}/connect`} variant="primary">Check connections</RecoveryAction>} detail={data.detail ?? (available ? "Schedule a completed review from its evidence page to re-admit that exact revision later. No runner capacity or provider action is reserved before admission." : "The scheduled-admission ledger could not be verified. No empty result is inferred from this state.")} kind={available ? "first-use-empty" : "unavailable"} title={available ? "No scheduled admissions" : "Scheduled admissions unavailable"} />; }
  return <>
    {data.source === "demo" && data.detail ? <p className="rounded-[12px] border border-amber-500/20 bg-amber-500/[0.06] px-4 py-3 text-xs leading-5 text-[var(--ls-warning-text)]">{data.detail}</p> : null}
    <p className="text-xs text-[var(--ls-text-tertiary)]">Each item is a one-shot, exact-revision admission request. It remains outside the running queue until its scheduled time.</p>
    <div className="hidden overflow-x-auto rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] md:block">
      <table className="w-full min-w-[900px] text-left text-sm">
        <thead className="border-b border-[var(--ls-line)] bg-[var(--ls-surface-muted)] text-xs text-[var(--ls-text-tertiary)]">
          <tr>
            <th className="px-5 py-3 font-medium" scope="col">Review</th>
            <th className="px-4 py-3 font-medium" scope="col">Admission time</th>
            <th className="px-4 py-3 font-medium" scope="col">State</th>
            <th className="px-4 py-3 font-medium" scope="col">Requested by</th>
            <th className="px-5 py-3 text-right font-medium" scope="col">Next action</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-[var(--ls-line)]">
          {data.schedules.map((schedule) => <ScheduledAdmissionRow enabled={data.source === "live"} key={schedule.id} org={org} schedule={schedule} />)}
        </tbody>
      </table>
    </div>
    <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-3 md:hidden">{data.schedules.map((schedule) => <ScheduledAdmissionCard key={schedule.id} org={org} schedule={schedule} enabled={data.source === "live"} />)}</div>
  </>;
}

function ScheduledAdmissionRow({ enabled, org, schedule }: { enabled: boolean; org: string; schedule: ReviewSchedule }) {
  const sourceHref = `/${encodeURIComponent(org)}/tasks/${encodeURIComponent(schedule.source_run_id)}?tab=overview`;
  const admittedHref = schedule.admitted_run_id ? `/${encodeURIComponent(org)}/tasks/${encodeURIComponent(schedule.admitted_run_id)}?tab=overview` : undefined;
  const label = schedule.title || `${schedule.repository} #${schedule.review_number}`;
  return <tr className="align-top transition hover:bg-[var(--ls-surface-muted)]">
    <td className="px-5 py-4">
      <div className="flex items-start gap-3">
        <CalendarClock aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />
        <div className="min-w-0">
          <Link className="luminous-focus rounded font-semibold text-[var(--ls-text)] hover:text-[var(--ls-accent)]" href={sourceHref}>{label}</Link>
          <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">{schedule.repository} #{schedule.review_number} · revision {shortSHA(schedule.head_sha)}</p>
          <p className={cn("mt-1 max-w-[32rem] text-xs leading-5", schedule.state === "blocked" ? "text-[var(--ls-warning-text)]" : "text-[var(--ls-text-secondary)]")}>{scheduleDetail(schedule)}</p>
          <ProviderReviewLink className="mt-1 inline-flex items-center gap-1.5 text-xs text-[var(--ls-accent)] hover:underline" run={schedule} />
        </div>
      </div>
    </td>
    <td className="whitespace-nowrap px-4 py-4 text-xs text-[var(--ls-text-secondary)]">{formatTime(schedule.scheduled_for)}</td>
    <td className="px-4 py-4"><ScheduleStateBadge state={schedule.state} /></td>
    <td className="px-4 py-4 text-xs text-[var(--ls-text-secondary)]">{schedule.requested_by}</td>
    <td className="px-5 py-4"><div className="flex flex-wrap items-start justify-end gap-2">
      {admittedHref ? <Link className="luminous-focus inline-flex h-9 items-center rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-semibold text-[var(--ls-text)] hover:text-[var(--ls-accent)]" href={admittedHref}>Open {schedule.state === "coalesced" ? "existing" : "admitted"} run</Link> : null}
      <CancelReviewScheduleControl enabled={enabled} org={org} schedule={schedule} />
      <Link className="luminous-focus inline-flex h-9 items-center gap-1 rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-semibold text-[var(--ls-text)] hover:text-[var(--ls-accent)]" href={sourceHref}>Source evidence<ArrowRight className="size-3.5" /></Link>
    </div></td>
  </tr>;
}

function scheduleDetail(schedule: ReviewSchedule) {
  switch (schedule.state) {
    case "blocked": return schedule.blocked_reason || "Admission stopped because the source connection is no longer eligible.";
    case "coalesced": return "A matching active review already existed, so no duplicate runner job was created.";
    case "admitted": return "A fresh review was admitted with the policies effective at the scheduled time.";
    case "cancelled": return `Cancelled${schedule.cancelled_by ? ` by ${schedule.cancelled_by}` : ""}.`;
    default: return "No scan, quota reservation, or provider-facing review exists before this time.";
  }
}

function ScheduledAdmissionCard({ enabled, org, schedule }: { enabled: boolean; org: string; schedule: ReviewSchedule }) {
  const sourceHref = `/${encodeURIComponent(org)}/tasks/${encodeURIComponent(schedule.source_run_id)}?tab=overview`;
  const admittedHref = schedule.admitted_run_id ? `/${encodeURIComponent(org)}/tasks/${encodeURIComponent(schedule.admitted_run_id)}?tab=overview` : undefined;
  const label = schedule.title || `${schedule.repository} #${schedule.review_number}`;
  const detail = scheduleDetail(schedule);
  return <article className="min-w-0 overflow-hidden rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]"><div className="flex min-w-0 flex-col gap-4 sm:flex-row sm:items-start sm:justify-between"><div className="min-w-0"><div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2"><CalendarClock className="size-4 shrink-0 text-[var(--ls-accent)]" /><Link className="luminous-focus min-w-0 truncate rounded text-sm font-semibold text-[var(--ls-text)] hover:text-[var(--ls-accent)]" href={sourceHref}>{label}</Link><ProviderReviewLink className="inline-flex items-center gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)] hover:text-[var(--ls-accent)]" run={schedule} /></div><p className="mt-1.5 truncate text-xs text-[var(--ls-text-tertiary)]">{schedule.repository} #{schedule.review_number}{schedule.author ? ` · ${schedule.author}` : ""} · {schedule.review_mode} review</p><p className={cn("mt-2 max-w-2xl text-sm leading-6", schedule.state === "blocked" ? "text-[var(--ls-warning-text)]" : "text-[var(--ls-text-secondary)]")}>{detail}</p><div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-[var(--ls-text-tertiary)]"><span className="flex items-center gap-1.5"><Clock3 className="size-3.5" />{schedule.state === "scheduled" || schedule.state === "blocked" ? `Admission ${formatTime(schedule.scheduled_for)}` : `Requested for ${formatTime(schedule.scheduled_for)}`}</span><span>Revision {shortSHA(schedule.head_sha)}</span><span className="min-w-0 break-all">Requested by {schedule.requested_by}</span>{admittedHref ? <Link className="luminous-focus rounded text-[var(--ls-accent)] hover:underline" href={admittedHref}>{schedule.state === "coalesced" ? "Open existing run" : "Open admitted run"}</Link> : null}<Link className="luminous-focus rounded text-[var(--ls-accent)] hover:underline" href={sourceHref}>Source evidence</Link></div></div><div className="flex shrink-0 items-start gap-2"><ScheduleStateBadge state={schedule.state} /><CancelReviewScheduleControl enabled={enabled} org={org} schedule={schedule} /></div></div></article>;
}

function ScheduleStateBadge({ state }: { state: ReviewSchedule["state"] }) {
  const style = state === "scheduled" ? "bg-sky-500/[0.08] text-sky-700 dark:text-sky-300" : state === "blocked" ? "bg-amber-500/10 text-[var(--ls-warning-text)]" : state === "admitted" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : state === "coalesced" ? "bg-violet-500/[0.08] text-violet-700 dark:text-violet-300" : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]";
  return <span className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium capitalize", style)}><span className="size-1.5 rounded-full bg-current" />{state}</span>;
}
function EmptyQueue({ detail, org, source, tab }: { detail?: string; org: string; source: Awaited<ReturnType<typeof getWorkQueueData>>["source"]; tab: QueueTab }) { const available = source === "live" || source === "demo"; return <PageState action={available ? <RecoveryAction href={`/${encodeURIComponent(org)}/reviews`}>Open pull requests</RecoveryAction> : <RecoveryAction href={`/${encodeURIComponent(org)}/connect`} variant="primary">Check connections</RecoveryAction>} detail={detail ?? (available ? "The control plane will show matching durable runs here when they exist." : "The work queue could not be verified. No empty result is inferred from this state.")} kind={available ? "first-use-empty" : "unavailable"} title={available ? tab === "running" ? "No reviews are running" : "Nothing needs intervention" : "Work queue unavailable"} />; }
function QueueTabLink({ count, href, label, selected }: { count?: number; href: string; label: string; selected: boolean }) { return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative inline-flex h-12 shrink-0 items-center gap-2 rounded-t-[10px] px-4 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={href} role="tab" tabIndex={selected ? 0 : -1}>{label}{count !== undefined ? <span className="rounded-full bg-[var(--ls-surface-muted)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)]">{count}</span> : null}{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>; }
function validTab(value?: string): QueueTab { return value === "needs-attention" || value === "scheduled" ? value : "running"; }
function queueHref(org: string, tab: QueueTab, query: QueueQuery, cursor?: string, direction?: "after" | "before") { const params = new URLSearchParams({ tab }); if (query.repository?.trim()) params.set("repository", query.repository.trim()); if (query.q?.trim()) params.set("q", query.q.trim()); if (cursor) params.set("cursor", cursor); if (direction) params.set("direction", direction); return `/${encodeURIComponent(org)}/tasks?${params.toString()}`; }
