import Link from "next/link";
import {
  ArrowUpRight,
  Clock3,
  DatabaseZap,
  Download,
  Gauge,
  Infinity as InfinityIcon,
  Layers3,
  ShieldCheck,
} from "lucide-react";

import { UsageEntitlementForm } from "@/components/console/usage-entitlement-form";
import { UsageReconciliationPanel } from "@/components/console/usage-reconciliation-panel";
import { DataFreshness, PageState, RecoveryAction } from "@/components/console/page-state";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { getUsageDashboard } from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

const count = new Intl.NumberFormat("en");
const timestamp = new Intl.DateTimeFormat("en", {
  dateStyle: "medium",
  timeStyle: "short",
  timeZone: "UTC",
});

type UsageTab = "overview" | "ledger" | "limits";

const tabs: Array<{ key: UsageTab; label: string }> = [
  { key: "overview", label: "Overview" },
  { key: "ledger", label: "Usage ledger" },
  { key: "limits", label: "Limits" },
];

export default async function UsagePage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string }>;
  searchParams: Promise<{ tab?: string }>;
}) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const tab = tabs.some((item) => item.key === query.tab)
    ? (query.tab as UsageTab)
    : "overview";
  const data = await getUsageDashboard(org);
  const hasEvidence = data.source === "live" || data.source === "demo";
  const limit = data.entitlement.monthly_review_limit;
  const consumed = data.settled + data.reserved;
  const periodStart = new Date(data.period_start).toISOString().slice(0, 10);
  const progress =
    limit > 0 ? Math.min(100, Math.round((consumed / limit) * 100)) : 0;

  return (
    <div className="space-y-7">
      <div className="flex flex-col justify-between gap-5 lg:flex-row lg:items-end">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">
            Capacity governance
          </p>
          <div className="mt-2 flex min-w-0 items-center gap-2"><h1 className="text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">
            Usage & quota
          </h1><HelpHint label="Usage & quota">Reserve review capacity before execution, settle completed runs,
            and retain a repository-attributed ledger. Commercial billing
            remains separate.</HelpHint></div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {data.source === "live" ? (
            <a
              className="luminous-focus inline-flex h-9 items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs font-semibold text-[var(--ls-text)] shadow-[var(--ls-shadow-control)] hover:bg-[var(--ls-surface-muted)]"
              download
              href={`/api/tenants/${encodeURIComponent(org)}/usage/export?period_start=${encodeURIComponent(periodStart)}`}
            >
              <Download className="size-3.5" />
              Export CSV
            </a>
          ) : null}
          <DataFreshness detail={data.detail} state={data.source} />
        </div>
      </div>

      <TabStateRouter
        className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]"
        label="Usage views"
      >
        {tabs.map((item) => (
          <Link
            aria-current={tab === item.key ? "page" : undefined}
            aria-selected={tab === item.key}
            className={cn(
              "luminous-focus relative h-11 shrink-0 rounded-t-[10px] px-4 py-3 text-sm font-medium",
              tab === item.key
                ? "text-[var(--ls-text)]"
                : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]",
            )}
            href={`/${encodeURIComponent(org)}/usage?tab=${item.key}`}
            key={item.key}
            role="tab"
            tabIndex={tab === item.key ? 0 : -1}
          >
            {item.label}
            {tab === item.key ? (
              <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" />
            ) : null}
          </Link>
        ))}
      </TabStateRouter>

      {data.source === "demo" ? (
        <div className="rounded-[14px] border border-amber-500/25 bg-amber-500/[0.06] px-4 py-3 text-sm leading-6 text-[var(--ls-warning-text)]">
          Preview usage data is read-only. Admission limits remain unchanged, and this page does not create invoices, subscriptions, or payment activity.
        </div>
      ) : null}

      {!hasEvidence ? (
        <PageState
          action={data.source === "unconfigured"
            ? <RecoveryAction href={`/${encodeURIComponent(org)}/connect`} variant="primary">Check connections</RecoveryAction>
            : <RecoveryAction href={`/${encodeURIComponent(org)}/settings/platform-health`}>View platform health</RecoveryAction>}
          detail={data.detail ?? "Usage reservations, settlements, and limits could not be verified. No zero balance or unlimited entitlement is inferred."}
          kind={data.source === "unconfigured" ? "first-use-empty" : "unavailable"}
          title={data.source === "unconfigured" ? "Usage needs a connected control plane" : "Usage evidence unavailable"}
        />
      ) : tab === "overview" ? (
        <Overview
          consumed={consumed}
          data={data}
          limit={limit}
          progress={progress}
        />
      ) : null}
      {hasEvidence && tab === "ledger" ? <Ledger data={data} org={org} /> : null}
      {hasEvidence && tab === "limits" ? <Limits data={data} org={org} /> : null}
    </div>
  );
}

function Overview({
  consumed,
  data,
  limit,
  progress,
}: {
  consumed: number;
  data: Awaited<ReturnType<typeof getUsageDashboard>>;
  limit: number;
  progress: number;
}) {
  return (
    <div className="space-y-5">
      <div className="grid gap-3 md:grid-cols-3">
        <Metric
          detail="Completed in this UTC period"
          icon={ShieldCheck}
          label="Settled reviews"
          value={count.format(data.settled)}
        />
        <Metric
          detail="Admitted and still in flight"
          icon={Clock3}
          label="Reserved"
          value={count.format(data.reserved)}
        />
        <Metric
          detail={
            limit > 0
              ? `${progress}% committed · warning at ${data.entitlement.soft_warning_percent}%`
              : "Self-hosted quota is disabled"
          }
          icon={limit > 0 ? Layers3 : InfinityIcon}
          label="Remaining"
          value={limit > 0 ? count.format(data.remaining ?? 0) : "Unlimited"}
        />
      </div>

      {limit > 0 ? (
        <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
          <div className="flex justify-between gap-4 text-xs">
            <span className="text-[var(--ls-text-secondary)]">
              Monthly admission capacity
            </span>
            <span
              className={
                data.warning
                  ? "text-[var(--ls-warning-text)]"
                  : "text-[var(--ls-text-tertiary)]"
              }
            >
              {count.format(consumed)} / {count.format(limit)}
            </span>
          </div>
          <div className="mt-3 h-2 overflow-hidden rounded-full bg-[var(--ls-surface-muted)]">
            <div
              className={cn(
                "h-full rounded-full",
                data.warning
                  ? "bg-[var(--ls-warning-text)]"
                  : "bg-[var(--ls-accent)]",
              )}
              style={{ width: `${progress}%` }}
            />
          </div>
        </section>
      ) : null}

      <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
        <div className="flex items-center justify-between border-b border-[var(--ls-line)] px-5 py-4">
          <div>
            <h2 className="text-sm font-semibold text-[var(--ls-text)]">
              Repository attribution
            </h2>
            <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
              Current UTC calendar month
            </p>
          </div>
          <span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">
            {data.repositories.length} repositories
          </span>
        </div>
        {data.repositories.length ? (
          data.repositories.map((item) => (
            <div
              className="flex items-center justify-between gap-4 border-b border-[var(--ls-line)] px-5 py-3.5 last:border-0"
              key={item.repository}
            >
              <span className="truncate font-mono text-xs text-[var(--ls-text)]">
                {item.repository}
              </span>
              <span className="shrink-0 text-xs text-[var(--ls-text-secondary)]">
                {item.settled} settled · {item.reserved} reserved
              </span>
            </div>
          ))
        ) : (
          <EmptyState
            detail="Repository attribution appears after the first review is admitted."
            icon={DatabaseZap}
            title="No usage in this period"
          />
        )}
      </section>
    </div>
  );
}

function Ledger({
  data,
  org,
}: {
  data: Awaited<ReturnType<typeof getUsageDashboard>>;
  org: string;
}) {
  return (
    <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
      <div className="flex flex-col justify-between gap-3 border-b border-[var(--ls-line)] px-5 py-4 sm:flex-row sm:items-center">
        <div>
          <div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">
            Immutable usage ledger
          </h2><HelpHint label="Immutable usage ledger">Reserve, settle, release, and adjustment events are append-only and
            idempotent.</HelpHint></div>
        </div>
        <span className="w-fit rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">
          {data.ledger.length} events
        </span>
      </div>
      {data.ledger.length ? (
        data.ledger.map((entry) => (
          <div
            className="grid gap-2 border-b border-[var(--ls-line)] px-5 py-3.5 last:border-0 sm:grid-cols-[120px_minmax(0,1fr)_120px_210px] sm:items-center"
            key={entry.id}
          >
            <span className="w-fit rounded-full border border-[var(--ls-line-strong)] px-2 py-0.5 text-[10px] font-semibold uppercase text-[var(--ls-text-secondary)]">
              {entry.event_kind}
            </span>
            <span className="truncate font-mono text-xs text-[var(--ls-text)]">
              {entry.repository || "workspace"}
            </span>
            <span className="text-xs text-[var(--ls-text-secondary)]">
              {entry.quantity} {entry.unit}
            </span>
            <span className="flex items-center justify-between gap-2 text-xs text-[var(--ls-text-tertiary)]">
              <time dateTime={entry.occurred_at}>
                {timestamp.format(new Date(entry.occurred_at))} UTC
              </time>
              {entry.run_id ? (
                <Link
                  aria-label={`Open review run ${entry.run_id}`}
                  className="luminous-focus rounded text-[var(--ls-accent)] hover:opacity-80"
                  href={`/${encodeURIComponent(org)}/tasks/${encodeURIComponent(entry.run_id)}?tab=evidence`}
                >
                  <ArrowUpRight className="size-3.5" />
                </Link>
              ) : null}
            </span>
          </div>
        ))
      ) : (
        <EmptyState
          detail="Ledger entries appear when a review reserves capacity."
          icon={DatabaseZap}
          title="No usage events yet"
        />
      )}
    </section>
  );
}

function Limits({
  data,
  org,
}: {
  data: Awaited<ReturnType<typeof getUsageDashboard>>;
  org: string;
}) {
  return (
    <div className="grid gap-5 xl:grid-cols-[minmax(0,0.95fr)_minmax(0,1.05fr)]">
      <UsageEntitlementForm
        dashboard={data}
        enabled={data.source === "live" && data.can_manage}
        org={org}
      />
      <section className="h-fit rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
        <span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-surface-muted)] text-[var(--ls-accent)]">
          <Gauge className="size-4" />
        </span>
        <div className="mt-4 flex min-w-0 items-center gap-2"><h2 className="text-base font-semibold text-[var(--ls-text)]">
          Self-hosted admission boundary
        </h2><HelpHint label="Self-hosted admission boundary">This policy limits review admissions inside this workspace. A value of
          0 means unlimited local operation; it does not activate a paid plan,
          create an invoice, or call a hosted billing service.</HelpHint></div>
        <dl className="mt-5 divide-y divide-[var(--ls-line)] rounded-[12px] bg-[var(--ls-surface-muted)] px-4">
          <BoundaryFact
            label="Period"
            value={`${timestamp.format(new Date(data.period_start))} — ${timestamp.format(new Date(data.period_end))}`}
          />
          <BoundaryFact
            label="Enforcement"
            value="Atomic reservation before queue admission"
          />
          <BoundaryFact
            label="Reconciliation"
            value="Settle or release from durable run outcomes"
          />
          <BoundaryFact
            label="Commercial billing"
            value="Not configured in this core flow"
          />
        </dl>
      </section>
      <div className="xl:col-span-2">
        <UsageReconciliationPanel
          dashboard={data}
          enabled={data.source === "live" && data.can_manage}
          org={org}
        />
      </div>
    </div>
  );
}

function Metric({
  detail,
  icon: Icon,
  label,
  value,
}: {
  detail: string;
  icon: typeof ShieldCheck;
  label: string;
  value: string;
}) {
  return (
    <div className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
      <div className="flex items-center gap-2 text-xs text-[var(--ls-text-secondary)]">
        <Icon className="size-4 text-[var(--ls-accent)]" />
        {label}
      </div>
      <p className="mt-4 text-2xl font-semibold tracking-tight text-[var(--ls-text)]">
        {value}
      </p>
      <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">{detail}</p>
    </div>
  );
}

function EmptyState({
  detail,
  icon: Icon,
  title,
}: {
  detail: string;
  icon: typeof DatabaseZap;
  title: string;
}) {
  return (
    <div className="grid min-h-48 place-items-center p-8 text-center">
      <div>
        <Icon className="mx-auto size-6 text-[var(--ls-text-tertiary)]" />
        <p className="mt-3 text-sm font-medium text-[var(--ls-text)]">{title}</p>
        <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">{detail}</p>
      </div>
    </div>
  );
}

function BoundaryFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col justify-between gap-1 py-3 text-xs sm:flex-row sm:gap-5">
      <dt className="text-[var(--ls-text-tertiary)]">{label}</dt>
      <dd className="text-left font-medium text-[var(--ls-text)] sm:text-right">
        {value}
      </dd>
    </div>
  );
}
