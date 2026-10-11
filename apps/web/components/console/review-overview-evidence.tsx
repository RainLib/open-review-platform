import Link from "next/link";
import { ArrowUpRight, CheckCircle2, CircleAlert, ClipboardCheck, GitCommitHorizontal, History, Layers3, ShieldCheck, TriangleAlert } from "lucide-react";

import { CopyEvidenceButton } from "@/components/console/copy-evidence-button";
import { TabStateRouter } from "@/components/console/tab-state-router";
import type { ReviewEvidence } from "@/lib/control-api";
import { shortSHA } from "@/lib/format";
import { reviewFileRows } from "@/lib/review-file-analysis";
import { observedSeverity, providerObservation, reviewConfigurationEvidence, selectedScopeCount, type ReviewOverviewView } from "@/lib/review-overview";
import { providerFileTarget, providerReviewDiffTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

const views: Array<{ id: ReviewOverviewView; label: string }> = [
  { id: "overview", label: "Overview" },
  { id: "scope", label: "Scope" },
  { id: "risk", label: "Risk" },
  { id: "verification", label: "Verification" },
  { id: "evidence", label: "Evidence" },
];

function viewURL(org: string, runID: string, view: ReviewOverviewView) {
  return `/${encodeURIComponent(org)}/reviews/${encodeURIComponent(runID)}?${new URLSearchParams({ tab: "overview", view })}`;
}

export function ReviewOverviewEvidence({ evidence, hasRuleSnapshot, org, view }: { evidence: ReviewEvidence; hasRuleSnapshot: boolean; org: string; view: ReviewOverviewView }) {
  return <div className="space-y-5">
    <div><div className="flex min-w-0 items-center gap-2"><h2 className="text-lg font-semibold text-[var(--ls-text)]">Review overview</h2><HelpHint label="Review overview">A compact decision surface for the exact retained revision. Detailed findings, files and checks stay in their own views.</HelpHint></div></div>
    <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="Review overview views">
      {views.map((item) => <Link aria-current={view === item.id ? "page" : undefined} aria-selected={view === item.id} className={cn("luminous-focus relative inline-flex h-11 shrink-0 items-center rounded-t-[9px] px-3 text-sm font-medium", view === item.id ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={viewURL(org, evidence.run.id, item.id)} key={item.id} role="tab" tabIndex={view === item.id ? 0 : -1}>{item.label}{view === item.id ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>)}
    </TabStateRouter>
    {view === "overview" ? <SummaryView evidence={evidence} org={org} /> : null}
    {view === "scope" ? <ScopeView evidence={evidence} org={org} /> : null}
    {view === "risk" ? <RiskView evidence={evidence} org={org} /> : null}
    {view === "verification" ? <VerificationView evidence={evidence} org={org} /> : null}
    {view === "evidence" ? <EvidenceView evidence={evidence} hasRuleSnapshot={hasRuleSnapshot} org={org} /> : null}
  </div>;
}

function SummaryView({ evidence, org }: { evidence: ReviewEvidence; org: string }) {
  const run = evidence.run;
  const selected = selectedScopeCount(evidence);
  const providerState = providerObservation(evidence);
  const configuration = reviewConfigurationEvidence(evidence.configuration_snapshot);
  return <div className="space-y-4">
    <ResultBanner evidence={evidence} />
    <div className="grid gap-3 md:grid-cols-2">
      <section className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><div className="flex items-center gap-2 text-xs font-semibold text-[var(--ls-text-secondary)]"><GitCommitHorizontal className="size-4 text-[var(--ls-accent)]" />Head revision</div><div className="mt-3 flex items-center gap-2"><span className="font-mono text-base font-semibold text-[var(--ls-text)]">{shortSHA(run.head_sha)}</span><CopyEvidenceButton value={run.head_sha} /></div><p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">Base {shortSHA(run.base_sha)} · run {run.id.slice(0, 8)}</p></section>
      <section className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><div className="flex items-center gap-2 text-xs font-semibold text-[var(--ls-text-secondary)]"><Layers3 className="size-4 text-[var(--ls-accent)]" />Prioritized scope</div><p className="mt-3 text-base font-semibold text-[var(--ls-text)]">{selected === undefined ? "Not retained" : `${selected} selected · ${evidence.execution_plan?.deferred_files ?? 0} deferred`}</p><p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">{selected === undefined ? "This run predates a retained execution plan." : "Static admission plan; deferred files are not reviewed findings."}</p><Link className="luminous-focus mt-3 inline-flex items-center gap-1 text-xs font-medium text-[var(--ls-accent)] hover:underline" href={viewURL(org, run.id, "scope")}>Explore scope<ArrowUpRight className="size-3" /></Link></section>
    </div>
    <GateSummary evidence={evidence} org={org} />
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4"><Signal label="Retained findings" value={String(evidence.findings.length)} detail="See line-level evidence" href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(run.id)}?tab=findings`} /><Signal label="Observed severity" value={observedSeverity(evidence.findings)} detail="Not a repository-wide risk rating" href={viewURL(org, run.id, "risk")} /><Signal label="Provider CI" value={providerState} detail="Separate from Open Review gate" href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(run.id)}?tab=checks&view=provider-ci`} /><Signal label="Provenance" value={`${configuration.retained}/${configuration.expected}`} detail="Review configuration sections retained" href={viewURL(org, run.id, "evidence")} /></div>
  </div>;
}

function ResultBanner({ evidence }: { evidence: ReviewEvidence }) {
  const { run, merge_gate: gate } = evidence;
  const state = run.state;
  let title = "Review in progress";
  let detail = "The durable workflow has not produced a terminal decision for this revision.";
  let tone = "border-violet-500/20 bg-violet-500/[0.06] text-[var(--ls-accent)]";
  let Icon = ClipboardCheck;
  if (state === "failed" || state === "needs_attention") { title = "Action required"; detail = run.failure_message || "Inspect the run before retrying or changing policy."; tone = "border-red-500/20 bg-red-500/[0.06] text-[var(--ls-critical-text)]"; Icon = CircleAlert; }
  else if (state === "superseded" || state === "cancelled") { title = state === "superseded" ? "Superseded evidence" : "Review cancelled"; detail = "This revision must not be used as a current merge decision."; tone = "border-[var(--ls-line)] bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]"; Icon = History; }
  else if (state === "completed" && gate?.enabled && gate.conclusion === "failure") { title = "Merge gate blocked"; detail = `${gate.blocking_findings} retained finding${gate.blocking_findings === 1 ? "" : "s"} meet the ${gate.threshold} threshold.`; tone = "border-red-500/20 bg-red-500/[0.06] text-[var(--ls-critical-text)]"; Icon = TriangleAlert; }
  else if (state === "completed" && gate?.enabled && gate.conclusion === "success") { title = "🎉 Merge gate passed"; detail = `${gate.blocking_findings} blocking of ${gate.finding_count} retained findings at the ${gate.threshold} threshold. Independent CI and branch protection remain separate.`; tone = "border-emerald-500/20 bg-emerald-500/[0.06] text-[var(--ls-success-text)]"; Icon = CheckCircle2; }
  else if (state === "completed") { title = gate ? "Review complete · advisory gate" : "Review complete · gate unknown"; detail = gate ? "This revision's admitted policy did not request blocking." : "No immutable merge-gate decision was retained; no passing status is inferred."; tone = "border-[var(--ls-line)] bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]"; Icon = ShieldCheck; }
  return <section className={cn("flex items-start gap-3 rounded-[12px] border p-4", tone)}><Icon className="mt-0.5 size-5 shrink-0" /><div><h3 className="text-sm font-semibold">{title}</h3><p className="mt-1 text-xs leading-5 opacity-90">{detail}</p></div></section>;
}

function GateSummary({ evidence, org }: { evidence: ReviewEvidence; org: string }) {
  const gate = evidence.merge_gate;
  return <section className="flex flex-wrap items-center justify-between gap-3 rounded-[12px] border border-[var(--ls-line)] p-4"><div><h3 className="text-xs font-semibold text-[var(--ls-text)]">Open Review merge gate</h3><p className="mt-1 text-sm font-medium text-[var(--ls-text-secondary)]">{!gate ? "Not retained" : !gate.enabled ? "Advisory" : gate.conclusion === "failure" ? "Blocked" : "Passed"}{gate ? ` · ${gate.threshold} threshold · ${gate.blocking_findings} blocking` : " · this is not a passing result"}</p></div><Link className="luminous-focus inline-flex items-center gap-1 text-xs font-medium text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(evidence.run.id)}?tab=checks&view=open-review`}>View checks<ArrowUpRight className="size-3" /></Link></section>;
}

function Signal({ label, value, detail, href }: { label: string; value: string; detail: string; href: string }) { return <Link className="luminous-focus rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 hover:border-[var(--ls-line-strong)]" href={href}><p className="text-xs font-medium text-[var(--ls-text-secondary)]">{label}</p><p className="mt-2 text-lg font-semibold capitalize text-[var(--ls-text)]">{value}</p><p className="mt-1 text-[11px] text-[var(--ls-text-tertiary)]">{detail}</p></Link>; }

function ScopeView({ evidence, org }: { evidence: ReviewEvidence; org: string }) {
  const plan = evidence.execution_plan;
  if (!plan) return <EvidenceEmpty title="Execution scope not retained" detail="This legacy run has no immutable selection plan; the page cannot claim which files were analyzed." />;
  const selected = reviewFileRows(evidence).filter((file) => file.scope === "selected");
  return <div className="space-y-4"><div className="grid gap-3 sm:grid-cols-3"><Datum label="Admission mode" value={plan.mode} /><Datum label="Selected paths" value={String(selected.length)} /><Datum label="Deferred files" value={String(plan.deferred_files)} /></div><p className="text-xs leading-5 text-[var(--ls-text-secondary)]">Selection is an immutable path-level review budget, not a resolved dependency graph or proof that deferred files were scanned.</p><section><h3 className="text-sm font-semibold text-[var(--ls-text)]">Selected paths</h3><div className="mt-3 divide-y divide-[var(--ls-line)] overflow-hidden rounded-[12px] border border-[var(--ls-line)]">{selected.length ? selected.map((file) => { const target = file.changeType === "deleted" ? providerReviewDiffTarget(evidence.run) : providerFileTarget({ ...evidence.run, path: file.path, head_sha: evidence.run.head_sha }); return <div className="flex flex-wrap items-center justify-between gap-2 px-4 py-3 text-xs" key={file.path}><div className="min-w-0"><p className="break-all font-mono text-[var(--ls-text)]">{file.path}</p>{file.reasons.length ? <p className="mt-1 text-[var(--ls-text-tertiary)]">{file.reasons.join(" · ")}</p> : null}</div>{target ? <a className="luminous-focus shrink-0 text-[var(--ls-accent)] hover:underline" href={target.url} rel="noreferrer" target="_blank">{file.changeType === "deleted" ? "View current diff ↗" : "View file ↗"}</a> : null}</div>; }) : <p className="p-4 text-xs text-[var(--ls-text-secondary)]">No selected path retained.</p>}</div></section>{plan.static_impact_signals?.length ? <section className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><h3 className="text-xs font-semibold text-[var(--ls-text)]">Static impact signals</h3><ul className="mt-2 space-y-1 text-xs text-[var(--ls-text-secondary)]">{plan.static_impact_signals.map((signal) => <li key={signal}>• {signal}</li>)}</ul></section> : null}<Link className="luminous-focus inline-flex items-center gap-1 text-xs font-medium text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(evidence.run.id)}?tab=files`}>Open complete file admission ledger<ArrowUpRight className="size-3" /></Link></div>;
}

function RiskView({ evidence, org }: { evidence: ReviewEvidence; org: string }) {
  const severities = ["critical", "high", "medium", "low"] as const;
  return <div className="space-y-4"><div className="grid gap-3 sm:grid-cols-2"><Datum label="Highest retained finding" value={observedSeverity(evidence.findings)} /><Datum label="Execution mode" value={evidence.execution_plan?.mode ?? "not retained"} /></div><p className="text-xs leading-5 text-[var(--ls-text-secondary)]">This summarizes detected findings only. No finding does not prove the repository is low risk; static path signals do not measure runtime blast radius.</p><section className="rounded-[12px] border border-[var(--ls-line)] p-4"><h3 className="text-sm font-semibold text-[var(--ls-text)]">Finding severity</h3><div className="mt-3 grid grid-cols-4 gap-2">{severities.map((severity) => <div className="rounded-[9px] bg-[var(--ls-surface-muted)] p-3 text-center" key={severity}><p className="text-lg font-semibold text-[var(--ls-text)]">{evidence.findings.filter((finding) => finding.severity === severity).length}</p><p className="mt-1 text-[10px] capitalize text-[var(--ls-text-secondary)]">{severity}</p></div>)}</div></section>{evidence.execution_plan?.static_impact_signals?.length ? <section className="rounded-[12px] border border-[var(--ls-line)] p-4"><h3 className="text-sm font-semibold text-[var(--ls-text)]">Path-priority signals</h3><ul className="mt-2 space-y-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{evidence.execution_plan.static_impact_signals.map((signal) => <li key={signal}>• {signal}</li>)}</ul></section> : null}<Link className="luminous-focus inline-flex items-center gap-1 text-xs font-medium text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(evidence.run.id)}?tab=findings`}>Inspect findings<ArrowUpRight className="size-3" /></Link></div>;
}

function VerificationView({ evidence, org }: { evidence: ReviewEvidence; org: string }) {
  const completedStages = evidence.stages.filter((stage) => stage.state === "succeeded" || stage.state === "skipped").length;
  const published = evidence.receipts.filter((receipt) => Boolean(receipt.published_at)).length;
  const providerState = providerObservation(evidence);
  return <div className="space-y-4"><div className="grid gap-3 sm:grid-cols-3"><Datum label="Execution stages complete" value={`${completedStages}/${evidence.stages.length}`} /><Datum label="Published receipts" value={`${published}/${evidence.receipts.length}`} /><Datum label="Provider CI collection" value={providerState} /></div><p className="text-xs leading-5 text-[var(--ls-text-secondary)]">Stages and publication receipts prove Open Review workflow state, not build, test, security or migration execution. Independent CI is a separate provider observation.</p><section className="divide-y divide-[var(--ls-line)] overflow-hidden rounded-[12px] border border-[var(--ls-line)]"><EvidenceLine label="Exact reviewed head" value={shortSHA(evidence.run.head_sha)} ready={Boolean(evidence.run.head_sha)} /><EvidenceLine label="Merge-gate decision" value={evidence.merge_gate ? `${evidence.merge_gate.conclusion} · ${evidence.merge_gate.threshold}` : "Not retained"} ready={Boolean(evidence.merge_gate)} /><EvidenceLine label="Provider CI snapshot" value={providerState} ready={providerState === "observed"} /><EvidenceLine label="Test execution evidence" value="Not supplied by this review record" ready={false} /></section><Link className="luminous-focus inline-flex items-center gap-1 text-xs font-medium text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(evidence.run.id)}?tab=checks`}>Inspect checks and provider statuses<ArrowUpRight className="size-3" /></Link></div>;
}

function EvidenceView({ evidence, hasRuleSnapshot, org }: { evidence: ReviewEvidence; hasRuleSnapshot: boolean; org: string }) {
  const run = evidence.run;
  const configuration = reviewConfigurationEvidence(evidence.configuration_snapshot);
  return <div className="space-y-4"><section className="divide-y divide-[var(--ls-line)] overflow-hidden rounded-[12px] border border-[var(--ls-line)]"><EvidenceLine label="Run identity" value={run.id} ready /><EvidenceLine label="Head / base" value={`${shortSHA(run.head_sha)} / ${shortSHA(run.base_sha)}`} ready={Boolean(run.head_sha && run.base_sha)} /><EvidenceLine label="Rule snapshot" value={hasRuleSnapshot ? "Attached at admission" : "Not attached"} ready={hasRuleSnapshot} /><EvidenceLine label="Configuration snapshot" value={`${configuration.retained}/${configuration.expected} sections retained`} ready={configuration.complete} /><EvidenceLine label="Execution plan" value={evidence.execution_plan ? `${evidence.execution_plan.mode} · ${evidence.execution_plan.selected_paths.length} selected` : "Not retained"} ready={Boolean(evidence.execution_plan)} /></section><section><h3 className="text-sm font-semibold text-[var(--ls-text)]">Configuration provenance</h3>{evidence.configuration_snapshot.length ? <div className="mt-3 grid gap-2 sm:grid-cols-2">{evidence.configuration_snapshot.map((snapshot) => <div className="rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3" key={snapshot.section}><p className="text-xs font-medium capitalize text-[var(--ls-text)]">{snapshot.section.replaceAll("_", " ")}</p><p className="mt-1 font-mono text-[10px] text-[var(--ls-text-tertiary)]">{snapshot.origin_scope_kind} r{snapshot.origin_revision} · {snapshot.content_sha256.slice(0, 12)}</p></div>)}</div> : <p className="mt-3 text-xs text-[var(--ls-text-secondary)]">No configuration sections were retained for this legacy or preview run.</p>}</section><p className="text-xs leading-5 text-[var(--ls-text-tertiary)]">This page reports retained provenance only. It does not infer model, test, security-scanner or rollout evidence that was not captured.</p><Link className="luminous-focus inline-flex items-center gap-1 text-xs font-medium text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/tasks/${encodeURIComponent(run.id)}`}>Open durable run<ArrowUpRight className="size-3" /></Link></div>;
}

function Datum({ label, value }: { label: string; value: string }) { return <div className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><p className="text-xs text-[var(--ls-text-secondary)]">{label}</p><p className="mt-2 break-all text-lg font-semibold capitalize text-[var(--ls-text)]">{value}</p></div>; }
function EvidenceLine({ label, value, ready }: { label: string; value: string; ready: boolean }) { return <div className="grid gap-1 px-4 py-3 text-xs sm:grid-cols-[20px_150px_minmax(0,1fr)] sm:items-center sm:gap-3"><span className={ready ? "text-[var(--ls-success-text)]" : "text-[var(--ls-warning-text)]"}>{ready ? <CheckCircle2 className="size-4" /> : <CircleAlert className="size-4" />}</span><span className="font-medium text-[var(--ls-text)]">{label}</span><span className="min-w-0 break-all font-mono text-[var(--ls-text-secondary)]">{value}</span></div>; }
function EvidenceEmpty({ title, detail }: { title: string; detail: string }) { return <div className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-6 text-center"><CircleAlert className="mx-auto size-5 text-[var(--ls-warning-text)]" /><h3 className="mt-2 text-sm font-semibold text-[var(--ls-text)]">{title}</h3><p className="mx-auto mt-2 max-w-lg text-xs leading-5 text-[var(--ls-text-secondary)]">{detail}</p></div>; }
