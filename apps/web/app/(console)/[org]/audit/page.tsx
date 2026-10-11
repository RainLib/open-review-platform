import Link from "next/link";
import { ChevronRight, Search, ShieldCheck, X } from "lucide-react";

import { AuditExportManager } from "@/components/console/audit-export-manager";
import { DataFreshness, PageState, RecoveryAction } from "@/components/console/page-state";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { getAuditData, getAuditEvent, getDataGovernanceData } from "@/lib/control-api";
import type { AuditData } from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

const dateFormatter = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "medium", timeZone: "UTC" });
type AuditTab = "events" | "exports";
type AuditScope = { actor: string; action: string; target: string; from: string; through: string };

export default async function AuditPage({ params, searchParams }: { params: Promise<{ org: string }>; searchParams: Promise<{ actor?: string | string[]; action?: string | string[]; target?: string | string[]; from?: string | string[]; through?: string | string[]; before?: string | string[]; event?: string | string[]; tab?: string }> }) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const actor = typeof query.actor === "string" ? query.actor : "";
  const action = typeof query.action === "string" ? query.action : "";
  const target = typeof query.target === "string" ? query.target : "";
  const from = typeof query.from === "string" ? query.from : "";
  const through = typeof query.through === "string" ? query.through : "";
  const scope: AuditScope = { actor, action, target, from, through };
  const before = typeof query.before === "string" ? query.before : "";
  const eventId = typeof query.event === "string" ? query.event : "";
  const tab: AuditTab = query.tab === "exports" ? "exports" : "events";
  const [auditData, eventDetail, governance] = await Promise.all([
    tab === "events" ? getAuditData(org, { actor, action, target, from: auditDateBound(from), until: auditDateBound(through, true), before }) : undefined,
    tab === "events" && eventId ? getAuditEvent(org, eventId) : undefined,
    tab === "exports" ? getDataGovernanceData(org) : undefined,
  ]);
  const data: AuditData = auditData ?? { source: governance?.source ?? "unconfigured", events: [], detail: governance?.detail };
  const selected = eventDetail?.event;
  const today = new Date();
  const start = new Date(today);
  start.setUTCDate(start.getUTCDate() - 30);
  const dateInputValue = (value: Date) => value.toISOString().slice(0, 10);
  return <div className="space-y-7">
    <div className="flex flex-col justify-between gap-5 lg:flex-row lg:items-end"><div><p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">Governance evidence</p><div className="mt-2 flex min-w-0 items-center gap-2"><h1 className="text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">Audit trail</h1><HelpHint label="Audit trail">Inspect append-only workspace changes and their recorded metadata.</HelpHint></div></div><DataFreshness detail={data.detail} state={data.source} /></div>
    <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="Audit views"><AuditTabLink href={`/${org}/audit?tab=events`} label="Events" selected={tab === "events"} /><AuditTabLink href={`/${org}/audit?tab=exports`} label="Export jobs" selected={tab === "exports"} /></TabStateRouter>
    {tab === "exports" ? <AuditExportManager detail={governance?.detail} endDate={dateInputValue(today)} jobs={governance?.jobs ?? []} org={org} source={governance?.source ?? "unconfigured"} startDate={dateInputValue(start)} /> : <>
      <form className="grid gap-3 rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 shadow-[var(--ls-shadow-control)] sm:grid-cols-2 xl:grid-cols-[repeat(5,minmax(0,1fr))_auto]" method="get"><input name="tab" type="hidden" value="events" /><Field defaultValue={actor} label="Actor" name="actor" placeholder="user subject" /><Field defaultValue={action} label="Action prefix" mono name="action" placeholder="notification_" /><Field defaultValue={target} label="Target contains" mono name="target" placeholder="repository or object" /><Field defaultValue={from} label="From (UTC)" name="from" type="date" /><Field defaultValue={through} label="Through (UTC)" name="through" type="date" /><button className="luminous-focus mt-auto inline-flex h-10 items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-5 text-sm font-medium text-white" type="submit"><Search className="size-4" />Filter</button></form>
      <div className={cn("grid gap-5", eventId && "xl:grid-cols-[minmax(0,1fr)_380px]")}>
        <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
          <div className="flex items-center justify-between border-b border-[var(--ls-line)] px-5 py-4"><div><h2 className="text-sm font-semibold text-[var(--ls-text)]">Recorded events</h2><p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">Newest first · UTC</p></div><span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">{data.events.length} events</span></div>
          {data.events.length ? <div className="divide-y divide-[var(--ls-line)]">{data.events.map((event) => <Link className={cn("grid gap-3 px-5 py-4 transition hover:bg-[var(--ls-surface-muted)] sm:grid-cols-[180px_minmax(0,1fr)_minmax(0,1fr)_180px_auto] sm:items-center", selected?.id === event.id && "bg-[var(--ls-surface-selected)]")} href={auditHref(org, scope, before, event.id)} key={event.id}><time className="text-xs tabular-nums text-[var(--ls-text-tertiary)]" dateTime={event.created_at}>{dateFormatter.format(new Date(event.created_at))}</time><span className="truncate font-mono text-xs text-[var(--ls-accent)]">{event.action}</span><span className="truncate text-sm text-[var(--ls-text)]">{event.target}</span><span className="truncate text-xs text-[var(--ls-text-secondary)]">{event.actor_subject}</span><ChevronRight className="size-4 text-[var(--ls-text-tertiary)]" /></Link>)}</div> : <AuditEmptyState before={before} detail={data.detail} org={org} scope={scope} source={data.source} />}
          {data.source === "live" || data.source === "demo" ? <nav aria-label="Audit history pages" className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--ls-line)] px-5 py-4 text-xs"><div>{before ? <Link className="luminous-focus font-semibold text-[var(--ls-accent)]" href={auditHref(org, scope)}>Latest events</Link> : <span className="text-[var(--ls-text-tertiary)]">Latest events</span>}</div>{data.nextCursor ? <Link className="luminous-focus inline-flex items-center gap-1 font-semibold text-[var(--ls-accent)]" href={auditHref(org, scope, data.nextCursor)}>Older events <ChevronRight className="size-3.5" /></Link> : <span className="text-[var(--ls-text-tertiary)]">End of history</span>}</nav> : null}
        </section>
        {selected ? <aside className="luminous-frosted h-fit rounded-[20px] border border-[var(--ls-line-strong)] p-5 shadow-[var(--ls-shadow-float)] xl:sticky xl:top-20"><div className="flex items-center justify-between gap-3"><div className="flex items-center gap-2"><ShieldCheck className="size-4 text-[var(--ls-accent)]" /><h2 className="font-semibold text-[var(--ls-text)]">Event detail</h2></div><Link aria-label="Close event detail" className="luminous-focus rounded-[8px] p-1.5 text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]" href={auditHref(org, scope, before)} title="Close event detail"><X className="size-4" /></Link></div><dl className="mt-5 space-y-4"><Fact label="Action" value={selected.action} mono /><Fact label="Actor" value={selected.actor_subject} /><Fact label="Target" value={selected.target} /><Fact label="Recorded" value={`${dateFormatter.format(new Date(selected.created_at))} UTC`} /></dl><details className="mt-5 rounded-[12px] bg-[var(--ls-surface-muted)] p-3"><summary className="cursor-pointer text-xs font-semibold text-[var(--ls-text)]">Recorded metadata</summary><pre className="mt-3 overflow-x-auto whitespace-pre-wrap break-all font-mono text-[11px] leading-5 text-[var(--ls-text-secondary)]">{JSON.stringify(selected.metadata, null, 2)}</pre></details></aside> : eventId ? <PageState action={<RecoveryAction href={auditHref(org, scope, before)}>Close detail</RecoveryAction>} detail={eventDetail?.detail ?? "This event may no longer be available to your workspace."} kind="unavailable" title="Audit event could not be opened" /> : null}
      </div>
    </>}
  </div>;
}

function AuditTabLink({ href, label, selected }: { href: string; label: string; selected: boolean }) { return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative h-11 shrink-0 rounded-t-[10px] px-4 py-3 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={href} role="tab" tabIndex={selected ? 0 : -1}>{label}{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>; }
function AuditEmptyState({ before, detail, org, scope, source }: { before: string; detail?: string; org: string; scope: AuditScope; source: "live" | "demo" | "unconfigured" | "unavailable" }) {
  if (source === "unavailable") return <PageState detail={detail ?? "Audit history could not be loaded."} kind="unavailable" title="Audit history is temporarily unavailable" />;
  if (source === "unconfigured") return <PageState action={<RecoveryAction href={`/${org}/connect?tab=installations`} variant="primary">Connect control plane</RecoveryAction>} detail={detail ?? "Connect the control plane before viewing audit history."} kind="first-use-empty" title="Audit history needs a connected control plane" />;
  if (before) return <PageState action={<RecoveryAction href={auditHref(org, scope)}>Latest events</RecoveryAction>} detail="There are no earlier events for this workspace and filter." kind="filtered-empty" title="No older audit events" />;
  if (Object.values(scope).some(Boolean)) return <PageState action={<RecoveryAction href={`/${org}/audit?tab=events`}>Clear filters</RecoveryAction>} detail="Try another actor, action, target, or UTC date range, or clear the current filters." kind="filtered-empty" title="No audit events match these filters" />;
  return <PageState detail="Auditable tenant changes will appear here as the platform is configured and used." kind="first-use-empty" title="No audit events recorded yet" />;
}
function auditHref(org: string, scope: AuditScope, before = "", event = "") {
  const query = new URLSearchParams({ tab: "events" });
  for (const [key, value] of Object.entries(scope)) if (value) query.set(key, value);
  if (before) query.set("before", before);
  if (event) query.set("event", event);
  return `/${encodeURIComponent(org)}/audit?${query.toString()}`;
}
function Field({ defaultValue, label, mono, name, placeholder, type = "text" }: { defaultValue: string; label: string; mono?: boolean; name: string; placeholder?: string; type?: string }) { return <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{label}<input className={cn("luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]", mono && "font-mono")} defaultValue={defaultValue} name={name} placeholder={placeholder} type={type} /></label>; }
function auditDateBound(value: string, exclusiveEnd = false): string | undefined {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return undefined;
  const parsed = new Date(`${value}T00:00:00.000Z`);
  if (Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== value) return undefined;
  if (exclusiveEnd) parsed.setUTCDate(parsed.getUTCDate() + 1);
  return parsed.toISOString();
}
function Fact({ label, mono, value }: { label: string; mono?: boolean; value: string }) { return <div><dt className="text-[11px] font-medium text-[var(--ls-text-tertiary)]">{label}</dt><dd className={cn("mt-1 break-all text-sm text-[var(--ls-text)]", mono && "font-mono text-xs")}>{value}</dd></div>; }
