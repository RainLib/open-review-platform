import Link from "next/link";
import { ArrowUpRight, Braces, CircleAlert, FileCode2, Search, ShieldAlert, TriangleAlert } from "lucide-react";

import { CopyEvidenceButton } from "@/components/console/copy-evidence-button";
import { FindingFocus } from "@/components/console/finding-focus";
import { FindingNarrative } from "@/components/console/finding-narrative";
import type { ReviewEvidence, ReviewFindingEvidence } from "@/lib/control-api";
import { blockingFindings, filterReviewFindings, findingPrompt, findingPublicationState, type FindingFilters, type FindingPublicationState, type FindingSeverityView } from "@/lib/review-findings";
import { providerFileTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

function findingsURL(org: string, runID: string, filters: FindingFilters, finding?: string) {
  const params = new URLSearchParams({ tab: "findings" });
  if (filters.severity !== "all") params.set("severity", filters.severity);
  if (filters.status !== "all") params.set("status", filters.status);
  if (filters.category) params.set("category", filters.category);
  if (filters.file) params.set("file", filters.file);
  if (filters.query) params.set("q", filters.query);
  if (finding) params.set("finding", finding);
  return `/${encodeURIComponent(org)}/reviews/${encodeURIComponent(runID)}?${params}`;
}

export function ReviewFindingsEvidence({ evidence, filters, focusedFinding, org }: { evidence: ReviewEvidence; filters: FindingFilters; focusedFinding?: string; org: string }) {
  const blocking = blockingFindings(evidence);
  const filtered = filterReviewFindings(evidence.findings, filters, blocking);
  const selected = evidence.findings.find((finding) => finding.id === focusedFinding) ?? filtered[0];
  const selectedOutsideFilters = Boolean(selected && !filtered.some((finding) => finding.id === selected.id));
  const categories = [...new Set(evidence.findings.map((finding) => finding.category))].sort();
  const files = [...new Set(evidence.findings.map((finding) => finding.path))].sort();
  const confirmedInline = evidence.findings.filter((finding) => findingPublicationState(finding, evidence.receipts) === "published").length;
  const summary: Array<{ id: FindingSeverityView; label: string; count?: number }> = [
    { id: "all", label: "All", count: evidence.findings.length },
    { id: "blocking", label: "Blocking", count: blocking?.length },
    { id: "critical", label: "Critical", count: evidence.findings.filter((finding) => finding.severity === "critical").length },
    { id: "high", label: "High", count: evidence.findings.filter((finding) => finding.severity === "high").length },
    { id: "medium", label: "Medium", count: evidence.findings.filter((finding) => finding.severity === "medium").length },
    { id: "low", label: "Low", count: evidence.findings.filter((finding) => finding.severity === "low").length },
  ];
  return <div className="space-y-5">
    <div><div className="flex min-w-0 items-center gap-2"><h2 className="text-lg font-semibold text-[var(--ls-text)]">Findings</h2><HelpHint label="Findings">Actionable findings retained for this exact review run. Select a finding for its evidence, recommended direction and copyable LLM prompt.</HelpHint></div></div>
    <div className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" aria-label="Finding severity views">
      {summary.map((item) => item.id === "blocking" && blocking === undefined
        ? <span className="inline-flex h-10 shrink-0 items-center gap-1.5 px-3 text-xs text-[var(--ls-text-tertiary)]" key={item.id} title="No enabled immutable merge-gate threshold was retained">Blocking <span>—</span></span>
        : <Link aria-current={filters.severity === item.id ? "page" : undefined} className={cn("luminous-focus relative inline-flex h-10 shrink-0 items-center gap-1.5 rounded-t-[9px] px-3 text-xs font-medium", filters.severity === item.id ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={findingsURL(org, evidence.run.id, { ...filters, severity: item.id })} key={item.id}>{item.label}<span className="rounded-full bg-[var(--ls-surface-muted)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)]">{item.count}</span>{filters.severity === item.id ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>)}
    </div>
    {blocking && evidence.merge_gate && blocking.length !== evidence.merge_gate.blocking_findings ? <p className="flex gap-2 rounded-[10px] border border-amber-500/20 bg-amber-500/[0.06] p-3 text-xs text-[var(--ls-warning-text)]"><CircleAlert className="size-4 shrink-0" />The gate retained {evidence.merge_gate.blocking_findings} blocking findings, while {blocking.length} listed findings match its threshold. Inspect the immutable gate before acting on this count.</p> : null}
    {evidence.findings.length ? <p className="flex gap-2 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3 text-xs leading-5 text-[var(--ls-text-secondary)]"><CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />{confirmedInline} of {evidence.findings.length} findings have a confirmed inline publication receipt for this run. A missing receipt is not proof that the provider displayed the finding; inspect each finding and the Checks tab.</p> : null}
    <form action={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(evidence.run.id)}`} className="grid gap-2 sm:grid-cols-2 xl:grid-cols-[minmax(0,1fr)_repeat(3,minmax(110px,auto))_auto]" method="get">
      <input name="tab" type="hidden" value="findings" /><input name="severity" type="hidden" value={filters.severity} />
      <label className="relative"><span className="sr-only">Search findings</span><Search className="pointer-events-none absolute left-3 top-3 size-4 text-[var(--ls-text-tertiary)]" /><input className="luminous-focus h-10 w-full rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] pl-9 pr-3 text-xs text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]" defaultValue={filters.query} maxLength={200} name="q" placeholder="Search findings" /></label>
      <label><span className="sr-only">Category</span><select className="luminous-focus h-10 w-full rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2 text-xs text-[var(--ls-text)]" defaultValue={filters.category} name="category"><option value="">All categories</option>{categories.map((category) => <option key={category} value={category}>{category}</option>)}</select></label>
      <label><span className="sr-only">File</span><select className="luminous-focus h-10 w-full rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2 text-xs text-[var(--ls-text)]" defaultValue={filters.file} name="file"><option value="">All files</option>{files.map((file) => <option key={file} value={file}>{file}</option>)}</select></label>
      <label><span className="sr-only">Status</span><select className="luminous-focus h-10 w-full rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2 text-xs text-[var(--ls-text)]" defaultValue={filters.status} name="status"><option value="all">All statuses</option><option value="open">Open</option><option value="resolved">Resolved</option><option value="wont_fix">Won&apos;t fix</option></select></label>
      <button className="luminous-focus h-10 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs font-medium text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" type="submit">Apply</button>
    </form>
    {!evidence.findings.length ? <EmptyFindings title="No retained findings" detail="No actionable finding was retained for this run. This does not prove external tests or security scanners passed." /> : <div className="grid gap-4 lg:grid-cols-[220px_minmax(0,1fr)]">
      <section aria-label="Filtered findings" className="min-w-0 overflow-hidden rounded-[12px] border border-[var(--ls-line)]"><div className="border-b border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2 text-xs font-medium text-[var(--ls-text-secondary)]">{filtered.length} visible · {evidence.findings.length} retained</div><div className="max-h-[680px] overflow-y-auto">{filtered.length ? filtered.map((finding) => <Link aria-current={selected?.id === finding.id ? "true" : undefined} className={cn("luminous-focus block border-b border-[var(--ls-line)] p-3 last:border-b-0", selected?.id === finding.id ? "bg-[var(--ls-accent-soft)]" : "hover:bg-[var(--ls-surface-muted)]")} href={findingsURL(org, evidence.run.id, filters, finding.id)} key={finding.id}><span className="flex items-center justify-between gap-2"><span className="text-xs font-semibold text-[var(--ls-text)]">{finding.category}</span><SeverityLabel severity={finding.severity} /></span><span className="mt-2 block line-clamp-2 text-xs leading-5 text-[var(--ls-text-secondary)]">{finding.body}</span><span className="mt-2 block truncate font-mono text-[10px] text-[var(--ls-text-tertiary)]">{finding.path}:{finding.start_line}</span></Link>) : <div className="p-4 text-xs leading-5 text-[var(--ls-text-secondary)]">No findings match these filters. <Link className="font-medium text-[var(--ls-accent)] hover:underline" href={findingsURL(org, evidence.run.id, { severity: "all", status: "all", category: "", file: "", query: "" })}>Clear filters</Link>.</div>}</div></section>
      <div className="min-w-0">{selected ? <FindingDetail evidence={evidence} finding={selected} focused={focusedFinding === selected.id} org={org} outsideFilters={selectedOutsideFilters} /> : <EmptyFindings title="Select a finding" detail="Choose a result from the list to inspect its retained evidence." />}</div>
    </div>}
  </div>;
}

function FindingDetail({ evidence, finding, focused, org, outsideFilters }: { evidence: ReviewEvidence; finding: ReviewFindingEvidence; focused: boolean; org: string; outsideFilters: boolean }) {
  const target = providerFileTarget({ ...evidence.run, path: finding.path, start_line: finding.start_line, end_line: finding.end_line, head_sha: evidence.run.head_sha });
  const prompt = findingPrompt(evidence.run.repository, evidence.run.head_sha, finding);
  return <article id={`finding-${finding.id}`} tabIndex={-1} className="scroll-mt-24 overflow-hidden rounded-[12px] border border-[var(--ls-line-strong)]">
    {focused ? <FindingFocus findingId={finding.id} /> : null}
    {outsideFilters ? <p className="border-b border-[var(--ls-line)] bg-amber-500/[0.06] px-4 py-2 text-xs text-[var(--ls-warning-text)]">This deep-linked finding is outside the current filters; it remains visible so the link does not hide evidence.</p> : null}
    <div className="border-b border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><div className="flex flex-wrap items-center gap-2"><SeverityLabel severity={finding.severity} /><span className="rounded-full bg-[var(--ls-surface)] px-2 py-1 text-xs text-[var(--ls-text-secondary)]">{finding.category}</span>{finding.disposition ? <span className="rounded-full bg-emerald-500/10 px-2 py-1 text-xs capitalize text-[var(--ls-success-text)]">{finding.disposition.replaceAll("_", " ")}</span> : null}</div><h3 className="mt-3 break-all font-mono text-sm font-semibold text-[var(--ls-text)]">{finding.path}:{finding.start_line}{finding.end_line > finding.start_line ? `–${finding.end_line}` : ""}</h3>{target ? <a className="luminous-focus mt-2 inline-flex items-center gap-1 text-xs font-medium text-[var(--ls-accent)] hover:underline" href={target.url} rel="noreferrer" target="_blank"><FileCode2 className="size-3.5" />Open exact source<ArrowUpRight className="size-3" /></a> : <p className="mt-2 text-xs text-[var(--ls-warning-text)]">An exact source link is unavailable for this retained revision.</p>}</div>
    <FindingPublicationCard evidence={evidence} finding={finding} org={org} />
    <div className="space-y-4 p-4"><section><h4 className="text-xs font-semibold uppercase tracking-[0.1em] text-[var(--ls-text-tertiary)]">Why it matters</h4><FindingNarrative className="mt-2 text-sm leading-6 text-[var(--ls-text)]" text={finding.body} /></section>{finding.suggestion ? <section className="rounded-[10px] border-l-2 border-[var(--ls-accent)] bg-[var(--ls-surface-muted)] p-3"><h4 className="text-xs font-semibold text-[var(--ls-text)]">Recommended direction</h4><FindingNarrative className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]" text={finding.suggestion} variant="suggestion" /></section> : null}<FindingCodeEvidence finding={finding} />{finding.rule_attributions.length ? <section className="rounded-[10px] border border-[var(--ls-line)] p-3"><h4 className="text-xs font-semibold text-[var(--ls-text)]">Policy provenance</h4><div className="mt-2 flex flex-wrap gap-2">{finding.rule_attributions.map((attribution) => <Link className="luminous-focus rounded-full bg-[var(--ls-accent-soft)] px-2 py-1 font-mono text-[10px] text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/rules/${encodeURIComponent(attribution.rule_set_id)}?tab=overview`} key={`${attribution.rule_key}:${attribution.rule_version_id}`}>{attribution.rule_key} · v{attribution.version}</Link>)}</div></section> : null}<div className="flex flex-wrap gap-3 text-[11px] text-[var(--ls-text-tertiary)]"><span>👍 {finding.useful_feedback_count}</span><span>👎 {finding.false_positive_feedback_count}</span><span className="font-mono">{finding.fingerprint.slice(0, 12)}</span></div></div>
    <details className="border-t border-[var(--ls-line)] bg-[var(--ls-surface-muted)]"><summary className="luminous-focus flex cursor-pointer items-center gap-2 p-4 text-xs font-medium text-[var(--ls-text)]"><Braces className="size-4 text-[var(--ls-accent)]" />Prompt for LLM<span className="ml-auto text-[var(--ls-text-tertiary)]">Copyable</span></summary><div className="border-t border-[var(--ls-line)] p-4"><div className="flex justify-end"><CopyEvidenceButton value={prompt} /></div><pre className="mt-3 overflow-x-auto whitespace-pre-wrap font-mono text-xs leading-6 text-[var(--ls-text-secondary)]">{prompt}</pre></div></details>
  </article>;
}

function FindingCodeEvidence({ finding }: { finding: ReviewFindingEvidence }) {
  const excerpt = finding.code_excerpt;
  const patch = finding.proposed_patch;
  const numbered = excerpt?.split("\n").map((line, index) => `${String((finding.code_excerpt_start_line ?? 0) + index).padStart(5)}  ${line}`).join("\n");
  return <section className="min-w-0 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3">
    <h4 className="flex items-center gap-2 text-xs font-semibold text-[var(--ls-text)]"><ShieldAlert className="size-4 text-[var(--ls-warning-text)]" />Code context and patch</h4>
    {excerpt ? <div className="mt-3"><p className="text-xs text-[var(--ls-text-secondary)]">Bounded excerpt from the exact reviewed head · lines {finding.code_excerpt_start_line}–{(finding.code_excerpt_start_line ?? 0) + excerpt.split("\n").length - 1}</p><pre aria-label="Exact-head source excerpt with line numbers" className="mt-2 max-h-72 overflow-auto rounded-[9px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-3 font-mono text-[11px] leading-5 text-[var(--ls-text)]">{numbered}</pre></div> : <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">No bounded source excerpt was retained for this finding. Open the exact provider file to inspect its code.</p>}
    {patch ? <details className="mt-3 rounded-[9px] border border-[var(--ls-line)] bg-[var(--ls-surface)]"><summary className="luminous-focus cursor-pointer p-3 text-xs font-medium text-[var(--ls-text)]">Candidate patch · structurally checked against the reviewed head</summary><div className="border-t border-[var(--ls-line)] p-3"><p className="text-xs leading-5 text-[var(--ls-text-secondary)]">This patch passed Git apply-check for the reviewed checkout. It has not been compiled, tested, approved or applied to the repository.</p><div className="mt-2 flex justify-end"><CopyEvidenceButton value={patch} /></div><pre aria-label="Candidate unified patch" className="mt-2 max-h-80 overflow-auto font-mono text-[11px] leading-5 text-[var(--ls-text)]">{patch}</pre></div></details> : <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">No candidate patch was validated for this finding. The recommendation above is guidance, not an applyable diff.</p>}
  </section>;
}

function SeverityLabel({ severity }: { severity: ReviewFindingEvidence["severity"] }) { const tone = severity === "critical" ? "text-[var(--ls-critical-text)]" : severity === "high" || severity === "medium" ? "text-[var(--ls-warning-text)]" : "text-[var(--ls-success-text)]"; return <span className={cn("inline-flex items-center gap-1 text-[10px] font-semibold capitalize", tone)}><TriangleAlert className="size-3.5" />{severity}</span>; }

function FindingPublicationCard({ evidence, finding, org }: { evidence: ReviewEvidence; finding: ReviewFindingEvidence; org: string }) {
  const state = findingPublicationState(finding, evidence.receipts);
  const copy: Record<FindingPublicationState, { title: string; detail: string }> = {
    published: { title: "Inline publication confirmed", detail: "A matching marker and timestamped receipt were retained for this exact run. The provider remains the source of truth for current comment visibility." },
    failed: { title: "Inline publication failed", detail: "A matching receipt records a failed provider attempt. The finding remains in this run, but its inline comment is not confirmed at the provider." },
    pending: { title: "Inline publication pending", detail: "A matching receipt exists without a confirmed publication timestamp. Do not assume the provider accepted the comment." },
    unconfirmed: { title: "Inline publication unconfirmed", detail: "No matching inline receipt was retained. The finding may be present only in the summary or still awaiting publication." },
    untracked: { title: "Inline publication not tracked", detail: "This finding has no retained provider marker, so an inline provider comment cannot be matched to it." },
  };
  const tone = state === "published" ? "text-[var(--ls-success-text)]" : state === "failed" ? "text-[var(--ls-critical-text)]" : "text-[var(--ls-warning-text)]";
  return <div className="border-b border-[var(--ls-line)] px-4 py-3 text-xs leading-5">
    <p className={cn("font-semibold", tone)}>{copy[state].title}</p>
    <p className="mt-1 text-[var(--ls-text-secondary)]">{copy[state].detail} <Link className="luminous-focus font-medium text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(evidence.run.id)}?tab=checks&view=open-review`}>View publication receipts</Link>.</p>
  </div>;
}
function EmptyFindings({ title, detail }: { title: string; detail: string }) { return <div className="grid min-h-64 place-items-center rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-6 text-center"><div><CircleAlert className="mx-auto size-5 text-[var(--ls-text-tertiary)]" /><h3 className="mt-2 text-sm font-semibold text-[var(--ls-text)]">{title}</h3><p className="mt-2 max-w-sm text-xs leading-5 text-[var(--ls-text-secondary)]">{detail}</p></div></div>; }
