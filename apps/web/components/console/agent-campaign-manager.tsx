"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus, Download, ChevronRight, RefreshCw } from "lucide-react";
import { DropdownMenu } from "radix-ui";
import { SectionDisclosure } from "./section-disclosure";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import type { ProviderInstallation } from "@/lib/control-api";
import type { Campaign, CampaignDetail, CampaignRepository } from "@/lib/agent-campaign";
import { FieldLabel, HelpHint } from "./help-hint";
import { useWorkflowText } from "./ui-language-context";

const field = "w-full rounded-lg border border-[var(--ls-line)] bg-[var(--ls-surface)] p-2 text-sm";
const button = "luminous-focus rounded-lg border border-[var(--ls-line)] px-3 py-2 text-sm disabled:opacity-40";
const panel = "rounded-xl border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 sm:p-5";
const lines = (value: string) => value.split("\n").map(line => line.trim()).filter(Boolean);

export function AgentCampaignManager({ org, installations, canManage, initialID }: { org: string; installations: ProviderInstallation[]; canManage: boolean; initialID?: string }) {
  const t = useWorkflowText();
  const router = useRouter();
  const [composerOpen, setComposerOpen] = useState(false);
  const composerTrigger = useRef<HTMLButtonElement>(null);
  const setComposerExpanded = useCallback((open: boolean) => {
    setComposerOpen(open);
    if (!open) composerTrigger.current?.focus({preventScroll:true});
  }, []);
  const [repositoryQuery, setRepositoryQuery] = useState("");
  const [repositoryLimit, setRepositoryLimit] = useState(20);
  const [targetFilter, setTargetFilter] = useState("all");
  const endpoint = `/api/tenants/${encodeURIComponent(org)}/agent-campaigns`;
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [cursor, setCursor] = useState("");
  const [selected, setSelected] = useState(initialID ?? "");
  const selectedID = useRef(initialID ?? "");
  const [detail, setDetail] = useState<CampaignDetail>();
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [installation, setInstallation] = useState(installations.find(item => item.active && item.verification_state === "verified")?.id ?? "");
  const [inventory, setInventory] = useState<{ repositories: CampaignRepository[]; complete: boolean }>();
  const [inventoryError, setInventoryError] = useState("");
  const [all, setAll] = useState(false);
  const [repos, setRepos] = useState<string[]>([]);
  const [mode, setMode] = useState("scan");
  const [plans, setPlans] = useState<string[]>([]);
  const [reason, setReason] = useState("");
  const requestKey = useRef<{ body: string; key: string } | undefined>(undefined);

  const api = useCallback(async <T,>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    const response = await fetch(path, { method: body === undefined ? "GET" : "POST", body: body === undefined ? undefined : JSON.stringify(body), headers: body === undefined ? undefined : { "Content-Type": "application/json" }, signal });
    const result = await response.json().catch(() => { throw new Error("Campaign operation failed."); });
    if (!response.ok) throw new Error(result.error ?? `HTTP ${response.status}`);
    return result as T;
  }, []);
  const refresh = useCallback(async (signal?: AbortSignal) => {
    const result = await api<{ campaigns: Campaign[]; next_cursor?: string }>(endpoint, undefined, signal);
    setCampaigns(result.campaigns); setCursor(result.next_cursor ?? "");
  }, [api, endpoint]);
  useEffect(() => {
    const controller = new AbortController();
    api<{ campaigns: Campaign[]; next_cursor?: string }>(endpoint, undefined, controller.signal).then(result => {if (!controller.signal.aborted) {setCampaigns(result.campaigns);setCursor(result.next_cursor ?? "");if (!selectedID.current && result.campaigns[0]) {selectedID.current=result.campaigns[0].id;setSelected(result.campaigns[0].id);}}}).catch(err => { if (!controller.signal.aborted) setError(err.message); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [api, endpoint]);
  useEffect(() => {
    if (!installation || !canManage || !composerOpen) return;
    const controller = new AbortController();
    api<{ repositories: CampaignRepository[]; complete: boolean }>(`/api/tenants/${encodeURIComponent(org)}/agent-campaign-repositories?installation_id=${encodeURIComponent(installation)}`, undefined, controller.signal).then(result => {if (!controller.signal.aborted) setInventory(result);}).catch(err => { if (!controller.signal.aborted) setInventoryError(err.message); });
    return () => controller.abort();
  }, [api, org, installation, canManage, composerOpen]);
  const loadDetail = useCallback(async (id: string, signal?: AbortSignal) => {
    const result = await api<CampaignDetail>(`${endpoint}/${encodeURIComponent(id)}`, undefined, signal);
    if (signal?.aborted || selectedID.current !== id) return;
    setDetail(result);
    setPlans(result.targets.flatMap(target => target.detail?.task.state === "awaiting_approval" && target.detail.plan_permissions?.can_approve_plan ? [target.detail.task.id] : []));
  }, [api, endpoint]);
  useEffect(() => {
    if (!selected) return;
    const controller = new AbortController();
    api<CampaignDetail>(`${endpoint}/${encodeURIComponent(selected)}`, undefined, controller.signal).then(result => {if (!controller.signal.aborted) {setDetail(result);setPlans(result.targets.flatMap(target => target.detail?.task.state === "awaiting_approval" && target.detail.plan_permissions?.can_approve_plan ? [target.detail.task.id] : []));}}).catch(err => { if (!controller.signal.aborted) setError(err.message); });
    return () => controller.abort();
  }, [selected, api, endpoint]);
  useEffect(() => {
    if (!detail || busy || detail.summary.closed || !detail.targets.some(target =>
      ["scan_queued", "scanning", "execution_queued", "executing"].includes(target.state) || target.detail?.task.state === "completed")) return;
    const controller = new AbortController();
    let inFlight = false;
    const timer = window.setInterval(async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        const [next, history] = await Promise.all([
          api<CampaignDetail>(`${endpoint}/${encodeURIComponent(selected)}`, undefined, controller.signal),
          api<{ campaigns: Campaign[]; next_cursor?: string }>(endpoint, undefined, controller.signal),
        ]);
        if (controller.signal.aborted || selectedID.current !== selected) return;
        // Refresh evidence without selecting a new or superseding plan on
        // the operator's behalf. A different revision needs explicit review.
        setPlans(current => current.filter(id => {
          const previous = detail.targets.find(target => target.detail?.task.id === id)?.detail?.plans.find(plan => plan.state === "awaiting_approval");
          const pending = next.targets.find(target => target.detail?.task.id === id)?.detail?.plans.find(plan => plan.state === "awaiting_approval");
          return previous && pending && previous.id === pending.id && previous.plan_sha256 === pending.plan_sha256;
        }));
        setDetail(next); setCampaigns(history.campaigns); setCursor(history.next_cursor ?? "");
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : "Campaign operation failed.");
      } finally { inFlight = false; }
    }, 5000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [api, endpoint, selected, detail, busy]);

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (busy) return; setBusy(true); setError(""); setNotice("");
    const data = new FormData(event.currentTarget);
    const input = { title: data.get("title"), mode, requirements: data.get("requirements") ?? "", acceptance_criteria: lines(String(data.get("criteria") ?? "")), paths: lines(String(data.get("paths"))), search: data.get("search") ?? "", regex: mode === "scan" && data.get("regex") === "on", replacement: data.get("replacement") ?? "", expected_matches: data.get("expected") ? Number(data.get("expected")) : undefined, concurrency: Number(data.get("concurrency")), installation_ids: all ? [installation] : [], repositories: all ? [] : repos.map(repository => ({ installation_id: installation, repository })), all_repositories: all };
    const body = JSON.stringify(input);
    if (requestKey.current?.body !== body) requestKey.current = { body, key: crypto.randomUUID() };
    try {
      const result = await api<Campaign>(endpoint, { ...input, idempotency_key: requestKey.current!.key });
      selectedID.current = result.id; setDetail(undefined); setSelected(result.id); setComposerOpen(false); router.replace(`/${encodeURIComponent(org)}/agent-campaigns?campaign=${encodeURIComponent(result.id)}`, {scroll:false}); await refresh(); setNotice(t("Campaign created. Read-only scans are queued."));
    } catch (err) { setError(err instanceof Error ? err.message : t("Campaign operation failed.")); } finally { setBusy(false); }
  }
  async function mutate(action: string) {
    if (!detail || busy) return; setBusy(true); setError(""); setNotice("");
    try {
      if (action === "approve") {
        const approval = detail.targets.flatMap(target => { const task = target.detail; const plan = task?.plans.find(item => item.state === "awaiting_approval"); return task && plan && plans.includes(task.task.id) ? [{ task_id: task.task.id, plan_id: plan.id, revision: plan.revision, sha256: plan.plan_sha256 }] : []; });
        await api(`${endpoint}/${selected}/approve`, { revision: detail.campaign.revision, plans: approval });
      } else await api(`${endpoint}/${selected}/actions`, { revision: detail.campaign.revision, action, reason });
      await loadDetail(selected); await refresh(); setNotice(t("Campaign updated. Inspect the latest evidence."));
    } catch (err) { setError(err instanceof Error ? err.message : t("Campaign operation failed.")); } finally { setBusy(false); }
  }
  const status = (state: string) => ({ scan_queued: t("Scan queued"), scanning: t("Scanning"), scan_complete: t("Scan complete"), no_match: t("No match"), plan_ready: t("Plan ready"), awaiting_approval: t("Awaiting approval"), execution_queued: t("Execution queued"), executing: t("Executing"), completed: t("Draft delivered"), accepted: t("Accepted"), awaiting_acceptance: t("Awaiting acceptance"), reviewing: t("Reviewing"), checks_failed: t("Checks failed"), changes_requested: t("Changes requested"), needs_attention: t("Needs attention"), cancelled: t("Cancelled"), active: t("Active"), paused: t("Paused"), superseded: t("Superseded"), rejected: t("Rejected"), passed: t("Passed"), failed: t("Failed") })[state] ?? state;
  const scansPending = detail?.targets.some(target => target.state === "scan_queued" || target.state === "scanning");

  function selectCampaign(id: string) {
    selectedID.current = id;
    setDetail(undefined); setError(""); setTargetFilter("all"); setSelected(id);
    router.replace(`/${encodeURIComponent(org)}/agent-campaigns?campaign=${encodeURIComponent(id)}`, {scroll:false});
  }
  function refreshCampaigns() {
    setError("");
    refresh().catch(err => setError(err.message));
    if (selected) loadDetail(selected).catch(err => setError(err.message));
  }
  async function loadOlderCampaigns() {
    try {
      const result = await api<{campaigns:Campaign[];next_cursor?:string}>(`${endpoint}?cursor=${encodeURIComponent(cursor)}`);
      setCampaigns(current => [...current, ...result.campaigns.filter(item => !current.some(old => old.id === item.id))]);
      setCursor(result.next_cursor ?? "");
    } catch (err) { setError(err instanceof Error ? err.message : t("Campaign operation failed.")); }
  }

  const filteredRepositories = inventory?.repositories.filter(item => item.repository.toLowerCase().includes(repositoryQuery.trim().toLowerCase())) ?? [];
  const visibleTargets = detail?.targets.filter(target => targetFilter === "all" || (targetFilter === "attention" ? ["needs_attention", "checks_failed", "changes_requested"].includes(target.state) : target.state === "awaiting_approval")) ?? [];

  return <div className="min-w-0 space-y-6">
    <header className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-2"><h1 className="text-2xl font-semibold tracking-tight">{t("Multi-repository campaigns")}</h1><HelpHint label={t("Multi-repository campaigns")}>{t("Freeze a repository selection, scan the requested paths, approve exact plans, then follow verification, repair, review and requirement acceptance for each repository.")}</HelpHint></div>
      <div className="flex flex-wrap items-center gap-2"><Link className={button} href={`/${encodeURIComponent(org)}/agent-work`}>{t("Agent work")}</Link>{canManage ? <button ref={composerTrigger} type="button" aria-expanded={composerOpen} aria-controls="campaign-composer" className={`${button} inline-flex items-center gap-2 border-transparent bg-[var(--ls-accent)] font-medium text-[var(--ls-on-accent)]`} onClick={() => setComposerOpen(current => !current)}><Plus className="size-4" />{t("New campaign")}</button> : null}</div>
    </header>
    {error ? <p role="alert" className="rounded-lg border border-red-500/40 p-3 text-sm text-red-600">{t(error)}</p> : null}
    {notice ? <p role="status" className="text-sm">{notice}</p> : null}
    {canManage ? <SectionDisclosure id="campaign-composer" title={t("New campaign")} description={t("Choose repositories, define the request, then review the prepared plans.")} open={composerOpen} onOpenChange={setComposerExpanded} className={composerOpen ? undefined : "hidden"}>
      <form onSubmit={create}>
        <fieldset disabled={busy} className="grid min-w-0 gap-5 sm:grid-cols-2">
          <div><FieldLabel htmlFor="campaign-title" label={t("Title")} /><input id="campaign-title" name="title" className={field} required minLength={3} maxLength={200} /></div>
          <div><FieldLabel htmlFor="campaign-mode" label={t("Operation")} /><select id="campaign-mode" className={field} value={mode} onChange={event => setMode(event.target.value)}><option value="scan">{t("Read-only check")}</option><option value="replace">{t("Literal replacement")}</option><option value="docs">{t("Documentation update")}</option></select></div>
          <div><FieldLabel htmlFor="campaign-installation" label={t("Provider installation")} /><select id="campaign-installation" className={field} value={installation} onChange={event => {setInventory(undefined);setInventoryError("");setRepos([]);setRepositoryQuery("");setInstallation(event.target.value);}}>{installations.filter(item => item.active && item.verification_state === "verified").map(item => <option key={item.id} value={item.id}>{item.provider} · {item.repository_scope}</option>)}</select></div>
          <div><FieldLabel htmlFor="campaign-paths" label={t("Path scope")} help={t("One pattern per line. Use README.md, docs/** or **. Scans cover eligible text files; binary, symlink and credential paths are excluded and counted.")} /><textarea id="campaign-paths" name="paths" className={field} defaultValue="README.md" required rows={2} /></div>
          <div className="space-y-3 rounded-lg bg-[var(--ls-surface-muted)] p-4 sm:col-span-2">
            <div className="flex flex-wrap items-center justify-between gap-2"><span className="text-sm font-semibold">{t("Repository selection")}</span><span className="text-xs tabular-nums">{t("{count} repositories selected", {count: all ? inventory?.repositories.length ?? 0 : repos.length})}</span></div>
            <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={all} onChange={event => setAll(event.target.checked)} />{t("Select all authorized repositories")}</label>
            {inventoryError ? <p role="alert" className="text-sm text-red-600">{t(inventoryError)}</p> : inventory ? <>
              <p className="text-xs text-[var(--ls-text-secondary)]">{t("{count} repositories in the retained inventory", {count:inventory.repositories.length})} · {inventory.complete ? t("Inventory complete") : t("Inventory incomplete — refresh the connection before selecting all")}</p>
              {!all ? <>
                <FieldLabel htmlFor="campaign-repository-query" label={t("Search repositories")} /><input id="campaign-repository-query" type="search" className={field} value={repositoryQuery} onChange={event => {setRepositoryQuery(event.target.value);setRepositoryLimit(20);}} />
                {repos.length ? <div className="flex max-h-28 flex-wrap gap-2 overflow-auto">{repos.map(repo => <button key={repo} type="button" className="luminous-focus max-w-full rounded-md border border-[var(--ls-line)] px-2 py-1 text-xs" aria-label={t("Remove {repository}", {repository:repo})} onClick={() => setRepos(current => current.filter(item => item !== repo))}><span className="break-all">{repo}</span><span aria-hidden="true"> ×</span></button>)}</div> : null}
                <div className="max-h-56 overflow-auto rounded-lg border border-[var(--ls-line)] bg-[var(--ls-surface)] p-2">{filteredRepositories.slice(0,repositoryLimit).map(item => <label key={item.repository} className="flex items-center gap-2 rounded-md px-2 py-2 text-sm hover:bg-[var(--ls-surface-muted)]"><input type="checkbox" checked={repos.includes(item.repository)} onChange={event => setRepos(current => event.target.checked ? [...current,item.repository] : current.filter(repo => repo !== item.repository))} /><span className="break-all">{item.repository}</span></label>)}{!filteredRepositories.length ? <p className="p-2 text-sm">{t("No matching repositories.")}</p> : null}</div>
                {filteredRepositories.length > repositoryLimit ? <button type="button" className={button} onClick={() => setRepositoryLimit(current => current+20)}>{t("Show more repositories")}</button> : null}
              </> : null}
            </> : <p className="text-sm">{t("Loading repository inventory…")}</p>}
          </div>
          <div><FieldLabel htmlFor="campaign-search" label={t("Search text")} /><input id="campaign-search" name="search" className={field} required={mode !== "docs"} maxLength={1000} />{mode === "scan" ? <label className="mt-2 flex items-center gap-2 text-xs"><input name="regex" type="checkbox" />{t("Regular expression")}</label> : null}</div>
          {mode === "replace" ? <div><FieldLabel htmlFor="campaign-replacement" label={t("Replacement text")} /><textarea id="campaign-replacement" name="replacement" className={field} maxLength={4000} /><FieldLabel htmlFor="campaign-expected" label={t("Expected matches per repository")} help={t("Optional strict match count. A different count blocks execution and requires a new scan or request.")} /><input id="campaign-expected" type="number" min={1} max={100000} name="expected" className={field} /></div> : null}
          {mode !== "scan" ? <><div className="sm:col-span-2"><FieldLabel htmlFor="campaign-requirements" label={t("Requirements")} /><textarea id="campaign-requirements" name="requirements" rows={4} className={field} required minLength={80} maxLength={12000} /></div><div className="sm:col-span-2"><FieldLabel htmlFor="campaign-criteria" label={t("Acceptance criteria")} help={t("One verifiable criterion per line. Every criterion needs independent verification evidence and a human acceptance decision at the exact delivered commit.")} /><textarea id="campaign-criteria" name="criteria" rows={3} className={field} required /></div></> : null}
          <div><FieldLabel htmlFor="campaign-concurrency" label={t("Maximum concurrent repositories")} /><select id="campaign-concurrency" name="concurrency" className={field} defaultValue="2">{[1,2,5,10].map(value => <option key={value}>{value}</option>)}</select></div>
          <div className="flex flex-wrap items-end justify-end gap-2"><button type="button" className={button} onClick={() => setComposerExpanded(false)}>{t("Close")}</button><button className={`${button} bg-[var(--ls-accent)] font-medium text-[var(--ls-on-accent)]`} disabled={!inventory || (all ? !inventory.complete || !inventory.repositories.length : !repos.length)}>{t("Create and scan")}</button></div>
        </fieldset>
      </form>
    </SectionDisclosure> : <p className="text-sm">{t("Workspace owner/admin permission is required to create or control campaigns.")}</p>}
    <div className="space-y-2 lg:hidden">
      <label htmlFor="campaign-history-select" className="sr-only">{t("Campaign history")}</label>
      <div className="flex min-w-0 items-center gap-2">
        <select id="campaign-history-select" className={`${field} min-w-0 flex-1`} value={selected} disabled={busy || loading || !campaigns.length} onChange={event=>selectCampaign(event.target.value)}>
          {!campaigns.some(campaign=>campaign.id===selected) ? <option value={selected}>{loading?t("Loading…"):t("Campaign history")}</option> : null}
          {campaigns.map(campaign=><option key={campaign.id} value={campaign.id}>{campaign.input.title}</option>)}
        </select>
        <button type="button" className={`${button} shrink-0`} aria-label={t("Refresh")} disabled={busy} onClick={refreshCampaigns}><RefreshCw aria-hidden="true" className="size-4" /></button>
      </div>
      {cursor ? <button type="button" className={button} onClick={loadOlderCampaigns}>{t("Older campaigns")}</button> : null}
    </div>
    <div className="grid min-w-0 gap-5 lg:grid-cols-[240px_minmax(0,1fr)]">
      <section className={`${panel} hidden h-fit lg:block`} aria-label={t("Campaign history")}>
        <div className="mb-3 flex items-center justify-between gap-2"><h2 className="text-sm font-semibold">{t("Campaign history")}</h2><button className={`${button} px-2 py-1 text-xs`} disabled={busy} onClick={refreshCampaigns}>{t("Refresh")}</button></div>
        {loading ? <p className="text-sm">{t("Loading…")}</p> : campaigns.length ? <div className="max-h-80 space-y-2 overflow-auto lg:max-h-[65vh]">{campaigns.map(campaign => <button key={campaign.id} className={`${button} w-full text-left ${selected === campaign.id ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)]" : ""}`} disabled={busy} aria-pressed={selected === campaign.id} onClick={() => selectCampaign(campaign.id)}><span className="block break-words font-medium">{campaign.input.title}</span><span className="mt-1 block text-xs text-[var(--ls-text-secondary)]">{status(campaign.state)}</span></button>)}</div> : !error ? <p className="text-sm">{t("No campaigns yet.")}</p> : null}
        {cursor ? <button className={`${button} mt-3`} onClick={loadOlderCampaigns}>{t("Older campaigns")}</button> : null}
      </section>
      <div className="min-w-0">
        {selected && !detail && !error ? <p role="status" className="p-5 text-sm">{t("Loading campaign evidence…")}</p> : null}
        {detail ? <section className={`${panel} space-y-5`}>
          <div className="flex flex-wrap items-start justify-between gap-3"><div className="min-w-0"><h2 className="break-words text-lg font-semibold tracking-tight">{detail.campaign.input.title}</h2><p className="mt-1 text-sm text-[var(--ls-text-secondary)]">{status(detail.campaign.state)} · {detail.summary.closed?t("All required outcomes closed"):t("Execution or acceptance remains open")}</p></div><DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button" className={`${button} inline-flex shrink-0 items-center gap-2`}><Download aria-hidden="true" className="size-4" />{t("Export report")}</button></DropdownMenu.Trigger><DropdownMenu.Content align="end" sideOffset={8} collisionPadding={16} className="z-50 min-w-40 max-w-[calc(100vw-2rem)] rounded-lg border border-[var(--ls-line)] bg-[var(--ls-surface)] p-1 shadow-lg">{["markdown","csv","json"].map(format=><DropdownMenu.Item asChild key={format}><a className="luminous-focus block cursor-pointer rounded-md px-3 py-2 text-sm text-[var(--ls-text)] outline-none data-[highlighted]:bg-[var(--ls-surface-muted)]" href={`${endpoint}/${selected}/report?format=${format}`}>{t("Export {format}",{format:format==="markdown"?"Markdown":format.toUpperCase()})}</a></DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Root></div>
          <div className="flex flex-wrap gap-2"><span className="rounded-md bg-[var(--ls-surface-muted)] px-3 py-2 text-sm">{t("{count} repositories",{count:detail.summary.total})}</span>{Object.entries(detail.summary.counts).map(([state,count])=><span key={state} className="rounded-md bg-[var(--ls-surface-muted)] px-3 py-2 text-sm">{status(state)}: {count}</span>)}</div>
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--ls-line)] pt-4"><h3 className="text-sm font-semibold">{t("Repository outcomes")}</h3><label className="flex items-center gap-2 text-xs"><span className="shrink-0">{t("Show")}</span><select className={`${field} w-auto min-w-0`} value={targetFilter} onChange={event=>setTargetFilter(event.target.value)}><option value="all">{t("All repositories")}</option><option value="attention">{t("Needs attention")}</option><option value="approval">{t("Awaiting approval")}</option></select></label></div>
          <div className="space-y-3">{visibleTargets.map(target=>{const task=target.detail;const plan=task?.plans.find(item=>item.state==="awaiting_approval");const draft=task?.attempts.find(item=>item.pull_request_url);return <article key={target.id} className="min-w-0 rounded-lg border border-[var(--ls-line)] p-4">
            <div className="flex flex-wrap items-center justify-between gap-2"><h4 className="break-all text-sm font-semibold">{target.repository}</h4><span className="rounded-md bg-[var(--ls-surface-muted)] px-2 py-1 text-xs">{status(target.state)}</span></div>
            <div className="mt-2 flex flex-wrap gap-3 text-xs text-[var(--ls-text-secondary)]"><span>{t("Scanned {count} files",{count:target.scan.files_scanned??0})}</span><span>{t("{count} matches",{count:target.scan.matches??0})}</span><span>{target.scan.complete?t("Scan complete"):t("Scan incomplete")}</span></div>
            {target.error_code?<p role="alert" className="mt-3 text-sm text-[var(--ls-warning-text)]">{target.error_code} · {t("Inspect the evidence before retrying. Existing task attempts and budgets are retained.")}</p>:null}
            {task?.acceptance?.state==="needs_attention"?<p className="mt-3 text-sm leading-6 text-[var(--ls-warning-text)]">{t(task.acceptance.reason ?? "")}</p>:null}
            <div className="mt-3 flex flex-wrap items-center gap-4">{task?<Link className="luminous-focus inline-flex items-center gap-1 text-sm font-medium text-[var(--ls-accent)]" href={`/${encodeURIComponent(org)}/agent-work?task=${encodeURIComponent(task.task.id)}`}>{t("View task details")}<ChevronRight className="size-4" /></Link>:null}{draft?<a className="luminous-focus text-sm text-[var(--ls-accent)]" href={draft.pull_request_url} target="_blank" rel="noreferrer">{t("Open draft pull request")}</a>:null}</div>
            {plan&&task?<div className="mt-3 space-y-3 border-t border-[var(--ls-line)] pt-3"><details><summary className="luminous-focus cursor-pointer text-sm font-medium">{t("Preview plan revision {revision}",{revision:plan.revision})}</summary><code className="block break-all py-2 text-xs">{plan.plan_sha256}</code><pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-md bg-[var(--ls-surface-muted)] p-3 text-xs">{plan.summary}</pre></details><label className="flex items-center gap-2 text-sm"><input type="checkbox" disabled={!canManage||!task.plan_permissions?.can_approve_plan||busy} checked={plans.includes(task.task.id)} onChange={event=>setPlans(current=>event.target.checked?[...current,task.task.id]:current.filter(id=>id!==task.task.id))} />{t("Approve this exact plan")}</label>{!task.plan_permissions?.can_approve_plan?<p className="text-xs">{t("Approval is blocked by the current role, self-approval policy, or workflow budget.")}</p>:null}</div>:null}
            <details className="mt-3 border-t border-[var(--ls-line)] pt-3"><summary className="luminous-focus cursor-pointer text-xs font-medium text-[var(--ls-text-secondary)]">{t("Scan and verification evidence")}</summary><div className="mt-3 space-y-2 text-xs"><code className="block break-all">{target.scan.base_sha}</code><p>{t("Excluded {count} files",{count:target.scan.files_excluded??0})}</p>{target.scan.files?.map(file=><p key={file.path} className="break-all"><code>{file.path}</code> · {file.matches} · <code>{file.sha256}</code></p>)}{task?.acceptance?.verification_criteria?.map((criterion,index)=><p key={index}>{criterion.criterion} · {status(criterion.status)} · {criterion.evidence}</p>)}</div></details>
          </article>;})}{!visibleTargets.length?<p className="py-4 text-sm text-[var(--ls-text-secondary)]">{t("No repositories in this view.")}</p>:null}</div>
          {canManage&&detail.targets.some(target=>target.detail?.plans.some(plan=>plan.state==="awaiting_approval"))?<button className={`${button} bg-[var(--ls-accent)] text-[var(--ls-on-accent)]`} disabled={busy||scansPending||!plans.length||detail.campaign.state!=="active"} onClick={()=>mutate("approve")}>{t("Approve selected plans ({count})",{count:plans.length})}</button>:null}
          <SectionDisclosure title={t("Frozen request and criteria")}><div className="space-y-3 text-sm"><code className="block break-all text-xs">{detail.campaign.request_sha256}</code><p className="whitespace-pre-wrap">{detail.campaign.input.requirements}</p><ul className="list-inside list-disc">{detail.campaign.input.acceptance_criteria.map((criterion,index)=><li key={index}>{criterion}</li>)}</ul><code className="break-all">{detail.campaign.input.paths.join(", ")}</code></div></SectionDisclosure>
          {canManage?<SectionDisclosure title={t("Batch controls")} description={t("Pause, retry or cancel with a recorded reason.")}><div className="space-y-3"><HelpHint label={t("Batch controls")}>{t("Pause stops new execution leases; an already running approved task may finish. Cancel revokes active leases, but a provider write already in flight needs reconciliation. Retry prepares a new plan without resetting attempts or budgets.")}</HelpHint><FieldLabel htmlFor="campaign-reason" label={t("Action reason")} /><input id="campaign-reason" className={field} value={reason} maxLength={1000} onChange={event=>setReason(event.target.value)} /><div className="flex flex-wrap gap-2">{["pause","resume","retry_failed","cancel"].filter(action=>action!=="resume"||detail.campaign.state==="paused").map(action=><button key={action} className={button} disabled={busy||reason.trim().length<3||detail.summary.closed||["cancelled","completed"].includes(detail.campaign.state)||(action==="pause"&&detail.campaign.state!=="active")||(action==="retry_failed"&&!detail.targets.some(target=>target.state==="needs_attention"&&!target.detail?.attempts.some(attempt=>attempt.state==="succeeded")))} onClick={()=>mutate(action)}>{({pause:t("Pause queued work"),resume:t("Resume"),retry_failed:t("Retry failed targets"),cancel:t("Cancel remaining work")})[action]}</button>)}</div></div></SectionDisclosure>:null}
        </section>:null}
      </div>
    </div>
  </div>;
}
