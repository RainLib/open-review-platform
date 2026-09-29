import Link from "next/link";
import {
  ArrowLeft,
  ArrowUpRight,
  CheckCircle2,
  CircleAlert,
  Clock3,
  Fingerprint,
  GitCommitHorizontal,
  History,
  Layers3,
  Radio,
  ReceiptText,
  ShieldCheck,
  TriangleAlert,
} from "lucide-react";

import { CopyEvidenceButton } from "@/components/console/copy-evidence-button";
import { DownloadEvidenceButton } from "@/components/console/download-evidence-button";
import { RunControls } from "@/components/console/run-controls";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { ProviderMark } from "@/components/providers/provider-icons";
import {
  displayReviewMode,
  getReviewEvidenceData,
  type ReviewEvidence,
  type ReviewFindingEvidence,
  type ReviewRun,
} from "@/lib/control-api";
import { formatTime, shortSHA } from "@/lib/format";
import { providerFileTarget, providerReviewTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";

type CLIReviewDetailTab = "overview" | "evidence" | "activity";

export default async function CLIReviewDetailPage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string; runId: string }>;
  searchParams: Promise<{ tab?: string }>;
}) {
  const [{ org, runId }, query] = await Promise.all([params, searchParams]);
  const data = await getReviewEvidenceData(org, runId);
  const evidence = data.evidence;
  const tab = validTab(query.tab);

  if (!evidence || evidence.run.trigger_kind !== "cli") {
    return (
      <div className="space-y-5">
        <BackLink org={org} />
        <section className="grid min-h-[420px] place-items-center rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-8 text-center">
          <div>
            <CircleAlert className="mx-auto size-7 text-[var(--ls-text-tertiary)]" />
            <h1 className="mt-4 text-lg font-semibold text-[var(--ls-text)]">CLI review evidence unavailable</h1>
            <p className="mx-auto mt-2 max-w-lg text-sm leading-6 text-[var(--ls-text-secondary)]">
              {data.detail ?? "The run is not a CLI-triggered review, was removed, or is outside this workspace."}
            </p>
          </div>
        </section>
      </div>
    );
  }

  const run = evidence.run;
  const provider = providerReviewTarget(run);
  const caller = evidence.events.find((event) => event.event_type === "run.acknowledged")?.actor_subject ?? "unknown";
  const evidenceJSON = JSON.stringify(evidence, null, 2);

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <BackLink org={org} />
        <span className="flex items-center gap-2 text-xs text-[var(--ls-text-tertiary)]">
          <span className={cn("size-2 rounded-full", data.source === "live" ? "bg-[var(--ls-success)]" : "bg-[var(--ls-warning)]")} />
          {data.source === "live" ? "Immutable live evidence" : data.source}
        </span>
      </div>

      <header className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_auto] xl:items-end">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2 text-xs font-medium text-[var(--ls-accent)]">
            <ProviderMark className="size-4" provider={run.provider} />
            <span>{run.repository}</span>
            <span className="text-[var(--ls-text-tertiary)]">/</span>
            <span>#{run.review_number}</span>
            <span className="rounded-full bg-[var(--ls-accent-soft)] px-2 py-0.5 uppercase tracking-[0.08em]">CLI</span>
          </div>
          <h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">Run {run.id.slice(0, 8)}</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">
            Exact revision <span className="font-mono text-[var(--ls-text)]">{shortSHA(run.head_sha)}</span> · caller <span className="font-mono text-[var(--ls-text)]">{callerLabel(caller)}</span> · immutable revision r{run.revision}.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <DownloadEvidenceButton fileName={`open-review-${run.id}.json`} value={evidenceJSON} />
          {provider ? <a className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-medium text-white shadow-[var(--ls-shadow-control)]" href={provider.url} rel="noreferrer" target="_blank">Open in {provider.label}<ArrowUpRight className="size-4" /></a> : null}
          <RunControls enabled={data.source === "live"} org={org} revision={run.revision} runID={run.id} state={run.state} />
        </div>
      </header>

      <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="CLI review run views">
        <Tab href={`/${org}/cli-reviews/${run.id}?tab=overview`} label="Overview" selected={tab === "overview"} />
        <Tab count={evidence.findings.length + evidence.receipts.length} href={`/${org}/cli-reviews/${run.id}?tab=evidence`} label="Evidence" selected={tab === "evidence"} />
        <Tab count={evidence.events.length} href={`/${org}/cli-reviews/${run.id}?tab=activity`} label="Activity" selected={tab === "activity"} />
      </TabStateRouter>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_330px]">
        <section aria-label="Selected CLI review evidence" className="min-w-0 rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 sm:p-6">
          {tab === "overview" ? <Overview evidence={evidence} /> : null}
          {tab === "evidence" ? <Evidence evidence={evidence} hasRuleSnapshot={Boolean(data.ruleSnapshot)} /> : null}
          {tab === "activity" ? <Activity evidence={evidence} /> : null}
        </section>
        <RunContext caller={caller} evidence={evidence} />
      </div>
    </div>
  );
}

function Overview({ evidence }: { evidence: ReviewEvidence }) {
  const run = evidence.run;
  const highRisk = evidence.findings.filter((finding) => finding.severity === "critical" || finding.severity === "high").length;
  const completedStages = evidence.stages.filter((stage) => stage.state === "succeeded" || stage.state === "skipped").length;
  const result = resultState(run, highRisk);
  return (
    <div className="space-y-6">
      <section className={cn("rounded-[14px] border p-5", result.tone)}>
        <div className="flex items-start gap-3"><result.icon className="mt-0.5 size-5 shrink-0" /><div><h2 className="font-semibold">{result.title}</h2><p className="mt-1 text-sm leading-6 text-[var(--ls-text-secondary)]">{result.detail}</p></div></div>
      </section>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Metric label="Findings" value={String(evidence.findings.length)} />
        <Metric label="High risk" value={String(highRisk)} />
        <Metric label="Stages complete" value={`${completedStages}/${evidence.stages.length}`} />
        <Metric label="Provider receipts" value={String(evidence.receipts.length)} />
      </div>
      <section>
        <SectionHeading detail="Durable stage state is updated by the workflow and retained with the exact run." title="Execution stages" />
        <div className="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {evidence.stages.length ? evidence.stages.map((stage) => <div className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4" key={stage.id}><div className="flex items-center justify-between gap-3"><p className="text-sm font-semibold capitalize text-[var(--ls-text)]">{stage.stage}</p><span className={cn("size-2 rounded-full", stage.state === "succeeded" ? "bg-[var(--ls-success)]" : stage.state === "failed" ? "bg-[var(--ls-critical)]" : stage.state === "running" ? "bg-[var(--ls-accent)]" : "bg-[var(--ls-text-tertiary)]")} /></div><p className="mt-2 text-xs capitalize text-[var(--ls-text-secondary)]">{stage.state} · attempt {stage.attempt + 1}</p><p className="mt-1 text-[11px] text-[var(--ls-text-tertiary)]">{stage.finished_at ? formatTime(stage.finished_at) : stage.started_at ? "In progress" : "Awaiting execution"}</p></div>) : <Empty detail="Stage evidence has not been attached yet." title="No stages retained" />}
        </div>
      </section>
      <section className="rounded-[13px] border border-amber-500/20 bg-amber-500/[0.05] p-4 text-sm leading-6 text-[var(--ls-text-secondary)]">
        <strong className="text-[var(--ls-warning-text)]">Merge decision boundary.</strong> This page reports review evidence. Branch protection and a retained merge-policy snapshot remain authoritative; finding severity alone does not prove that GitHub or GitLab blocked the merge.
      </section>
    </div>
  );
}

function Evidence({ evidence, hasRuleSnapshot }: { evidence: ReviewEvidence; hasRuleSnapshot: boolean }) {
  return (
    <div className="space-y-7">
      <section>
        <SectionHeading detail="Actionable findings link to the exact provider file and line when a trusted target can be constructed." title="Findings" />
        <div className="mt-4 divide-y divide-[var(--ls-line)] overflow-hidden rounded-[13px] border border-[var(--ls-line)]">
          {evidence.findings.length ? evidence.findings.map((finding) => <Finding evidence={evidence} finding={finding} key={finding.id} />) : <Empty detail="No actionable findings were retained for this run." title="No findings" />}
        </div>
      </section>
      <section>
        <SectionHeading detail="Historical conclusions use the snapshots attached at admission, never the workspace's current settings." title="Pinned configuration" />
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          <EvidenceState label="Rule snapshot" ready={hasRuleSnapshot} value={hasRuleSnapshot ? "Immutable" : "Not attached"} />
          <EvidenceState label="Review configuration" ready={evidence.configuration_snapshot.length > 0} value={`${evidence.configuration_snapshot.length} section${evidence.configuration_snapshot.length === 1 ? "" : "s"}`} />
        </div>
      </section>
      <ExecutionScope plan={evidence.execution_plan} run={evidence.run} />
      <section>
        <SectionHeading detail="Receipts prove provider publication attempts without exposing provider credentials or private model reasoning." title="Publication receipts" />
        <div className="mt-4 divide-y divide-[var(--ls-line)] overflow-hidden rounded-[13px] border border-[var(--ls-line)]">
          {evidence.receipts.length ? evidence.receipts.map((receipt) => <div className="grid gap-2 p-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center" key={receipt.id}><div><p className="text-sm font-semibold capitalize text-[var(--ls-text)]">{receipt.receipt_kind.replaceAll("_", " ")}</p><p className="mt-1 break-all font-mono text-[11px] text-[var(--ls-text-tertiary)]">{receipt.stable_marker} · {receipt.payload_hash.slice(0, 12)}</p>{receipt.last_error ? <p className="mt-2 text-xs text-[var(--ls-critical-text)]">{receipt.last_error}</p> : null}</div><span className={cn("w-fit rounded-full px-2.5 py-1 text-xs font-medium", receipt.published_at ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : "bg-amber-500/10 text-[var(--ls-warning-text)]")}>{receipt.published_at ? "Published" : "Pending"}</span></div>) : <Empty detail="No provider publication receipts are attached yet." title="No receipts" />}
        </div>
      </section>
    </div>
  );
}

function ExecutionScope({ plan, run }: { plan: ReviewEvidence["execution_plan"]; run: ReviewEvidence["run"] }) {
  if (!plan) {
    return <section><SectionHeading detail="This legacy run has no persisted execution plan, so the page cannot claim which files were analyzed." title="Execution scope unavailable" /></section>;
  }
  return <section>
    <SectionHeading detail="The plan was persisted before model execution. Selected paths link to the exact provider revision; deferred files were not analyzed by this run." title="Execution scope" />
    <div className="mt-4 flex flex-wrap gap-2 text-xs text-[var(--ls-text-secondary)]"><span className="rounded-full bg-[var(--ls-accent-soft)] px-2.5 py-1 font-medium capitalize text-[var(--ls-accent)]">{plan.mode} · {plan.selected_paths.length} selected</span>{plan.deferred_files ? <span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1">{plan.deferred_files} deferred</span> : null}</div>
    <ul className="mt-4 max-h-56 space-y-1 overflow-y-auto rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3 font-mono text-xs leading-5 text-[var(--ls-text-secondary)]">{plan.selected_paths.map((path) => { const target = providerFileTarget({ ...run, head_sha: run.head_sha, path }); return <li className="truncate" key={path}>{target ? <a className="luminous-focus inline-flex max-w-full items-center gap-1 rounded text-[var(--ls-accent)] hover:underline" href={target.url} rel="noreferrer" target="_blank"><span className="truncate">{path}</span><ArrowUpRight className="size-3 shrink-0" /></a> : path}</li>; })}</ul>
  </section>;
}

function Finding({ evidence, finding }: { evidence: ReviewEvidence; finding: ReviewFindingEvidence }) {
  const run = evidence.run;
  const target = providerFileTarget({ ...run, path: finding.path, start_line: finding.start_line, end_line: finding.end_line });
  const prompt = [`Fix the ${finding.severity} ${finding.category} finding in ${run.repository}.`, `Exact revision: ${run.head_sha}`, `Location: ${finding.path}:${finding.start_line}-${finding.end_line}`, `Finding: ${finding.body}`, finding.suggestion ? `Recommended direction: ${finding.suggestion}` : undefined, "Preserve unrelated behavior and add focused verification."].filter(Boolean).join("\n\n");
  return (
    <article className="p-4">
      <div className="flex flex-wrap items-start justify-between gap-3"><div><p className="text-xs font-semibold uppercase tracking-[0.08em] text-[var(--ls-warning-text)]">{finding.severity} · {finding.category}</p>{target ? <a className="luminous-focus mt-1 inline-flex items-center gap-1 rounded font-mono text-sm text-[var(--ls-accent)] hover:underline" href={target.url} rel="noreferrer" target="_blank">{finding.path}:{finding.start_line}<ArrowUpRight className="size-3.5" /></a> : <p className="mt-1 font-mono text-sm text-[var(--ls-text)]">{finding.path}:{finding.start_line}</p>}</div><CopyEvidenceButton label="Copy prompt" value={prompt} /></div>
      <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">{finding.body}</p>
      {finding.suggestion ? <details className="mt-3 rounded-[10px] bg-[var(--ls-surface-muted)] p-3"><summary className="luminous-focus cursor-pointer text-xs font-semibold text-[var(--ls-text)]">Recommended change</summary><p className="mt-2 whitespace-pre-wrap text-xs leading-5 text-[var(--ls-text-secondary)]">{finding.suggestion}</p></details> : null}
    </article>
  );
}

function Activity({ evidence }: { evidence: ReviewEvidence }) {
  if (!evidence.events.length) return <Empty detail="No durable transition events are attached to this run." title="No activity events" />;
  return (
    <div>
      <SectionHeading detail="Events are ordered by immutable run revision. Credentials and private model reasoning are excluded." title="Run activity" />
      <ol className="mt-6 border-l border-[var(--ls-line-strong)] pl-6">
        {evidence.events.map((event, index) => <li className="relative border-b border-[var(--ls-line)] py-5 first:pt-0 last:border-0" key={event.id}><span className={cn("absolute -left-[29px] top-6 size-2.5 rounded-full bg-[var(--ls-accent)] ring-4 ring-[var(--ls-surface)]", index === 0 && "top-1")} /><div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between"><div><p className="text-sm font-semibold text-[var(--ls-text)]">{event.event_type.replaceAll("_", " ")}</p><p className="mt-1 text-xs text-[var(--ls-text-secondary)]">Revision {event.revision} · {event.actor_kind}{event.actor_subject ? ` · ${callerLabel(event.actor_subject)}` : ""}</p>{Object.keys(event.payload).length ? <details className="mt-3"><summary className="luminous-focus cursor-pointer text-xs font-medium text-[var(--ls-accent)]">Event payload</summary><pre className="mt-2 overflow-x-auto whitespace-pre-wrap rounded-[9px] bg-[var(--ls-surface-muted)] p-3 font-mono text-[11px] leading-5 text-[var(--ls-text-tertiary)]">{JSON.stringify(event.payload, null, 2)}</pre></details> : null}</div><time className="shrink-0 text-xs text-[var(--ls-text-tertiary)]" dateTime={event.created_at}>{formatTime(event.created_at)}</time></div></li>)}
      </ol>
    </div>
  );
}

function RunContext({ caller, evidence }: { caller: string; evidence: ReviewEvidence }) {
  const run = evidence.run;
  return (
    <aside className="luminous-frosted h-fit rounded-[24px] border border-[var(--ls-line-strong)] p-5 shadow-[var(--ls-shadow-float)] xl:sticky xl:top-20">
      <h2 className="text-lg font-semibold text-[var(--ls-text)]">Run context</h2>
      <dl className="mt-4 space-y-3">
        <Context icon={Radio} label="State" value={run.state.replaceAll("_", " ")} />
        <Context icon={Layers3} label="Mode" value={displayReviewMode(run.review_mode)} />
        <Context icon={GitCommitHorizontal} label="Head" mono value={shortSHA(run.head_sha)} />
        <Context icon={GitCommitHorizontal} label="Base" mono value={shortSHA(run.base_sha)} />
        <Context icon={Fingerprint} label="Caller" mono value={callerLabel(caller)} />
        <Context icon={Clock3} label="Created" value={formatTime(run.created_at)} />
      </dl>
      <div className="mt-5 border-t border-[var(--ls-line)] pt-4 text-xs leading-5 text-[var(--ls-text-tertiary)]"><p className="flex items-center gap-2 font-semibold text-[var(--ls-text)]"><ShieldCheck className="size-3.5 text-[var(--ls-accent)]" />Trust boundary</p><p className="mt-2">The installation selected the provider and clone destination. This page never accepts a caller-supplied clone URL.</p></div>
    </aside>
  );
}

function resultState(run: ReviewRun, highRisk: number) {
  if (run.state === "failed" || run.state === "needs_attention") return { detail: run.failure_message || "The run needs explicit human attention.", icon: CircleAlert, title: "Action required", tone: "border-red-500/20 bg-red-500/[0.06] text-[var(--ls-critical-text)]" };
  if (run.state === "superseded") return { detail: "A newer head revision replaced this result. This run remains read-only audit evidence.", icon: History, title: "Superseded evidence", tone: "border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]" };
  if (run.state !== "completed") return { detail: "Stages and receipts update as the durable workflow progresses.", icon: Radio, title: "Review in progress", tone: "border-violet-500/20 bg-violet-500/[0.06] text-[var(--ls-accent)]" };
  if (highRisk > 0) return { detail: `${highRisk} high-risk finding${highRisk === 1 ? "" : "s"} require review. The retained merge policy and provider protection decide whether they block.`, icon: TriangleAlert, title: "Completed with high-risk findings", tone: "border-amber-500/20 bg-amber-500/[0.06] text-[var(--ls-warning-text)]" };
  return { detail: "The review completed. Verify external tests and branch protection before merging.", icon: CheckCircle2, title: "Review completed", tone: "border-emerald-500/20 bg-emerald-500/[0.06] text-[var(--ls-success-text)]" };
}

function Metric({ label, value }: { label: string; value: string }) { return <div className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><p className="text-[11px] font-medium text-[var(--ls-text-tertiary)]">{label}</p><p className="mt-2 text-2xl font-semibold tracking-[-0.04em] text-[var(--ls-text)]">{value}</p></div>; }
function EvidenceState({ label, ready, value }: { label: string; ready: boolean; value: string }) { return <div className="grid grid-cols-[24px_minmax(0,1fr)_auto] items-center gap-3 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><span className={cn("grid size-6 place-items-center rounded-full", ready ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : "bg-amber-500/10 text-[var(--ls-warning-text)]")}>{ready ? <CheckCircle2 className="size-4" /> : <CircleAlert className="size-4" />}</span><span className="text-sm font-medium text-[var(--ls-text)]">{label}</span><span className="text-xs text-[var(--ls-text-secondary)]">{value}</span></div>; }
function Context({ icon: Icon, label, mono = false, value }: { icon: typeof Radio; label: string; mono?: boolean; value: string }) { return <div className="grid grid-cols-[18px_64px_minmax(0,1fr)] items-start gap-2 text-xs"><Icon className="mt-0.5 size-3.5 text-[var(--ls-text-tertiary)]" /><dt className="text-[var(--ls-text-tertiary)]">{label}</dt><dd className={cn("min-w-0 break-all text-right capitalize text-[var(--ls-text)]", mono && "font-mono normal-case")}>{value}</dd></div>; }
function SectionHeading({ detail, title }: { detail: string; title: string }) { return <div><h2 className="text-lg font-semibold text-[var(--ls-text)]">{title}</h2><p className="mt-1 text-sm leading-6 text-[var(--ls-text-secondary)]">{detail}</p></div>; }
function Empty({ detail, title }: { detail: string; title: string }) { return <div className="col-span-full grid min-h-40 place-items-center p-6 text-center"><div><ReceiptText className="mx-auto size-5 text-[var(--ls-text-tertiary)]" /><h3 className="mt-3 text-sm font-semibold text-[var(--ls-text)]">{title}</h3><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{detail}</p></div></div>; }
function Tab({ count, href, label, selected }: { count?: number; href: string; label: string; selected: boolean }) { return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative inline-flex h-11 shrink-0 items-center gap-2 rounded-t-[10px] px-4 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={href} role="tab" tabIndex={selected ? 0 : -1}>{label}{count !== undefined ? <span className="rounded-full bg-[var(--ls-surface-muted)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)]">{count}</span> : null}{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>; }
function BackLink({ org }: { org: string }) { return <Link className="luminous-focus inline-flex items-center gap-2 rounded text-sm text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]" href={`/${org}/cli-reviews`}><ArrowLeft className="size-4" />Back to CLI reviews</Link>; }
function callerLabel(subject: string) { if (!subject) return "unknown"; if (subject.startsWith("api-key:")) { const value = subject.slice("api-key:".length); return value.length > 12 ? `key:${value.slice(0, 8)}…` : `key:${value}`; } return subject.length > 18 ? `${subject.slice(0, 16)}…` : subject; }
function validTab(value?: string): CLIReviewDetailTab { return value === "evidence" || value === "activity" ? value : "overview"; }
