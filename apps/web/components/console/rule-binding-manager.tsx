"use client";

import { SectionDisclosure } from "./section-disclosure";

import { useWorkflowStatus, useWorkflowText } from "@/components/console/ui-language-context";

import { FormEvent, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { CircleAlert, GitBranch, LoaderCircle, Power, Radar, ShieldCheck } from "lucide-react";

import type { RuleBinding, RuleSet } from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

const inputClass = "luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)] disabled:opacity-45";

export function RuleBindingManager({ bindings, enabled, org, ruleSets }: { bindings: RuleBinding[]; enabled: boolean; org: string; ruleSets: RuleSet[] }) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  const router = useRouter();
  const published = useMemo(() => ruleSets.filter((item) => item.latest_version?.state === "published"), [ruleSets]);
  const versionNames = useMemo(() => new Map(ruleSets.flatMap((item) => item.latest_version ? [[item.latest_version.id, `${item.name} · v${item.latest_version.version}`] as const] : [])), [ruleSets]);
  const [busy, setBusy] = useState<string>();
  const [message, setMessage] = useState<string>();
	const [scopeKind, setScopeKind] = useState<"tenant" | "repository">("repository");
	const [scopeProvider, setScopeProvider] = useState<"github" | "gitlab">("github");
	const [scopeAPIBaseURL, setScopeAPIBaseURL] = useState("https://api.github.com");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    setBusy("create"); setMessage(undefined);
    const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/rule-bindings`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({
        rule_version_id: form.get("rule_version_id"), scope_kind: scopeKind, scope_ref: scopeKind === "tenant" ? "" : form.get("scope_ref"),
        scope_provider: scopeKind === "tenant" ? "" : scopeProvider, scope_api_base_url: scopeKind === "tenant" ? "" : scopeAPIBaseURL,
        precedence: Number(form.get("precedence")), target_branch_glob: form.get("target_branch_glob"), path_include_glob: form.get("path_include_glob"), path_exclude_glob: form.get("path_exclude_glob"), state: form.get("state"),
      }),
    });
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setMessage(response.ok ? t("Binding created. Admission will use it according to its lifecycle state.") : body.error ?? t("Binding could not be created."));
    setBusy(undefined); if (response.ok) { formElement.reset(); setScopeKind("repository"); setScopeProvider("github"); setScopeAPIBaseURL("https://api.github.com"); router.refresh(); }
  }

  async function changeState(binding: RuleBinding, state: RuleBinding["state"]) {
    setBusy(binding.id); setMessage(undefined);
    const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/rule-bindings/${encodeURIComponent(binding.id)}`, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ state }) });
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setMessage(response.ok ? `Binding moved to ${state}. Existing review snapshots remain unchanged.` : body.error ?? t("Binding state could not be updated."));
    setBusy(undefined); if (response.ok) router.refresh();
  }

  return <div className="space-y-5">
    {message ? <div className="flex items-start gap-2 rounded-[12px] border border-violet-500/20 bg-violet-500/[0.07] px-4 py-3 text-sm text-[var(--ls-text-secondary)]"><CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />{message}</div> : null}
    <SectionDisclosure title={t("Create rollout binding")} ><form className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6" onSubmit={submit}>
      <div className="flex items-start gap-3"><span className="grid size-9 place-items-center rounded-[11px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><Radar className="size-4" /></span><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">{t("Create rollout binding")}</h2><HelpHint label={t("Create rollout binding")}>{t("Start a candidate in Shadow. Upgrades to an existing baseline use the evidence-gated Shadow & Canary flow below.")}</HelpHint></div></div></div>
      <div className="mt-5 grid gap-4 lg:grid-cols-4">
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Published policy")}<select className={inputClass} disabled={!enabled || busy !== undefined} name="rule_version_id" required><option value="">{t("Select version")}</option>{published.map((item) => <option key={item.id} value={item.latest_version?.id}>{item.name} · v{item.latest_version?.version}</option>)}</select></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Scope")}<select className={inputClass} disabled={!enabled || busy !== undefined} name="scope_kind" onChange={(event) => setScopeKind(event.target.value as "tenant" | "repository")} value={scopeKind}><option value="repository">{t("Repository")}</option><option value="tenant">{t("Entire workspace")}</option></select></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Provider")}<select className={inputClass} disabled={!enabled || busy !== undefined || scopeKind === "tenant"} onChange={(event) => { const provider = event.target.value as "github" | "gitlab"; setScopeProvider(provider); setScopeAPIBaseURL(provider === "github" ? "https://api.github.com" : "https://gitlab.com/api/v4"); }} value={scopeProvider}><option value="github">GitHub</option><option value="gitlab">GitLab</option></select></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Provider API URL")}<input className={inputClass} disabled={!enabled || busy !== undefined || scopeKind === "tenant"} onChange={(event) => setScopeAPIBaseURL(event.target.value)} placeholder="https://gitlab.example.com/api/v4" value={scopeAPIBaseURL} /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Repository")}<input className={inputClass} disabled={!enabled || busy !== undefined || scopeKind === "tenant"} name="scope_ref" placeholder="RainLib/open-review-platform" required={scopeKind === "repository"} /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Initial state")}<select className={inputClass} defaultValue="shadow" disabled={!enabled || busy !== undefined} name="state"><option value="shadow">{t("Shadow")}</option><option value="active">{t("Active")}</option></select></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Precedence")}<input className={inputClass} defaultValue="100" disabled={!enabled || busy !== undefined} min={0} name="precedence" type="number" /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Target branch glob")}<input className={inputClass} disabled={!enabled || busy !== undefined} name="target_branch_glob" placeholder="main" /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Include paths")}<input className={inputClass} disabled={!enabled || busy !== undefined} name="path_include_glob" placeholder="services/**" /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Exclude paths")}<input className={inputClass} disabled={!enabled || busy !== undefined} name="path_exclude_glob" placeholder="**/generated/**" /></label>
      </div>
      <button className="luminous-focus mt-5 inline-flex h-9 items-center gap-2 rounded-[9px] bg-[var(--ls-accent)] px-4 text-xs font-semibold text-white hover:bg-[var(--ls-accent-hover)] disabled:opacity-45" disabled={!enabled || busy !== undefined || !published.length} type="submit">{busy === "create" ? <LoaderCircle className="size-4 animate-spin" /> : <ShieldCheck className="size-4" />}{t("Create binding")}</button>
    </form></SectionDisclosure>
    <div className="space-y-3">{bindings.map((binding) => <article className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]" key={binding.id}>
      <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-start"><div><div className="flex flex-wrap items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">{versionNames.get(binding.rule_version_id) ?? `Version ${binding.rule_version_id.slice(0, 8)}`}</h2><span className={cn("rounded-full px-2 py-0.5 text-[11px] font-medium", binding.state === "active" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : binding.state === "shadow" ? "bg-amber-500/10 text-[var(--ls-warning-text)]" : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-tertiary)]")}>{status(binding.state)}</span></div><p className="mt-2 flex items-center gap-1.5 text-xs text-[var(--ls-text-secondary)]"><GitBranch className="size-3.5" />{binding.scope_kind === "tenant" ? t("Entire workspace") : `${binding.scope_provider || t("legacy provider")}@${providerHost(binding.scope_api_base_url)} · ${binding.scope_ref}`} · {binding.target_branch_glob || t("all branches")} {t(" · precedence ")}{binding.precedence}</p></div>
      <div className="flex flex-wrap gap-2">{binding.state !== "shadow" ? <button className="luminous-focus rounded-[8px] border border-amber-500/20 px-2.5 py-1.5 text-xs text-[var(--ls-warning-text)] disabled:opacity-45" disabled={!enabled || busy !== undefined} onClick={() => changeState(binding, "shadow")} type="button">{t("Shadow")}</button> : null}{binding.state === "shadow" ? <a className="luminous-focus rounded-[8px] border border-violet-500/20 px-2.5 py-1.5 text-xs text-[var(--ls-accent)]" href="#rollouts">{t("Review rollout evidence")}</a> : null}{binding.state !== "disabled" ? <button className="luminous-focus inline-flex items-center gap-1 rounded-[8px] border border-red-500/20 px-2.5 py-1.5 text-xs text-[var(--ls-critical-text)] disabled:opacity-45" disabled={!enabled || busy !== undefined} onClick={() => changeState(binding, "disabled")} type="button"><Power className="size-3" />{t("Disable")}</button> : null}</div></div>
    </article>)}{!bindings.length ? <div className="grid min-h-44 place-items-center rounded-[16px] border border-dashed border-[var(--ls-line-strong)] bg-[var(--ls-surface)] text-center text-sm text-[var(--ls-text-secondary)]">{t("No rollout bindings yet.")}</div> : null}</div>
  </div>;
}

function providerHost(value?: string) {
  if (!value) return "unqualified";
  try { return new URL(value).host; } catch { return value; }
}
