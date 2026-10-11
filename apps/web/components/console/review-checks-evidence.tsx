import Link from "next/link";
import { ArrowUpRight, CircleAlert, ShieldCheck } from "lucide-react";

import { TabStateRouter } from "@/components/console/tab-state-router";
import type { ReviewEvidence, ReviewRunStageEvidence } from "@/lib/control-api";
import { formatTime, shortSHA } from "@/lib/format";
import { groupProviderChecks, prioritizeProviderChecks, providerCheckState, reviewChecksCount, safeProviderCheckURL, type ReviewChecksView } from "@/lib/provider-checks";
import { providerReviewTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

type CheckRow = { id: string; name: string; source: string; state: string; time?: string; duration?: string; url?: string; note?: string };

function checkViewURL(org: string, runID: string, view: ReviewChecksView) {
  return `/${encodeURIComponent(org)}/reviews/${encodeURIComponent(runID)}?${new URLSearchParams({ tab: "checks", view })}`;
}

function stageDuration(stage: ReviewRunStageEvidence) {
  if (!stage.started_at || !stage.finished_at) return stage.state === "running" ? "In progress" : "—";
  return `${Math.max(0, Math.round((new Date(stage.finished_at).getTime() - new Date(stage.started_at).getTime()) / 1000))}s`;
}

function stageRows(evidence: ReviewEvidence, org: string): CheckRow[] {
  return evidence.stages.map((stage) => ({
    id: stage.id,
    name: `${stage.stage[0].toUpperCase()}${stage.stage.slice(1)} stage`,
    source: "Open Review",
    state: stage.state,
    time: stage.started_at,
    duration: stageDuration(stage),
    url: `/${encodeURIComponent(org)}/tasks/${encodeURIComponent(evidence.run.id)}`,
    note: `Attempt ${stage.attempt + 1}`,
  }));
}

function providerRows(checks: NonNullable<ReviewEvidence["provider_checks"]>["checks"], observedAt?: string): CheckRow[] {
  return checks.map((check, index) => ({
    id: `${check.origin ?? "unclassified"}:${check.kind}:${check.name}:${index}`,
    name: check.name,
    source: check.origin === "independent" ? "Provider CI" : check.origin === "open_review" ? "Open Review status" : "Unclassified status",
    state: check.state,
    time: observedAt,
    url: safeProviderCheckURL(check.url),
    note: check.kind.replaceAll("_", " "),
  }));
}

export function ReviewChecksEvidence({ evidence, org, view }: { evidence: ReviewEvidence; org: string; view: ReviewChecksView }) {
  const observed = evidence.provider_checks?.head_sha.toLowerCase() === evidence.run.head_sha.toLowerCase()
    ? evidence.provider_checks : undefined;
  const fresh = observed?.state === "observed" && !observed.stale && Boolean(observed.observed_at);
  const { independent, other } = groupProviderChecks(observed?.checks ?? []);
  const own = other.filter((check) => check.origin === "open_review");
  const unclassified = other.filter((check) => check.origin !== "open_review");
  const provider = providerReviewTarget(evidence.run);
  const counts = reviewChecksCount(evidence);
  const views: Array<{ id: ReviewChecksView; label: string; count: string }> = [
    { id: "all", label: "All", count: `${counts.all}${observed?.truncated ? "+" : ""}` },
    { id: "open-review", label: "Open Review", count: String(counts.openReview) },
    { id: "provider-ci", label: "Provider CI", count: observed?.state === "observed" ? `${counts.independent}${observed.truncated ? "+" : ""}` : "—" },
  ];
  return <div className="space-y-5">
    <div><h2 className="text-lg font-semibold text-[var(--ls-text)]">Checks</h2><p className="mt-1 text-sm leading-6 text-[var(--ls-text-secondary)]">Open Review’s immutable gate and execution evidence are separate from independent CI observed at the provider for head <span className="font-mono">{shortSHA(evidence.run.head_sha)}</span>.</p></div>
    <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="Check evidence views">
      {views.map(({ id, label, count }) => <Link aria-current={view === id ? "page" : undefined} aria-selected={view === id} className={cn("luminous-focus relative inline-flex h-11 shrink-0 items-center gap-2 rounded-t-[9px] px-3 text-sm font-medium", view === id ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={checkViewURL(org, evidence.run.id, id)} key={id} role="tab" tabIndex={view === id ? 0 : -1}>{label}<span className="rounded-full bg-[var(--ls-surface-muted)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)]">{count}</span>{view === id ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>)}
    </TabStateRouter>
    {view !== "provider-ci" ? <div className="space-y-5">
      <section><h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><ShieldCheck className="size-4 text-[var(--ls-accent)]" />Open Review merge gate</h3><div className="mt-3"><MergeGateCard decision={evidence.merge_gate} org={org} /></div></section>
      <section><div className="flex min-w-0 items-center gap-2"><h3 className="text-sm font-semibold text-[var(--ls-text)]">Durable execution stages</h3><HelpHint label="Durable execution stages">These are workflow stages, not independent tests. Historical timing may be conservative.</HelpHint></div><div className="mt-3"><CheckTable empty="No stage evidence retained." rows={stageRows(evidence, org)} /></div></section>
      {own.length ? <section><h3 className="text-sm font-semibold text-[var(--ls-text)]">Open Review provider status</h3><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">A platform status is not independent CI evidence{fresh ? "." : "; the provider snapshot is pending or stale."}</p><div className="mt-3"><CheckTable rows={providerRows(own, observed?.observed_at)} stale={!fresh} /></div></section> : null}
      <PublicationReceipts evidence={evidence} />
    </div> : null}
    {view !== "open-review" ? <div className="space-y-4">
      <ProviderObservation observed={observed} independentCount={independent.length} provider={provider} />
      <section><div className="flex min-w-0 items-center gap-2"><h3 className="text-sm font-semibold text-[var(--ls-text)]">Independent provider CI</h3><HelpHint label="Independent provider CI">Only checks explicitly classified as independent appear here. A success state is not current evidence when the snapshot is stale or partial.</HelpHint></div><div className="mt-3"><CheckTable empty="No independent CI was confirmed for this exact revision." rows={providerRows(prioritizeProviderChecks(independent), observed?.observed_at)} stale={!fresh} /></div></section>
      {unclassified.length ? <details className="rounded-[12px] border border-[var(--ls-line)]"><summary className="luminous-focus cursor-pointer px-4 py-3 text-xs font-medium text-[var(--ls-text-secondary)]">{unclassified.length} unclassified provider status{unclassified.length === 1 ? "" : "es"} · not independent CI</summary><div className="px-4 pb-4"><CheckTable rows={providerRows(unclassified, observed?.observed_at)} stale={!fresh} /></div></details> : null}
    </div> : null}
    <p className="border-t border-[var(--ls-line)] pt-4 text-xs leading-5 text-[var(--ls-text-tertiary)]">Branch protection at the Git provider remains authoritative for merge permission. This page does not turn a missing, stale, partial, or unclassified observation into a passing check.</p>
  </div>;
}

function MergeGateCard({ decision, org }: { decision: ReviewEvidence["merge_gate"]; org: string }) {
  if (!decision) return <div className="flex gap-2 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 text-sm text-[var(--ls-text-secondary)]"><CircleAlert className="mt-0.5 size-4 shrink-0" /><span>No immutable merge-gate decision was retained for this legacy or in-progress run. No result is inferred from findings or provider statuses.</span></div>;
  const label = !decision.enabled ? "Advisory" : decision.conclusion === "failure" ? "Blocked" : "Passed";
  const tone = label === "Blocked" ? "border-red-500/20 bg-red-500/[0.06] text-[var(--ls-critical-text)]" : label === "Passed" ? "border-emerald-500/20 bg-emerald-500/[0.06] text-[var(--ls-success-text)]" : "border-[var(--ls-line)] bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]";
  return <div className={cn("flex flex-wrap items-center justify-between gap-3 rounded-[12px] border p-4", tone)}><div><p className="text-sm font-semibold">{label} · {decision.enabled ? `${decision.threshold} and above` : "blocking disabled"}</p><p className="mt-1 text-xs leading-5">{decision.blocking_findings} blocking of {decision.finding_count} retained findings · policy {decision.origin_scope_kind} r{decision.origin_revision} · {formatTime(decision.decided_at)}</p></div><Link className="luminous-focus rounded-[9px] px-3 py-2 text-xs font-medium text-[var(--ls-accent)] hover:bg-[var(--ls-accent-soft)]" href={`/${encodeURIComponent(org)}/review-config/general`}>View gate policy</Link></div>;
}

function ProviderObservation({ observed, independentCount, provider }: { observed: ReviewEvidence["provider_checks"]; independentCount: number; provider: ReturnType<typeof providerReviewTarget> }) {
  const fresh = observed?.state === "observed" && !observed.stale && Boolean(observed.observed_at);
  const title = !observed ? "Not observed for this revision" : observed.state === "failed" ? "Provider observation failed" : observed.state === "queued" ? "Provider observation queued" : observed.state === "running" ? "Provider observation in progress" : !fresh ? "Provider observation is stale" : observed.truncated ? "Partial provider observation" : independentCount ? `${independentCount} independent CI check${independentCount === 1 ? "" : "s"} observed` : "No independent CI confirmed";
  const detail = !observed ? "No provider checks have been retained for this exact head." : observed.state === "failed" ? "The read failed after bounded retries; CI outcome is unknown." : observed.state === "queued" || observed.state === "running" ? "Read-only collection has not finished." : !fresh ? "The snapshot is older than its freshness window." : observed.truncated ? "The bounded snapshot omits some provider checks; inspect the complete list at the provider." : independentCount ? "Inspect required checks and branch protection at the provider." : "Own and unclassified statuses cannot establish that external tests passed.";
  return <div className="flex flex-wrap items-start justify-between gap-3 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><div><p className="text-sm font-semibold text-[var(--ls-text)]">{title}</p><p className="mt-1 max-w-2xl text-xs leading-5 text-[var(--ls-text-secondary)]">{detail}{observed?.observed_at ? ` Observed ${formatTime(observed.observed_at)}.` : ""}</p></div>{provider ? <a className="luminous-focus inline-flex items-center gap-1.5 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-2 text-xs font-medium text-[var(--ls-accent)]" href={provider.url} rel="noreferrer" target="_blank">View in {provider.label}<ArrowUpRight className="size-3.5" /></a> : null}</div>;
}

function CheckTable({ rows, empty = "No checks retained.", stale = false }: { rows: CheckRow[]; empty?: string; stale?: boolean }) {
  if (!rows.length) return <p className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 text-sm text-[var(--ls-text-secondary)]">{empty}</p>;
  return <div className="overflow-x-auto rounded-[12px] border border-[var(--ls-line)]"><table className="w-full min-w-[650px] text-left text-xs"><thead className="bg-[var(--ls-surface-muted)] text-[var(--ls-text-tertiary)]"><tr><th className="px-4 py-3 font-medium" scope="col">Name</th><th className="px-3 py-3 font-medium" scope="col">Source</th><th className="px-3 py-3 font-medium" scope="col">Status</th><th className="px-3 py-3 font-medium" scope="col">Started / observed</th><th className="px-3 py-3 font-medium" scope="col">Duration</th><th className="px-4 py-3 text-right font-medium" scope="col">Action</th></tr></thead><tbody className="divide-y divide-[var(--ls-line)]">{rows.map((row) => <tr className="text-[var(--ls-text-secondary)]" key={row.id}><td className="px-4 py-3"><p className="font-medium text-[var(--ls-text)]">{row.name}</p>{row.note ? <p className="mt-1 text-[10px] text-[var(--ls-text-tertiary)]">{row.note}</p> : null}</td><td className="px-3 py-3">{row.source}</td><td className="px-3 py-3"><CheckState state={row.state} stale={stale} /></td><td className="px-3 py-3 whitespace-nowrap">{formatTime(row.time)}</td><td className="px-3 py-3 whitespace-nowrap">{row.duration ?? "Not retained"}</td><td className="px-4 py-3 text-right">{row.url ? <a className="luminous-focus inline-flex items-center gap-1 text-[var(--ls-accent)] hover:underline" href={row.url} rel={row.url.startsWith("http") ? "noreferrer" : undefined} target={row.url.startsWith("http") ? "_blank" : undefined}>View<ArrowUpRight className="size-3" /></a> : "—"}</td></tr>)}</tbody></table></div>;
}

function CheckState({ state, stale }: { state: string; stale: boolean }) {
  const normalized = providerCheckState(state);
  const tone = normalized === "success" && !stale ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : normalized === "failed" ? "bg-red-500/10 text-[var(--ls-critical-text)]" : normalized === "running" || normalized === "queued" ? "bg-violet-500/10 text-[var(--ls-accent)]" : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]";
  return <span className={cn("inline-flex rounded-full px-2 py-1 font-medium capitalize", tone)}>{stale ? `Stale · ${normalized}` : normalized === "unknown" ? state : normalized}</span>;
}

function PublicationReceipts({ evidence }: { evidence: ReviewEvidence }) {
  return <details className="rounded-[12px] border border-[var(--ls-line)]"><summary className="luminous-focus cursor-pointer px-4 py-3 text-xs font-medium text-[var(--ls-accent)]">Publication receipts ({evidence.receipts.length}) · not CI checks</summary><div className="divide-y divide-[var(--ls-line)] border-t border-[var(--ls-line)]">{evidence.receipts.length ? evidence.receipts.map((receipt) => <div className="flex flex-wrap items-start justify-between gap-3 p-4 text-xs" key={receipt.id}><div className="min-w-0"><p className="font-medium capitalize text-[var(--ls-text)]">{receipt.receipt_kind.replaceAll("_", " ")}</p><p className="mt-1 break-all font-mono text-[var(--ls-text-tertiary)]">{receipt.stable_marker}</p>{receipt.last_error ? <p className="mt-1 text-[var(--ls-critical-text)]">{receipt.last_error}</p> : null}</div><span className={receipt.published_at ? "text-[var(--ls-success-text)]" : "text-[var(--ls-warning-text)]"}>{receipt.published_at ? "Published" : "Pending"}</span></div>) : <p className="p-4 text-sm text-[var(--ls-text-secondary)]">No publication receipts retained.</p>}</div></details>;
}
