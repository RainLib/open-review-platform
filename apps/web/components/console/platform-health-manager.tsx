"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import {
  Activity,
  BookOpenCheck,
  Boxes,
  Clock3,
  CloudCog,
  Database,
  Gauge,
  GitBranch,
  HeartPulse,
  Inbox,
  Network,
  RadioTower,
  ServerCog,
  ShieldCheck,
  Siren,
  TriangleAlert,
  UsersRound,
} from "lucide-react";

import { EnterpriseSettingsTabs } from "@/components/console/enterprise-settings-tabs";
import { DataFreshness, PageState, RecoveryAction } from "@/components/console/page-state";
import { TabStateRouter } from "@/components/console/tab-state-router";
import type { GitLabAuthorAdmissionHealthItem, HealthComponent, HealthState, PlatformHealthData, PlatformIncident, PlatformRunbook, ProviderHealth, QueueHealth, WorkerHealth } from "@/lib/control-api";
import { providerReviewTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

type PlatformHealthTab = "overview" | "queues" | "workers" | "providers" | "incidents" | "runbooks";

const tabs: Array<[PlatformHealthTab, string]> = [
  ["overview", "Overview"],
  ["queues", "Queues"],
  ["workers", "Workers"],
  ["providers", "Providers"],
  ["incidents", "Incidents"],
  ["runbooks", "Runbooks"],
];

const stateLabels: Record<HealthState, string> = {
  live: "Live",
  degraded: "Degraded",
  critical: "Critical",
  stale: "Stale",
  configured_only: "Configured only",
};

export function PlatformHealthManager({ data, org, tab }: { data: PlatformHealthData; org: string; tab: PlatformHealthTab }) {
  const preview = data.source === "demo";
  const sampledAt = data.source === "live" || preview ? formatDate(data.sampled_at) : "No sample";
  return <div className="space-y-6">
    <header className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between"><div><p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">Operational evidence</p><div className="mt-2 flex min-w-0 items-center gap-2"><h1 className="text-[32px] font-semibold tracking-[-0.045em] text-[var(--ls-text)]">Platform health</h1><HelpHint label="Platform health">Inspect tenant-scoped queue state, execution leases, provider outcomes, and recovery guidance without confusing deployment configuration with a live health probe.</HelpHint></div></div><div className="flex flex-wrap items-center gap-2"><DataFreshness state={data.source === "live" ? "live" : data.source === "demo" ? "demo" : "unavailable"} /><span className="inline-flex items-center gap-1.5 rounded-full bg-[var(--ls-surface-muted)] px-3 py-1.5 text-xs text-[var(--ls-text-secondary)]"><Clock3 className="size-3.5" />{sampledAt}</span></div></header>
    <EnterpriseSettingsTabs active="health" org={org} />
    <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="Platform health views">{tabs.map(([key, label]) => <Subtab href={`/${org}/settings/health?tab=${key}`} key={key} label={label} selected={tab === key} />)}</TabStateRouter>
    {data.source !== "live" && !preview ? <PageState action={<RecoveryAction href={`/${org}/settings/health?tab=${tab}`}>Refresh sample</RecoveryAction>} detail={data.detail ?? "The platform health read model could not be loaded."} kind="unavailable" title="Health sample unavailable" /> : <>{preview ? <section className="rounded-[14px] border border-amber-500/25 bg-amber-500/[0.06] px-4 py-3 text-sm leading-6 text-[var(--ls-warning-text)]">Preview data is illustrative only. It does not assert live worker, broker, provider, or incident health.</section> : null}{tab === "queues" ? <QueuesView data={data} org={org} /> : tab === "workers" ? <WorkersView data={data} /> : tab === "providers" ? <ProvidersView data={data} org={org} /> : tab === "incidents" ? <IncidentsView data={data} org={org} /> : tab === "runbooks" ? <RunbooksView data={data} /> : <Overview data={data} />}</>}
  </div>;
}

function Overview({ data }: { data: PlatformHealthData }) {
  const observed = data.components.filter((item) => item.observed).length;
  const degraded = [...data.components, ...data.queues, ...data.providers].filter((item) => item.state === "degraded" || item.state === "critical").length;
  const ready = data.queues.reduce((sum, item) => sum + item.ready, 0);
  return <div className="space-y-5">
    <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"><MetricCard icon={HeartPulse} label="Observed components" value={`${observed}/${data.components.length}`} detail="live sample coverage" tone={observed === data.components.length ? "success" : "warning"} /><MetricCard icon={TriangleAlert} label="Needs attention" value={String(degraded)} detail="degraded or critical" tone={degraded ? "critical" : "success"} /><MetricCard icon={Inbox} label="Queued work" value={String(ready)} detail="tenant authoritative rows" tone={ready ? "warning" : "neutral"} /><MetricCard icon={RadioTower} label="Broker telemetry" value={data.broker_metrics_available ? "Observed" : "Not ingested"} detail="separate from DB queues" tone={data.broker_metrics_available ? "success" : "warning"} /></section>
    <div className="grid gap-5 lg:grid-cols-[minmax(0,1.25fr)_minmax(300px,.75fr)]"><section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-7"><div className="flex items-start justify-between gap-4"><div><p className="text-xs font-semibold uppercase tracking-[0.1em] text-[var(--ls-accent)]">Capability canvas</p><div className="mt-2 flex min-w-0 items-center gap-2"><h2 className="text-xl font-semibold text-[var(--ls-text)]">Observed and configured state</h2><HelpHint label="Observed and configured state">Each card names its sample source. Configured-only components remain neutral even when the service is expected to exist.</HelpHint></div></div><Activity className="size-5 text-[var(--ls-text-tertiary)]" /></div><div className="mt-6 grid gap-3 md:grid-cols-2">{data.components.map((item) => <ComponentCard item={item} key={item.key} />)}</div></section><aside className="space-y-5"><CoverageCard icon={Database} title="Authority boundary">PostgreSQL rows are authoritative for tenant workflow state. RabbitMQ can accelerate delivery but its absence does not erase pending outbox work.</CoverageCard><CoverageCard icon={Gauge} title="Freshness window">Samples are current for {duration(data.freshness_window_seconds)}. A timestamp older than that must be rendered stale, not live.</CoverageCard><CoverageCard icon={ShieldCheck} title="Tenant isolation">Queue, worker lease, and provider activity are filtered to this workspace. Platform-wide tenant names and counts are not exposed here.</CoverageCard></aside></div>
  </div>;
}

function QueuesView({ data, org }: { data: PlatformHealthData; org: string }) {
  const executor = data.agent_executor;
  const executorFailed = executor?.state === "unreachable" || executor?.state === "partially_reachable";
  const executorTitle = executor?.state === "reachable_unverified"
    ? "Agent adapter · Signed probe reached service"
    : executorFailed
      ? "Agent adapter · Reachability needs attention"
      : "Agent adapter · No verified reachability";
  return <div className="space-y-5">
    <BoundaryBanner icon={ServerCog} title={executorTitle} tone={executorFailed ? "warning" : "neutral"}>
      {executor?.detail ?? "A current runner heartbeat and signed adapter probe are unavailable."} A successful probe does not verify coding, sandbox isolation, or provider write access.
    </BoundaryBanner>
    <BoundaryBanner icon={Activity} title="Jev classification · Configuration evidence" tone="neutral">
      {data.agent_decision?.detail ?? "Fresh classifier configuration evidence is unavailable."} A configured classifier is not proof of a successful decision.
    </BoundaryBanner>
    <BoundaryBanner icon={ShieldCheck} title="Coding credentials · Private broker evidence" tone={data.agent_credential_broker?.state === "unobserved" ? "warning" : "neutral"}>
      {data.agent_credential_broker?.detail ?? "Fresh private coding-credential broker heartbeat evidence is unavailable."} This is separate from RabbitMQ telemetry and never exposes a token or installation mapping.
    </BoundaryBanner>
    <BoundaryBanner icon={Database} title="PostgreSQL queue authority" tone="neutral">
      These counts are sampled from tenant workflow rows. RabbitMQ ready/unacked/delayed/DLQ depth is intentionally unavailable until broker metrics are ingested and timestamped.
    </BoundaryBanner>
    <section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
      <div className="border-b border-[var(--ls-line)] px-5 py-4"><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Tenant queues</h2><HelpHint label="Tenant queues">Ready, running, failed, expired leases, and oldest ready age</HelpHint></div></div>
      <div className="grid gap-3 p-4 lg:grid-cols-2">{data.queues.map((queue) => <QueueCard key={queue.key} queue={queue} />)}</div>
    </section>
    {data.gitlab_author_admissions?.length ? <section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6"><h2 className="text-sm font-semibold text-[var(--ls-text)]">GitLab author verification</h2><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Up to 12 tenant-owned pending or failed MRs. Restoring the connection does not replay an old webhook; request a fresh event for the current head.</p><ul className="mt-4 grid gap-2">{data.gitlab_author_admissions.map((item) => <GitLabAuthorAdmissionRow item={item} key={item.delivery_id} org={org} />)}</ul></section> : null}
    <BoundaryBanner icon={CloudCog} title="Broker view not available" tone="warning">
      No RabbitMQ management sample is attached to this response. Do not interpret zero PostgreSQL failures as zero DLQ messages or healthy broker alarms.
    </BoundaryBanner>
  </div>;
}

function WorkersView({ data }: { data: PlatformHealthData }) { return <div className="space-y-5"><BoundaryBanner icon={UsersRound} title={data.worker_heartbeat_available ? "Heartbeat registry active" : "Heartbeat registry not available"} tone={data.worker_heartbeat_available ? "success" : "warning"}>{data.worker_heartbeat_available ? "Heartbeat freshness and process version are shown only when a worker has a current lease on this workspace. Worker identities are tenant-stable aliases; shared-fleet capacity and occupancy are intentionally not exposed." : "Cards below are derived only from tenant execution leases. A process can be alive without a lease, and a lease row is not a process heartbeat."}</BoundaryBanner>{data.workers.length ? <section className="grid gap-3 lg:grid-cols-2">{data.workers.map((worker) => <WorkerCard key={worker.worker_id} observedAt={data.sampled_at} worker={worker} />)}</section> : <PageState detail="No durable heartbeat or tenant execution lease was observed in the current evidence window." kind="first-use-empty" title="No worker evidence" />}{data.worker_heartbeat_available ? <CoverageCard icon={Network} title="Operator boundary">The registry is read-only from this tenant console. Restart, drain, shell access, shared-fleet capacity, memory telemetry, and topology remain unavailable until a separately authorized operator command plane is implemented.</CoverageCard> : <CoverageCard icon={Network} title="Missing execution telemetry">A current tenant execution lease and a durable worker heartbeat are both required to show worker evidence. Until then, the console will not offer restart or drain actions.</CoverageCard>}</div>; }

function ProvidersView({ data, org }: { data: PlatformHealthData; org: string }) { const probed = data.providers.filter((provider) => provider.last_probe_at).length; const needsAttention = data.providers.some((provider) => provider.state !== "live"); return <div className="space-y-5"><BoundaryBanner icon={GitBranch} title={probed ? needsAttention ? "Access probes require attention" : "Read-only access probes active" : "Activity is not permission health"} tone={probed && !needsAttention ? "success" : "warning"}>{probed ? `${probed} installation(s) have fresh or stale read-only access evidence. Successful inventory/identity reads and rate-limit headers do not prove write permissions; publication receipts remain the write-path evidence.` : "Webhook and publication timestamps prove historical activity. They do not prove current app permissions, token validity, rate-limit headroom, or provider availability."}</BoundaryBanner>{data.providers.length ? <section className="grid gap-3 lg:grid-cols-2">{data.providers.map((provider) => <ProviderCard key={provider.installation_id} provider={provider} />)}</section> : <PageState action={<RecoveryAction href={`/${org}/connect`} variant="primary">Connect provider</RecoveryAction>} detail="Connect GitHub or GitLab before provider-scoped activity can be sampled." kind="first-use-empty" title="No provider installations" />}</div>; }

function IncidentsView({ data, org }: { data: PlatformHealthData; org: string }) {
  const router = useRouter();
  const [opening, setOpening] = useState(false);
  const [title, setTitle] = useState("");
  const [scope, setScope] = useState("workspace");
  const [affectedArea, setAffectedArea] = useState("");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();
  const canMutate = data.source === "live" && data.incident_tracking_available;

  async function openIncident(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/platform-incidents`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title, scope, affected_area: affectedArea }),
      });
      const payload = await response.json().catch(() => ({})) as { error?: string };
      if (!response.ok) throw new Error(payload.error ?? "The incident could not be opened.");
      setOpening(false);
      setTitle("");
      setAffectedArea("");
      router.refresh();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "The incident could not be opened.");
    } finally {
      setPending(false);
    }
  }

  return <div className="space-y-5">
    {!data.incident_tracking_available ? <BoundaryBanner icon={Siren} title="Incident registry not configured" tone="warning">The control plane has no durable incident timeline yet. Empty does not mean there are no incidents; it means this evidence source is unavailable.</BoundaryBanner> : null}
    {canMutate ? <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]"><div className="flex flex-wrap items-center justify-between gap-3"><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Incident timeline</h2><HelpHint label="Incident timeline">Open only a tenant-scoped operational incident. It records a durable audit trail; it does not restart services or alter review policy.</HelpHint></div></div><button className="luminous-focus rounded-[10px] bg-[var(--ls-accent)] px-3.5 py-2 text-xs font-semibold text-white transition hover:bg-[var(--ls-accent-hover)]" onClick={() => setOpening((value) => !value)} type="button">{opening ? "Close form" : "Open incident"}</button></div>{opening ? <form className="mt-5 grid gap-3 border-t border-[var(--ls-line)] pt-5 md:grid-cols-2" onSubmit={openIncident}><label className="grid gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)] md:col-span-2">Title<input className="luminous-focus h-10 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3 text-sm text-[var(--ls-text)]" maxLength={180} minLength={3} onChange={(event) => setTitle(event.target.value)} required value={title} /></label><label className="grid gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">Affected area<input className="luminous-focus h-10 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3 text-sm text-[var(--ls-text)]" maxLength={160} minLength={3} onChange={(event) => setAffectedArea(event.target.value)} required value={affectedArea} /></label><label className="grid gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">Scope<select className="luminous-focus h-10 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3 text-sm text-[var(--ls-text)]" onChange={(event) => setScope(event.target.value)} value={scope}><option value="workspace">Workspace</option><option value="provider">Provider</option><option value="queue">Queue</option><option value="worker">Worker</option><option value="model">Model</option><option value="data_governance">Data governance</option><option value="identity">Identity</option></select></label>{message ? <p className="text-xs text-[var(--ls-critical-text)] md:col-span-2">{message}</p> : null}<div className="flex justify-end md:col-span-2"><button className="luminous-focus rounded-[10px] bg-[var(--ls-accent)] px-3.5 py-2 text-xs font-semibold text-white disabled:cursor-not-allowed disabled:opacity-60" disabled={pending} type="submit">{pending ? "Opening…" : "Record incident"}</button></div></form> : null}</section> : null}
    {data.incidents.length ? <section className="space-y-3">{data.incidents.map((incident) => <IncidentCard incident={incident} key={incident.id} org={org} writable={canMutate} />)}</section> : <PageState detail={data.incident_tracking_available ? "The incident registry contains no visible records for this workspace." : "Add a durable incident store and tenant-safe timeline before using this page as an operational source of truth."} kind={data.incident_tracking_available ? "first-use-empty" : "partial"} title={data.incident_tracking_available ? "No incidents recorded" : "No incident evidence source"} />}
  </div>;
}

function IncidentCard({ incident, org, writable }: { incident: PlatformIncident; org: string; writable: boolean }) {
  const router = useRouter();
  const [resolving, setResolving] = useState(false);
  const [resolution, setResolution] = useState("");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();
  const state: HealthState = incident.state === "active" ? "critical" : incident.state === "mitigating" ? "degraded" : "live";
  async function resolveIncident(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/platform-incidents/${encodeURIComponent(incident.id)}/resolve`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ expected_revision: incident.revision, resolution }) });
      const payload = await response.json().catch(() => ({})) as { error?: string };
      if (!response.ok) throw new Error(payload.error ?? "The incident could not be resolved.");
      router.refresh();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "The incident could not be resolved.");
    } finally {
      setPending(false);
    }
  }
  return <article className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><div className="flex flex-wrap items-center justify-between gap-3"><div><h2 className="text-sm font-semibold text-[var(--ls-text)]">{incident.title}</h2><p className="mt-2 text-xs text-[var(--ls-text-secondary)]">{incident.scope} · {incident.affected_area} · started {formatDate(incident.started_at)}</p></div><StatusPill state={state}>{incident.state}</StatusPill></div>{incident.resolved_at ? <p className="mt-3 rounded-[10px] bg-[var(--ls-surface-muted)] px-3 py-2 text-xs leading-5 text-[var(--ls-text-secondary)]">Resolved {formatDate(incident.resolved_at)}{incident.resolution ? ` · ${incident.resolution}` : ""}</p> : null}{writable && incident.state !== "resolved" ? <div className="mt-4 border-t border-[var(--ls-line)] pt-4"><button className="luminous-focus text-xs font-semibold text-[var(--ls-accent)]" onClick={() => setResolving((value) => !value)} type="button">{resolving ? "Cancel resolution" : "Resolve incident"}</button>{resolving ? <form className="mt-3 flex flex-col gap-2 sm:flex-row" onSubmit={resolveIncident}><label className="sr-only" htmlFor={`resolution-${incident.id}`}>Resolution</label><input className="luminous-focus h-10 min-w-0 flex-1 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3 text-sm text-[var(--ls-text)]" id={`resolution-${incident.id}`} minLength={3} onChange={(event) => setResolution(event.target.value)} placeholder="What restored this service?" required value={resolution} /><button className="luminous-focus rounded-[10px] bg-[var(--ls-accent)] px-3.5 py-2 text-xs font-semibold text-white disabled:cursor-not-allowed disabled:opacity-60" disabled={pending} type="submit">{pending ? "Resolving…" : "Resolve"}</button></form> : null}{message ? <p className="mt-2 text-xs text-[var(--ls-critical-text)]">{message}</p> : null}</div> : null}</article>;
}

function RunbooksView({ data }: { data: PlatformHealthData }) { return <div className="space-y-5"><BoundaryBanner icon={BookOpenCheck} title="Guidance only" tone="neutral">Runbooks are versioned recovery guidance. This page cannot execute arbitrary shell, restart shared services, purge queues, or mutate provider state.</BoundaryBanner><section className="grid gap-4 lg:grid-cols-2">{data.runbooks.map((runbook) => <RunbookCard key={runbook.key} runbook={runbook} />)}</section></div>; }

function ComponentCard({ item }: { item: HealthComponent }) { const Icon = item.key === "postgresql" ? Database : item.key === "rabbitmq" ? Boxes : item.key === "workers" ? ServerCog : item.key === "publisher" ? GitBranch : Activity; return <article className="rounded-[15px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><div className="flex items-start gap-3"><span className="grid size-9 shrink-0 place-items-center rounded-[11px] bg-[var(--ls-surface)] text-[var(--ls-accent)]"><Icon className="size-4" /></span><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-semibold text-[var(--ls-text)]">{item.name}</h3><StatusPill state={item.state}>{stateLabels[item.state]}</StatusPill></div><p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">{item.detail}</p><p className="mt-2 text-[11px] text-[var(--ls-text-tertiary)]">Capability: {item.capability}</p><p className="mt-1 text-[11px] text-[var(--ls-text-tertiary)]">{item.observed && item.sampled_at ? `Sampled ${formatDate(item.sampled_at)}` : "No live probe attached"}</p></div></div></article>; }
function QueueCard({ queue }: { queue: QueueHealth }) { return <article className="rounded-[15px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><div className="flex items-center justify-between gap-3"><div><h3 className="text-sm font-semibold text-[var(--ls-text)]">{queue.name}</h3><p className="mt-1 text-[11px] text-[var(--ls-text-tertiary)]">{queue.source} · sampled {formatDate(queue.sampled_at)}</p></div><StatusPill state={queue.state}>{stateLabels[queue.state]}</StatusPill></div><dl className="mt-4 grid grid-cols-3 gap-2"><TinyMetric label="Ready" value={queue.ready} /><TinyMetric label="Running" value={queue.running} /><TinyMetric label="Failed" value={queue.failed} /><TinyMetric label="Expired" value={queue.expired_leases} /><TinyMetric label="Oldest" value={duration(queue.oldest_age_seconds)} /><TinyMetric label="Source" value="DB" /></dl><p className="mt-3 text-xs text-[var(--ls-text-secondary)]">{queue.detail}</p></article>; }
function GitLabAuthorAdmissionRow({ item, org }: { item: GitLabAuthorAdmissionHealthItem; org: string }) {
  const target = item.api_base_url ? providerReviewTarget({ provider: "gitlab", api_base_url: item.api_base_url, repository: item.repository, review_number: item.review_number }) : undefined;
  return <li className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-4 py-3"><div className="flex flex-wrap items-start justify-between gap-3"><div className="min-w-0"><p className="break-all text-sm font-medium text-[var(--ls-text)]">{item.repository} !{item.review_number}</p><p className="mt-1 font-mono text-[11px] text-[var(--ls-text-tertiary)]">head {item.head_sha.slice(0, 12)} · attempt {item.attempt} · {formatDate(item.updated_at)}</p></div><span className={cn("rounded-full px-2.5 py-1 text-[11px] font-semibold", item.state === "failed" ? "bg-red-500/10 text-[var(--ls-critical-text)]" : "bg-amber-500/10 text-[var(--ls-warning-text)]")}>{item.state}</span></div>{item.error_code ? <p className="mt-2 text-xs text-[var(--ls-text-secondary)]">Last result: <code>{item.error_code}</code></p> : null}<div className="mt-2 flex flex-wrap gap-x-4 gap-y-2"><Link className="luminous-focus rounded text-xs font-semibold text-[var(--ls-accent)]" href={`/${encodeURIComponent(org)}/connect/${encodeURIComponent(item.installation_id)}`}>Inspect connection</Link>{target ? <a className="luminous-focus rounded text-xs font-semibold text-[var(--ls-accent)]" href={target.url} rel="noopener noreferrer" target="_blank">Open MR in {target.label}</a> : null}</div></li>;
}
function WorkerCard({ observedAt, worker }: { observedAt: string; worker: WorkerHealth }) { return <article className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><div className="flex items-start justify-between gap-3"><div><p className="font-mono text-sm font-semibold text-[var(--ls-text)]">{worker.worker_id}</p><p className="mt-1 text-xs text-[var(--ls-text-secondary)]">{worker.kind}{worker.version ? ` · ${worker.version}` : " · version unobserved"}</p></div><StatusPill state={worker.state}>{stateLabels[worker.state]}</StatusPill></div><div className="mt-4 grid grid-cols-3 gap-2"><TinyMetric label="Active leases" value={worker.active_leases} /><TinyMetric label="Expired leases" value={worker.expired_leases} /><TinyMetric label="Heartbeat" value={worker.last_heartbeat ? formatRelative(worker.last_heartbeat, observedAt) : "Not observed"} /></div><p className="mt-3 text-xs leading-5 text-[var(--ls-text-secondary)]">{worker.detail}</p></article>; }
function ProviderCard({ provider }: { provider: ProviderHealth }) {
  const rateLimit = provider.rate_limit_limit !== undefined && provider.rate_limit_remaining !== undefined ? `${provider.rate_limit_remaining}/${provider.rate_limit_limit}` : "Not observed";
  return <article className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5">
    <div className="flex items-start justify-between gap-3"><div><div className="flex items-center gap-2"><GitBranch className="size-4 text-[var(--ls-accent)]" /><h2 className="text-sm font-semibold capitalize text-[var(--ls-text)]">{provider.provider}</h2></div><p className="mt-2 font-mono text-xs text-[var(--ls-text-secondary)]">{provider.host}</p><p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">{provider.repository_scope}</p></div><StatusPill state={provider.state}>{stateLabels[provider.state]}</StatusPill></div>
    <dl className="mt-4 space-y-2"><LineMetric label="Last access probe" value={provider.last_probe_at ? formatDate(provider.last_probe_at) : "Not observed"} /><LineMetric label="Observed capability" value={provider.permissions.filter((permission) => !permission.startsWith("issue_triage:") && !permission.startsWith("agent_coding:")).join(", ") || "Not observed"} /><LineMetric label="User Issue AI analysis" value={issueTriageCapabilityLabel(provider)} /><LineMetric label="Agent write prerequisite" value={agentCodingCapabilityLabel(provider)} /><LineMetric label="Rate limit remaining" value={rateLimit} /><LineMetric label="Probe latency" value={provider.last_probe_at ? `${provider.probe_latency_ms} ms` : "Not observed"} /><LineMetric label="Last webhook" value={provider.last_webhook_at ? formatDate(provider.last_webhook_at) : "Not observed"} /><LineMetric label="Last publication" value={provider.last_publish_at ? formatDate(provider.last_publish_at) : "Not observed"} /><LineMetric label="24h publication failures" value={String(provider.publish_failures)} /></dl>
    <p className="mt-4 text-xs leading-5 text-[var(--ls-text-secondary)]">{provider.detail}</p>
  </article>;
}
function issueTriageCapabilityLabel(provider: ProviderHealth) { if (provider.provider === "gitlab") return "Requires Issue Hook delivery evidence"; const state = provider.permissions.find((permission) => permission.startsWith("issue_triage:")); if (state === "issue_triage:ready") return "Ready"; if (state === "issue_triage:missing_event") return "Missing Issues event subscription"; if (state === "issue_triage:missing_write_permission") return "Missing Issues write permission"; if (state === "issue_triage:unobserved") return "App registration unavailable"; return "Not observed"; }
function agentCodingCapabilityLabel(provider: ProviderHealth) {
  if (provider.provider === "gitlab") return "Project-scoped write token not probed";
  const state = provider.permissions.find((permission) => permission.startsWith("agent_coding:"));
  if (state === "agent_coding:app_permissions_declared") return "App permissions declared; write not tested";
  if (state === "agent_coding:missing_contents_write") return "Missing Contents write permission";
  if (state === "agent_coding:missing_pull_requests_write") return "Missing Pull requests write permission";
  if (state === "agent_coding:missing_issues_read") return "Missing Issues read permission";
  if (state === "agent_coding:unobserved") return "App registration unavailable";
  return "Not observed";
}
function RunbookCard({ runbook }: { runbook: PlatformRunbook }) { return <article className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><div className="flex items-start justify-between gap-4"><div><p className="text-[10px] font-semibold uppercase tracking-[0.12em] text-[var(--ls-accent)]">{runbook.version} · {runbook.required_role}</p><h2 className="mt-2 text-base font-semibold text-[var(--ls-text)]">{runbook.title}</h2></div><StatusPill state={runbook.executable ? "live" : "configured_only"}>{runbook.executable ? "Executable" : "Read only"}</StatusPill></div><ol className="mt-4 space-y-2">{runbook.steps.map((step, index) => <li className="flex gap-3 text-xs leading-5 text-[var(--ls-text-secondary)]" key={step}><span className="grid size-5 shrink-0 place-items-center rounded-full bg-[var(--ls-surface-muted)] text-[10px] font-semibold text-[var(--ls-text)]">{index + 1}</span>{step}</li>)}</ol><p className="mt-4 border-t border-[var(--ls-line)] pt-4 text-xs leading-5 text-[var(--ls-text-tertiary)]">{runbook.detail}</p></article>; }

function MetricCard({ icon: Icon, label, value, detail, tone }: { icon: typeof Activity; label: string; value: string; detail: string; tone: "success" | "warning" | "critical" | "neutral" }) { return <article className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4"><div className="flex items-start justify-between gap-3"><div><p className="text-xs text-[var(--ls-text-secondary)]">{label}</p><p className="mt-2 text-xl font-semibold tracking-tight text-[var(--ls-text)]">{value}</p><p className="mt-1 text-[11px] text-[var(--ls-text-tertiary)]">{detail}</p></div><span className={cn("grid size-9 place-items-center rounded-[11px]", tone === "success" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : tone === "warning" ? "bg-amber-500/10 text-[var(--ls-warning-text)]" : tone === "critical" ? "bg-red-500/10 text-[var(--ls-critical-text)]" : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]")}><Icon className="size-4" /></span></div></article>; }
function TinyMetric({ label, value }: { label: string; value: number | string }) { return <div className="rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-2.5"><dt className="text-[10px] text-[var(--ls-text-tertiary)]">{label}</dt><dd className="mt-1 truncate text-xs font-semibold text-[var(--ls-text)]">{value}</dd></div>; }
function LineMetric({ label, value }: { label: string; value: string }) { return <div className="flex items-center justify-between gap-4 text-xs"><dt className="text-[var(--ls-text-secondary)]">{label}</dt><dd className="text-right font-medium text-[var(--ls-text)]">{value}</dd></div>; }
function CoverageCard({ icon: Icon, title, children }: { icon: typeof Database; title: string; children: React.ReactNode }) { return <section className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><Icon className="size-4 text-[var(--ls-accent)]" /><h3 className="mt-3 text-sm font-semibold text-[var(--ls-text)]">{title}</h3><p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">{children}</p></section>; }
function BoundaryBanner({ icon: Icon, title, tone, children }: { icon: typeof Database; title: string; tone: "success" | "warning" | "neutral"; children: React.ReactNode }) { return <section className={cn("flex items-start gap-3 rounded-[15px] border p-4", tone === "success" ? "border-emerald-500/20 bg-emerald-500/[0.05]" : tone === "warning" ? "border-amber-500/20 bg-amber-500/[0.05]" : "border-[var(--ls-line)] bg-[var(--ls-surface)]")}><Icon className={cn("mt-0.5 size-4 shrink-0", tone === "success" ? "text-[var(--ls-success-text)]" : tone === "warning" ? "text-[var(--ls-warning-text)]" : "text-[var(--ls-accent)]")} /><div><h2 className="text-sm font-semibold text-[var(--ls-text)]">{title}</h2><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{children}</p></div></section>; }
function StatusPill({ children, state }: { children: React.ReactNode; state: HealthState }) { return <span className={cn("inline-flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-[10px] font-semibold", state === "live" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : state === "degraded" || state === "stale" ? "bg-amber-500/10 text-[var(--ls-warning-text)]" : state === "critical" ? "bg-red-500/10 text-[var(--ls-critical-text)]" : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]")}>{state === "live" ? <span className="size-1.5 rounded-full bg-current" /> : null}{children}</span>; }
function Subtab({ href, label, selected }: { href: string; label: string; selected: boolean }) { return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative h-11 shrink-0 rounded-t-[10px] px-4 pt-3 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={href} role="tab" tabIndex={selected ? 0 : -1}>{label}{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>; }
function formatDate(value: string) { return new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" }).format(new Date(value)); }
function formatRelative(value: string, observedAt: string) { const seconds = Math.max(0, Math.round((new Date(observedAt).getTime() - new Date(value).getTime()) / 1000)); return seconds < 60 ? `${seconds}s ago` : seconds < 3600 ? `${Math.round(seconds / 60)}m ago` : `${Math.round(seconds / 3600)}h ago`; }
function duration(seconds: number) { if (seconds < 60) return `${seconds}s`; if (seconds < 3600) return `${Math.round(seconds / 60)}m`; return `${Math.round(seconds / 3600)}h`; }
