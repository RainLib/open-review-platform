"use client";

import { useWorkflowStatus, useWorkflowText } from "@/components/console/ui-language-context";

import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  FlaskConical,
  GitCompareArrows,
  History,
  LoaderCircle,
  RefreshCw,
  ShieldCheck,
  PlayCircle,
} from "lucide-react";

import type { ReviewRun, RuleImpactPreview, RuleSet, RuleTestRun } from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

const inputClassName =
  "luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3.5 text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)] disabled:cursor-not-allowed disabled:opacity-45";

function KeyList({ label, keys, tone }: { label: string; keys: string[]; tone: string }) {
  const t = useWorkflowText();
  return (
    <div className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3">
      <div className="flex items-center justify-between text-xs text-[var(--ls-text-secondary)]">
        {label} <span className={cn("font-semibold", tone)}>{keys.length}</span>
      </div>
      {keys.length ? (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {keys.map((key) => (
            <code className="rounded-md bg-[var(--ls-surface)] px-1.5 py-1 text-[11px] text-[var(--ls-text-secondary)]" key={key}>
              {key}
            </code>
          ))}
        </div>
      ) : (
        <p className="mt-2 text-[11px] text-[var(--ls-text-tertiary)]">{t("None")}</p>
      )}
    </div>
  );
}

export function RuleTestLab({
  enabled,
  org,
  ruleSets,
  runs,
}: {
  enabled: boolean;
  org: string;
  ruleSets: RuleSet[];
  runs: ReviewRun[];
}) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  const candidates = useMemo(
    () => ruleSets.filter((ruleSet) => Boolean(ruleSet.latest_version)),
    [ruleSets],
  );
  const [selectedID, setSelectedID] = useState(candidates[0]?.id ?? "");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();
  const [preview, setPreview] = useState<RuleImpactPreview>();
	const [previewProvider, setPreviewProvider] = useState<"github" | "gitlab">("github");
	const [previewAPIBaseURL, setPreviewAPIBaseURL] = useState("https://api.github.com");
  const eligibleRuns = useMemo(
    () => runs.filter((run) => run.state === "completed" || run.state === "needs_attention"),
    [runs],
  );
  const [sourceRunID, setSourceRunID] = useState(eligibleRuns[0]?.id ?? "");
  const [testPrecedence, setTestPrecedence] = useState(100);
  const [testRuns, setTestRuns] = useState<RuleTestRun[]>([]);
  const [testRunsLoaded, setTestRunsLoaded] = useState(false);
  const [refreshingTestRuns, setRefreshingTestRuns] = useState(false);
  const [testPending, setTestPending] = useState(false);
  const selected = candidates.find((ruleSet) => ruleSet.id === selectedID);

  const refreshTestRuns = useCallback(async () => {
    if (!enabled) {
      setTestRunsLoaded(true);
      return;
    }
    setRefreshingTestRuns(true);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/rule-test-runs`, {
        cache: "no-store",
      });
      if (!response.ok) return;
      const payload = (await response.json()) as { test_runs?: RuleTestRun[] };
      setTestRuns(payload.test_runs ?? []);
    } finally {
      setTestRunsLoaded(true);
      setRefreshingTestRuns(false);
    }
  }, [enabled, org]);

  useEffect(() => {
    // The initial durable read is deliberately deferred one microtask. This
    // prevents a synchronous state cascade during commit while retaining the
    // manual refresh control as the only later refresh mechanism.
    let active = true;
    queueMicrotask(() => {
      if (active) void refreshTestRuns();
    });
    return () => {
      active = false;
    };
  }, [refreshTestRuns]);

  async function startIsolatedRun() {
    if (!selected?.latest_version || !sourceRunID || testPending) return;
    setTestPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/rule-sets/${encodeURIComponent(selected.id)}/versions/${selected.latest_version.version}/test-runs`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ source_run_id: sourceRunID, precedence: testPrecedence }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as RuleTestRun | { error?: string };
      if (!response.ok) {
        throw new Error("error" in payload && payload.error ? payload.error : t("The isolated run could not be created."));
      }
      await refreshTestRuns();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : t("The isolated run failed to start."));
    } finally {
      setTestPending(false);
    }
  }

  async function runPreview(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selected?.latest_version || pending) return;
    const form = new FormData(event.currentTarget);
    setPending(true);
    setMessage(undefined);
    setPreview(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/rule-sets/${encodeURIComponent(selected.id)}/versions/${selected.latest_version.version}/impact-preview`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            repository: form.get("repository"),
			provider: previewProvider,
			api_base_url: previewAPIBaseURL,
            target_branch: form.get("target_branch"),
            precedence: Number(form.get("precedence")),
          }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as
        | RuleImpactPreview
        | { error?: string };
      if (!response.ok) {
        throw new Error("error" in payload && payload.error ? payload.error : t("The preview could not be generated."));
      }
      setPreview(payload as RuleImpactPreview);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : t("The preview failed."));
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="space-y-5">
      <form className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6" onSubmit={runPreview}>
        <div className="flex items-start gap-3">
          <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
            <FlaskConical className="size-4" />
          </span>
          <div>
            <div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">{t("Preview an exact policy version")}</h2><HelpHint label={t("Preview an exact policy version")}>{t(" Compile against one exact provider endpoint and current active bindings without creating a review run or writing to GitHub or GitLab. ")}</HelpHint></div>
          </div>
        </div>
        <div className="mt-5 grid gap-4 lg:grid-cols-4">
          <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
            {t(" Policy version ")}<select className={inputClassName} disabled={!enabled || pending || !candidates.length} onChange={(event) => setSelectedID(event.target.value)} required value={selectedID}>
              <option value="">{t("Select policy")}</option>
              {candidates.map((ruleSet) => (
                <option key={ruleSet.id} value={ruleSet.id}>
                  {ruleSet.name} · v{ruleSet.latest_version?.version}
                </option>
              ))}
            </select>
          </label>
          <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
            {t(" Repository ")}<input className={inputClassName} disabled={!enabled || pending} name="repository" placeholder="RainLib/open-review-platform" required />
          </label>
		  <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
			{t(" Provider ")}<select className={inputClassName} disabled={!enabled || pending} onChange={(event) => { const provider = event.target.value as "github" | "gitlab"; setPreviewProvider(provider); setPreviewAPIBaseURL(provider === "github" ? "https://api.github.com" : "https://gitlab.com/api/v4"); }} value={previewProvider}><option value="github">GitHub</option><option value="gitlab">GitLab</option></select>
		  </label>
		  <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
			{t(" Provider API URL ")}<input className={inputClassName} disabled={!enabled || pending} onChange={(event) => setPreviewAPIBaseURL(event.target.value)} placeholder="https://gitlab.example.com/api/v4" value={previewAPIBaseURL} />
		  </label>
          <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
            {t(" Target branch ")}<input className={inputClassName} defaultValue="main" disabled={!enabled || pending} name="target_branch" required />
          </label>
          <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
            {t(" Candidate precedence ")}<input className={inputClassName} defaultValue="100" disabled={!enabled || pending} max={10000} min={0} name="precedence" required type="number" />
          </label>
        </div>
        <div className="mt-5 flex flex-col justify-between gap-3 border-t border-[var(--ls-line)] pt-4 sm:flex-row sm:items-center">
          <p className="text-xs leading-5 text-[var(--ls-text-tertiary)]">
            {t(" Results are evidence, not a release authorization. Approval and publication remain separate gates. ")}</p>
          <button className="luminous-focus inline-flex h-9 items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-xs font-semibold text-white shadow-[var(--ls-shadow-control)] hover:bg-[var(--ls-accent-hover)] disabled:cursor-not-allowed disabled:opacity-45" disabled={!enabled || pending || !selected} type="submit">
            {pending ? <LoaderCircle className="size-4 animate-spin" /> : <GitCompareArrows className="size-4" />}
            {t(" Run static preview ")}</button>
        </div>
      </form>

      <section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
        <div className="flex flex-col justify-between gap-4 lg:flex-row lg:items-start">
          <div className="flex items-start gap-3">
            <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><PlayCircle className="size-4" /></span>
            <div>
              <div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">{t("Run an isolated historical replay")}</h2><HelpHint label={t("Run an isolated historical replay")}>{t(" Re-run OCR on an exact completed PR revision with this immutable policy snapshot. The isolated worker has no publisher, check, or merge-gate capability. ")}</HelpHint></div>
            </div>
          </div>
          <span className="w-fit rounded-full bg-[color:color-mix(in_srgb,var(--ls-success)_12%,transparent)] px-2.5 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] text-[var(--ls-success)]">{t("Provider writes disabled")}</span>
        </div>
        <div className="mt-5 grid gap-3 lg:grid-cols-[1fr_180px_auto] lg:items-end">
          <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
            {t(" Historical source revision ")}<select className={inputClassName} disabled={!enabled || testPending || !eligibleRuns.length} onChange={(event) => setSourceRunID(event.target.value)} value={sourceRunID}>
              <option value="">{t("Select a completed review")}</option>
              {eligibleRuns.map((run) => <option key={run.id} value={run.id}>{run.repository} #{run.review_number} · {run.head_sha.slice(0, 10)} · {status(run.state)}</option>)}
            </select>
          </label>
          <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
            {t(" Candidate precedence ")}<input className={inputClassName} disabled={!enabled || testPending} max={10000} min={0} onChange={(event) => setTestPrecedence(Number(event.target.value))} type="number" value={testPrecedence} />
          </label>
          <button className="luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] border border-[color:color-mix(in_srgb,var(--ls-accent)_28%,transparent)] bg-[var(--ls-accent-soft)] px-4 text-xs font-semibold text-[var(--ls-accent)] hover:bg-[color:color-mix(in_srgb,var(--ls-accent)_16%,var(--ls-surface))] disabled:cursor-not-allowed disabled:opacity-45" disabled={!enabled || !selected || !sourceRunID || testPending} onClick={startIsolatedRun} type="button">
            {testPending ? <LoaderCircle className="size-4 animate-spin" /> : <PlayCircle className="size-4" />} {t(" Run isolated OCR ")}</button>
        </div>
        {!eligibleRuns.length ? <p className="mt-3 text-xs text-[var(--ls-warning)]">{t("No completed or needs-attention review is available as an immutable replay source.")}</p> : null}
      </section>

      <section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
          <div className="flex items-center justify-between gap-3"><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">{t("Isolated run history")}</h2><HelpHint label={t("Isolated run history")}>{t("Active jobs continue after navigation. Refresh manually to read their latest durable state.")}</HelpHint></div></div><button className="luminous-focus inline-flex h-8 shrink-0 items-center gap-1.5 rounded-[8px] border border-[var(--ls-line-strong)] px-2.5 text-xs text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]" disabled={!enabled || refreshingTestRuns} onClick={() => void refreshTestRuns()} type="button">{refreshingTestRuns ? <LoaderCircle className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}{t("Refresh")}</button></div>
          {!testRunsLoaded ? <p className="mt-4 text-xs text-[var(--ls-text-tertiary)]">{t("Loading durable test receipts…")}</p> : null}
          {testRuns.length ? (
          <div className="mt-4 space-y-2">
            {testRuns.map((run) => (
              <details className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)]" key={run.id}>
                <summary className="flex cursor-pointer list-none items-center justify-between gap-4 px-3.5 py-3 text-xs">
                  <span className="min-w-0"><strong className="text-[var(--ls-text)]">{run.rule_set_name} v{run.rule_version}</strong><span className="ml-2 text-[var(--ls-text-tertiary)]">{run.repository} #{run.review_number}</span></span>
                  <span className={cn("shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase", run.state === "completed" ? "bg-[color:color-mix(in_srgb,var(--ls-success)_12%,transparent)] text-[var(--ls-success)]" : run.state === "failed" ? "bg-red-500/10 text-[var(--ls-critical-text)]" : "bg-[color:color-mix(in_srgb,var(--ls-warning)_12%,transparent)] text-[var(--ls-warning)]")}>{status(run.state)}</span>
                </summary>
                <div className="border-t border-[var(--ls-line)] px-3.5 py-3 text-xs text-[var(--ls-text-secondary)]">
                  <div className="grid gap-2 sm:grid-cols-4"><span>{run.finding_count} {t(" findings")}</span><span>{run.selected_path_count} {t(" selected paths")}</span><span>{run.deferred_path_count} {t(" deferred")}</span><span>{run.duration_ms ? `${(run.duration_ms / 1000).toFixed(1)}s` : t("Pending")}</span></div>
                  {run.error_message ? <p className="mt-3 text-[var(--ls-critical-text)]">{run.error_message}</p> : null}
                  {run.findings.length ? <ul className="mt-3 space-y-2">{run.findings.map((finding, index) => <li className="rounded-[8px] bg-[var(--ls-surface)] p-2.5" key={`${finding.path}:${finding.start_line}:${index}`}><strong className="text-[var(--ls-text-secondary)]">{finding.severity.toUpperCase()} · {finding.category}</strong><span className="ml-2 font-mono text-[var(--ls-accent)]">{finding.path}:{finding.start_line}</span><p className="mt-1 line-clamp-3 leading-5">{finding.body}</p></li>)}</ul> : null}
                  <p className="mt-3 break-all font-mono text-[10px] text-[var(--ls-text-tertiary)]">{t("snapshot ")}{run.snapshot_sha256}</p>
                </div>
              </details>
            ))}
          </div>
          ) : testRunsLoaded ? <p className="mt-4 text-xs text-[var(--ls-text-tertiary)]">{t("No isolated replay has been requested for this workspace.")}</p> : null}
        </section>

      {message ? (
        <div className="flex items-start gap-2 rounded-[12px] border border-red-500/25 bg-red-500/[0.07] px-4 py-3 text-sm text-[var(--ls-critical-text)]">
          <AlertTriangle className="mt-0.5 size-4 shrink-0" /> {message}
        </div>
      ) : null}

      {preview ? (
        <section className="space-y-4" aria-live="polite">
          <div className={cn("flex items-start gap-3 rounded-[16px] border p-4", preview.valid ? "border-[color:color-mix(in_srgb,var(--ls-success)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-success)_8%,var(--ls-surface))]" : "border-red-500/25 bg-red-500/[0.07]")}>
            {preview.valid ? <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-[var(--ls-success)]" /> : <AlertTriangle className="mt-0.5 size-5 shrink-0 text-[var(--ls-critical-text)]" />}
            <div>
              <h2 className="text-sm font-semibold text-[var(--ls-text)]">{preview.valid ? t("Static policy composition is valid") : t("Policy composition conflict")}</h2>
              <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">
                {preview.valid ? `${preview.rule_set_name} v${preview.version} can compose with ${preview.matched_bindings} matching active binding(s).` : preview.conflict}
              </p>
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {[
              [t("Baseline rules"), preview.baseline_rule_count],
              [t("Candidate rules"), preview.candidate_rule_count],
              [t("Mandatory"), preview.candidate_counts.mandatory],
              [t("High / critical"), preview.candidate_counts.high + preview.candidate_counts.critical],
            ].map(([label, value]) => (
              <div className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 shadow-[var(--ls-shadow-control)]" key={String(label)}>
                <p className="text-xs text-[var(--ls-text-secondary)]">{label}</p>
                <p className="mt-2 text-2xl font-semibold text-[var(--ls-text)]">{value}</p>
              </div>
            ))}
          </div>

          <div className="grid gap-4 xl:grid-cols-[1.25fr_0.75fr]">
            <div className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
              <div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><GitCompareArrows className="size-4 text-[var(--ls-accent)]" /> {t(" Effective rule delta")}</div>
              <div className="mt-4 grid gap-3 sm:grid-cols-3">
                <KeyList keys={preview.added_rule_keys} label={t("Added")} tone="text-[var(--ls-success)]" />
                <KeyList keys={preview.changed_rule_keys} label={t("Changed")} tone="text-[var(--ls-warning)]" />
                <KeyList keys={preview.removed_rule_keys} label={t("Removed")} tone="text-[var(--ls-critical-text)]" />
              </div>
              <details className="mt-4 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)]">
                <summary className="cursor-pointer list-none px-3.5 py-2.5 text-xs font-medium text-[var(--ls-text-secondary)]">{t("Snapshot provenance")}</summary>
                <dl className="grid gap-3 border-t border-[var(--ls-line)] px-3.5 py-3 text-xs sm:grid-cols-2">
                  <div><dt className="text-[var(--ls-text-tertiary)]">{t("Baseline SHA")}</dt><dd className="mt-1 break-all font-mono text-[var(--ls-text-secondary)]">{preview.baseline_sha256}</dd></div>
                  <div><dt className="text-[var(--ls-text-tertiary)]">{t("Candidate SHA")}</dt><dd className="mt-1 break-all font-mono text-[var(--ls-text-secondary)]">{preview.candidate_sha256 ?? t("Not generated because composition failed")}</dd></div>
                  <div><dt className="text-[var(--ls-text-tertiary)]">{t("Version content SHA")}</dt><dd className="mt-1 break-all font-mono text-[var(--ls-text-secondary)]">{preview.content_sha256}</dd></div>
				  <div><dt className="text-[var(--ls-text-tertiary)]">{t("Scope")}</dt><dd className="mt-1 text-[var(--ls-text-secondary)]">{preview.provider}@{providerHost(preview.api_base_url)} · {preview.repository} → {preview.target_branch}</dd></div>
                </dl>
              </details>
            </div>

            <div className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
              <div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><History className="size-4 text-[var(--ls-accent)]" /> {t(" Historical sample envelope")}</div>
              <dl className="mt-4 grid grid-cols-3 gap-2 text-center">
                <div className="rounded-[10px] bg-[var(--ls-surface-muted)] p-3"><dt className="text-[11px] text-[var(--ls-text-tertiary)]">{t("Runs")}</dt><dd className="mt-1 text-lg font-semibold text-[var(--ls-text)]">{preview.historical_sample.run_count}</dd></div>
                <div className="rounded-[10px] bg-[var(--ls-surface-muted)] p-3"><dt className="text-[11px] text-[var(--ls-text-tertiary)]">{t("Findings")}</dt><dd className="mt-1 text-lg font-semibold text-[var(--ls-text)]">{preview.historical_sample.finding_count}</dd></div>
                <div className="rounded-[10px] bg-[var(--ls-surface-muted)] p-3"><dt className="text-[11px] text-[var(--ls-text-tertiary)]">{t("High risk")}</dt><dd className="mt-1 text-lg font-semibold text-[var(--ls-text)]">{preview.historical_sample.high_risk_finding_count}</dd></div>
              </dl>
              <div className="mt-4 flex items-start gap-2 rounded-[10px] border border-[color:color-mix(in_srgb,var(--ls-warning)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,var(--ls-surface))] p-3 text-xs leading-5 text-[var(--ls-warning)]">
                <ShieldCheck className="mt-0.5 size-4 shrink-0" />
                {t(" This is the available replay sample, not a replay result. No model call or provider comment occurred. ")}</div>
            </div>
          </div>

          <details className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
            <summary className="cursor-pointer list-none px-5 py-4 text-sm font-medium text-[var(--ls-text-secondary)]">{t("Uncertainty and missing evidence (")}{preview.uncertainty.length})</summary>
            <ul className="space-y-2 border-t border-[var(--ls-line)] px-5 py-4 text-xs leading-5 text-[var(--ls-text-secondary)]">
              {preview.uncertainty.map((item) => <li key={item}>• {item}</li>)}
            </ul>
          </details>
        </section>
      ) : null}
    </div>
  );
}

function providerHost(value: string) {
  try { return new URL(value).host; } catch { return value || "unqualified"; }
}
