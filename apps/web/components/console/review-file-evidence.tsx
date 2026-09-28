import Link from "next/link";
import {
  ArrowUpRight,
  CircleAlert,
  FileCode2,
  GitBranch,
  ScanSearch,
  ShieldAlert,
} from "lucide-react";

import { TabStateRouter } from "@/components/console/tab-state-router";
import type { ReviewEvidence } from "@/lib/control-api";
import {
  dependencyManifestRows,
  filterReviewFileRows,
  prioritizedReviewFileRows,
  reviewFileSummary,
  type ReviewFileRow,
  type ReviewFileView,
} from "@/lib/review-file-analysis";
import { providerFileTarget, providerReviewDiffTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";

type Props = {
  evidence: ReviewEvidence;
  files: ReviewFileRow[];
  org: string;
  view: ReviewFileView;
  query?: string;
  scope?: string;
  change?: string;
};

const VIEWS: { id: ReviewFileView; label: string }[] = [
  { id: "files", label: "Files" },
  { id: "dependencies", label: "Dependencies" },
  { id: "coverage", label: "Coverage" },
  { id: "blast-radius", label: "Blast radius" },
];

function viewURL(org: string, runID: string, view: ReviewFileView) {
  const params = new URLSearchParams({ tab: "files", view });
  return `/${encodeURIComponent(org)}/reviews/${encodeURIComponent(runID)}?${params.toString()}`;
}

export function ReviewFileEvidence({ evidence, files, org, view, query = "", scope = "all", change = "all" }: Props) {
  const summary = reviewFileSummary(evidence, files);
  const provider = providerReviewDiffTarget(evidence.run);
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-[var(--ls-text)]">Changed-file evidence</h2>
          <p className="mt-1 max-w-2xl text-sm leading-6 text-[var(--ls-text-secondary)]">
            Frozen to this review’s admitted head. File selection and static signals are retained; dependency reachability and test coverage are shown only when measured.
          </p>
        </div>
        {provider ? <a className="luminous-focus inline-flex h-10 items-center gap-1.5 rounded-[9px] border border-[var(--ls-line)] px-3 text-xs font-medium text-[var(--ls-accent)] hover:bg-[var(--ls-accent-soft)]" href={provider.url} rel="noreferrer" target="_blank">Current diff in {provider.label}<ArrowUpRight className="size-3.5" /></a> : null}
      </div>
      {provider ? <p className="text-xs leading-5 text-[var(--ls-text-tertiary)]">The provider link may show a newer PR/MR revision. Evidence below remains frozen to this run.</p> : null}
      <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="Changed-file evidence views">
        {VIEWS.map(({ id, label }) => <Link
          aria-current={view === id ? "page" : undefined}
          aria-selected={view === id}
          className={cn("luminous-focus relative inline-flex h-11 shrink-0 items-center gap-2 rounded-t-[9px] px-3 text-sm font-medium", view === id ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")}
          href={viewURL(org, evidence.run.id, id)}
          key={id}
          role="tab"
          tabIndex={view === id ? 0 : -1}
        >{label}{id === "files" ? <span className="rounded-full bg-[var(--ls-surface-muted)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)]">{files.length}</span> : null}{view === id ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>)}
      </TabStateRouter>
      {view === "files" ? <FilesView evidence={evidence} files={files} org={org} query={query} scope={scope} change={change} unknownDeferred={summary.unknownDeferred} /> : null}
      {view === "dependencies" ? <DependenciesView evidence={evidence} files={files} /> : null}
      {view === "coverage" ? <CoverageView evidence={evidence} selected={summary.selected} deferred={summary.deferred} findingOnly={summary.findingOnly} /> : null}
      {view === "blast-radius" ? <BlastRadiusView evidence={evidence} files={files} /> : null}
    </div>
  );
}

function EvidenceNotice({ title, children }: { title: string; children: React.ReactNode }) {
  return <div className="flex gap-3 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-text-tertiary)]" /><div><p className="text-sm font-semibold text-[var(--ls-text)]">{title}</p><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{children}</p></div></div>;
}

function FilesView({ evidence, files, org, query, scope, change, unknownDeferred }: Omit<Props, "view"> & { unknownDeferred: number }) {
  const allowedScope = scope === "selected" || scope === "deferred" || scope === "finding-only" ? scope : "all";
  const allowedChange = ["added", "modified", "deleted", "renamed", "copied", "type_changed"].includes(change ?? "") ? change! : "all";
  const term = (query ?? "").slice(0, 80);
  const filtered = filterReviewFileRows(files, term, allowedScope, allowedChange);
  const action = `/${encodeURIComponent(org)}/reviews/${encodeURIComponent(evidence.run.id)}`;
  return <div className="space-y-4">
    <form action={action} className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_150px_150px_auto]" method="get">
      <input name="tab" type="hidden" value="files" /><input name="view" type="hidden" value="files" />
      <label className="sr-only" htmlFor="review-file-search">Search file paths</label>
      <input className="luminous-focus h-10 min-w-0 rounded-[9px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]" defaultValue={term} id="review-file-search" maxLength={80} name="q" placeholder="Search file paths" type="search" />
      <label className="sr-only" htmlFor="review-file-scope">Analysis scope</label>
      <select className="luminous-focus h-10 rounded-[9px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" defaultValue={allowedScope} id="review-file-scope" name="scope"><option value="all">All scopes</option><option value="selected">Selected</option><option value="deferred">Deferred</option><option value="finding-only">Finding only</option></select>
      <label className="sr-only" htmlFor="review-file-change">Change type</label>
      <select className="luminous-focus h-10 rounded-[9px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" defaultValue={allowedChange} id="review-file-change" name="change"><option value="all">All changes</option>{["added", "modified", "deleted", "renamed", "copied", "type_changed"].map((value) => <option key={value} value={value}>{value.replaceAll("_", " ")}</option>)}</select>
      <button className="luminous-focus h-10 rounded-[9px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white" type="submit">Filter</button>
    </form>
    <p className="text-xs text-[var(--ls-text-secondary)]">Showing {filtered.length} of {files.length} retained paths. A deferred path is not evidence of model analysis.</p>
    {filtered.length ? <div className="overflow-x-auto rounded-[12px] border border-[var(--ls-line)]"><table className="w-full min-w-[660px] text-left text-xs"><thead className="bg-[var(--ls-surface-muted)] text-[var(--ls-text-tertiary)]"><tr><th className="px-4 py-3 font-medium" scope="col">File</th><th className="px-3 py-3 font-medium" scope="col">Change</th><th className="px-3 py-3 font-medium" scope="col">Scope</th><th className="px-3 py-3 font-medium" scope="col">Static priority</th><th className="px-4 py-3 text-right font-medium" scope="col">Findings</th></tr></thead><tbody className="divide-y divide-[var(--ls-line)]">{filtered.slice(0, 100).map((file) => <FileTableRow evidence={evidence} file={file} key={file.path} />)}</tbody></table>{filtered.length > 100 ? <details className="border-t border-[var(--ls-line)]"><summary className="luminous-focus cursor-pointer px-4 py-3 text-sm font-medium text-[var(--ls-accent)]">Show {filtered.length - 100} more files</summary><table className="w-full min-w-[660px] text-left text-xs"><caption className="sr-only">Additional retained files</caption><thead className="sr-only"><tr><th scope="col">File</th><th scope="col">Change</th><th scope="col">Scope</th><th scope="col">Static priority</th><th scope="col">Findings</th></tr></thead><tbody className="divide-y divide-[var(--ls-line)]">{filtered.slice(100).map((file) => <FileTableRow evidence={evidence} file={file} key={file.path} />)}</tbody></table></details> : null}</div> : <EvidenceNotice title={files.length ? "No matching files" : "No retained file paths"}>{files.length ? "Adjust the search or filters to inspect the retained scope." : evidence.execution_plan ? "This run did not retain per-file scope; no file selection is inferred." : "This legacy run has no immutable file-level scope or finding paths."}</EvidenceNotice>}
    {unknownDeferred > 0 ? <EvidenceNotice title="Deferred paths not retained">{unknownDeferred} deferred path{unknownDeferred === 1 ? " was" : "s were"} counted in the legacy plan without identities or reasons. They are not silently presented as analyzed files.</EvidenceNotice> : null}
  </div>;
}

function FileEvidenceLink({ evidence, file }: { evidence: ReviewEvidence; file: ReviewFileRow }) {
  const first = file.findings[0];
  const target = file.changeType === "deleted"
    ? providerReviewDiffTarget(evidence.run)
    : providerFileTarget({ ...evidence.run, path: file.path, head_sha: evidence.run.head_sha, start_line: first?.start_line, end_line: first?.end_line });
  if (!target) return <span className="break-all font-mono text-xs text-[var(--ls-text)]">{file.path}</span>;
  return <a className="luminous-focus inline-flex max-w-full items-center gap-1.5 rounded font-mono text-xs text-[var(--ls-accent)] hover:underline" href={target.url} rel="noreferrer" target="_blank" title={file.changeType === "deleted" ? "Current provider diff; may differ from this run" : `Exact reviewed head: ${evidence.run.head_sha}`}><FileCode2 className="size-3.5 shrink-0" /><span className="min-w-0 break-all">{file.path}</span><ArrowUpRight className="size-3 shrink-0" /></a>;
}

function FileTableRow({ evidence, file }: { evidence: ReviewEvidence; file: ReviewFileRow }) {
  return <tr className="align-top text-[var(--ls-text-secondary)]"><td className="px-4 py-3"><FileEvidenceLink evidence={evidence} file={file} />{file.previousPath ? <p className="mt-1 break-all font-mono text-[10px] text-[var(--ls-text-tertiary)]">From {file.previousPath}</p> : null}{file.reasons.length ? <p className="mt-1 max-w-lg text-[11px] leading-5 text-[var(--ls-text-tertiary)]">{file.reasons.join(" · ")}</p> : null}</td><td className="px-3 py-3 capitalize">{file.changeType?.replaceAll("_", " ") ?? "Unknown"}{file.statsKnown ? <span className="mt-1 block font-mono text-[11px] text-[var(--ls-text-tertiary)]">{file.binary ? "Binary" : `+${file.additions ?? 0} / −${file.deletions ?? 0}`}</span> : null}</td><td className="px-3 py-3 capitalize">{file.scope.replaceAll("-", " ")}</td><td className="px-3 py-3 font-mono">{file.score === undefined ? "Not retained" : `${file.score}/100`}</td><td className="px-4 py-3 text-right font-medium">{file.findings.length}</td></tr>;
}

function DependenciesView({ evidence, files }: Pick<Props, "evidence" | "files">) {
  const manifests = dependencyManifestRows(files);
  return <div className="space-y-4"><EvidenceNotice title="Dependency graph not measured">The review retained filenames and static priority signals, not resolved package versions, vulnerable dependencies, import edges, or runtime reachability. A changed manifest is a review lead, not proof of impact.</EvidenceNotice><section><h3 className="text-sm font-semibold text-[var(--ls-text)]">Retained manifest and lockfile paths <span className="font-normal text-[var(--ls-text-tertiary)]">({manifests.length})</span></h3>{manifests.length ? <ul className="mt-3 divide-y divide-[var(--ls-line)] rounded-[12px] border border-[var(--ls-line)]">{manifests.map((file) => <li className="flex flex-wrap items-center justify-between gap-2 px-4 py-3" key={file.path}><FileEvidenceLink evidence={evidence} file={file} /><span className="text-xs capitalize text-[var(--ls-text-secondary)]">{file.scope.replaceAll("-", " ")}</span></li>)}</ul> : <p className="mt-3 rounded-[12px] bg-[var(--ls-surface-muted)] p-4 text-sm text-[var(--ls-text-secondary)]">No known dependency manifest filename is present in retained file evidence. This does not establish that dependencies are unchanged.</p>}</section></div>;
}

function CoverageView({ evidence, selected, deferred, findingOnly }: { evidence: ReviewEvidence; selected: number; deferred: number; findingOnly: number }) {
  const denominator = selected + deferred;
  const share = evidence.execution_plan && denominator > 0 ? Math.round((selected / denominator) * 100) : undefined;
  return <div className="space-y-4"><EvidenceNotice title="Test coverage not supplied">No line, branch, or test-suite coverage report is retained for this run. Provider CI status, findings, and the selection share below cannot be used as substitutes.</EvidenceNotice><section className="rounded-[12px] border border-[var(--ls-line)] p-5"><div className="flex flex-wrap items-end justify-between gap-3"><div><h3 className="text-sm font-semibold text-[var(--ls-text)]">Review admission scope</h3><p className="mt-1 text-xs text-[var(--ls-text-secondary)]">Selected versus deferred file paths in the frozen execution plan.</p></div><span className="text-2xl font-semibold tracking-tight text-[var(--ls-text)]">{share === undefined ? "Not retained" : `${share}% selected`}</span></div>{share !== undefined ? <div aria-label="Share of changed file paths selected for analysis" aria-valuemax={100} aria-valuemin={0} aria-valuenow={share} className="mt-4 h-2 overflow-hidden rounded-full bg-[var(--ls-surface-muted)]" role="progressbar"><div className="h-full rounded-full bg-[var(--ls-accent)]" style={{ width: `${share}%` }} /></div> : null}<dl className="mt-4 grid gap-3 sm:grid-cols-3"><div className="rounded-[9px] bg-[var(--ls-surface-muted)] p-3"><dt className="text-xs text-[var(--ls-text-tertiary)]">Selected paths</dt><dd className="mt-1 text-lg font-semibold text-[var(--ls-text)]">{evidence.execution_plan ? selected : "—"}</dd></div><div className="rounded-[9px] bg-[var(--ls-surface-muted)] p-3"><dt className="text-xs text-[var(--ls-text-tertiary)]">Deferred paths</dt><dd className="mt-1 text-lg font-semibold text-[var(--ls-text)]">{evidence.execution_plan ? deferred : "—"}</dd></div><div className="rounded-[9px] bg-[var(--ls-surface-muted)] p-3"><dt className="text-xs text-[var(--ls-text-tertiary)]">Finding-only paths</dt><dd className="mt-1 text-lg font-semibold text-[var(--ls-text)]">{findingOnly}</dd></div></dl></section><p className="text-xs leading-5 text-[var(--ls-text-tertiary)]">Selection share is not test coverage or a percentage of code reviewed. The exact head tree remains available to the review engine even when the focused plan defers a file from model analysis.</p></div>;
}

function BlastRadiusView({ evidence, files }: Pick<Props, "evidence" | "files">) {
  const prioritized = prioritizedReviewFileRows(files);
  const signals = evidence.execution_plan?.static_impact_signals ?? [];
  return <div className="space-y-4"><EvidenceNotice title="Static blast-radius leads only">Path-priority scores and reasons help triage what was selected. No call graph, runtime dependency reachability, service impact, or deployment radius was measured.</EvidenceNotice>{signals.length ? <section className="rounded-[12px] border border-[var(--ls-line)] p-4"><h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><GitBranch className="size-4 text-[var(--ls-accent)]" />Retained static impact signals</h3><ul className="mt-3 space-y-2 text-xs leading-5 text-[var(--ls-text-secondary)]">{signals.map((signal, index) => <li className="flex gap-2" key={`${index}:${signal}`}><ScanSearch className="mt-0.5 size-3.5 shrink-0 text-[var(--ls-accent)]" /><span>{signal}</span></li>)}</ul></section> : <EvidenceNotice title="No static impact signals retained">This run does not contain source-free path impact signals. An empty signal list is not evidence of a small blast radius.</EvidenceNotice>}<section><h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><ShieldAlert className="size-4 text-[var(--ls-accent)]" />Selected files by recorded priority <span className="font-normal text-[var(--ls-text-tertiary)]">({prioritized.length})</span></h3>{prioritized.length ? <div className="mt-3 divide-y divide-[var(--ls-line)] overflow-hidden rounded-[12px] border border-[var(--ls-line)]">{prioritized.slice(0, 12).map((file) => <div className="flex flex-wrap items-start justify-between gap-3 px-4 py-3" key={file.path}><div className="min-w-0"><FileEvidenceLink evidence={evidence} file={file} />{file.reasons.length ? <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{file.reasons.join(" · ")}</p> : null}</div><span className="shrink-0 font-mono text-xs text-[var(--ls-text-secondary)]">{file.score === undefined ? "Priority not retained" : `${file.score}/100`}</span></div>)}</div> : <p className="mt-3 rounded-[12px] bg-[var(--ls-surface-muted)] p-4 text-sm text-[var(--ls-text-secondary)]">No selected file scope was retained.</p>}{prioritized.length > 12 ? <p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">Showing the first 12; switch to Files to inspect all selected paths.</p> : null}</section></div>;
}
