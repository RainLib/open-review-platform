import { getUiLanguage } from "@/lib/ui-language-server";
import { HelpHint } from "@/components/console/help-hint";
import { workflowStatus, workflowText, type WorkflowMessageValues } from "@/lib/workflow-copy";
import type { UiLanguage } from "@/lib/ui-language";
import type { ReactNode } from "react";
import Link from "next/link";
import {
  Bot,
  CheckCircle2,
  CircleAlert,
  Clock3,
  ExternalLink,
  History,
  MessageSquareText,
  Search,
  SlidersHorizontal,
  ThumbsDown,
  ThumbsUp,
  X,
} from "lucide-react";

import { DataFreshness, PageState, RecoveryAction } from "@/components/console/page-state";
import { ProviderIssueRetry } from "@/components/console/provider-issue-retry";
import { ProviderIssueAgentTask } from "@/components/console/provider-issue-agent-task";
import { ProviderMark } from "@/components/providers/provider-icons";
import {
  getProviderIssueAnalysisData,
  type ProviderIssueAnalysis,
  type ProviderIssueAnalysisDetail,
  type ProviderIssueAnalysisFilterState,
  type ProviderIssueAnalysisState,
} from "@/lib/control-api";
import { providerIssueTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";

type Query = {
  q?: string;
  selected?: string;
  state?: string;
};

const states: Array<{ key: "all" | ProviderIssueAnalysisFilterState; label: string }> = [
  { key: "all", label: "All" },
  { key: "needs_attention", label: "Needs attention" },
  { key: "queued", label: "Queued" },
  { key: "acknowledged", label: "Acknowledged" },
  { key: "completed", label: "Completed" },
  { key: "failed", label: "Failed" },
];

export default async function ProviderIssuesPage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string }>;
  searchParams: Promise<Query>;
}) {
  const language = await getUiLanguage();
  const t = (source: string, values?: WorkflowMessageValues) => workflowText(language, source, values);
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const state = validState(query.state);
  const data = await getProviderIssueAnalysisData(org, {
    state,
    query: query.q?.trim(),
    selected: query.selected,
  });
  const readable = data.source === "live" || data.source === "demo";

  return (
    <div className="space-y-5">
      <header className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">
              {t(" Provider Issue triage ")}</h1>
            <HelpHint label={t("Provider Issue triage")}>{t(" AI analysis of Issues authored in GitHub or GitLab. Each row is the latest revision of one provider Issue; code-review finding aggregates remain separate. ")}</HelpHint>
            <DataFreshness
              language={language}
              state={data.source === "live" ? "live" : data.source === "demo" ? "demo" : "unavailable"}
            />
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Link
            className="luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-medium text-[var(--ls-text)] shadow-[var(--ls-shadow-control)] hover:bg-[var(--ls-surface-muted)]"
            href={`/${encodeURIComponent(org)}/review-config/issue-triage`}
          >
            <SlidersHorizontal className="size-4 text-[var(--ls-accent)]" /> {t(" Issue format rules ")}</Link>
          <Link
            className="luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-medium text-[var(--ls-text)] shadow-[var(--ls-shadow-control)] hover:bg-[var(--ls-surface-muted)]"
            href={`/${encodeURIComponent(org)}/issues`}
          >
            <MessageSquareText className="size-4 text-[var(--ls-accent)]" /> {t(" Finding aggregates ")}</Link>
        </div>
      </header>

      {readable ? (
        <>
          <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3" aria-label={t("Provider Issue analysis counts")}>
            <Metric label={t("Queued")} value={data.counts.queued} tone="neutral" />
            <Metric label={t("Acknowledged")} value={data.counts.acknowledged} tone="accent" />
            <Metric label={t("Completed")} value={data.counts.completed} tone="success" />
            <Metric label={t("Failed")} value={data.counts.failed} tone="danger" />
            <Metric label={t("Delivery exhausted")} value={data.counts.delivery_failures ?? 0} tone="danger" />
            <Metric label={t("Step consumed")} value={data.counts.skipped_deliveries ?? 0} tone="danger" />
            <Metric label={t("Connection blocked")} value={data.counts.connection_blocked ?? 0} tone="danger" />
          </section>

          {(data.counts.connection_blocked ?? 0) > 0 ? (
            <p className="rounded-[12px] border border-amber-500/25 bg-amber-500/[0.07] px-4 py-3 text-sm text-[var(--ls-text-secondary)]">
              {t("{count} pending Issue analysis(es) blocked because the Git connection is inactive or unverified. Restore it, then check whether the retained delivery needs an authorized retry.", { count: data.counts.connection_blocked ?? 0 })}{" "}
              <Link className="font-semibold text-[var(--ls-accent)] underline underline-offset-2" href={providerIssueHref(org, query, { state: "needs_attention", selected: undefined })}>{t("View blocked analyses")}</Link>
            </p>
          ) : null}

          {(data.counts.delivery_failures ?? 0) > 0 ? (
            <p className="rounded-[12px] border border-red-500/20 bg-red-500/[0.06] px-4 py-3 text-sm text-[var(--ls-danger-text)]">
              {t("{count} Issue step(s) exhausted queue retries. Repair the connection, then an authorized operator must retry the retained revision.", { count: data.counts.delivery_failures ?? 0 })}{" "}
              <Link className="font-semibold underline underline-offset-2" href={providerIssueHref(org, query, { state: "needs_attention", selected: undefined })}>{t("View tasks needing attention")}</Link>
            </p>
          ) : null}

          {(data.counts.skipped_deliveries ?? 0) > 0 ? (
            <p className="rounded-[12px] border border-amber-500/25 bg-amber-500/[0.07] px-4 py-3 text-sm text-[var(--ls-text-secondary)]">
              {t("{count} Issue step(s) were consumed without progress. Restoring a connection cannot replay that message; an authorized operator can retry the exact snapshot once its gates are ready.", { count: data.counts.skipped_deliveries ?? 0 })}{" "}
              <Link className="font-semibold text-[var(--ls-accent)] underline underline-offset-2" href={providerIssueHref(org, query, { state: "needs_attention", selected: undefined })}>{t("View recovery tasks")}</Link>
            </p>
          ) : null}

          <nav className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" aria-label={t("Provider Issue analysis states")}>
            {states.map((item) => {
              const active = item.key === (state ?? "all");
              const count = item.key === "all"
                ? data.counts.queued + data.counts.acknowledged + data.counts.completed + data.counts.failed
                : item.key === "needs_attention"
                  ? (data.counts.needs_attention ?? data.counts.failed + (data.counts.delivery_failures ?? 0))
                  : data.counts[item.key];
              return (
                <Link
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "luminous-focus -mb-px inline-flex h-11 shrink-0 items-center gap-2 border-b-2 px-3 text-sm font-medium transition",
                    active
                      ? "border-[var(--ls-accent)] text-[var(--ls-text)]"
                      : "border-transparent text-[var(--ls-text-tertiary)] hover:text-[var(--ls-text)]",
                  )}
                  href={providerIssueHref(org, query, { state: item.key === "all" ? undefined : item.key, selected: undefined })}
                  key={item.key}
                >
                  {t(item.label)}
                  <span className="rounded-full bg-[var(--ls-surface-muted)] px-1.5 py-0.5 text-[10px] tabular-nums">{count}</span>
                </Link>
              );
            })}
          </nav>

          <form className="flex flex-col gap-2 rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-2 shadow-[var(--ls-shadow-control)] sm:flex-row" method="get">
            {state ? <input name="state" type="hidden" value={state} /> : null}
            <label className="relative block flex-1">
              <span className="sr-only">{t("Search Provider Issues")}</span>
              <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[var(--ls-text-tertiary)]" />
              <input
                className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] pl-9 pr-3 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]"
                defaultValue={query.q}
                name="q"
                placeholder={t("Search repository, title, author, or Issue number…")}
              />
            </label>
            <button className="luminous-focus h-10 rounded-[10px] bg-[var(--ls-accent)] px-5 text-sm font-medium text-white hover:bg-[var(--ls-accent-hover)]" type="submit">
              {t(" Search ")}</button>
          </form>

          <div className={cn("grid min-h-[500px] gap-4", data.selected ? "xl:grid-cols-[minmax(0,1fr)_500px]" : "grid-cols-1")}>
            <section className="min-w-0 overflow-hidden rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
              {data.items.length ? (
                <>
                  <div className="hidden overflow-x-auto md:block">
                    <table className="w-full border-collapse text-left">
                      <thead>
                        <tr className="h-10 border-b border-[var(--ls-line)] text-[11px] font-medium text-[var(--ls-text-tertiary)]">
                          <th className="w-12 px-4"><span className="sr-only">{t("Provider")}</span></th>
                          <th className="px-2">Issue</th>
                          <th className="w-44 px-3">{t("Repository")}</th>
                          <th className="w-28 px-3">{t("Revision")}</th>
                          <th className="w-32 px-3">{t("Updated")}</th>
                          <th className="w-32 px-4">{t("State")}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {data.items.map((item) => (
                          <ProviderIssueRow item={item} key={item.id} org={org} query={query} selected={item.id === data.selected?.id} />
                        ))}
                      </tbody>
                    </table>
                  </div>
                  <div className="divide-y divide-[var(--ls-line)] md:hidden">
                    {data.items.map((item) => <ProviderIssueCard item={item} key={item.id} org={org} query={query} />)}
                  </div>
                </>
              ) : (
                <PageState
                  action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect`}>{t("Check Issue event connection")}</RecoveryAction>}
                  detail={data.detail ?? (query.q || state ? t("No Provider Issue analyses match these filters.") : t("No GitHub or GitLab Issue webhook has been admitted for AI triage yet."))}
                  kind={query.q || state ? "filtered-empty" : "first-use-empty"}
                  title={query.q || state ? t("No matching analyses") : t("No Provider Issues analyzed yet")}
                />
              )}
            </section>
            {data.selected ? <AnalysisInspector analysis={data.selected} org={org} query={query} retryEnabled={data.source === "live"} /> : null}
          </div>
        </>
      ) : (
        <PageState
          action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect`} variant="primary">{t("Open connections")}</RecoveryAction>}
          detail={data.detail ?? t("Provider Issue triage is temporarily unavailable.")}
          kind="unavailable"
          title={t("Provider Issue triage unavailable")}
        />
      )}
    </div>
  );
}

async function ProviderIssueRow({ item, org, query, selected }: { item: ProviderIssueAnalysis; org: string; query: Query; selected: boolean }) {
  const language = await getUiLanguage();
  const t = (source: string, values?: WorkflowMessageValues) => workflowText(language, source, values);
  return (
    <tr className={cn("h-[66px] border-b border-[var(--ls-line)] last:border-0 hover:bg-[var(--ls-surface-muted)]", selected && "bg-[var(--ls-surface-selected)]")}>
      <td className={cn("border-l-2 px-4", selected ? "border-l-[var(--ls-accent)]" : "border-l-transparent")}><ProviderMark className="size-4" provider={item.provider} /></td>
      <td className="max-w-md px-2">
        <Link className="luminous-focus block rounded" href={providerIssueHref(org, query, { selected: item.id })}>
          <span className="block truncate text-sm font-medium text-[var(--ls-text)]">#{item.issue_number} {item.title}</span>
          <span className="mt-1 block truncate text-xs text-[var(--ls-text-tertiary)]">{t("opened by ")}{item.author || t("unknown")}</span>
        </Link>
      </td>
      <td className="max-w-44 px-3 text-xs font-medium text-[var(--ls-accent)]"><span className="block truncate">{item.repository}</span></td>
      <td className="px-3 text-xs text-[var(--ls-text-secondary)]"><span className="font-mono">r{item.revision}</span> · a{item.analysis_attempt} · {item.action}</td>
      <td className="whitespace-nowrap px-3 text-xs text-[var(--ls-text-secondary)]">{formatTime(item.updated_at, language)}</td>
      <td className="px-4"><AnalysisState connectionBlocked={item.connection_blocked} deliveryFailure={item.delivery_failure} skippedDelivery={item.skipped_delivery} state={item.state} /></td>
    </tr>
  );
}

async function ProviderIssueCard({ item, org, query }: { item: ProviderIssueAnalysis; org: string; query: Query }) {
  const language = await getUiLanguage();
  const t = (source: string, values?: WorkflowMessageValues) => workflowText(language, source, values);
  return (
    <Link className="luminous-focus block p-4 hover:bg-[var(--ls-surface-muted)]" href={providerIssueHref(org, query, { selected: item.id })}>
      <div className="flex items-center justify-between gap-3"><ProviderMark className="size-4" provider={item.provider} /><AnalysisState connectionBlocked={item.connection_blocked} deliveryFailure={item.delivery_failure} skippedDelivery={item.skipped_delivery} state={item.state} /></div>
      <h2 className="mt-3 text-sm font-semibold text-[var(--ls-text)]">#{item.issue_number} {item.title}</h2>
      <p className="mt-1 truncate text-xs text-[var(--ls-text-tertiary)]">{item.repository} · r{item.revision} {t(" · attempt ")}{item.analysis_attempt} · {item.action}</p>
    </Link>
  );
}

async function AnalysisInspector({ analysis, org, query, retryEnabled }: { analysis: ProviderIssueAnalysisDetail; org: string; query: Query; retryEnabled: boolean }) {
  const language = await getUiLanguage();
  const t = (source: string, values?: WorkflowMessageValues) => workflowText(language, source, values);
  const target = providerIssueTarget(analysis);
  return (
    <aside className="luminous-frosted fixed inset-y-14 right-0 z-30 w-[min(100%,500px)] overflow-y-auto border-l border-[var(--ls-line-strong)] p-5 shadow-[var(--ls-shadow-float)] xl:sticky xl:inset-auto xl:top-20 xl:z-auto xl:max-h-[calc(100vh-6rem)] xl:w-auto xl:rounded-[24px] xl:border">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2"><ProviderMark className="size-4" provider={analysis.provider} /><p className="text-[11px] font-semibold uppercase tracking-[.12em] text-[var(--ls-accent)]">{analysis.repository} {t(" · Issue #")}{analysis.issue_number}</p></div>
          <h2 className="mt-2 text-xl font-semibold tracking-[-.03em] text-[var(--ls-text)]">{analysis.title}</h2>
        </div>
        <Link aria-label={t("Close analysis")} className="luminous-focus grid size-8 shrink-0 place-items-center rounded-[9px] text-[var(--ls-text-tertiary)] hover:bg-[var(--ls-surface-muted)]" href={providerIssueHref(org, query, { selected: undefined })}><X className="size-4" /></Link>
      </div>
      <div className="mt-4 flex flex-wrap items-center gap-2"><AnalysisState connectionBlocked={analysis.connection_blocked} deliveryFailure={analysis.delivery_failure} skippedDelivery={analysis.skipped_delivery} state={analysis.state} /><span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">r{analysis.revision} {t(" · attempt ")}{analysis.analysis_attempt} · {analysis.action}</span>{analysis.labels.map((label) => <span className="rounded-full border border-[var(--ls-line)] px-2.5 py-1 text-xs text-[var(--ls-text-tertiary)]" key={label}>{label}</span>)}</div>
      <div aria-label={t("Provider reaction feedback")} className="mt-3 flex items-center gap-2 text-xs text-[var(--ls-text-secondary)]">
        <span className="inline-flex items-center gap-1 rounded-full border border-emerald-500/20 bg-emerald-500/[0.06] px-2.5 py-1"><ThumbsUp className="size-3.5 text-emerald-600 dark:text-emerald-400" />{analysis.useful_count}</span>
        <span className="inline-flex items-center gap-1 rounded-full border border-amber-500/20 bg-amber-500/[0.06] px-2.5 py-1"><ThumbsDown className="size-3.5 text-amber-600 dark:text-amber-400" />{analysis.not_useful_count}</span>
        <span className="text-[var(--ls-text-tertiary)]">{t("Feedback only · never retriggers analysis")}</span>
      </div>
      {analysis.feedback_sync ? <div className="mt-2 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2 text-[11px] leading-5 text-[var(--ls-text-tertiary)]"><strong className="font-medium text-[var(--ls-text-secondary)]">{t("GitHub reaction sync:")}</strong> {workflowStatus(language, analysis.feedback_sync.state)}{analysis.feedback_sync.observed_at ? t(" · observed {time}", { time: formatExactTime(analysis.feedback_sync.observed_at, language) }) : t(" · awaiting first poll")} {t(" · next ")}{formatExactTime(analysis.feedback_sync.next_poll_at, language)}{analysis.feedback_sync.last_error ? ` · ${analysis.feedback_sync.last_error}` : ""}</div> : null}
      {target ? <a className="luminous-focus mt-4 inline-flex h-10 w-full items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] text-sm font-medium text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" href={target.url} rel="noreferrer" target="_blank">{t("Open Issue in ")}{target.label}<ExternalLink className="size-4" /></a> : null}
      <ProviderIssueAgentTask admission={analysis.agent_admission} analysisID={analysis.id} enabled={retryEnabled} expectedRevision={analysis.revision} org={org} />
      <Link className="luminous-focus mt-2 inline-flex h-10 w-full items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] text-sm font-medium text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" href={`/${encodeURIComponent(org)}/review-config/issue-triage?scope=repository&repository=${encodeURIComponent(analysis.repository)}`}><SlidersHorizontal className="size-4 text-[var(--ls-accent)]" />{t("Configure this repository's format")}</Link>
      {analysis.connection_blocked ? (
        <div className="mt-4 rounded-[12px] border border-amber-500/25 bg-amber-500/[0.07] p-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
          <strong className="text-[var(--ls-text)]">{t("Connection blocked:")}</strong> {t(" This analysis retains its queued state and exact Issue revision, but cannot advance while its Git connection is inactive or unverified. ")}{" "}
          <Link className="font-semibold text-[var(--ls-accent)] underline underline-offset-2" href={`/${encodeURIComponent(org)}/connect/${encodeURIComponent(analysis.installation_id)}`}>{t("Open this connection")}</Link>
          {analysis.delivery_failure || analysis.skipped_delivery ? t(" After restoring it, an authorized operator must retry this retained delivery.") : t(" After verification, check whether this pending delivery advances; this page will show an explicit retry if its message is consumed or exhausted.")}
        </div>
      ) : null}
      {analysis.last_error || analysis.delivery_failure || analysis.skipped_delivery ? (
        <div className="mt-4 rounded-[12px] border border-red-500/20 bg-red-500/[0.06] p-3 text-xs leading-5 text-[var(--ls-danger-text)]">
          <strong>{analysis.delivery_failure ? t("Delivery exhausted:") : analysis.skipped_delivery ? t("Step consumed:") : t("Last error:")}</strong>{" "}
          {analysis.delivery_failure
            ? t("The current Issue step exhausted queue retries. The retained revision can be retried after its gates are ready.")
            : analysis.skipped_delivery
              ? t("The current Issue message was consumed without advancing this job. Reconnect if needed, then retry the exact retained snapshot.")
              : analysis.last_error}
          {analysis.connection_blocked ? null : !analysis.retry_readiness?.installation_ready ? (
            <p className="mt-2">
              {t(" This connection is inactive or not verified. Repair it before retrying. ")}{" "}
              <Link className="font-semibold underline underline-offset-2" href={`/${encodeURIComponent(org)}/connect/${encodeURIComponent(analysis.installation_id)}`}>
                {t(" Open this connection ")}</Link>
            </p>
          ) : !analysis.retry_readiness.setup_ready ? (
            <p className="mt-2">{t("Finish workspace setup before retrying this Issue analysis.")}</p>
          ) : !analysis.retry_readiness.role_allowed ? (
            <p className="mt-2">{t("Your workspace role cannot retry this Issue analysis. Ask an authorized reviewer or administrator.")}</p>
          ) : !analysis.retry_readiness.can_retry ? (
            <p className="mt-2">{t("This retained attempt is not eligible for another retry. Review its revision and retry limit before taking further action.")}</p>
          ) : null}
          <ProviderIssueRetry analysisID={analysis.id} deliveryFailure={analysis.delivery_failure} skippedDelivery={analysis.skipped_delivery} enabled={retryEnabled && Boolean(analysis.retry_readiness?.can_retry)} org={org} revision={analysis.revision} state={analysis.state} />
        </div>
      ) : null}
      <section className="mt-5 border-t border-[var(--ls-line)] pt-5">
        <div className="flex items-center gap-2"><Bot className="size-4 text-[var(--ls-accent)]" /><h3 className="text-sm font-semibold text-[var(--ls-text)]">{t("Latest AI analysis")}</h3></div>
        {analysis.analysis ? <StructuredAnalysis source={analysis.analysis} /> : <p className="mt-3 rounded-[12px] bg-[var(--ls-surface-muted)] p-3 text-sm leading-6 text-[var(--ls-text-secondary)]">{t("The latest revision has not produced a final analysis yet.")}</p>}
      </section>
      <section className="mt-6 border-t border-[var(--ls-line)] pt-5">
        <div className="flex items-center gap-2"><History className="size-4 text-[var(--ls-text-tertiary)]" /><h3 className="text-sm font-semibold text-[var(--ls-text)]">{t("Webhook revision evidence")}</h3></div>
        <ol className="mt-3 space-y-3 border-l border-[var(--ls-line-strong)] pl-4">
          {analysis.receipts.map((receipt) => <li className="relative text-xs text-[var(--ls-text-secondary)]" key={`${receipt.revision}-${receipt.admitted_at}`}><span className="absolute -left-[19px] top-1 size-2 rounded-full bg-[var(--ls-accent)]" /><span className="font-mono text-[var(--ls-text)]">r{receipt.revision}</span> · {receipt.action}<span className="mt-1 block text-[var(--ls-text-tertiary)]">{receipt.event_name} · {formatExactTime(receipt.admitted_at, language)}</span></li>)}
        </ol>
      </section>
      <dl className="mt-6 grid gap-3 border-t border-[var(--ls-line)] pt-5 text-xs">
        <Evidence label={t("Model route snapshot")} value={analysis.model_route_sha256} />
        <Evidence label={t("Prompt snapshot")} value={analysis.prompt_config_sha256} />
        <Evidence label={t("Issue format snapshot")} value={analysis.issue_triage_config_sha256} />
        <Evidence label={t("Latest update")} value={formatExactTime(analysis.updated_at, language)} />
      </dl>
    </aside>
  );
}

function StructuredAnalysis({ source }: { source: string }) {
  return <div className="mt-3 space-y-2.5 text-sm leading-6 text-[var(--ls-text-secondary)]">{renderAnalysisBlocks(source.split(/\r?\n/))}</div>;
}

function renderAnalysisBlocks(lines: string[], prefix = "analysis"): ReactNode[] {
  const nodes: ReactNode[] = [];
  for (let index = 0; index < lines.length;) {
    const value = lines[index].trim();
    const key = `${prefix}-${index}`;
    if (!value) {
      index += 1;
      continue;
    }
    if (value === "<details>") {
      const closing = lines.findIndex((line, candidate) => candidate > index && line.trim() === "</details>");
      const end = closing === -1 ? lines.length : closing;
      const detailLines = lines.slice(index + 1, end);
      const summaryMatch = detailLines[0]?.trim().match(/^<summary><strong>(.+)<\/strong><\/summary>$/);
      const summary = summaryMatch?.[1] ?? "Analysis detail";
      const content = summaryMatch ? detailLines.slice(1) : detailLines;
      nodes.push(
        <details className="group rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)]" key={key}>
          <summary className="luminous-focus cursor-pointer list-none rounded-[12px] px-3 py-2.5 text-sm font-semibold text-[var(--ls-text)] marker:hidden">
            <span className="flex items-center justify-between gap-3"><span>{summary}</span><span aria-hidden className="text-[var(--ls-text-tertiary)] transition group-open:rotate-45">＋</span></span>
          </summary>
          <div className="space-y-2.5 border-t border-[var(--ls-line)] px-3 py-3">{renderAnalysisBlocks(content, key)}</div>
        </details>,
      );
      index = closing === -1 ? lines.length : closing + 1;
      continue;
    }
    if (isTableRow(value) && index + 1 < lines.length && isTableDivider(lines[index + 1].trim())) {
      const headers = parseTableRow(value);
      const rows: string[][] = [];
      index += 2;
      while (index < lines.length && isTableRow(lines[index].trim())) {
        rows.push(parseTableRow(lines[index].trim()));
        index += 1;
      }
      nodes.push(
        <div className="overflow-x-auto rounded-[10px] border border-[var(--ls-line)]" key={key}>
          <table className="w-full min-w-[360px] border-collapse text-left text-xs">
            <thead className="bg-[var(--ls-surface-muted)] text-[var(--ls-text)]"><tr>{headers.map((header, cell) => <th className="border-b border-[var(--ls-line)] px-3 py-2 font-semibold" key={`${key}-h-${cell}`}>{renderInline(header)}</th>)}</tr></thead>
            <tbody>{rows.map((row, rowIndex) => <tr className="border-b border-[var(--ls-line)] last:border-0" key={`${key}-r-${rowIndex}`}>{headers.map((_, cell) => <td className="px-3 py-2 align-top" key={`${key}-r-${rowIndex}-c-${cell}`}>{renderInline(row[cell] ?? "")}</td>)}</tr>)}</tbody>
          </table>
        </div>,
      );
      continue;
    }
    if (/^[-*] /.test(value)) {
      const items: string[] = [];
      while (index < lines.length && /^[-*] /.test(lines[index].trim())) {
        items.push(lines[index].trim().slice(2));
        index += 1;
      }
      nodes.push(<ul className="space-y-1.5 pl-5" key={key}>{items.map((item, itemIndex) => <li className="list-disc pl-1 marker:text-[var(--ls-accent)]" key={`${key}-${itemIndex}`}>{renderInline(item)}</li>)}</ul>);
      continue;
    }
    if (/^\d+\. /.test(value)) {
      const items: string[] = [];
      while (index < lines.length && /^\d+\. /.test(lines[index].trim())) {
        items.push(lines[index].trim().replace(/^\d+\. /, ""));
        index += 1;
      }
      nodes.push(<ol className="space-y-1.5 pl-5" key={key}>{items.map((item, itemIndex) => <li className="list-decimal pl-1 marker:font-semibold marker:text-[var(--ls-accent)]" key={`${key}-${itemIndex}`}>{renderInline(item)}</li>)}</ol>);
      continue;
    }
    if (/^---+$/.test(value)) {
      nodes.push(<hr className="border-[var(--ls-line)]" key={key} />);
      index += 1;
      continue;
    }
    if (value.startsWith("#### ")) nodes.push(<h6 className="pt-1 text-xs font-semibold uppercase tracking-[.08em] text-[var(--ls-text)]" key={key}>{renderInline(value.slice(5))}</h6>);
    else if (value.startsWith("### ")) nodes.push(<h5 className="pt-2 text-sm font-semibold text-[var(--ls-text)]" key={key}>{renderInline(value.slice(4))}</h5>);
    else if (value.startsWith("## ")) nodes.push(<h4 className="border-b border-[var(--ls-line)] pb-1.5 pt-3 text-base font-semibold text-[var(--ls-text)]" key={key}>{renderInline(value.slice(3))}</h4>);
    else if (value.startsWith("> ")) nodes.push(<p className="rounded-[10px] border border-sky-500/15 bg-sky-500/[0.055] px-3 py-2 text-[var(--ls-text)]" key={key}>{renderInline(value.slice(2))}</p>);
    else if (!value.startsWith("</details>") && !value.startsWith("<summary>")) nodes.push(<p key={key}>{renderInline(value)}</p>);
    index += 1;
  }
  return nodes;
}

function isTableRow(value: string) {
  return value.startsWith("|") && value.endsWith("|") && value.split("|").length >= 4;
}

function isTableDivider(value: string) {
  return isTableRow(value) && parseTableRow(value).every((cell) => /^:?-{3,}:?$/.test(cell));
}

function parseTableRow(value: string) {
  return value.slice(1, -1).split("|").map((cell) => cell.trim());
}

function renderInline(value: string): ReactNode[] {
  const tokenPattern = /(\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\(https?:\/\/[^)]+\))/g;
  return value.split(tokenPattern).filter(Boolean).map((token, index) => {
    if (token.startsWith("**") && token.endsWith("**")) return <strong className="font-semibold text-[var(--ls-text)]" key={index}>{token.slice(2, -2)}</strong>;
    if (token.startsWith("`") && token.endsWith("`")) return <code className="rounded bg-[var(--ls-surface-muted)] px-1 py-0.5 font-mono text-[12px] text-[var(--ls-text)]" key={index}>{token.slice(1, -1)}</code>;
    const link = token.match(/^\[([^\]]+)\]\((https?:\/\/[^)]+)\)$/);
    if (link) return <a className="font-medium text-[var(--ls-accent)] underline-offset-2 hover:underline" href={link[2]} key={index} rel="noreferrer" target="_blank">{link[1]}</a>;
    return token;
  });
}

function Metric({ label, tone, value }: { label: string; tone: "neutral" | "accent" | "success" | "danger"; value: number }) {
  return <article className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 shadow-[var(--ls-shadow-control)]"><div className="flex items-center justify-between"><p className="text-xs font-medium text-[var(--ls-text-secondary)]">{label}</p><span className={cn("size-2 rounded-full", tone === "accent" && "bg-sky-500", tone === "success" && "bg-emerald-500", tone === "danger" && "bg-red-500", tone === "neutral" && "bg-slate-400")} /></div><p className="mt-2 text-2xl font-semibold tabular-nums tracking-[-.04em] text-[var(--ls-text)]">{value}</p></article>;
}

async function AnalysisState({ state, deliveryFailure = false, skippedDelivery = false, connectionBlocked = false }: { state: ProviderIssueAnalysisState; deliveryFailure?: boolean; skippedDelivery?: boolean; connectionBlocked?: boolean }) {
  const language = await getUiLanguage();
  const t = (source: string, values?: WorkflowMessageValues) => workflowText(language, source, values);
  if (deliveryFailure) {
    return <span className="inline-flex items-center gap-1.5 rounded-full bg-red-500/10 px-2.5 py-1 text-[11px] font-semibold text-red-600 dark:text-red-400" title={t("Delivery retries exhausted while the retained job state is {state}", { state: workflowStatus(language, state) })}><CircleAlert className="size-3" />{t("Needs attention")}</span>;
  }
  if (connectionBlocked) {
    return <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/10 px-2.5 py-1 text-[11px] font-semibold text-amber-700 dark:text-amber-300" title={t("Connection is inactive or unverified; retained job state is {state}", { state: workflowStatus(language, state) })}><CircleAlert className="size-3" />{t("Connection blocked")}</span>;
  }
  if (skippedDelivery) {
    return <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/10 px-2.5 py-1 text-[11px] font-semibold text-amber-700 dark:text-amber-300" title={t("Current {state} step was consumed without progress; retry is required", { state: workflowStatus(language, state) })}><CircleAlert className="size-3" />{t("Retry required")}</span>;
  }
  const Icon = state === "completed" ? CheckCircle2 : state === "failed" ? CircleAlert : Clock3;
  return <span className={cn("inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11px] font-semibold capitalize", state === "completed" && "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400", state === "failed" && "bg-red-500/10 text-red-600 dark:text-red-400", state === "acknowledged" && "bg-sky-500/10 text-sky-600 dark:text-sky-400", state === "queued" && "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]")}><Icon className="size-3" />{workflowStatus(language, state)}</span>;
}

function Evidence({ label, value }: { label: string; value: string }) {
  return <div><dt className="text-[var(--ls-text-tertiary)]">{label}</dt><dd className="mt-1 break-all font-mono text-[11px] text-[var(--ls-text-secondary)]">{value}</dd></div>;
}

function validState(value?: string): ProviderIssueAnalysisFilterState | undefined {
  return value === "queued" || value === "acknowledged" || value === "completed" || value === "failed" || value === "needs_attention" ? value : undefined;
}

function providerIssueHref(org: string, query: Query, updates: Partial<Query>) {
  const next = new URLSearchParams();
  const values = { ...query, ...updates };
  if (validState(values.state)) next.set("state", values.state!);
  if (values.q?.trim()) next.set("q", values.q.trim());
  if (values.selected) next.set("selected", values.selected);
  const encoded = next.toString();
  return `/${encodeURIComponent(org)}/provider-issues${encoded ? `?${encoded}` : ""}`;
}

function formatTime(value: string, language: UiLanguage) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return workflowText(language, "Unknown");
  return new Intl.RelativeTimeFormat(language, { numeric: "auto" }).format(
    Math.round((date.getTime() - Date.now()) / 3_600_000),
    "hour",
  );
}

function formatExactTime(value: string, language: UiLanguage) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat(language, { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" }).format(date) + " UTC";
}
