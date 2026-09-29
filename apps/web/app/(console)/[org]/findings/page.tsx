import Link from "next/link";
import { Activity, CircleAlert, ShieldAlert } from "lucide-react";

import { FindingFeedbackDashboardView } from "@/components/console/finding-feedback-dashboard";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { getFindingExplorerPage, getFindingFeedbackDashboard } from "@/lib/control-api";
import { cn } from "@/lib/utils";

type FindingView = "active" | "high-risk" | "actioned";

const views: Array<{ key: FindingView; label: string }> = [
  { key: "active", label: "Active" },
  { key: "high-risk", label: "High risk" },
  { key: "actioned", label: "Actioned" },
];

export default async function FindingsPage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string }>;
  searchParams: Promise<{ view?: string | string[]; repository?: string | string[]; q?: string | string[]; cursor?: string | string[]; direction?: string | string[] }>;
}) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const requestedView = firstValue(query.view);
  const view = views.some((item) => item.key === requestedView)
    ? (requestedView as FindingView)
    : "active";
  const repository = firstValue(query.repository).trim();
  const search = firstValue(query.q).trim();
  const cursor = firstValue(query.cursor);
  const direction = firstValue(query.direction) === "before" ? "before" : "after";
  const [data, findingPage] = await Promise.all([
    getFindingFeedbackDashboard(org),
    getFindingExplorerPage(org, { view, repository, query: search, cursor, direction }),
  ]);
  const fullyLive = data.source === "live" && findingPage.source === "live";
  const baseQuery = new URLSearchParams({ view });
  if (repository) baseQuery.set("repository", repository);
  if (search) baseQuery.set("q", search);
  const pageHref = (pageCursor: string, pageDirection: "after" | "before") => {
    const params = new URLSearchParams(baseQuery);
    params.set("cursor", pageCursor);
    params.set("direction", pageDirection);
    return `/${encodeURIComponent(org)}/findings?${params.toString()}`;
  };

  return (
    <div className="space-y-7">
      <header className="flex flex-col justify-between gap-5 lg:flex-row lg:items-end">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">
            Cross-run evidence
          </p>
          <h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">
            Finding explorer
          </h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">
            Triage findings across review runs without changing the immutable AI
            result. Dispositions are actor-scoped, auditable feedback.
          </p>
        </div>
        <span
          className={cn(
            "inline-flex w-fit items-center gap-2 rounded-full px-3 py-1.5 text-xs",
            fullyLive
              ? "bg-emerald-500/10 text-[var(--ls-success-text)]"
              : "bg-amber-500/10 text-[var(--ls-warning-text)]",
          )}
        >
          <span className="size-1.5 rounded-full bg-current" />
          {fullyLive ? "Live control-plane data" : data.source === "live" ? "Partial control-plane data" : data.source.replaceAll("_", " ")}
        </span>
      </header>

      <TabStateRouter
        className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]"
        label="Finding views"
      >
        {views.map((item) => (
          <Link
            aria-current={view === item.key ? "page" : undefined}
            aria-selected={view === item.key}
            className={cn(
              "luminous-focus relative h-11 shrink-0 rounded-t-[10px] px-4 py-3 text-sm font-medium",
              view === item.key
                ? "text-[var(--ls-text)]"
                : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]",
            )}
            href={`/${encodeURIComponent(org)}/findings?${new URLSearchParams({ ...Object.fromEntries(baseQuery), view: item.key }).toString()}`}
            key={item.key}
            role="tab"
            tabIndex={view === item.key ? 0 : -1}
          >
            {item.label}
            {view === item.key ? (
              <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" />
            ) : null}
          </Link>
        ))}
      </TabStateRouter>

      <form action={`/${encodeURIComponent(org)}/findings`} className="flex flex-wrap items-end gap-3 rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4" method="GET">
        <input name="view" type="hidden" value={view} />
        <label className="min-w-[220px] flex-1 text-xs font-medium text-[var(--ls-text-secondary)]">
          Repository
          <input className="luminous-focus mt-1 block h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" defaultValue={repository} maxLength={512} name="repository" placeholder="owner/repository" />
        </label>
        <label className="min-w-[220px] flex-[2] text-xs font-medium text-[var(--ls-text-secondary)]">
          Search evidence
          <input className="luminous-focus mt-1 block h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" defaultValue={search} maxLength={128} name="q" placeholder="Path, category, repository or finding text" />
        </label>
        <button className="luminous-focus h-10 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-medium text-white" type="submit">Apply filters</button>
        {(repository || search) ? <Link className="luminous-focus inline-flex h-10 items-center rounded-[10px] px-3 text-sm text-[var(--ls-text-secondary)]" href={`/${encodeURIComponent(org)}/findings?view=${view}`}>Clear</Link> : null}
      </form>

      <div className="flex items-start gap-3 rounded-[14px] border border-violet-500/20 bg-violet-500/[0.07] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
        {view === "high-risk" ? (
          <ShieldAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-warning-text)]" />
        ) : view === "actioned" ? (
          <Activity className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />
        ) : (
          <CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />
        )}
        {view === "high-risk"
          ? "Only unresolved high and critical findings are shown. Severity does not replace the configured merge-gate policy."
          : view === "actioned"
            ? "Resolved and won't-fix dispositions remain visible here; the original finding and its run evidence are unchanged."
            : "Active shows findings without an actor disposition. Provider comments and exact run evidence remain the source of review context."}
      </div>

      <FindingFeedbackDashboardView
        data={data}
        enabled={fullyLive}
        findingHeading="Finding results"
        findings={findingPage.findings}
        findingsError={findingPage.source === "unavailable" || findingPage.source === "unconfigured" ? findingPage.detail : undefined}
        org={org}
      />
      {findingPage.source === "live" && (findingPage.previous_cursor || findingPage.next_cursor) ? (
        <nav aria-label="Finding results pages" className="flex items-center justify-between gap-3 text-sm">
          {findingPage.previous_cursor ? <Link className="luminous-focus rounded-[10px] border border-[var(--ls-line-strong)] px-4 py-2 text-[var(--ls-text)]" href={pageHref(findingPage.previous_cursor, "before")}>Previous findings</Link> : <span />}
          {findingPage.next_cursor ? <Link className="luminous-focus rounded-[10px] border border-[var(--ls-line-strong)] px-4 py-2 text-[var(--ls-text)]" href={pageHref(findingPage.next_cursor, "after")}>Next findings</Link> : <span />}
        </nav>
      ) : null}
    </div>
  );
}

function firstValue(value: string | string[] | undefined): string {
  return Array.isArray(value) ? value[0] ?? "" : value ?? "";
}
