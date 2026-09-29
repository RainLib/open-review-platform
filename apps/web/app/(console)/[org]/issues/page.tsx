import Link from "next/link";
import {
  Activity,
  CheckCircle2,
  CircleAlert,
  ClipboardCheck,
  Clock3,
  Filter,
  FolderGit2,
  MessagesSquare,
  Search,
  ShieldOff,
  SlidersHorizontal,
  Sparkles,
  X,
} from "lucide-react";

import {
  IssueStatusBadge,
  ProviderFileAnchor,
  ProviderReviewAnchor,
  SeverityBadge,
  formatIssueAge,
  formatIssueTime,
  shortIssueID,
} from "@/components/console/issue-primitives";
import { IssueActions } from "@/components/console/issue-actions";
import { IssueAutoCreatePolicyEditor } from "@/components/console/issue-auto-create-policy";
import { IssueViewControls } from "@/components/console/issue-view-controls";
import { DataFreshness, PageState, RecoveryAction } from "@/components/console/page-state";
import { TabStateRouter } from "@/components/console/tab-state-router";
import {
  getIssueDetailData,
  getIssueAutoCreatePolicyData,
  getIssueInboxData,
  getIssueViewsData,
  getMemberData,
  type IssueInboxData,
  type IssueDetail,
  type ReviewIssue,
  type WorkspaceMember,
} from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { getUiLanguage } from "@/lib/ui-language-server";
import { type UiLanguage, uiText } from "@/lib/ui-language";
import { issueDefinitionFromQuery, issueDisplaySummary, issueDisplayTitle, issueFilterAnchor, type IssueQuery, type IssueView, type IssueViewDefinition } from "@/lib/issue-filters";

export default async function IssuesPage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string }>;
  searchParams: Promise<IssueQuery>;
}) {
  const [{ org }, query, language] = await Promise.all([params, searchParams, getUiLanguage()]);
  const t = (key: Parameters<typeof uiText>[1]) => uiText(language, key);
  let definition: IssueViewDefinition | undefined;
  let invalidFilter: string | undefined;
  let filterTime: string | undefined;
  try { definition = issueDefinitionFromQuery(query); filterTime = issueFilterAnchor(query); }
  catch (error) { invalidFilter = error instanceof Error ? error.message : "The issue filter is invalid."; }
  const view = definition?.view ?? "open";
  const seenAfter = resolveIssueSince(query);
  const filter = process.env.OPEN_REVIEW_CONSOLE_DEMO === "true" && !query.filters ? issueFilterForView(view, query, seenAfter) : { view, filters: definition?.filters, filterTime, cursor: query.cursor, cursorDirection: query.direction, limit: 25 };
  const [data, memberData, selectedData, policyData, viewsData] = await Promise.all([
    invalidFilter ? Promise.resolve<IssueInboxData>({ source: "unavailable", issues: [], counts: { open: 0, regressed: 0, critical: 0, assigned: 0, resolved: 0, suppressed: 0 }, detail: invalidFilter }) : getIssueInboxData(org, { ...filter, selectedIssueID: query.selected }),
    getMemberData(org),
    query.selected ? getIssueDetailData(org, query.selected) : Promise.resolve(undefined),
    getIssueAutoCreatePolicyData(org),
    getIssueViewsData(org),
  ]);
  const issues = data.source === "demo" && hasContradictoryStatusFilter(view, query.status) ? [] : data.issues;
  const navigationQuery = { ...query, filter_time: data.filterTime ?? query.filter_time };
  const readable = data.source === "live" || data.source === "demo";
  const preview = data.source === "demo";

  const counts = data.counts;
  const viewSummary = issueViewSummary(view, counts, preview);
  const repositories = unique(data.facets?.repositories ?? data.issues.map((issue) => issue.repository));
  const categories = unique(data.facets?.categories ?? data.issues.map((issue) => issue.category));
  const firstUseEmpty = data.source === "live" && issues.length === 0 && counts.open === 0 && counts.regressed === 0 && counts.critical === 0 && counts.assigned === 0 && counts.resolved === 0 && counts.suppressed === 0 && !hasIssueFilters(query);
  // Preview records are intentionally immutable, but their retained evidence
  // is still useful for reviewing the complete inbox-to-detail journey.
  const selectedIssue = readable && data.selectedInView !== false ? selectedData?.issue : undefined;

  return (
    <div className="space-y-5">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h1 className="text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">
              {t("issueInbox")}
            </h1>
            <span className="text-sm text-[var(--ls-text-secondary)]">
              {readable ? data.totalCount === undefined ? viewSummary.countLabel : `${data.totalCount} matching issues` : "Issue aggregate unavailable"}
            </span>
          </div>
          <p className="mt-1 text-sm text-[var(--ls-text-secondary)]">
            {readable ? viewSummary.description : "Durable finding aggregates across repositories"}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <DataFreshness language={language} state={data.source === "live" ? "live" : data.source === "demo" ? "demo" : "unavailable"} />
          {readable ? <IssueAutoCreatePolicyEditor enabled={data.source === "live"} org={org} policy={policyData.policy} /> : null}
          <Link
            className="luminous-focus inline-flex h-10 items-center gap-2 whitespace-nowrap rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-medium text-[var(--ls-text)] shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-surface-muted)]"
            href={issueTriageHref(org)}
          >
            <ClipboardCheck className="size-4 text-[var(--ls-accent)]" /> {t("configureIssueFormat")}
          </Link>
          <Link
            className="luminous-focus inline-flex h-10 items-center gap-2 whitespace-nowrap rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-medium text-[var(--ls-text)] shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-surface-muted)]"
            href={`/${encodeURIComponent(org)}/provider-issues`}
          >
            <MessagesSquare className="size-4 text-[var(--ls-accent)]" /> {t("providerTriage")}
          </Link>
          <Link
            className="luminous-focus inline-flex h-10 items-center gap-2 whitespace-nowrap rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-medium text-[var(--ls-text)] shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-surface-muted)]"
            href={`/${encodeURIComponent(org)}/findings`}
          >
            <Sparkles className="size-4 text-[var(--ls-accent)]" /> {t("exploreFindings")}
          </Link>
        </div>
      </div>

      <IssueViewControls data={viewsData} definition={definition} invalidFilter={invalidFilter} key={JSON.stringify([query, viewsData.views])} org={org} savedID={query.saved_view} />

      {readable ? (
        <>
          {preview ? <div className="flex items-start gap-3 rounded-[14px] border border-sky-500/20 bg-sky-500/[0.06] px-4 py-3 text-sm text-[var(--ls-text-secondary)]"><CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" /><p>Illustrative issue aggregates for design review. Filters and retained evidence are available in this preview; changing status, assigning owners, and creating provider Issues require a live workspace.</p></div> : null}
          <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" dismissSelection={Boolean(query.selected && data.selectedInView === false)} label={t("issueInbox")}>
            <ViewTab href={issueHref(org, query, { view: "all", status: undefined, cursor: undefined, direction: undefined, since: undefined })} icon={FolderGit2} label={t("all")} selected={view === "all"} />
            <ViewTab count={counts.open} href={issueHref(org, query, { view: "open", status: undefined, cursor: undefined, direction: undefined, since: undefined })} icon={Clock3} label={t("open")} selected={view === "open"} />
            <ViewTab count={counts.regressed} href={issueHref(org, query, { view: "regressed", status: undefined, cursor: undefined, direction: undefined, since: undefined })} icon={Activity} label={t("regressed")} selected={view === "regressed"} />
            <ViewTab count={counts.critical} critical href={issueHref(org, query, { view: "critical", status: undefined, cursor: undefined, direction: undefined, since: undefined })} icon={CircleAlert} label={t("critical")} selected={view === "critical"} />
            <ViewTab count={counts.assigned} href={issueHref(org, query, { view: "assigned", status: undefined, cursor: undefined, direction: undefined, since: undefined })} icon={FolderGit2} label={t("assignedToMe")} selected={view === "assigned"} />
            <ViewTab count={counts.resolved} href={issueHref(org, query, { view: "resolved", status: undefined, cursor: undefined, direction: undefined, since: undefined })} icon={CheckCircle2} label={t("resolved")} selected={view === "resolved"} />
            <ViewTab count={counts.suppressed} href={issueHref(org, query, { view: "suppressed", status: undefined, cursor: undefined, direction: undefined, since: undefined })} icon={ShieldOff} label={t("suppressed")} selected={view === "suppressed"} />
          </TabStateRouter>

          {!query.filters ? <form key={JSON.stringify(query)} className="grid gap-2 rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-2 shadow-[var(--ls-shadow-control)] sm:grid-cols-2 xl:grid-cols-[minmax(220px,1fr)_132px_150px_180px_120px_110px_auto]" method="get">
            <input name="view" type="hidden" value={view} />
            {query.saved_view ? <input name="saved_view" type="hidden" value={query.saved_view} /> : null}
            {(["provider", "api_base_url", "path", "assignee", "rule"] as const).map((field) => query[field] ? <input key={field} name={field} type="hidden" value={query[field]} /> : null)}
            <label className="relative block">
              <span className="sr-only">{t("searchIssues")}</span>
              <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[var(--ls-text-tertiary)]" />
              <input
                className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] pl-9 pr-3 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]"
                defaultValue={query.query}
                id="issue-search"
                name="query"
                placeholder={t("searchIssues")}
              />
            </label>
            <input name="since" type="hidden" value="" />
            <FilterSelect defaultValue={query.severity} label={t("severity")} name="severity" options={["critical", "high", "medium", "low"]} />
            <FilterText defaultValue={query.category} label={t("category")} name="category" options={categories} />
            <FilterText defaultValue={query.repository} label={t("repository")} name="repository" options={repositories} />
            <FilterSelect defaultValue={query.status} label={t("status")} name="status" options={["open", "regressed", "resolved", "suppressed"]} />
            <FilterSelect defaultValue={query.age} label={t("age")} name="age" options={["24h", "7d", "30d"]} />
            <div className="flex gap-2">
              <button className="luminous-focus inline-flex h-10 flex-1 items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-3 text-sm font-medium text-white hover:bg-[var(--ls-accent-hover)]" type="submit">
                <Filter className="size-4" /> {t("apply")}
              </button>
              {hasIssueFilters(query) ? (
                <Link aria-label={t("clearFilters")} className="luminous-focus grid size-10 place-items-center rounded-[10px] border border-[var(--ls-line-strong)] text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]" href={`/${encodeURIComponent(org)}/issues?view=${view}`} title={t("clearFilters")}>
                  <SlidersHorizontal className="size-4" />
                </Link>
              ) : null}
            </div>
          </form> : null}

          <div className={cn("grid min-h-[560px] gap-4", selectedIssue ? "xl:grid-cols-[minmax(0,1fr)_480px]" : "grid-cols-1")}>
            <section className="min-w-0 overflow-hidden rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)]">
              {issues.length ? (
            <>
              <div className="hidden overflow-x-auto md:block">
                <table className="w-full border-collapse text-left">
                  <thead>
                    <tr className="h-10 border-b border-[var(--ls-line)] text-[11px] font-medium text-[var(--ls-text-tertiary)]">
                      <th className="w-28 px-4">{t("severity")}</th>
                      <th className="px-3">{t("issues")}</th>
                      <th className="px-3">{t("repositoryFile")}</th>
                      <th className="w-24 px-3 text-center">{t("occurrences")}</th>
                      <th className="w-24 px-3">{t("age")}</th>
                      <th className="w-24 px-3">{t("status")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {issues.map((issue) => (
                      <IssueTableRow allowSelection={readable} issue={issue} key={issue.id} language={language} org={org} query={navigationQuery} selected={selectedIssue?.id === issue.id} />
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="divide-y divide-[var(--ls-line)] md:hidden">
                {issues.map((issue) => <IssueCard allowSelection={readable} issue={issue} key={issue.id} language={language} org={org} />)}
              </div>
            </>
              ) : firstUseEmpty ? <PageState action={<RecoveryAction href={`/${encodeURIComponent(org)}/findings`} variant="primary">Explore review findings</RecoveryAction>} detail="No review findings have been aggregated yet. This inbox tracks durable findings from Open Review runs; it does not mirror historical GitHub or GitLab Issues. Configure the auto-create policy above to open a provider Issue only for future matching aggregates." kind="first-use-empty" title="No review issues yet" /> : <PageState action={<RecoveryAction href={`/${encodeURIComponent(org)}/issues?view=${hasIssueFilters(query) ? view : "all"}`}>{hasIssueFilters(query) ? "Clear filters" : "View all issues"}</RecoveryAction>} detail={hasContradictoryStatusFilter(view, query.status) ? "The selected status conflicts with this inbox view. Clear the status filter or choose the matching tab." : hasIssueFilters(query) ? "No issue aggregates match this view and its current filters." : "No issues match this view. Open All to inspect other statuses."} kind="filtered-empty" title="No matching issues" />}
            </section>
            {selectedIssue ? <IssueInspector issue={selectedIssue} language={language} members={memberData.members} org={org} query={navigationQuery} readOnly={preview} /> : null}
          </div>
          <IssuePager data={data} issues={issues} org={org} query={navigationQuery} seenAfter={preview ? seenAfter : undefined} />
        </>
      ) : (
        <PageState action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect`} variant="primary">Check connections</RecoveryAction>} detail={data.detail ?? "Issue aggregates are temporarily unavailable. No empty result or zero finding count is inferred from this state."} kind="unavailable" title="Issue inbox unavailable" />
      )}
    </div>
  );
}

function IssueTableRow({ allowSelection, issue, language, org, query, selected }: { allowSelection: boolean; issue: ReviewIssue; language: UiLanguage; org: string; query: IssueQuery; selected: boolean }) {
  return (
    <tr className={cn("h-[58px] border-b border-[var(--ls-line)] last:border-0 hover:bg-[var(--ls-surface-muted)]", selected && "bg-[var(--ls-surface-selected)]")}>
      <td className={cn("border-l-2 px-4", selected ? "border-l-[var(--ls-accent)]" : "border-l-transparent")}><SeverityBadge language={language} severity={issue.severity} /></td>
      <td className="max-w-80 px-3">
        {allowSelection ? <Link className="luminous-focus block rounded" href={issueHref(org, query, { selected: issue.id })}>
          <span className="block truncate text-sm font-medium text-[var(--ls-text)]">{issueDisplayTitle(issue.body_preview)}</span>
          <span className="mt-0.5 block truncate text-xs text-[var(--ls-text-tertiary)]">{issue.category}</span>
        </Link> : <span className="block"><span className="block truncate text-sm font-medium text-[var(--ls-text)]">{issueDisplayTitle(issue.body_preview)}</span><span className="mt-0.5 block truncate text-xs text-[var(--ls-text-tertiary)]">{issue.category}</span></span>}
      </td>
      <td className="max-w-64 px-3">
        <span className="block truncate text-xs font-medium text-[var(--ls-accent)]">{issue.repository}</span>
        <span className="mt-0.5 block truncate font-mono text-[11px] text-[var(--ls-text-tertiary)]">{issue.path}</span>
      </td>
      <td className="px-3 text-center text-xs tabular-nums text-[var(--ls-text-secondary)]">{issue.occurrence_count}</td>
      <td className="whitespace-nowrap px-3 text-xs text-[var(--ls-text-secondary)]">{formatIssueAge(issue.last_seen_at, language)}</td>
      <td className="px-3"><IssueStatusBadge language={language} status={issue.status} /></td>
    </tr>
  );
}

function IssueCard({ allowSelection, issue, language, org }: { allowSelection: boolean; issue: ReviewIssue; language: UiLanguage; org: string }) {
  const content = <>
      <div className="flex items-start justify-between gap-3"><SeverityBadge language={language} severity={issue.severity} /><IssueStatusBadge language={language} status={issue.status} /></div>
      <h2 className="mt-3 line-clamp-2 text-sm font-semibold text-[var(--ls-text)]">{issueDisplayTitle(issue.body_preview)}</h2>
      <p className="mt-1 truncate font-mono text-xs text-[var(--ls-text-tertiary)]">{issue.repository} · {issue.path}</p>
      <p className="mt-3 text-xs text-[var(--ls-text-secondary)]">{language === "zh-CN" ? `出现 ${issue.occurrence_count} 次 · 最近 ${formatIssueAge(issue.last_seen_at, language)}` : `${issue.occurrence_count} occurrences · last seen ${formatIssueAge(issue.last_seen_at, language)}`}</p>
    </>;
  return allowSelection ? <Link className="luminous-focus block p-4 hover:bg-[var(--ls-surface-muted)]" href={`/${org}/issues/${issue.id}`}>{content}</Link> : <article className="p-4">{content}</article>;
}

function IssueInspector({ issue, language, members, org, query, readOnly }: { issue: IssueDetail; language: UiLanguage; members: WorkspaceMember[]; org: string; query: IssueQuery; readOnly: boolean }) {
  const latest = issue.occurrences[0];
  const pullRequests = unique(issue.occurrences.map((occurrence) => String(occurrence.review_number))).slice(0, 3);
  return (
    <aside className="luminous-frosted fixed inset-y-14 right-0 z-30 w-[min(100%,480px)] overflow-y-auto border-l border-[var(--ls-line-strong)] p-5 shadow-[var(--ls-shadow-float)] xl:sticky xl:inset-auto xl:top-20 xl:z-auto xl:max-h-[calc(100vh-6rem)] xl:w-auto xl:rounded-[24px] xl:border">
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--ls-accent)]">{shortIssueID(issue.id)}</p>
          <h2 className="mt-1 text-xl font-semibold tracking-[-0.03em] text-[var(--ls-text)]">{issueDisplayTitle(issue.body_preview)}</h2>
        </div>
        <Link aria-label="Close issue inspector" className="luminous-focus grid size-8 shrink-0 place-items-center rounded-[9px] text-[var(--ls-text-tertiary)] hover:bg-[var(--ls-surface-muted)]" href={issueHref(org, query, { selected: undefined })}><X className="size-4" /></Link>
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-3"><SeverityBadge language={language} severity={issue.severity} /><span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">{issue.category}</span><IssueStatusBadge language={language} status={issue.status} /></div>
      <Link className="luminous-focus mt-3 inline-flex min-h-7 items-center gap-1.5 rounded-[8px] text-xs font-medium text-[var(--ls-accent)] hover:text-[var(--ls-accent-hover)]" href={issueTriageHref(org, issue)}>
        <ClipboardCheck className="size-3.5" /> Configure format for {issue.repository}
      </Link>
      <p className="mt-4 text-sm leading-6 text-[var(--ls-text-secondary)]">{issueDisplaySummary(latest?.body ?? issue.body_preview)}</p>
      <div className="mt-4">{readOnly ? <p className="rounded-[12px] border border-sky-500/20 bg-sky-500/[0.06] px-3 py-2.5 text-xs leading-5 text-[var(--ls-text-secondary)]">Preview evidence is immutable. Assignment, disposition, exception requests, and provider Issue automation are available only from a live workspace.</p> : <IssueActions key={issue.revision} issue={issue} members={members} org={org} />}</div>
      {issue.disposition_kind ? (
        <section className="mt-4 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3">
          <p className="text-[11px] font-semibold uppercase tracking-[0.1em] text-[var(--ls-text-tertiary)]">Decision evidence</p>
          <p className="mt-1.5 text-xs font-medium capitalize text-[var(--ls-text)]">{issue.disposition_kind.replaceAll("_", " ")}</p>
          {issue.disposition_reason ? <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{issue.disposition_reason}</p> : null}
        </section>
      ) : null}
      {latest ? <div className="mt-5 border-t border-[var(--ls-line)] pt-4"><p className="mb-2 text-xs font-medium text-[var(--ls-text)]">Affected file</p><ProviderFileAnchor issue={issue} occurrence={latest} /><div className="mt-3 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3"><p className="font-mono text-[11px] text-[var(--ls-text-tertiary)]">{latest.path}:{latest.start_line}{latest.end_line > latest.start_line ? `–${latest.end_line}` : ""} · {latest.head_sha.slice(0, 12)}</p><p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">{issueDisplaySummary(latest.body)}</p>{latest.suggestion ? <p className="mt-2 border-t border-[var(--ls-line)] pt-2 text-xs leading-5 text-[var(--ls-accent)]">Suggested direction: {issueDisplayTitle(latest.suggestion)}</p> : null}</div></div> : null}
      <dl className="mt-5 grid grid-cols-3 gap-3 border-y border-[var(--ls-line)] py-4 text-xs">
        <InspectorMetric label="Occurrences" value={String(issue.occurrence_count)} />
        <InspectorMetric label="Pull requests" value={String(issue.pull_request_count)} />
        <InspectorMetric label="First seen" value={formatIssueAge(issue.first_seen_at)} />
      </dl>
      {pullRequests.length ? <div className="mt-5"><div className="flex items-center justify-between"><h3 className="text-xs font-semibold text-[var(--ls-text)]">Affected pull requests</h3><Link className="text-xs text-[var(--ls-accent)]" href={`/${org}/issues/${issue.id}?tab=pull-requests`}>View all</Link></div><ul className="mt-3 space-y-2">{pullRequests.map((reviewNumber) => { const occurrence = issue.occurrences.find((item) => String(item.review_number) === reviewNumber)!; return <li className="flex items-center justify-between text-xs" key={reviewNumber}><ProviderReviewAnchor issue={issue} occurrence={occurrence} /><span className="text-[var(--ls-text-tertiary)]">{formatIssueAge(occurrence.created_at)}</span></li>; })}</ul></div> : null}
      <div className="mt-5"><h3 className="text-xs font-semibold text-[var(--ls-text)]">Occurrence timeline</h3><ol className="mt-3 space-y-3 border-l border-[var(--ls-line-strong)] pl-4">{issue.occurrences.slice(0, 4).map((occurrence) => <li className="relative text-xs text-[var(--ls-text-secondary)]" key={occurrence.id}><span className="absolute -left-[19px] top-1 size-2 rounded-full bg-[var(--ls-accent)]" /><time dateTime={occurrence.created_at}>{formatIssueTime(occurrence.created_at)}</time><span className="mt-1 block text-[var(--ls-text-tertiary)]">Detected in #{occurrence.review_number} at {occurrence.head_sha.slice(0, 8)}</span></li>)}</ol></div>
      <Link className="luminous-focus mt-5 inline-flex h-10 w-full items-center justify-center rounded-[10px] border border-[var(--ls-line-strong)] text-sm font-medium text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" href={`/${org}/issues/${issue.id}`}>Open full evidence</Link>
    </aside>
  );
}

function InspectorMetric({ label, value }: { label: string; value: string }) { return <div><dt className="text-[var(--ls-text-tertiary)]">{label}</dt><dd className="mt-1 font-semibold tabular-nums text-[var(--ls-text)]">{value}</dd></div>; }

function FilterSelect({ defaultValue, label, name, options }: { defaultValue?: string; label: string; name: string; options: string[] }) {
  return <label><span className="sr-only">{label}</span><select className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text-secondary)]" defaultValue={defaultValue ?? ""} name={name}><option value="">{label}</option>{options.map((option) => <option key={option} value={option}>{option}</option>)}</select></label>;
}

function FilterText({ defaultValue, label, name, options }: { defaultValue?: string; label: string; name: string; options: string[] }) {
  const listID = `issue-${name}-options`;
  return <label><span className="sr-only">{label}</span><input className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text-secondary)] placeholder:text-[var(--ls-text-secondary)]" defaultValue={defaultValue ?? ""} list={listID} name={name} placeholder={label} type="text" /><datalist id={listID}>{options.map((option) => <option key={option} value={option} />)}</datalist></label>;
}

function ViewTab({ count, critical = false, href, icon: Icon, label, selected }: { count?: number; critical?: boolean; href: string; icon: typeof Clock3; label: string; selected: boolean }) {
  return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative inline-flex h-11 shrink-0 items-center gap-2 rounded-t-[10px] px-4 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={href} role="tab" tabIndex={selected ? 0 : -1}><Icon className={cn("size-4", critical && "text-[var(--ls-critical-text)]")} />{label}<span className={selected ? "text-[var(--ls-accent)]" : "text-[var(--ls-text-tertiary)]"}>{count}</span>{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>;
}


function IssuePager({ data, issues, org, query, seenAfter }: { data: IssueInboxData; issues: ReviewIssue[]; org: string; query: IssueQuery; seenAfter?: string }) {
  if (data.source !== "live") return null;
  const pageQuery = seenAfter ? { ...query, since: seenAfter } : query;
  if (!issues.length && !data.previousCursor && !data.nextCursor) return null;
  return (
    <nav aria-label="Issue pagination" className="flex flex-col gap-3 rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-4 py-3 shadow-[var(--ls-shadow-control)] sm:flex-row sm:items-center sm:justify-between">
      <p className="text-xs text-[var(--ls-text-secondary)]">{data.totalCount === undefined ? "Showing up to 25 issues per page." : `${issues.length} on this page · ${data.totalCount} matching issues.`} New findings are placed ahead of the current cursor.</p>
      <div className="flex items-center gap-2">
        {data.previousCursor ? <Link className="luminous-focus inline-flex h-9 items-center rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-sm font-medium text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" href={issueHref(org, pageQuery, { cursor: data.previousCursor, direction: "before", selected: undefined })}>Previous</Link> : <span className="inline-flex h-9 items-center rounded-[9px] border border-[var(--ls-line)] px-3 text-sm text-[var(--ls-text-tertiary)]">Previous</span>}
        {data.nextCursor ? <Link className="luminous-focus inline-flex h-9 items-center rounded-[9px] bg-[var(--ls-accent)] px-3 text-sm font-medium text-white hover:bg-[var(--ls-accent-hover)]" href={issueHref(org, pageQuery, { cursor: data.nextCursor, direction: "after", selected: undefined })}>Next</Link> : <span className="inline-flex h-9 items-center rounded-[9px] border border-[var(--ls-line)] px-3 text-sm text-[var(--ls-text-tertiary)]">Next</span>}
      </div>
    </nav>
  );
}

function issueHref(org: string, query: IssueQuery, patch: Partial<IssueQuery>) {
  const next = new URLSearchParams();
  const merged = { ...query, ...patch };
  if (patch.view !== undefined) merged.filter_time = undefined;
  for (const [key, value] of Object.entries(merged)) if (value) next.set(key, value);
  const serialized = next.toString();
  return `/${org}/issues${serialized ? `?${serialized}` : ""}`;
}

function issueTriageHref(org: string, issue?: Pick<ReviewIssue, "repository" | "provider" | "api_base_url">) {
  if (!issue?.repository || !issue.provider || !issue.api_base_url) {
    return `/${encodeURIComponent(org)}/review-config/issue-triage`;
  }
  const params = new URLSearchParams({
    scope: "repository",
    repository: issue.repository,
    provider: issue.provider,
    api_base_url: issue.api_base_url,
  });
  return `/${encodeURIComponent(org)}/review-config/issue-triage?${params.toString()}`;
}

type IssueDataOptions = NonNullable<Parameters<typeof getIssueInboxData>[1]>;

function issueFilterForView(view: IssueView, query: IssueQuery, seenAfter?: string): IssueDataOptions {
  const filter: IssueDataOptions = {
    category: query.category,
    cursor: query.cursor,
    cursorDirection: query.direction,
    limit: 25,
    query: query.query,
    repository: query.repository,
    seenAfter,
    severity: validIssueSeverity(query.severity),
  };
  const requestedStatus = validIssueStatus(query.status);
  if (view === "open") filter.status = "open";
  if (view === "regressed") filter.status = "regressed";
  if (view === "resolved") filter.status = "resolved";
  if (view === "suppressed") filter.status = "suppressed";
  if (view === "critical") {
    filter.severity = "critical";
    filter.activeOnly = true;
    filter.status = requestedStatus;
  }
  if (view === "assigned") {
    filter.assignedToMe = true;
    filter.activeOnly = true;
    filter.status = requestedStatus;
  }
  return filter;
}

function hasContradictoryStatusFilter(view: IssueView, status?: string) {
  const requested = validIssueStatus(status);
  const fixedStatus = view === "open" || view === "regressed" || view === "resolved" || view === "suppressed" ? view : undefined;
  return Boolean(fixedStatus && requested && requested !== fixedStatus);
}

function hasIssueFilters(query: IssueQuery) {
  return Boolean(query.filters || query.age || query.category || query.query || query.repository || query.severity || query.status || query.since || query.provider || query.api_base_url || query.path || query.assignee || query.rule);
}

function validIssueStatus(value?: string): "open" | "regressed" | "resolved" | "suppressed" | undefined {
  return value === "open" || value === "regressed" || value === "resolved" || value === "suppressed" ? value : undefined;
}

function validIssueSeverity(value?: string): "low" | "medium" | "high" | "critical" | undefined {
  return value === "low" || value === "medium" || value === "high" || value === "critical" ? value : undefined;
}

function resolveIssueSince(query: IssueQuery) {
  if (query.since) {
    const parsed = new Date(query.since);
    if (!Number.isNaN(parsed.getTime())) return parsed.toISOString();
  }
  const now = Date.now();
  if (query.age === "24h") return new Date(now - 24 * 60 * 60 * 1000).toISOString();
  if (query.age === "7d") return new Date(now - 7 * 24 * 60 * 60 * 1000).toISOString();
  if (query.age === "30d") return new Date(now - 30 * 24 * 60 * 60 * 1000).toISOString();
  return undefined;
}

function issueViewSummary(view: IssueView, counts: IssueInboxData["counts"], preview: boolean) {
  const source = preview ? "preview" : "loaded";
  switch (view) {
    case "all":
      return { countLabel: `${counts.open + counts.regressed + counts.resolved + counts.suppressed} ${source} total`, description: "All findings retained across repositories and dispositions" };
    case "regressed":
      return { countLabel: `${counts.regressed} ${source} regressed`, description: "Findings that returned after an earlier resolution" };
    case "critical":
      return { countLabel: `${counts.critical} ${source} critical`, description: "Active critical findings requiring immediate attention" };
    case "assigned":
      return { countLabel: `${counts.assigned} ${source} assigned`, description: "Active findings assigned to your account" };
    case "resolved":
      return { countLabel: `${counts.resolved} ${source} resolved`, description: "Resolved findings retained with their review and provider evidence" };
    case "suppressed":
      return { countLabel: `${counts.suppressed} ${source} suppressed`, description: "Suppressed findings retained for governance and audit" };
    default:
      return { countLabel: `${counts.open} ${source} open`, description: "Recurring and unresolved findings across repositories" };
  }
}
function unique(values: string[]) { return [...new Set(values.filter(Boolean))].sort((left, right) => left.localeCompare(right)); }
