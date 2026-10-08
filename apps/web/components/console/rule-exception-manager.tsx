"use client";

import { useUiLanguage, useWorkflowStatus, useWorkflowText } from "@/components/console/ui-language-context";

import Link from "next/link";
import { FormEvent, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Ban, Check, CircleAlert, Clock3, ExternalLink, LoaderCircle, ShieldOff, X } from "lucide-react";

import type { RuleException, RuleSet } from "@/lib/control-api";
import { cn } from "@/lib/utils";

const inputClass = "luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)] disabled:opacity-45";
const stateTone: Record<RuleException["effective_state"], string> = {
  pending: "border-amber-500/20 bg-amber-500/[0.08] text-[var(--ls-warning-text)]",
  approved: "border-emerald-500/20 bg-emerald-500/[0.08] text-[var(--ls-success-text)]",
  rejected: "border-red-500/20 bg-red-500/[0.08] text-[var(--ls-critical-text)]",
  revoked: "border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]",
  expired: "border-[var(--ls-line)] bg-[var(--ls-surface-muted)] text-[var(--ls-text-tertiary)]",
};

type SourceIssue = { id: string; revision: number; provider: "github" | "gitlab"; apiBaseURL: string; repository: string; bodyPreview: string };

export function RuleExceptionManager({ enabled, exceptions, org, ruleSets, sourceIssue }: { enabled: boolean; exceptions: RuleException[]; org: string; ruleSets: RuleSet[]; sourceIssue?: SourceIssue }) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  const language = useUiLanguage();
  const router = useRouter();
  const published = useMemo(() => ruleSets.filter((item) => item.latest_version?.state === "published"), [ruleSets]);
  const [scope, setScope] = useState<"tenant" | "repository">("repository");
	const [scopeProvider, setScopeProvider] = useState<"github" | "gitlab">(sourceIssue?.provider ?? "github");
	const [scopeAPIBaseURL, setScopeAPIBaseURL] = useState(sourceIssue?.apiBaseURL ?? "https://api.github.com");
  const [items, setItems] = useState(exceptions);
  const [busy, setBusy] = useState<string>();
  const [message, setMessage] = useState<string>();

  async function createException(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const expires = String(form.get("expires_at") ?? "");
    setBusy("create"); setMessage(undefined);
    const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/rule-exceptions`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({
        rule_version_id: form.get("rule_version_id"), rule_key: form.get("rule_key"), scope_kind: scope,
		scope_ref: scope === "tenant" ? "" : (sourceIssue?.repository ?? form.get("scope_ref")),
		scope_provider: scope === "tenant" ? "" : (sourceIssue?.provider ?? scopeProvider),
		scope_api_base_url: scope === "tenant" ? "" : (sourceIssue?.apiBaseURL ?? scopeAPIBaseURL),
		target_branch_glob: form.get("target_branch_glob"),
        reason: form.get("reason"), ticket_url: form.get("ticket_url"), expires_at: new Date(expires).toISOString(),
        source_issue_id: sourceIssue?.id, source_issue_revision: sourceIssue?.revision,
      }),
    });
    const body = (await response.json().catch(() => ({}))) as RuleException & { error?: string };
    setMessage(response.ok ? t("Exception requested. A different rule manager must approve it before it can affect admission.") : body.error ?? t("Exception request could not be created."));
    setBusy(undefined);
	if (response.ok) { setItems((current) => [body, ...current.filter((item) => item.id !== body.id)]); formElement.reset(); setScope("repository"); setScopeProvider(sourceIssue?.provider ?? "github"); setScopeAPIBaseURL(sourceIssue?.apiBaseURL ?? "https://api.github.com"); router.refresh(); }
  }

  async function mutate(item: RuleException, action: "approved" | "rejected" | "revoke") {
    setBusy(`${item.id}:${action}`); setMessage(undefined);
    const endpoint = action === "revoke" ? "revoke" : "decisions";
    const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/rule-exceptions/${encodeURIComponent(item.id)}/${endpoint}`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: action === "revoke" ? undefined : JSON.stringify({ decision: action }),
    });
    const body = (await response.json().catch(() => ({}))) as RuleException & { error?: string };
    setMessage(response.ok ? `Exception ${action === "revoke" ? "revoked" : action}. Future admissions will resolve the updated governance state.` : body.error ?? t("Exception transition failed."));
    setBusy(undefined); if (response.ok) { setItems((current) => current.map((candidate) => candidate.id === body.id ? body : candidate)); router.refresh(); }
  }

  return <div className="space-y-5">
    {message ? <div aria-live="polite" className="flex items-start gap-2 rounded-[14px] border border-[var(--ls-line-strong)] bg-[var(--ls-accent-soft)] px-4 py-3 text-sm text-[var(--ls-text)]"><CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />{message}</div> : null}
    <form className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6" onSubmit={createException}>
      <div className="flex items-start gap-3"><span className="grid size-9 place-items-center rounded-[11px] bg-amber-500/[0.08] text-[var(--ls-warning-text)]"><ShieldOff className="size-4" /></span><div><h2 className="text-sm font-semibold text-[var(--ls-text)]">{t("Request a bounded exception")}</h2><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{t("The rule key must exist in the selected published version. Requests cannot be self-approved.")}</p></div></div>
      {sourceIssue ? <div className="mt-4 rounded-[12px] border border-[var(--ls-line-strong)] bg-[var(--ls-accent-soft)] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]"><span className="font-semibold text-[var(--ls-accent)]">{t("Linked issue revision ")}{sourceIssue.revision}</span><span className="mx-2 text-[var(--ls-line-strong)]">·</span>{sourceIssue.bodyPreview}<div className="mt-1 font-mono text-[var(--ls-text-tertiary)]">{sourceIssue.provider}@{providerHost(sourceIssue.apiBaseURL)} · {sourceIssue.repository}</div></div> : null}
      <div className="mt-5 grid gap-4 lg:grid-cols-3">
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Published version")}<select className={inputClass} disabled={!enabled || Boolean(busy)} name="rule_version_id" required><option value="">{t("Select version")}</option>{published.map((item) => <option key={item.latest_version?.id} value={item.latest_version?.id}>{item.name} · v{item.latest_version?.version}</option>)}</select></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Exact rule key")}<input className={inputClass} disabled={!enabled || Boolean(busy)} name="rule_key" placeholder="security.credentials" required /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Expires at")}<input className={inputClass} disabled={!enabled || Boolean(busy)} name="expires_at" required type="datetime-local" /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Scope")}<select className={inputClass} disabled={!enabled || Boolean(busy) || Boolean(sourceIssue)} onChange={(event) => setScope(event.target.value as "tenant" | "repository")} value={scope}><option value="repository">{t("Repository")}</option><option value="tenant">{t("Entire workspace")}</option></select></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Provider")}<select className={inputClass} disabled={!enabled || Boolean(busy) || scope === "tenant" || Boolean(sourceIssue)} onChange={(event) => { const provider = event.target.value as "github" | "gitlab"; setScopeProvider(provider); setScopeAPIBaseURL(provider === "github" ? "https://api.github.com" : "https://gitlab.com/api/v4"); }} value={sourceIssue?.provider ?? scopeProvider}><option value="github">GitHub</option><option value="gitlab">GitLab</option></select></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Provider API URL")}<input className={inputClass} disabled={!enabled || Boolean(busy) || scope === "tenant" || Boolean(sourceIssue)} onChange={(event) => setScopeAPIBaseURL(event.target.value)} placeholder="https://gitlab.example.com/api/v4" value={sourceIssue?.apiBaseURL ?? scopeAPIBaseURL} /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Repository")}<input className={inputClass} defaultValue={sourceIssue?.repository} disabled={!enabled || Boolean(busy) || scope === "tenant" || Boolean(sourceIssue)} name="scope_ref" placeholder="RainLib/open-review-platform" required={scope === "repository"} /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Target branch glob")}<input className={inputClass} disabled={!enabled || Boolean(busy)} name="target_branch_glob" placeholder="main or release/*" /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)] lg:col-span-2">{t("Risk acceptance reason")}<input className={inputClass} defaultValue={sourceIssue ? t("Temporary risk acceptance for issue {id}: {body}", { id: sourceIssue.id, body: sourceIssue.bodyPreview }) : undefined} disabled={!enabled || Boolean(busy)} name="reason" placeholder={t("Why the exception is necessary and what compensating control exists")} required /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">{t("Ticket URL")}<input className={inputClass} disabled={!enabled || Boolean(busy)} name="ticket_url" placeholder="https://tracker.example/SEC-123" type="url" /></label>
      </div>
      <button className="luminous-focus mt-5 inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-xs font-semibold text-white shadow-[var(--ls-shadow-control)] disabled:opacity-45" disabled={!enabled || Boolean(busy) || !published.length} type="submit">{busy === "create" ? <LoaderCircle className="size-4 animate-spin" /> : <ShieldOff className="size-4" />}{t("Request exception")}</button>
    </form>
    <div className="space-y-3">{items.map((item) => <article className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]" key={item.id}>
      <div className="flex flex-col justify-between gap-4 lg:flex-row lg:items-start"><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><h2 className="font-mono text-sm font-semibold text-[var(--ls-text)]">{item.rule_key}</h2><span className={cn("rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase", stateTone[item.effective_state])}>{status(item.effective_state)}</span></div><p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">{item.rule_set_name} v{item.rule_version} · {item.scope_kind === "tenant" ? t("Entire workspace") : `${item.scope_provider || t("legacy provider")}@${providerHost(item.scope_api_base_url)} · ${item.scope_ref}`} · {item.target_branch_glob || t("all branches")}</p><p className="mt-3 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">{item.reason}</p><div className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-xs text-[var(--ls-text-tertiary)]"><span className="inline-flex items-center gap-1.5"><Clock3 className="size-3.5" />{t("Expires ")}{formatDate(item.expires_at, language)}</span><span>{t("Requested by ")}{item.requested_by}</span>{item.approved_by ? <span>{t("Decided by ")}{item.approved_by}</span> : null}{item.source_issue_id ? <Link className="text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/issues/${encodeURIComponent(item.source_issue_id)}`}>{t("Source issue · revision ")}{item.source_issue_revision}</Link> : null}{item.ticket_url ? <a className="inline-flex items-center gap-1 text-[var(--ls-accent)] hover:underline" href={item.ticket_url} rel="noreferrer" target="_blank">{t("Ticket ")}<ExternalLink className="size-3" /></a> : null}</div></div>
      <div className="flex shrink-0 flex-wrap gap-2">{item.can_decide ? <><button className="luminous-focus inline-flex items-center gap-1 rounded-[9px] border border-emerald-500/20 px-2.5 py-1.5 text-xs text-[var(--ls-success-text)] disabled:opacity-45" disabled={!enabled || Boolean(busy)} onClick={() => mutate(item, "approved")} type="button"><Check className="size-3.5" />{t("Approve")}</button><button className="luminous-focus inline-flex items-center gap-1 rounded-[9px] border border-red-500/20 px-2.5 py-1.5 text-xs text-[var(--ls-critical-text)] disabled:opacity-45" disabled={!enabled || Boolean(busy)} onClick={() => mutate(item, "rejected")} type="button"><X className="size-3.5" />{t("Reject")}</button></> : null}{item.can_revoke ? <button className="luminous-focus inline-flex items-center gap-1 rounded-[9px] border border-[var(--ls-line-strong)] px-2.5 py-1.5 text-xs text-[var(--ls-text-secondary)] disabled:opacity-45" disabled={!enabled || Boolean(busy)} onClick={() => mutate(item, "revoke")} type="button"><Ban className="size-3.5" />{t("Revoke")}</button> : null}</div></div>
    </article>)}{!items.length ? <div className="grid min-h-44 place-items-center rounded-[14px] border border-dashed border-[var(--ls-line-strong)] text-center text-sm text-[var(--ls-text-tertiary)]">{t("No policy exceptions have been requested.")}</div> : null}</div>
  </div>;
}

function providerHost(value?: string) {
  if (!value) return "unqualified";
  try { return new URL(value).host; } catch { return value; }
}

function formatDate(value: string, language: import("@/lib/ui-language").UiLanguage) {
  return new Intl.DateTimeFormat(language, { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" }).format(new Date(value));
}
