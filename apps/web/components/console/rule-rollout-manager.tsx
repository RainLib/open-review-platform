"use client";

import { useUiLanguage, useWorkflowStatus, useWorkflowText } from "@/components/console/ui-language-context";

import { type FormEvent, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { ArrowRight, CircleAlert, FlaskConical, LoaderCircle, Pause, Play, RotateCcw, ShieldCheck, TrendingUp } from "lucide-react";

import type { RuleBinding, RuleRollout, RuleRolloutComparison, RuleSet } from "@/lib/control-api";
import { formatTime } from "@/lib/format";
import { workflowText } from "@/lib/workflow-copy";
import type { UiLanguage } from "@/lib/ui-language";
import { HelpHint } from "@/components/console/help-hint";

const fieldClass = "luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none disabled:opacity-45";
const actionClass = "luminous-focus inline-flex h-9 items-center justify-center gap-2 rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-semibold text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)] disabled:cursor-not-allowed disabled:opacity-45";

function matchingScope(left: RuleBinding, right: RuleBinding) {
  return left.rule_version_id !== right.rule_version_id &&
    left.scope_kind === right.scope_kind && left.scope_ref === right.scope_ref &&
    left.scope_provider === right.scope_provider && left.scope_api_base_url === right.scope_api_base_url &&
    left.precedence === right.precedence && left.target_branch_glob === right.target_branch_glob &&
    left.path_include_glob === right.path_include_glob && left.path_exclude_glob === right.path_exclude_glob;
}

function bindingName(binding: RuleBinding | undefined, versions: Map<string, string>, language: UiLanguage) {
  if (!binding) return workflowText(language, "Binding unavailable");
  const scope = binding.scope_kind === "tenant" ? workflowText(language, "Workspace") : binding.scope_ref;
  return `${versions.get(binding.rule_version_id) ?? workflowText(language, "Version {version}", { version: binding.rule_version_id.slice(0, 8) })} · ${scope}`;
}

async function responseError(response: Response, fallback: string) {
  const payload = (await response.json().catch(() => ({}))) as { error?: string };
  return payload.error || fallback;
}

export function RuleRolloutManager({ bindings, enabled, org, rollouts, ruleSets }: {
  bindings: RuleBinding[]; enabled: boolean; org: string; rollouts: RuleRollout[]; ruleSets: RuleSet[];
}) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  const language = useUiLanguage();
  const router = useRouter();
  const versions = useMemo(() => new Map(ruleSets.flatMap((item) => item.latest_version ? [[item.latest_version.id, `${item.name} · v${item.latest_version.version}`] as const] : [])), [ruleSets]);
  const activeBindings = useMemo(() => bindings.filter((item) => item.state === "active"), [bindings]);
  const [baselineID, setBaselineID] = useState("");
  const baseline = bindings.find((item) => item.id === baselineID);
  const candidates = baseline ? bindings.filter((item) => item.state === "shadow" && matchingScope(baseline, item)) : [];
  const [candidateID, setCandidateID] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();

  async function create(mode: "shadow" | "canary", baselineBindingID: string, candidateBindingID: string, basisPoints = 0, failedRuns = 1, windowMinutes = 60) {
    if (!enabled || busy) return;
    setBusy(true);
    setMessage(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/rule-rollouts`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ baseline_binding_id: baselineBindingID, candidate_binding_id: candidateBindingID, mode, canary_basis_points: basisPoints, auto_rollback_failed_runs: failedRuns, auto_rollback_window_minutes: windowMinutes }),
      });
      if (!response.ok) throw new Error(await responseError(response, t("The rollout could not be created.")));
      setMessage(mode === "shadow" ? t("Shadow replay is active. The next matching completed review will produce a provider-silent comparison.") : t("Canary is active for future admissions. Existing reviews keep their frozen rule snapshots."));
      if (mode === "shadow") { setBaselineID(""); setCandidateID(""); }
      router.refresh();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : t("The rollout could not be created."));
    } finally {
      setBusy(false);
    }
  }

  async function update(rollout: RuleRollout, state: "active" | "paused" | "promoted" | "rolled_back", canaryBasisPoints = 0) {
    if (!enabled || busy) return;
    if (state === "rolled_back" && !window.confirm(t("Roll back this rollout? Future reviews will use the baseline. This rollout cannot be resumed."))) return;
    if (state === "promoted" && !window.confirm(t("Promote this candidate as the default rule? The baseline binding will be disabled for future reviews. Historical reviews will not change."))) return;
    setBusy(true);
    setMessage(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/rule-rollouts/${encodeURIComponent(rollout.id)}`, {
        method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ state, revision: rollout.revision, canary_basis_points: canaryBasisPoints }),
      });
      if (!response.ok) throw new Error(await responseError(response, t("The rollout state could not be changed.")));
      setMessage(state === "promoted" ? t("Candidate promoted. Future reviews use its binding; retained run snapshots are unchanged.") : canaryBasisPoints ? t("Canary advanced to {percent}%. The new observation window starts now.", {percent:canaryBasisPoints/100}) : t("{mode} {state}. Only future review admissions change.", {mode:rollout.mode === "canary" ? t("Canary") : t("Shadow"),state:status(state)}));
      router.refresh();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : t("The rollout state could not be changed."));
    } finally {
      setBusy(false);
    }
  }

  function createShadow(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (baseline && candidates.some((item) => item.id === candidateID)) void create("shadow", baseline.id, candidateID);
  }

  return <section aria-labelledby="rollout-heading" className="space-y-4" id="rollouts">
    <div className="flex flex-col justify-between gap-2 sm:flex-row sm:items-end"><div><p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">{t("Evidence before exposure")}</p><div className="mt-2 flex min-w-0 items-center gap-2"><h2 className="text-xl font-semibold tracking-[-0.035em] text-[var(--ls-text)]" id="rollout-heading">{t("Shadow & Canary")}</h2><HelpHint label={t("Shadow & Canary")}>{t("Compare a matching published candidate without provider writes, then let a different owner or administrator approve a bounded Canary.")}</HelpHint></div></div><span className="text-xs text-[var(--ls-text-tertiary)]">{t("{count} rollout(s)", { count: rollouts.length })}</span></div>
    {message ? <p aria-live="polite" className="flex items-start gap-2 rounded-[12px] border border-violet-500/20 bg-violet-500/[0.07] px-4 py-3 text-sm text-[var(--ls-text-secondary)]"><CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />{message}</p> : null}
    <form className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6" onSubmit={createShadow}>
      <div className="flex items-start gap-3"><span className="grid size-9 place-items-center rounded-[11px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><FlaskConical className="size-4" /></span><div><div className="flex min-w-0 items-center gap-2"><h3 className="text-sm font-semibold text-[var(--ls-text)]">{t("Start provider-silent Shadow")}</h3><HelpHint label={t("Start provider-silent Shadow")}>{t("Only a candidate with the same scope, branch, path filters and precedence can pair with the active baseline. The server also validates the rule set and versions.")}</HelpHint></div></div></div>
      <div className="mt-5 grid gap-4 md:grid-cols-2"><label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Active baseline")}<select className={fieldClass} disabled={!enabled || busy} onChange={(event) => { setBaselineID(event.target.value); setCandidateID(""); }} required value={baselineID}><option value="">{t("Select baseline")}</option>{activeBindings.map((item) => <option key={item.id} value={item.id}>{bindingName(item, versions, language)}</option>)}</select></label><label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Shadow candidate")}<select className={fieldClass} disabled={!enabled || busy || !baseline} onChange={(event) => setCandidateID(event.target.value)} required value={candidateID}><option value="">{t("Select matching candidate")}</option>{candidates.map((item) => <option key={item.id} value={item.id}>{bindingName(item, versions, language)}</option>)}</select></label></div>
      {baseline && !candidates.length ? <p className="mt-3 text-xs text-[var(--ls-warning-text)]">{t("No matching Shadow binding exists. Create one above with the same scope and filters first.")}</p> : null}
      <button className="luminous-focus mt-5 inline-flex h-9 items-center gap-2 rounded-[9px] bg-[var(--ls-accent)] px-4 text-xs font-semibold text-white hover:bg-[var(--ls-accent-hover)] disabled:opacity-45" disabled={!enabled || busy || !candidateID} type="submit">{busy ? <LoaderCircle className="size-4 animate-spin" /> : <FlaskConical className="size-4" />}{t("Start Shadow")}</button>
    </form>
    <div className="space-y-3">{rollouts.map((rollout) => <RolloutCard bindings={bindings} busy={busy} createCanary={(basisPoints, failedRuns, windowMinutes) => create("canary", rollout.baseline_binding_id, rollout.candidate_binding_id, basisPoints, failedRuns, windowMinutes)} enabled={enabled} key={rollout.id} onStateChange={(state, canaryBasisPoints) => update(rollout, state, canaryBasisPoints)} org={org} rollout={rollout} rollouts={rollouts} versions={versions} />)}{!rollouts.length ? <div className="rounded-[16px] border border-dashed border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-5 py-8 text-center text-sm text-[var(--ls-text-secondary)]">{t("No Shadow or Canary rollout yet. Create a matching binding pair to begin collecting evidence.")}</div> : null}</div>
  </section>;
}

function RolloutCard({ bindings, busy, createCanary, enabled, onStateChange, org, rollout, rollouts, versions }: {
  bindings: RuleBinding[]; busy: boolean; createCanary: (basisPoints: number, failedRuns: number, windowMinutes: number) => void; enabled: boolean;
  onStateChange: (state: "active" | "paused" | "promoted" | "rolled_back", canaryBasisPoints?: number) => void;
  org: string; rollout: RuleRollout; rollouts: RuleRollout[]; versions: Map<string, string>;
}) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  const language = useUiLanguage();
  const [comparisons, setComparisons] = useState<RuleRolloutComparison[]>();
  const [comparisonError, setComparisonError] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [basisPoints, setBasisPoints] = useState(500);
  const [failedRuns, setFailedRuns] = useState(1);
  const [windowMinutes, setWindowMinutes] = useState(60);
  const [acknowledged, setAcknowledged] = useState(false);
  const baseline = bindings.find((item) => item.id === rollout.baseline_binding_id);
  const candidate = bindings.find((item) => item.id === rollout.candidate_binding_id);
  const hasActiveCanary = rollouts.some((item) => item.mode === "canary" && item.state !== "rolled_back" && item.state !== "promoted" && item.candidate_binding_id === rollout.candidate_binding_id);
  const latestComparison = comparisons?.[0];
  const nextBasisPoints = rollout.canary_basis_points === 100 ? 500 : rollout.canary_basis_points === 500 ? 2500 : rollout.canary_basis_points === 2500 ? 10000 : 0;

  async function loadComparisons() {
    if (rollout.mode !== "shadow" || loading) return;
    setLoading(true);
    setComparisonError(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/rule-rollouts/${encodeURIComponent(rollout.id)}/comparisons`, { cache: "no-store" });
      if (!response.ok) throw new Error(await responseError(response, t("Shadow comparisons could not be loaded.")));
      const payload = (await response.json()) as { comparisons?: RuleRolloutComparison[] };
      setComparisons(payload.comparisons ?? []);
    } catch (error) {
      setComparisonError(error instanceof Error ? error.message : t("Shadow comparisons could not be loaded."));
    } finally {
      setLoading(false);
    }
  }

  return <details className="group rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]" onToggle={(event) => { if (event.currentTarget.open && comparisons === undefined) void loadComparisons(); }}>
    <summary className="luminous-focus flex cursor-pointer list-none flex-wrap items-center justify-between gap-3 rounded-[16px] px-5 py-4 marker:hidden"><span className="flex min-w-0 items-center gap-3"><span className="grid size-9 shrink-0 place-items-center rounded-[11px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">{rollout.mode === "shadow" ? <FlaskConical className="size-4" /> : <ShieldCheck className="size-4" />}</span><span className="min-w-0"><strong className="block truncate text-sm font-semibold text-[var(--ls-text)]">{rollout.mode === "shadow" ? t("Shadow comparison") : `${t("Canary")} · ${(rollout.canary_basis_points / 100).toFixed(2)}%`}</strong><span className="mt-0.5 block truncate text-xs text-[var(--ls-text-secondary)]">{bindingName(candidate, versions, language)} <ArrowRight className="inline size-3" /> {bindingName(baseline, versions, language)}</span></span></span><span className="flex items-center gap-3"><span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-[11px] font-medium capitalize text-[var(--ls-text-secondary)]">{status(rollout.state)}</span><span className="text-xs text-[var(--ls-text-tertiary)]">{formatTime(rollout.created_at, language)}</span></span></summary>
    <div className="space-y-4 border-t border-[var(--ls-line)] px-5 py-5 text-sm text-[var(--ls-text-secondary)]"><p>{t("Created by ")}<span className="font-medium text-[var(--ls-text)]">{rollout.created_by}</span> {t(" · revision ")}{rollout.revision}{t(". Existing run snapshots never change when this rollout changes state.")}</p>
      {rollout.mode === "shadow" && comparisons !== undefined ? <button className={actionClass} disabled={loading} onClick={() => void loadComparisons()} type="button">{loading ? <LoaderCircle className="size-3.5 animate-spin" /> : <RotateCcw className="size-3.5" />}{t("Refresh evidence")}</button> : null}
      {rollout.mode === "canary" ? <p className="rounded-[10px] bg-[var(--ls-surface-muted)] px-3 py-2 text-xs">{t("Frozen Shadow comparison: ")}<code>{rollout.approved_shadow_comparison_id?.slice(0, 12) ?? t("legacy rollout — no approval evidence")}</code>{t(". Stable PR cohort; automatic rollback after ")}{t("{count} distinct failed candidate review(s) within {minutes} minutes. Historical run snapshots remain unchanged.", { count: rollout.auto_rollback_failed_runs, minutes: rollout.auto_rollback_window_minutes })}{rollout.auto_rollback_reason ? <span className="mt-1 block font-medium text-[var(--ls-warning-text)]">{t("Rollback evidence: ")}{rollout.auto_rollback_reason}</span> : null}</p> : loading ? <p className="flex items-center gap-2 text-xs"><LoaderCircle className="size-4 animate-spin" />{t("Loading provider-silent comparisons…")}</p> : comparisonError ? <button className={actionClass} onClick={() => void loadComparisons()} type="button">{comparisonError} {t(" Retry")}</button> : comparisons?.length ? <div className="space-y-2"><p className="text-xs font-semibold text-[var(--ls-text)]">{t("Latest replay evidence")}</p>{comparisons.slice(0, 3).map((comparison) => <div className="grid gap-2 rounded-[10px] border border-[var(--ls-line)] px-3 py-3 text-xs sm:grid-cols-4" key={comparison.id}><span className="font-medium capitalize text-[var(--ls-text)]">{status(comparison.state)} · {formatTime(comparison.completed_at ?? comparison.created_at, language)}</span><span>{t("Baseline ")}{comparison.baseline_finding_count} {t(" → candidate ")}{comparison.candidate_finding_count}</span><span>+{comparison.added_finding_count} {t(" added · −")}{comparison.removed_finding_count} {t(" removed")}</span><span>{comparison.matched_finding_count} {t(" matched · run ")}{comparison.baseline_run_id.slice(0, 8)}</span>{comparison.error_message ? <span className="sm:col-span-4">{comparison.error_message}</span> : null}</div>)}</div> : <p className="text-xs">{t("No replay result yet. The next matching completed review will queue a silent comparison; no provider comment or check is created by the replay.")}</p>}
      {rollout.mode === "shadow" && rollout.state === "active" && latestComparison?.state === "completed" && !hasActiveCanary ? <div className="rounded-[12px] border border-violet-500/20 bg-violet-500/[0.05] p-4"><p className="text-xs font-semibold text-[var(--ls-text)]">{t("Approve a bounded Canary")}</p><p className="mt-1 text-xs leading-5">{t("A different workspace owner/admin must review this completed comparison. The API rejects self-approval and mismatched bindings.")}</p><div className="mt-3 flex flex-wrap items-end gap-3"><label className="text-xs font-medium">{t("Initial traffic share")}<select className={fieldClass} disabled={!enabled || busy} onChange={(event) => setBasisPoints(Number(event.target.value))} value={basisPoints}><option value={100}>{t("1% pilot cohort")}</option><option value={500}>5%</option></select></label><label className="text-xs font-medium">{t("Failed PRs to roll back")}<select className={fieldClass} disabled={!enabled || busy} onChange={(event) => setFailedRuns(Number(event.target.value))} value={failedRuns}><option value={1}>{t("1 distinct PR")}</option><option value={2}>{t("2 distinct PRs")}</option><option value={3}>{t("3 distinct PRs")}</option></select></label><label className="text-xs font-medium">{t("Observation window")}<select className={fieldClass} disabled={!enabled || busy} onChange={(event) => setWindowMinutes(Number(event.target.value))} value={windowMinutes}><option value={15}>{t("15 minutes")}</option><option value={60}>{t("1 hour")}</option><option value={240}>{t("4 hours")}</option></select></label></div><p className="mt-2 text-[11px] leading-5">{t("Only candidate-selected execution failures count; quota rejections and repeated heads of one PR do not. Rollback changes future admissions only.")}</p><div className="mt-3 flex flex-wrap items-center gap-3"><label className="flex items-start gap-2 text-xs"><input checked={acknowledged} className="mt-0.5 accent-violet-600" disabled={!enabled || busy} onChange={(event) => setAcknowledged(event.target.checked)} type="checkbox" />{t("I reviewed the comparison and accept exposure for future reviews.")}</label><button className={actionClass} disabled={!enabled || busy || !acknowledged} onClick={() => createCanary(basisPoints, failedRuns, windowMinutes)} type="button">{t("Start Canary")}</button></div></div> : null}
      {rollout.mode === "shadow" && hasActiveCanary ? <p className="text-xs">{t("A Canary already uses this candidate. Pause or roll it back before starting another.")}</p> : null}
      {rollout.mode === "canary" && rollout.state === "active" ? <p className="text-xs leading-5">{t("Stage started ")}{formatTime(rollout.updated_at, language)}{t(". Advancing requires the full ")}{rollout.auto_rollback_window_minutes}{t("-minute observation window, at least one completed candidate-selected PR/MR review, and no failed or in-flight candidate reviews. Promotion also requires a different owner/admin from the Canary creator.")}</p> : null}
      <div className="flex flex-wrap gap-2 border-t border-[var(--ls-line)] pt-4">{rollout.state === "active" ? <button className={actionClass} disabled={!enabled || busy} onClick={() => onStateChange("paused")} type="button"><Pause className="size-3.5" />{t("Pause")}</button> : null}{rollout.state === "paused" ? <button className={actionClass} disabled={!enabled || busy} onClick={() => onStateChange("active")} type="button"><Play className="size-3.5" />{t("Resume")}</button> : null}{rollout.mode === "canary" && rollout.state === "active" && nextBasisPoints ? <button className={actionClass} disabled={!enabled || busy} onClick={() => onStateChange("active", nextBasisPoints)} type="button"><TrendingUp className="size-3.5" />{t("Advance to ")}{nextBasisPoints / 100}%</button> : null}{rollout.mode === "canary" && rollout.state === "active" && rollout.canary_basis_points === 10000 ? <button className={actionClass} disabled={!enabled || busy} onClick={() => onStateChange("promoted")} type="button"><ShieldCheck className="size-3.5" />{t("Promote candidate")}</button> : null}{(rollout.state === "active" || rollout.state === "paused") ? <button className={actionClass} disabled={!enabled || busy} onClick={() => onStateChange("rolled_back")} type="button"><RotateCcw className="size-3.5" />{t("Roll back")}</button> : null}{rollout.state === "promoted" ? <span className="self-center text-xs text-[var(--ls-text-tertiary)]">{t("Candidate is the default binding for future reviews.")}</span> : null}</div>
    </div>
  </details>;
}
