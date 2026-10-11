"use client";

import { SectionDisclosure } from "./section-disclosure";

import { useWorkflowText } from "./ui-language-context";

import { useRouter } from "next/navigation";
import { useId, useState } from "react";
import { BrainCircuit, Check, CircleAlert, KeyRound, LoaderCircle, RefreshCcw, RefreshCw, Save, ShieldCheck, Sparkles } from "lucide-react";

import type { DataSource, ModelProbeReceipt, ReviewConfigChangeRequest, ReviewConfigView } from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { matchesModelProbeRoute, modelScopeQuery, sameModelScope, type ModelScope } from "@/lib/model-scope";
import { EnterpriseSettingsTabs } from "@/components/console/enterprise-settings-tabs";
import { ModelSettingsTabs } from "@/components/console/model-settings-tabs";
import { DataFreshness, PageState } from "@/components/console/page-state";
import { FieldLabel, HelpHint } from "@/components/console/help-hint";

type RouteContent = {
  enabled: boolean;
  provider: "deployment" | "openai-compatible" | "anthropic";
  protocol: "deployment" | "openai-chat" | "anthropic-messages";
  base_url: string;
  model: string;
  credential_ref: string;
  effort: "low" | "medium" | "high";
  max_prompt_tokens: number;
  token_budget: number;
  subtask_timeout_minutes: number;
	max_concurrent_runs: number;
};

const fallback: RouteContent = { enabled: false, provider: "deployment", protocol: "deployment", base_url: "", model: "", credential_ref: "", effort: "low", max_prompt_tokens: 8000, token_budget: 128000, subtask_timeout_minutes: 5, max_concurrent_runs: 2 };

export function ModelRouteEditor({ changes, changesDetail, changesSource, config, detail, org, probes: initialProbes, probeDetail, probeSource, requestedScope, source }: { changes: ReviewConfigChangeRequest[]; changesDetail?: string; changesSource: DataSource; config?: ReviewConfigView; detail?: string; org: string; probes: ModelProbeReceipt[]; probeDetail?: string; probeSource: DataSource; requestedScope: ModelScope; source: DataSource }) {
  const t = useWorkflowText();
  const router = useRouter();
  const initial = decode(config?.content);
  const [view, setView] = useState(config);
  const [content, setContent] = useState<RouteContent>(initial);
  const [baseline, setBaseline] = useState(JSON.stringify(initial));
  const [repository, setRepository] = useState(config?.requested_scope_ref ?? requestedScope.scope_ref ?? "");
  const [scopeProvider, setScopeProvider] = useState(config?.requested_scope_provider || requestedScope.scope_provider || "github");
  const [scopeAPIBaseURL, setScopeAPIBaseURL] = useState(config?.requested_scope_api_base_url || requestedScope.scope_api_base_url || "https://api.github.com");
  const [pending, setPending] = useState<"save" | "restore" | "probe" | "decision">();
  const [notice, setNotice] = useState<{ tone: "success" | "error"; text: string }>();
  const [submittedProbes, setSubmittedProbes] = useState<ModelProbeReceipt[]>([]);
  const [confirmProbe, setConfirmProbe] = useState(false);
  const [approvalReason, setApprovalReason] = useState("");
  const [approvalComment, setApprovalComment] = useState("");
  const dirty = JSON.stringify(content) !== baseline;
  const readOnlyPreview = source === "demo";
  const activeScope: ModelScope = view ? { scope_kind: view.requested_scope_kind, scope_ref: view.requested_scope_ref ?? "", scope_provider: view.requested_scope_provider ?? requestedScope.scope_provider ?? "", scope_api_base_url: view.requested_scope_api_base_url ?? requestedScope.scope_api_base_url ?? "" } : requestedScope;
  // Server refreshes replace queued local receipts with their latest durable state.
  const probes = [...initialProbes, ...submittedProbes.filter((probe) => !initialProbes.some((current) => current.id === probe.id))];
  const matchingProbes = probes.filter((probe) => view && matchesModelProbeRoute(probe, view)).sort((left, right) => right.created_at.localeCompare(left.created_at));
  const latestProbe = matchingProbes[0];
  const activeProbe = matchingProbes.some((probe) => probe.state === "queued" || probe.state === "running");
  const matchingChanges = changes.filter((change) => change.section === "models" && sameModelScope(change, activeScope));
  const pendingChange = matchingChanges.find((change) => change.state === "pending");

  function openScope(scope: "tenant" | "repository") {
    if (scope === "tenant") return router.push(`/${org}/settings/models`);
    if (!repository.trim() || !scopeProvider || !scopeAPIBaseURL.trim()) return setNotice({ tone: "error", text: "Choose a provider, its API base URL, and an owner/repository reference before opening repository scope." });
    router.push(`/${org}/settings/models?${modelScopeQuery({ scope_kind: "repository", scope_ref: repository.trim(), scope_provider: scopeProvider, scope_api_base_url: scopeAPIBaseURL.trim() })}`);
  }

  function update(patch: Partial<RouteContent>) {
    if (readOnlyPreview) return;
    setContent((current) => ({ ...current, ...patch }));
  }

  function chooseProvider(provider: RouteContent["provider"]) {
    if (readOnlyPreview) return;
    if (provider === "deployment") {
      setContent({ ...fallback });
      return;
    }
    update({ enabled: true, provider, protocol: provider === "anthropic" ? "anthropic-messages" : "openai-chat" });
  }

  async function save() {
    if (!view || source !== "live") {
      setNotice({ tone: "error", text: "Demo data is read-only. Connect the live control plane before publishing a model route." });
      return;
    }
    const requiresApproval = !view.inherited && view.revision > 0;
    if (!requiresApproval && activeScope.scope_kind === "repository" && (!activeScope.scope_provider || !activeScope.scope_api_base_url)) {
      setNotice({ tone: "error", text: "Choose the Git provider and API base URL, then open the repository scope before saving its initial model route." });
      return;
    }
    if (requiresApproval && approvalReason.trim().length < 3) {
      setNotice({ tone: "error", text: "Explain why this active model route must change before requesting independent approval." });
      return;
    }
    setPending("save"); setNotice(undefined);
    try {
      if (requiresApproval) {
        const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/review-config-change-requests`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ section: "models", ...activeScope, expected_revision: view.revision, content, reason: approvalReason.trim(), operation: "upsert" }) });
        const body = await response.json() as ReviewConfigChangeRequest | { error?: string };
        if (!response.ok || !("proposed_content_sha256" in body)) throw new Error("error" in body && body.error ? body.error : `Approval request failed (${response.status}).`);
        setNotice({ tone: "success", text: "Approval requested. The active route stays unchanged until a different owner or admin approves the exact proposed hash." }); setApprovalReason(""); router.refresh();
      } else {
        const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/review-config/models`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ...activeScope, expected_revision: 0, content }) });
        const body = await response.json() as ReviewConfigView | { error?: string };
        if (!response.ok || !("content" in body)) throw new Error("error" in body && body.error ? body.error : `Save failed (${response.status}).`);
        const next = decode(body.content); setView(body); setContent(next); setBaseline(JSON.stringify(next)); setNotice({ tone: "success", text: `Initial model route revision ${body.revision} is active for future runs.` }); router.refresh();
      }
    } catch (error) { setNotice({ tone: "error", text: error instanceof Error ? error.message : "Model route could not be saved." }); }
    finally { setPending(undefined); }
  }

  async function restore() {
    if (!view || source !== "live" || view.inherited || view.requested_scope_kind !== "repository") return;
    if (approvalReason.trim().length < 3) { setNotice({ tone: "error", text: "Explain why this active repository override should be removed before requesting independent approval." }); return; }
    setPending("restore"); setNotice(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/review-config-change-requests`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ section: "models", ...activeScope, expected_revision: view.revision, content, reason: approvalReason.trim(), operation: "restore_inheritance" }) });
      const body = await response.json() as ReviewConfigChangeRequest | { error?: string };
      if (!response.ok || !("proposed_content_sha256" in body)) throw new Error("error" in body && body.error ? body.error : `Restore approval request failed (${response.status}).`);
      setNotice({ tone: "success", text: "Inheritance restoration is awaiting independent approval. The current repository route remains active until then." }); setApprovalReason(""); router.refresh();
    } catch (error) { setNotice({ tone: "error", text: error instanceof Error ? error.message : "Inheritance could not be restored." }); }
    finally { setPending(undefined); }
  }

  async function decideChange(change: ReviewConfigChangeRequest, decision: "approved" | "rejected") {
    if (source !== "live") return;
    setPending("decision"); setNotice(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/review-config-change-requests/${encodeURIComponent(change.id)}/decision`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ decision, comment: approvalComment.trim() }) });
      const body = await response.json() as ReviewConfigChangeRequest | { error?: string };
      if (!response.ok || !("state" in body)) throw new Error("error" in body && body.error ? body.error : `Decision failed (${response.status}).`);
      setApprovalComment(""); setNotice({ tone: "success", text: body.state === "superseded" ? "The proposal was superseded because its base route changed; it was not applied." : `Proposal ${body.state}.` }); router.refresh();
    } catch (error) { setNotice({ tone: "error", text: error instanceof Error ? error.message : "Approval decision could not be saved." }); }
    finally { setPending(undefined); }
  }

  async function requestProbe() {
    if (!view || source !== "live" || dirty || !content.enabled || !confirmProbe) return;
    setPending("probe"); setNotice(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/model-probes`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ...activeScope, expected_revision: view.revision, acknowledged: true }) });
      const body = await response.json() as ModelProbeReceipt | { error?: string };
      if (!response.ok || !("state" in body)) throw new Error("error" in body && body.error ? body.error : `Connectivity probe failed (${response.status}).`);
      setSubmittedProbes((current) => [body, ...current.filter((probe) => probe.id !== body.id)]);
      setConfirmProbe(false);
      setNotice({ tone: "success", text: "Connectivity probe queued. Refresh its status when you want the latest durable receipt." });
    } catch (error) { setNotice({ tone: "error", text: error instanceof Error ? error.message : "Model connectivity probe could not be requested." }); }
    finally { setPending(undefined); }
  }

  return <fieldset aria-describedby={readOnlyPreview ? "model-route-demo-boundary" : undefined} className="m-0 min-w-0 space-y-6 border-0 p-0" disabled={readOnlyPreview}>
    {readOnlyPreview ? <legend className="sr-only">Read-only demo model route</legend> : null}
    <header className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between"><div><p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">Enterprise control plane</p><div className="mt-2 flex min-w-0 items-center gap-2"><h1 className="text-[32px] font-semibold tracking-[-0.045em] text-[var(--ls-text)]">Models & BYOK</h1><HelpHint label="Models & BYOK">Route review runs to a governed model without storing secret values in the application database.</HelpHint></div></div><DataFreshness state={source === "live" ? "live" : source === "demo" ? "demo" : "unavailable"} /></header>
    <EnterpriseSettingsTabs active="models" org={org} />
    <ModelSettingsTabs active="routes" org={org} scope={activeScope} />
    {readOnlyPreview ? <p id="model-route-demo-boundary" className="rounded-[12px] border border-amber-500/20 bg-amber-500/[0.07] px-3.5 py-3 text-xs leading-5 text-[var(--ls-warning-text)]">Demo route data is visual-only. Publishing and connectivity probes require the live control plane and remain unavailable here.</p> : null}
    {!view || (source !== "live" && source !== "demo") ? <PageState action={<button className="luminous-focus inline-flex h-9 items-center justify-center rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-xs font-semibold text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" onClick={() => router.refresh()} type="button">Refresh evidence</button>} detail={detail ?? "The model route could not be loaded."} kind="unavailable" title="Model control plane unavailable" /> : <>
      <section className="space-y-4 rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="rounded-full bg-[var(--ls-accent-soft)] px-2.5 py-1 text-xs font-semibold text-[var(--ls-accent)]">{view.requested_scope_kind === "repository" ? "Repository" : "Workspace"}</span>
            <span className="text-sm font-medium text-[var(--ls-text)]">{view.requested_scope_ref || org}</span>
            {activeScope.scope_provider ? <span className="break-all rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-[11px] text-[var(--ls-text-secondary)]">{activeScope.scope_provider} · {activeScope.scope_api_base_url}</span> : null}
            {view.inherited ? <span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">Inherited from {view.origin_scope_kind}</span> : null}
            {dirty ? <span className="rounded-full bg-amber-500/10 px-2.5 py-1 text-xs text-[var(--ls-warning-text)]">Unsaved changes</span> : <span className="inline-flex items-center gap-1 text-xs text-[var(--ls-text-tertiary)]"><Check className="size-3.5" /> Up to date</span>}
          </div>
          <div className="inline-flex rounded-[10px] bg-[var(--ls-surface-muted)] p-1"><ScopeButton active={view.requested_scope_kind === "tenant"} onClick={() => openScope("tenant")}>Workspace</ScopeButton><ScopeButton active={view.requested_scope_kind === "repository"} onClick={() => openScope("repository")}>Repository</ScopeButton></div>
        </div>
        <div className="grid gap-3 sm:grid-cols-[112px_minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end">
          <label className="block text-xs text-[var(--ls-text-secondary)]">Git provider<select aria-label="Repository provider" className="luminous-focus mt-2 h-9 w-full rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" onChange={(event) => { const provider = event.target.value; setScopeProvider(provider); setScopeAPIBaseURL(provider === "github" ? "https://api.github.com" : "https://gitlab.com/api/v4"); }} value={scopeProvider}><option value="github">GitHub</option><option value="gitlab">GitLab</option></select></label>
          <label className="block text-xs text-[var(--ls-text-secondary)]">Provider API base URL<input aria-label="Provider API base URL" className="luminous-focus mt-2 h-9 w-full min-w-0 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" onChange={(event) => setScopeAPIBaseURL(event.target.value)} placeholder="https://gitlab.example.com/api/v4" value={scopeAPIBaseURL} /></label>
          <label className="block text-xs text-[var(--ls-text-secondary)]">Repository<input aria-label="Repository scope" className="luminous-focus mt-2 h-9 w-full min-w-0 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" onChange={(event) => setRepository(event.target.value)} placeholder="owner/repository" value={repository} /></label>
          <button className="luminous-focus h-9 rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-semibold text-[var(--ls-text)]" onClick={() => openScope("repository")} type="button">Open repository</button>
        </div>
        <p className="text-xs leading-5 text-[var(--ls-text-secondary)]">Repository model routes are isolated by Git provider and API base URL. Use the verified connection URL for self-managed GitLab or GitHub Enterprise.</p>
        <p className="font-mono text-[11px] text-[var(--ls-text-tertiary)]">origin {view.origin_scope_kind} · revision {view.revision} · {view.content_sha256.slice(0, 12)}</p>
      </section>
      <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_330px]"><section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-6"><div className="flex items-center gap-3"><span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><BrainCircuit className="size-5" /></span><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-lg font-semibold text-[var(--ls-text)]">Primary model route</h2><HelpHint label="Primary model route">Snapshotted when a review run is admitted.</HelpHint></div></div></div><div className="mt-6 grid gap-5 sm:grid-cols-2"><Select label="Provider" onChange={(value) => chooseProvider(value as RouteContent["provider"])} options={["deployment", "openai-compatible", "anthropic"]} value={content.provider} /><Select label="Protocol" onChange={(value) => update({ protocol: value as RouteContent["protocol"] })} options={content.provider === "anthropic" ? ["anthropic-messages"] : content.provider === "deployment" ? ["deployment"] : ["openai-chat"]} value={content.protocol} /><Field disabled={!content.enabled} label="HTTPS model endpoint" onChange={(value) => update({ base_url: value })} placeholder="https://gateway.example/v1/chat/completions" value={content.base_url} /><Field disabled={!content.enabled} label="Model" onChange={(value) => update({ model: value })} placeholder="deepseek-v4-flash" value={content.model} /><Field disabled={!content.enabled} help="Use secret://… with an external broker, or env://OPEN_REVIEW_MODEL_SECRET_* for self-hosted mounted secrets." label="Credential reference" onChange={(value) => update({ credential_ref: value })} placeholder="env://OPEN_REVIEW_MODEL_SECRET_PRIMARY" value={content.credential_ref} /><Select label="Reasoning effort" onChange={(value) => update({ effort: value as RouteContent["effort"] })} options={["low", "medium", "high"]} value={content.effort} /><NumberField label="Max prompt tokens" onChange={(value) => update({ max_prompt_tokens: value })} value={content.max_prompt_tokens} /><NumberField label="Run token budget" onChange={(value) => update({ token_budget: value })} value={content.token_budget} /><NumberField label="Subtask timeout (minutes)" onChange={(value) => update({ subtask_timeout_minutes: value })} value={content.subtask_timeout_minutes} />{view.requested_scope_kind === "tenant" ? <NumberField label="Tenant concurrent reviews" max={64} onChange={(value) => update({ max_concurrent_runs: value })} value={content.max_concurrent_runs} /> : <p className="rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3 text-xs leading-5 text-[var(--ls-text-secondary)]">Tenant concurrency is controlled at Workspace scope. Repository routes may change provider routing but cannot change shared execution capacity.</p>}</div></section>
      <aside className="space-y-4 xl:sticky xl:top-20 xl:self-start">
        <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] p-5"><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Publish route</h2><HelpHint label="Publish route">Future runs retain this route hash. Existing runs remain pinned to their admission snapshot.</HelpHint></div>{!readOnlyPreview && !view.inherited && view.revision > 0 ? <label className="mt-4 block"><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">Change reason</span><textarea className="luminous-focus min-h-20 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-2 text-xs text-[var(--ls-text)]" onChange={(event) => setApprovalReason(event.target.value)} placeholder="Why is this model route change needed?" value={approvalReason} /></label> : null}{notice ? <p className={cn("mt-4 rounded-[10px] px-3 py-2.5 text-xs", notice.tone === "success" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : "bg-red-500/10 text-[var(--ls-critical-text)]")}>{notice.text}</p> : null}<button className="luminous-focus mt-4 inline-flex h-10 w-full items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] text-sm font-medium text-white disabled:opacity-45" disabled={!dirty || Boolean(pending) || Boolean(pendingChange)} onClick={save} type="button">{pending === "save" ? <LoaderCircle className="size-4 animate-spin" /> : <Save className="size-4" />}{view.inherited || view.revision === 0 ? " Save initial route" : " Request independent approval"}</button>{view.requested_scope_kind === "repository" && !view.inherited ? <button className="luminous-focus mt-2 inline-flex h-10 w-full items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] text-sm text-[var(--ls-text-secondary)]" disabled={Boolean(pending) || Boolean(pendingChange)} onClick={restore} type="button">{pending === "restore" ? <LoaderCircle className="size-4 animate-spin" /> : <RefreshCcw className="size-4" />} Request inheritance restore</button> : null}{pendingChange ? <p className="mt-3 text-[11px] leading-5 text-[var(--ls-warning-text)]">A {pendingChange.operation === "restore_inheritance" ? "restore" : "route change"} request is awaiting independent review. Active revision {view.revision} remains in force.</p> : null}</section>
        {changesSource === "live" && matchingChanges.length > 0 ? <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Route approvals</h2>{matchingChanges.slice(0, 3).map((change) => <div className="mt-3 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3" key={change.id}><div className="flex items-center justify-between gap-2"><span className="text-xs font-semibold text-[var(--ls-text)]">{change.operation === "restore_inheritance" ? "Restore inheritance" : "Model route update"}</span><span className="rounded-full bg-[var(--ls-accent-soft)] px-2 py-0.5 text-[10px] font-semibold text-[var(--ls-accent)]">{change.state}</span></div><p className="mt-2 text-[11px] leading-5 text-[var(--ls-text-secondary)]">{change.reason}</p><p className="mt-2 font-mono text-[10px] text-[var(--ls-text-tertiary)]">base r{change.base_revision} · {change.proposed_content_sha256.slice(0, 12)}</p>{change.can_decide ? <><textarea className="luminous-focus mt-3 min-h-16 w-full rounded-[8px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2 py-1.5 text-[11px] text-[var(--ls-text)]" onChange={(event) => setApprovalComment(event.target.value)} placeholder="Decision note (optional)" value={approvalComment} /><div className="mt-2 grid grid-cols-2 gap-2"><button className="luminous-focus h-8 rounded-[8px] bg-[var(--ls-accent)] text-xs font-semibold text-white disabled:opacity-45" disabled={Boolean(pending)} onClick={() => decideChange(change, "approved")} type="button">Approve</button><button className="luminous-focus h-8 rounded-[8px] border border-[var(--ls-line-strong)] text-xs font-semibold text-[var(--ls-text-secondary)] disabled:opacity-45" disabled={Boolean(pending)} onClick={() => decideChange(change, "rejected")} type="button">Reject</button></div></> : null}</div>)}</section> : changesDetail ? <p className="text-[11px] leading-5 text-[var(--ls-text-tertiary)]">{changesDetail}</p> : null}
        <SectionDisclosure title={t("Secret boundary")} ><div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><KeyRound className="size-4 text-[var(--ls-accent)]" /> Secret boundary</div><p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">Only the reference is stored. The runner resolves it immediately before OCR and puts the value only in the child process environment.</p></SectionDisclosure>
        <section className="rounded-[18px] border border-amber-500/25 bg-amber-500/[0.06] p-5"><div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-warning-text)]"><Sparkles className="size-4" /> Connectivity proof</div><p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">Saved configuration is not provider proof. A probe sends the fixed text “Reply exactly OK.”, no repository content, with at most 4 completion tokens.</p>{probeSource !== "live" ? <p className="mt-3 text-xs leading-5 text-[var(--ls-warning-text)]">{probeDetail ?? "Probe history is currently unavailable."}</p> : <><label className="mt-4 flex items-start gap-2.5 text-xs leading-5 text-[var(--ls-text-secondary)]"><input checked={confirmProbe} className="mt-0.5 size-4 accent-[var(--ls-accent)]" disabled={!content.enabled || dirty || activeProbe} onChange={(event) => setConfirmProbe(event.target.checked)} type="checkbox" />I understand this explicitly triggers a small, billable model request to <span className="font-mono">{content.base_url || "the saved endpoint"}</span>.</label><button className="luminous-focus mt-3 inline-flex h-10 w-full items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] text-sm font-medium text-[var(--ls-text)] disabled:opacity-45" disabled={!content.enabled || dirty || !confirmProbe || activeProbe || Boolean(pending)} onClick={requestProbe} type="button">{pending === "probe" || activeProbe ? <LoaderCircle className="size-4 animate-spin" /> : <ShieldCheck className="size-4 text-[var(--ls-accent)]" />}{activeProbe ? "Probe in progress" : "Run connectivity probe"}</button>{!content.enabled ? <p className="mt-3 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">Save an enabled external route before this action is available.</p> : dirty ? <p className="mt-3 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">Save or receive approval for the route before probing it.</p> : latestProbe ? <ProbeSummary probe={latestProbe} /> : <p className="mt-3 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">No connectivity probe has been requested for this route.</p>}</>}</section>
      </aside></div>
    </>}
  </fieldset>;
}

function ScopeButton({ active, children, onClick }: { active: boolean; children: React.ReactNode; onClick: () => void }) { return <button className={cn("luminous-focus rounded-[8px] px-3 py-1.5 text-xs font-medium", active ? "bg-[var(--ls-surface)] text-[var(--ls-text)] shadow-[var(--ls-shadow-control)]" : "text-[var(--ls-text-secondary)]")} onClick={onClick} type="button">{children}</button>; }
function Field({ disabled, help, label, onChange, placeholder, value }: { disabled?: boolean; help?: string; label: string; onChange: (value: string) => void; placeholder: string; value: string }) {
  const id = useId();
  return <div><FieldLabel htmlFor={id} label={label} help={help} /><input id={id} className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] disabled:opacity-45" disabled={disabled} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} value={value} /></div>;
}
function Select({ label, onChange, options, value }: { label: string; onChange: (value: string) => void; options: string[]; value: string }) { return <label><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">{label}</span><select className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" onChange={(event) => onChange(event.target.value)} value={value}>{options.map((option) => <option key={option} value={option}>{option}</option>)}</select></label>; }
function NumberField({ label, max, onChange, value }: { label: string; max?: number; onChange: (value: number) => void; value: number }) { return <label><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">{label}</span><input className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" max={max} min={1} onChange={(event) => onChange(Number(event.target.value))} type="number" value={value} /></label>; }
function ProbeSummary({ probe }: { probe: ModelProbeReceipt }) {
  const router = useRouter();
  const active = probe.state === "queued" || probe.state === "running";
  const tone = probe.state === "succeeded" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : probe.state === "failed" ? "bg-red-500/10 text-[var(--ls-critical-text)]" : "bg-amber-500/10 text-[var(--ls-warning-text)]";
  return <div className="mt-4 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3 text-xs"><div className="flex flex-wrap items-center gap-2"><span className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-1 text-[10px] font-semibold", tone)}>{active ? <LoaderCircle className="size-3 animate-spin" /> : probe.state === "succeeded" ? <Check className="size-3" /> : <CircleAlert className="size-3" />}{probe.state}</span><span className="font-mono text-[10px] text-[var(--ls-text-tertiary)]">v{probe.config_revision} · {probe.endpoint_host}</span>{active ? <button className="luminous-focus inline-flex h-7 items-center gap-1 rounded-[7px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2 text-[10px] font-medium text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]" onClick={() => router.refresh()} type="button"><RefreshCw className="size-3" />Refresh status</button> : null}</div>{active ? <p className="mt-2 text-[11px] leading-5 text-[var(--ls-text-secondary)]">The dedicated worker continues this probe after navigation. Refresh manually for the latest durable receipt.</p> : null}{probe.latency_ms !== undefined ? <p className="mt-2 text-[11px] text-[var(--ls-text-secondary)]">Completed in {probe.latency_ms} ms. Provider response retained only as SHA-256 evidence.</p> : null}{probe.error_message ? <p className="mt-2 leading-5 text-[var(--ls-critical-text)]">{probe.error_code}: {probe.error_message}</p> : null}</div>;
}
function decode(value?: Record<string, unknown>): RouteContent { return { enabled: typeof value?.enabled === "boolean" ? value.enabled : fallback.enabled, provider: value?.provider === "openai-compatible" || value?.provider === "anthropic" ? value.provider : "deployment", protocol: value?.protocol === "openai-chat" || value?.protocol === "anthropic-messages" ? value.protocol : "deployment", base_url: typeof value?.base_url === "string" ? value.base_url : "", model: typeof value?.model === "string" ? value.model : "", credential_ref: typeof value?.credential_ref === "string" ? value.credential_ref : "", effort: value?.effort === "medium" || value?.effort === "high" ? value.effort : "low", max_prompt_tokens: typeof value?.max_prompt_tokens === "number" ? value.max_prompt_tokens : 8000, token_budget: typeof value?.token_budget === "number" ? value.token_budget : 128000, subtask_timeout_minutes: typeof value?.subtask_timeout_minutes === "number" ? value.subtask_timeout_minutes : 5, max_concurrent_runs: typeof value?.max_concurrent_runs === "number" ? value.max_concurrent_runs : 2 }; }
