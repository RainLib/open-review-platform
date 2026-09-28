"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import {
  Braces,
  BookOpen,
  Check,
  ClipboardCheck,
  Copy,
  ChevronRight,
  CircleAlert,
  Download,
  FileText,
  Filter,
  Eye,
  Languages,
  Library,
  LoaderCircle,
  MessageSquareText,
  RefreshCcw,
  Save,
  ShieldCheck,
  SlidersHorizontal,
  Sparkles,
  Trash2,
} from "lucide-react";

import type {
  DataSource,
  IssueFormatTemplate,
  ReviewConfigHistory,
  ReviewConfigSection,
  ReviewConfigView,
} from "@/lib/control-api";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { ReviewConfigVersionHistory } from "@/components/console/review-config-version-history";
import { cn } from "@/lib/utils";

const sections: Array<{
  key: ReviewConfigSection;
  label: string;
  summary: string;
  icon: typeof SlidersHorizontal;
}> = [
  { key: "general", label: "General", summary: "Review cadence, drafts, language and merge gate", icon: SlidersHorizontal },
  { key: "categories", label: "Categories", summary: "Detection families and publication thresholds", icon: ShieldCheck },
  { key: "filters", label: "Review filters", summary: "Paths, authors, labels and target branches", icon: Filter },
  { key: "prompts", label: "Custom prompts", summary: "Trusted instructions and repository context", icon: Braces },
  { key: "issue-triage", label: "Issue triage", summary: "Issue templates, response format and repository overrides", icon: ClipboardCheck },
  { key: "summary", label: "PR summary", summary: "Evidence sections and output budget", icon: FileText },
  { key: "messages", label: "Custom messages", summary: "Lifecycle comments shown in the provider", icon: MessageSquareText },
];

type Notice = { tone: "success" | "error"; text: string } | undefined;
type SelectOption = string | { value: string; label: string };

const reviewLanguageOptions: SelectOption[] = [
  { value: "en", label: "English" },
  { value: "zh-CN", label: "简体中文" },
  { value: "ja", label: "日本語" },
  { value: "es", label: "Español" },
];

const issueResponseLanguageOptions: SelectOption[] = [
  { value: "inherit", label: "Follow the Issue language" },
  ...reviewLanguageOptions,
];

type ReviewConfigEditorProps = {
  config?: ReviewConfigView;
  detail?: string;
  history?: ReviewConfigHistory;
  historyDetail?: string;
  issueFormatDetail?: string;
  issueFormatSource?: DataSource;
  issueFormatTemplates?: IssueFormatTemplate[];
  org: string;
  section: ReviewConfigSection;
  source: DataSource;
};

// Reset the editor by identity rather than mirroring server props through an
// effect. A route/scope change now mounts a fresh draft, while local edits
// remain intact during ordinary re-renders.
export function ReviewConfigEditor(props: ReviewConfigEditorProps) {
  const scope = props.config;
  const draftKey = [
    props.section,
    scope?.requested_scope_kind ?? "tenant",
    scope?.requested_scope_ref ?? "",
    scope?.requested_scope_provider ?? "",
    scope?.requested_scope_api_base_url ?? "",
    scope?.revision ?? 0,
    scope?.content_sha256 ?? "",
  ].join("|");
  return <ReviewConfigEditorDraft key={draftKey} {...props} />;
}

function ReviewConfigEditorDraft({
  config,
  detail,
  history,
  historyDetail,
  issueFormatDetail,
  issueFormatSource = "live",
  issueFormatTemplates = [],
  org,
  section,
  source,
}: ReviewConfigEditorProps) {
  const router = useRouter();
  const active = sections.find((item) => item.key === section) ?? sections[0];
  const initialContent = config?.content ?? {};
  const [content, setContent] = useState<Record<string, unknown>>(initialContent);
  const [baseline, setBaseline] = useState(() => stableJSON(initialContent));
  const [view, setView] = useState(config);
  const [repository, setRepository] = useState(config?.requested_scope_ref ?? "");
  const [scopeProvider, setScopeProvider] = useState<"github" | "gitlab">(config?.requested_scope_provider ?? "github");
  const [scopeAPIBaseURL, setScopeAPIBaseURL] = useState(config?.requested_scope_api_base_url ?? "https://api.github.com");
  const [pending, setPending] = useState<"save" | "restore">();
  const [notice, setNotice] = useState<Notice>();
  const dirty = stableJSON(content) !== baseline;
  const readOnly = source !== "live";
  const query = view?.requested_scope_kind === "repository" && view.requested_scope_ref
    ? scopeQuery(view.requested_scope_ref, view.requested_scope_provider, view.requested_scope_api_base_url)
    : "";

  function openScope(kind: "tenant" | "repository") {
    if (kind === "tenant") {
      router.push(`/${org}/review-config/${section}`);
      return;
    }
    if (!repository.trim() || !scopeAPIBaseURL.trim()) {
      setNotice({ tone: "error", text: "Choose a provider, its API base URL, and an owner/repository reference before opening repository scope." });
      return;
    }
    router.push(`/${org}/review-config/${section}${scopeQuery(repository.trim(), scopeProvider, scopeAPIBaseURL.trim())}`);
  }

  async function save() {
    if (!view || source !== "live") return;
    const scopeProviderForSave = view.requested_scope_kind === "repository"
      ? (view.requested_scope_provider || scopeProvider)
      : "";
    const scopeAPIBaseURLForSave = view.requested_scope_kind === "repository"
      ? (view.requested_scope_api_base_url || scopeAPIBaseURL)
      : "";
    setPending("save");
    setNotice(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/review-config/${section}`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            scope_kind: view.requested_scope_kind,
            scope_ref: view.requested_scope_ref ?? "",
            scope_provider: scopeProviderForSave,
            scope_api_base_url: scopeAPIBaseURLForSave,
            expected_revision: view.inherited ? 0 : view.revision,
            content,
          }),
        },
      );
      const body = (await response.json()) as ReviewConfigView | { error?: string };
      if (!response.ok || !("content" in body)) {
        throw new Error("error" in body && body.error ? body.error : `Save failed (${response.status}).`);
      }
      setView(body);
      setContent(body.content);
      setBaseline(stableJSON(body.content));
      setNotice({ tone: "success", text: `Saved immutable revision ${body.revision}.` });
      router.replace(body.requested_scope_kind === "repository"
        ? `/${org}/review-config/${section}${scopeQuery(body.requested_scope_ref ?? "", body.requested_scope_provider, body.requested_scope_api_base_url)}`
        : `/${org}/review-config/${section}`);
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : "Review settings could not be saved." });
    } finally {
      setPending(undefined);
    }
  }

  async function restoreInheritance() {
    if (!view || source !== "live" || view.requested_scope_kind !== "repository" || view.inherited) return;
    setPending("restore");
    setNotice(undefined);
    const params = new URLSearchParams({
      scope_kind: "repository",
      scope_ref: view.requested_scope_ref ?? "",
      scope_provider: view.requested_scope_provider ?? "",
      scope_api_base_url: view.requested_scope_api_base_url ?? "",
      expected_revision: String(view.revision),
    });
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/review-config/${section}?${params.toString()}`,
        { method: "DELETE" },
      );
      const body = (await response.json()) as ReviewConfigView | { error?: string };
      if (!response.ok || !("content" in body)) {
        throw new Error("error" in body && body.error ? body.error : `Restore failed (${response.status}).`);
      }
      setView(body);
      setContent(body.content);
      setBaseline(stableJSON(body.content));
      setNotice({ tone: "success", text: "Repository override removed. This scope now inherits again." });
      router.replace(body.requested_scope_kind === "repository"
        ? `/${org}/review-config/${section}${scopeQuery(body.requested_scope_ref ?? "", body.requested_scope_provider, body.requested_scope_api_base_url)}`
        : `/${org}/review-config/${section}`);
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : "Inheritance could not be restored." });
    } finally {
      setPending(undefined);
    }
  }

  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">Policy studio</p>
          <h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">Review settings</h1>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-[var(--ls-text-secondary)]">Tune what the reviewer analyzes and how evidence is published. Repository settings inherit until you create an explicit override.</p>
        </div>
        <span className={cn("inline-flex w-fit items-center gap-2 rounded-full px-3 py-1.5 text-xs", source === "live" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : "bg-amber-500/10 text-[var(--ls-warning-text)]")}>
          <span className="size-1.5 rounded-full bg-current" />
          {source === "live" ? "Live control-plane data" : source.replaceAll("_", " ")}
        </span>
      </header>

      <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="Review settings sections">
        {sections.map((item) => {
          const Icon = item.icon;
          return (
            <Link
              aria-current={item.key === section ? "page" : undefined}
              aria-selected={item.key === section}
              className={cn("luminous-focus relative inline-flex h-12 shrink-0 items-center gap-2 rounded-t-[10px] px-3.5 text-sm font-medium", item.key === section ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")}
              href={`/${org}/review-config/${item.key}${query}`}
              key={item.key}
              role="tab"
              tabIndex={item.key === section ? 0 : -1}
            >
              <Icon className="size-4" /> {item.label}
              {item.key === section ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}
            </Link>
          );
        })}
      </TabStateRouter>

      <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 shadow-[var(--ls-shadow-control)] sm:p-5">
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_auto] xl:items-center">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="rounded-full bg-[var(--ls-accent-soft)] px-2.5 py-1 text-xs font-semibold text-[var(--ls-accent)]">{view?.requested_scope_kind === "repository" ? "Repository" : "Workspace"}</span>
              <ChevronRight className="size-3.5 text-[var(--ls-text-tertiary)]" />
              <span className="text-sm font-medium text-[var(--ls-text)]">{view?.requested_scope_ref || org}</span>
              {view?.requested_scope_provider ? <span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 font-mono text-[11px] text-[var(--ls-text-secondary)]">{view.requested_scope_provider} · {providerHost(view.requested_scope_api_base_url)}</span> : null}
              {view?.inherited ? <span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">Inherited from {view.origin_scope_kind}</span> : null}
              {dirty ? <span className="rounded-full bg-amber-500/10 px-2.5 py-1 text-xs font-medium text-[var(--ls-warning-text)]">Unsaved changes</span> : <span className="inline-flex items-center gap-1 text-xs text-[var(--ls-text-tertiary)]"><Check className="size-3.5" /> Up to date</span>}
            </div>
            <p className="mt-2 font-mono text-[11px] text-[var(--ls-text-tertiary)]">origin {view?.origin_scope_kind ?? "unavailable"}{view?.origin_scope_provider ? `/${view.origin_scope_provider}@${providerHost(view.origin_scope_api_base_url)}` : ""} · revision {view?.revision ?? 0} · {view?.content_sha256?.slice(0, 12) ?? "no hash"}</p>
          </div>
          <div className="flex flex-col gap-2 sm:items-center">
            <div className="inline-flex rounded-[10px] bg-[var(--ls-surface-muted)] p-1">
              <button className={scopeButton(view?.requested_scope_kind !== "repository")} onClick={() => openScope("tenant")} type="button">Workspace</button>
              <button className={scopeButton(view?.requested_scope_kind === "repository")} onClick={() => openScope("repository")} type="button">Repository</button>
            </div>
            <div className="grid gap-2 sm:grid-cols-[112px_minmax(190px,1fr)_minmax(180px,1fr)]">
              <select aria-label="Repository provider" className="luminous-focus h-9 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" onChange={(event) => {
                const provider = event.target.value as "github" | "gitlab";
                setScopeProvider(provider);
                setScopeAPIBaseURL(provider === "github" ? "https://api.github.com" : "https://gitlab.com/api/v4");
              }} value={scopeProvider}>
                <option value="github">GitHub</option>
                <option value="gitlab">GitLab</option>
              </select>
              <input aria-label="Provider API base URL" className="luminous-focus h-9 min-w-56 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]" onChange={(event) => setScopeAPIBaseURL(event.target.value)} placeholder="https://gitlab.example.com/api/v4" value={scopeAPIBaseURL} />
              <input aria-label="Repository scope" className="luminous-focus h-9 min-w-56 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]" onChange={(event) => setRepository(event.target.value)} placeholder="owner/repository" value={repository} />
            </div>
          </div>
        </div>
        {view?.requested_scope_kind === "repository" ? <p className="mt-3 text-xs leading-5 text-[var(--ls-text-secondary)]">Repository overrides are isolated by provider and API base URL. A self-managed GitLab URL must match an active, verified connection that includes this repository.</p> : null}
      </section>

      {!view || (source !== "live" && source !== "demo") ? (
        <section className="grid min-h-72 place-items-center rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-8 text-center">
          <div><CircleAlert className="mx-auto size-7 text-[var(--ls-warning-text)]" /><h2 className="mt-4 text-lg font-semibold text-[var(--ls-text)]">Settings are unavailable</h2><p className="mx-auto mt-2 max-w-lg text-sm leading-6 text-[var(--ls-text-secondary)]">{detail ?? "The control plane did not return a configuration."}</p></div>
        </section>
      ) : (
        <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_320px]">
          <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
            <div className="mb-6">
              <h2 className="text-xl font-semibold tracking-[-0.03em] text-[var(--ls-text)]">{active.label}</h2>
              <p className="mt-1 text-sm text-[var(--ls-text-secondary)]">{active.summary}</p>
            </div>
            {readOnly ? <p className="mb-5 rounded-[12px] border border-amber-500/20 bg-amber-500/[0.07] px-3.5 py-3 text-xs leading-5 text-[var(--ls-warning-text)]" id="review-config-demo-boundary">Demo preview uses fixture policy data. Editing and publishing are disabled; version provenance remains read-only.</p> : null}
            <fieldset aria-describedby={readOnly ? "review-config-demo-boundary" : undefined} className="min-w-0 disabled:opacity-70" disabled={readOnly}>
              <SectionFields
                content={content}
                issueFormatDetail={issueFormatDetail}
                issueFormatSource={issueFormatSource}
                issueFormatTemplates={issueFormatTemplates}
                onCatalogChanged={() => router.refresh()}
                org={org}
                section={section}
                setContent={setContent}
                source={source}
              />
            </fieldset>
          </section>

          <aside className="space-y-4 xl:sticky xl:top-20 xl:self-start">
            <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] p-5 shadow-[var(--ls-shadow-control)]">
              <h2 className="text-sm font-semibold text-[var(--ls-text)]">Publish configuration</h2>
              <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">Saving creates a new immutable revision guarded by the revision shown above. A stale editor is rejected rather than overwriting another administrator.</p>
              {notice ? <p className={cn("mt-4 rounded-[10px] px-3 py-2.5 text-xs leading-5", notice.tone === "success" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : "bg-red-500/10 text-[var(--ls-critical-text)]")}>{notice.text}</p> : null}
              <button className="luminous-focus mt-4 inline-flex h-10 w-full items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-medium text-white transition hover:bg-[var(--ls-accent-hover)] disabled:cursor-not-allowed disabled:opacity-45" disabled={readOnly || !dirty || Boolean(pending)} onClick={save} type="button">{pending === "save" ? <LoaderCircle className="size-4 animate-spin" /> : <Save className="size-4" />} Save new revision</button>
              {view.requested_scope_kind === "repository" && !view.inherited ? <button className="luminous-focus mt-2 inline-flex h-10 w-full items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] text-sm font-medium text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)] disabled:opacity-45" disabled={readOnly || Boolean(pending)} onClick={restoreInheritance} type="button">{pending === "restore" ? <LoaderCircle className="size-4 animate-spin" /> : <RefreshCcw className="size-4" />} Restore inheritance</button> : null}
            </section>
            <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5">
              <div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><Sparkles className="size-4 text-[var(--ls-accent)]" /> Effective behavior</div>
              <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">{view.inherited ? `This repository currently uses ${view.origin_scope_kind} revision ${view.revision}. Editing and saving creates its first explicit override.` : `This scope owns revision ${view.revision || "default"}. All new review runs resolve and snapshot this content at admission.`}</p>
            </section>
            <ReviewConfigVersionHistory history={history} historyDetail={historyDetail} org={org} section={section} source={source} view={view} />
          </aside>
        </div>
      )}
    </div>
  );
}

function scopeQuery(repository: string, provider?: string, apiBaseURL?: string) {
  const params = new URLSearchParams({ scope: "repository", repository });
  if (provider) params.set("provider", provider);
  if (apiBaseURL) params.set("api_base_url", apiBaseURL);
  return `?${params.toString()}`;
}

function providerHost(value?: string) {
  try {
    return value ? new URL(value).host : "unqualified legacy scope";
  } catch {
    return value || "unqualified legacy scope";
  }
}

function MergeGateEnforcementNotice() {
  return <details className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 text-xs leading-5 text-[var(--ls-text-secondary)]">
    <summary className="luminous-focus cursor-pointer text-sm font-medium text-[var(--ls-text)]">How to enforce merge blocking</summary>
    <div className="mt-3 space-y-2">
      <p><strong className="text-[var(--ls-text)]">GitHub:</strong> Require the <code>Open Review / Analysis</code> check from this App on every PR target branch. Protecting <code>main</code> does not protect PRs targeting other branches.</p>
      <p><strong className="text-[var(--ls-text)]">GitLab:</strong> Open Review publishes an external commit status. Enable <em>Pipelines must succeed</em> and confirm that the status appears in the pipeline GitLab evaluates for the MR; a status on a different pipeline cannot enforce the gate.</p>
      <p>After setup, verify a failing review against a test PR or MR before relying on the gate.</p>
    </div>
  </details>;
}

function SectionFields({ content, issueFormatDetail, issueFormatSource, issueFormatTemplates, onCatalogChanged, org, section, setContent, source }: { content: Record<string, unknown>; issueFormatDetail?: string; issueFormatSource: DataSource; issueFormatTemplates: IssueFormatTemplate[]; onCatalogChanged: () => void; org: string; section: ReviewConfigSection; setContent: (next: Record<string, unknown>) => void; source: DataSource }) {
  const update = (key: string, value: unknown) => setContent({ ...content, [key]: value });
  if (section === "general") {
    const triggerMode = asString(content.trigger_mode, asBool(content.automatic_review, true) ? "automatic" : "manual");
    const setTriggerMode = (value: string) => setContent({ ...content, trigger_mode: value, automatic_review: value === "automatic" });
    return <div className="space-y-3"><SelectField help="Off rejects new review work. Manual accepts only explicit CLI and @openreview commands. Automatic also admits eligible provider pull requests." label="Review trigger" onChange={setTriggerMode} options={["off", "manual", "automatic"]} value={triggerMode} /><ToggleRow checked={asBool(content.review_drafts)} description="Admit draft pull requests before the author marks them ready; disabled drafts are acknowledged as a policy skip without creating a run." label="Review drafts" onChange={(value) => update("review_drafts", value)} /><ToggleRow checked={asBool(content.rereview_on_push, true)} description="When enabled, a new provider head supersedes stale work. When disabled, synchronize/update events are policy-skipped." label="Re-review on push" onChange={(value) => update("rereview_on_push", value)} /><ToggleRow checked={asBool(content.merge_gate_enabled, true)} description="Publish a pass/fail review check. This setting alone does not enforce a provider merge block." label="Merge gate" onChange={(value) => update("merge_gate_enabled", value)} /><MergeGateEnforcementNotice /><div className="grid gap-4 pt-3 sm:grid-cols-2"><SelectField help="Used for automatic reviews and commands without --mode. The resolved mode is stored on the run, so later policy changes never rewrite queued work." label="Default review depth" onChange={(value) => update("default_review_mode", value)} options={["standard", "deep", "security"]} value={asString(content.default_review_mode, "standard")} /><SelectField label="Review language" onChange={(value) => update("review_language", value)} options={reviewLanguageOptions} value={asString(content.review_language, "en")} /><SelectField label="Minimum blocking severity" onChange={(value) => update("minimum_blocking_severity", value)} options={["low", "medium", "high", "critical"]} value={asString(content.minimum_blocking_severity, "high")} /></div></div>;
  }
  if (section === "categories") {
    return <div className="grid gap-3 sm:grid-cols-2">{["bug", "security", "performance", "maintainability"].map((category) => { const value = asCategory(content[category]); return <article className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4" key={category}><Toggle checked={value.enabled} label={titleCase(category)} onChange={(enabled) => update(category, { ...value, enabled })} /><div className="mt-4"><SelectField label="Publish from" onChange={(minimumSeverity) => update(category, { ...value, minimum_severity: minimumSeverity })} options={["low", "medium", "high", "critical"]} value={value.minimum_severity} /></div></article>; })}</div>;
  }
  if (section === "filters") {
    return <div className="space-y-5"><div className="grid gap-4 sm:grid-cols-2"><ListField label="Include paths" onChange={(value) => update("include_paths", value)} placeholder="src/**, internal/**" value={asStringArray(content.include_paths)} /><ListField label="Exclude paths" onChange={(value) => update("exclude_paths", value)} placeholder="**/vendor/**, **/generated/**" value={asStringArray(content.exclude_paths)} /><ListField help="Admission filter. Exact case-insensitive account names are skipped before a run is created." label="Exclude authors" onChange={(value) => update("exclude_authors", value)} placeholder="dependabot[bot]" value={asStringArray(content.exclude_authors)} /><ListField help="Admission filter. Every listed label must be present on the provider pull request." label="Required labels" onChange={(value) => update("required_labels", value)} placeholder="review-ready" value={asStringArray(content.required_labels)} /><ListField help="Admission filter. Gitignore-shaped patterns match the provider target branch." label="Target branches" onChange={(value) => update("target_branches", value)} placeholder="main, release/**" value={asStringArray(content.target_branches)} /></div><div className="grid gap-3 sm:grid-cols-2"><ToggleRow checked={asBool(content.skip_generated, true)} description="Ignore detected generated sources after risk selection and before model analysis." label="Skip generated files" onChange={(value) => update("skip_generated", value)} /><ToggleRow checked={asBool(content.skip_vendor, true)} description="Ignore vendored dependency trees after risk selection and before model analysis." label="Skip vendor files" onChange={(value) => update("skip_vendor", value)} /></div></div>;
  }
  if (section === "prompts") {
    return <div className="space-y-5"><TextAreaField help="Attached to the immutable run as a runner-owned OCR rule; it is never stored in a provider credential or process environment." label="Trusted system instruction" maxLength={8000} onChange={(value) => update("system_instruction", value)} rows={6} value={asString(content.system_instruction)} /><TextAreaField help="Control-plane context that is attached to the same OCR rule. Repository content remains untrusted input." label="Repository context" maxLength={8000} onChange={(value) => update("repository_context", value)} rows={8} value={asString(content.repository_context)} /><NumberField label="Maximum prompt tokens" max={100000} min={1000} onChange={(value) => update("max_prompt_tokens", value)} value={asNumber(content.max_prompt_tokens, 12000)} /><ToggleRow checked={asBool(content.allow_repository_instructions)} description="Opt in to bounded reads of AGENTS.md, OPENREVIEW.md, .openreview.md, .openreview/instructions.md and .github/openreview.md. Links outside the checkout are ignored." label="Allow repository instructions" onChange={(value) => update("allow_repository_instructions", value)} warning /></div>;
  }
  if (section === "issue-triage") {
    return <IssueTriageFields content={content} formatDetail={issueFormatDetail} formatSource={issueFormatSource} formatTemplates={issueFormatTemplates} onCatalogChanged={onCatalogChanged} org={org} setContent={setContent} source={source} />;
  }
  if (section === "summary") {
    return <SummaryFields content={content} onChange={update} />;
  }
  return <MessageFields content={content} onChange={update} />;
}

type IssueTriagePreset = {
  label: string;
  detail: string;
  required: readonly string[] | null;
  responses: readonly string[] | null;
  collapseSecondary?: boolean;
  maxItems?: number;
};

const fullIssueResponse = ["assessment", "missing_context", "acceptance_criteria", "risk", "affected_areas", "next_steps", "provenance"] as const;

const issueTriagePresets = {
  engineering: { label: "Engineering", detail: "Implementation work with reproducible evidence", required: ["outcome", "reproduction", "expected_behavior", "evidence", "acceptance_criteria"], responses: fullIssueResponse, collapseSecondary: true, maxItems: 6 },
  bug: { label: "Bug report", detail: "Observed versus expected behavior and reproduction", required: ["reproduction", "observed_behavior", "expected_behavior", "evidence", "acceptance_criteria"], responses: fullIssueResponse, collapseSecondary: true, maxItems: 6 },
  feature: { label: "Feature request", detail: "Outcome, impact, boundaries and acceptance", required: ["outcome", "expected_behavior", "impact", "acceptance_criteria", "non_goals"], responses: ["assessment", "missing_context", "acceptance_criteria", "risk", "affected_areas", "next_steps", "provenance"], collapseSecondary: true, maxItems: 5 },
  concise: { label: "Concise", detail: "Small-team contract with a short visible response", required: ["outcome", "expected_behavior", "acceptance_criteria"], responses: ["assessment", "missing_context", "acceptance_criteria", "next_steps"], collapseSecondary: false, maxItems: 3 },
  security: { label: "Security", detail: "Threat evidence, risk and security impact", required: ["outcome", "reproduction", "evidence", "security_impact", "risk", "acceptance_criteria"], responses: fullIssueResponse, collapseSecondary: true, maxItems: 8 },
  api_contract: { label: "API contract", detail: "Request, response, compatibility and error-contract evidence", required: ["outcome", "reproduction", "expected_behavior", "evidence", "risk", "acceptance_criteria"], responses: fullIssueResponse, collapseSecondary: true, maxItems: 6 },
  database: { label: "Database", detail: "Schema, data-integrity, impact and recovery evidence", required: ["outcome", "reproduction", "evidence", "risk", "impact", "acceptance_criteria"], responses: fullIssueResponse, collapseSecondary: true, maxItems: 8 },
  migration: { label: "Migration", detail: "Rollout, backfill, compatibility and rollback evidence", required: ["outcome", "reproduction", "evidence", "risk", "impact", "acceptance_criteria", "non_goals"], responses: fullIssueResponse, collapseSecondary: true, maxItems: 8 },
  accessibility: { label: "Accessibility", detail: "User impact, reproduction and accessible-behavior evidence", required: ["outcome", "reproduction", "observed_behavior", "expected_behavior", "evidence", "acceptance_criteria"], responses: ["assessment", "missing_context", "acceptance_criteria", "risk", "affected_areas", "next_steps", "provenance"], collapseSecondary: true, maxItems: 6 },
  reliability: { label: "Reliability", detail: "Availability, detection, mitigation and recovery evidence", required: ["outcome", "reproduction", "evidence", "impact", "detection", "mitigation", "acceptance_criteria"], responses: fullIssueResponse, collapseSecondary: true, maxItems: 8 },
  incident: { label: "Incident", detail: "Impact, timeline, detection and mitigation", required: ["impact", "timeline", "detection", "mitigation", "evidence"], responses: ["assessment", "missing_context", "risk", "affected_areas", "next_steps", "provenance"], collapseSecondary: true, maxItems: 8 },
  product: { label: "Product", detail: "User outcome, behavior, impact and non-goals", required: ["outcome", "expected_behavior", "impact", "acceptance_criteria", "non_goals"], responses: ["assessment", "missing_context", "acceptance_criteria", "risk", "next_steps", "provenance"], collapseSecondary: true, maxItems: 5 },
  performance: { label: "Performance", detail: "Measured behavior, reproduction and target outcome", required: ["outcome", "reproduction", "observed_behavior", "expected_behavior", "evidence", "impact", "acceptance_criteria"], responses: fullIssueResponse, collapseSecondary: true, maxItems: 8 },
  compliance: { label: "Compliance", detail: "Evidence, risk, data boundary and acceptance", required: ["outcome", "evidence", "risk", "security_impact", "acceptance_criteria", "non_goals"], responses: ["assessment", "missing_context", "acceptance_criteria", "risk", "affected_areas", "provenance"], collapseSecondary: true, maxItems: 8 },
  custom: { label: "Custom", detail: "Repository-owned section and response contract", required: null, responses: null, collapseSecondary: true, maxItems: 6 },
} satisfies Record<string, IssueTriagePreset>;

const issueRequirementOptions = ["outcome", "reproduction", "expected_behavior", "observed_behavior", "evidence", "acceptance_criteria", "risk", "security_impact", "impact", "timeline", "detection", "mitigation", "non_goals"];
const issueResponseOptions = ["assessment", "missing_context", "acceptance_criteria", "risk", "affected_areas", "next_steps", "provenance"];

const issueTemplateSections: Record<"en" | "zh-CN" | "ja" | "es", Record<string, string>> = {
  en: { outcome: "Outcome", reproduction: "Reproduction", expected_behavior: "Expected behavior", observed_behavior: "Observed behavior", evidence: "Evidence", acceptance_criteria: "Acceptance criteria", risk: "Risk", security_impact: "Security impact", impact: "Impact", timeline: "Timeline", detection: "Detection", mitigation: "Mitigation", non_goals: "Non-goals" },
  "zh-CN": { outcome: "结果", reproduction: "复现", expected_behavior: "预期行为", observed_behavior: "实际行为", evidence: "证据", acceptance_criteria: "验收标准", risk: "风险", security_impact: "安全影响", impact: "影响", timeline: "时间线", detection: "发现方式", mitigation: "缓解措施", non_goals: "非目标" },
  ja: { outcome: "結果", reproduction: "再現手順", expected_behavior: "期待される動作", observed_behavior: "実際の動作", evidence: "証拠", acceptance_criteria: "受け入れ基準", risk: "リスク", security_impact: "セキュリティ影響", impact: "影響", timeline: "タイムライン", detection: "検知", mitigation: "緩和策", non_goals: "対象外" },
  es: { outcome: "Resultado", reproduction: "Reproducción", expected_behavior: "Comportamiento esperado", observed_behavior: "Comportamiento observado", evidence: "Evidencia", acceptance_criteria: "Criterios de aceptación", risk: "Riesgo", security_impact: "Impacto de seguridad", impact: "Impacto", timeline: "Cronología", detection: "Detección", mitigation: "Mitigación", non_goals: "Fuera de alcance" },
};

const issueTemplateCopy: Record<"en" | "zh-CN" | "ja" | "es", { about: string; complete: string; hint: (section: string) => string }> = {
  en: { about: "Capture the evidence required by this repository's Issue policy", complete: "Complete each required section before requesting analysis.", hint: (section) => `Add ${section.toLowerCase()} evidence here.` },
  "zh-CN": { about: "记录本仓库 Issue 策略要求的证据", complete: "请求分析前，请完成每个必填章节。", hint: (section) => `请在此补充“${section}”相关证据。` },
  ja: { about: "このリポジトリの Issue ポリシーで必要な証拠を記録します", complete: "分析を依頼する前に、必須セクションをすべて記入してください。", hint: (section) => `ここに「${section}」の証拠を追加してください。` },
  es: { about: "Registre la evidencia requerida por la política de incidencias de este repositorio", complete: "Complete cada sección obligatoria antes de solicitar el análisis.", hint: (section) => `Agregue aquí la evidencia de “${section}”.` },
};

function issueTemplateLanguage(value: string): "en" | "zh-CN" | "ja" | "es" {
  return value === "zh-CN" || value === "ja" || value === "es" ? value : "en";
}

function IssueTriageFields({ content, formatDetail, formatSource, formatTemplates, onCatalogChanged, org, setContent }: { content: Record<string, unknown>; formatDetail?: string; formatSource: DataSource; formatTemplates: IssueFormatTemplate[]; onCatalogChanged: () => void; org: string; setContent: (next: Record<string, unknown>) => void; source: DataSource }) {
  const [copied, setCopied] = useState(false);
  const [downloaded, setDownloaded] = useState<"github" | "gitlab">();
  const required = asStringArray(content.required_issue_sections);
  const responses = asStringArray(content.response_sections);
  const update = (key: string, value: unknown) => setContent({ ...content, [key]: value });
  const updateCustomFormat = (key: string, value: unknown) => setContent({ ...content, preset: "custom", [key]: value });
  const toggle = (key: string, selected: string[], value: string) => setContent({
    ...content,
    preset: "custom",
    [key]: selected.includes(value) ? selected.filter((item) => item !== value) : [...selected, value],
  });
  const choosePreset = (preset: keyof typeof issueTriagePresets) => {
    const definition = issueTriagePresets[preset];
    if (preset === "custom") {
      setContent({ ...content, preset });
      return;
    }
    setContent({
      ...content,
      preset,
      required_issue_sections: [...(definition.required ?? [])],
      response_sections: [...(definition.responses ?? [])],
      collapse_secondary: definition.collapseSecondary ?? true,
      max_items_per_section: definition.maxItems ?? 6,
      custom_guidance: "",
    });
  };
  const templateLanguage = issueTemplateLanguage(asString(content.language, "inherit"));
  const templateCopy = issueTemplateCopy[templateLanguage];
  const template = required.map((section) => {
    const label = issueTemplateSections[templateLanguage][section] ?? titleCase(section);
    return `## ${label}\n\n<!-- ${templateCopy.hint(label)} -->`;
  }).join("\n\n");
  const preset = asString(content.preset, "engineering");
  const presetLabel = issueTriagePresets[preset as keyof typeof issueTriagePresets]?.label ?? titleCase(preset);
  const githubTemplate = githubIssueTemplate(presetLabel, template, templateCopy.about);
  const gitlabTemplate = gitlabIssueTemplate(presetLabel, template, templateCopy.complete);
  async function copyTemplate() {
    if (!template) return;
    await navigator.clipboard.writeText(template);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1800);
  }
  function downloadTemplate(provider: "github" | "gitlab") {
    const value = provider === "github" ? githubTemplate : gitlabTemplate;
    if (!value) return;
    downloadTextFile(value, provider === "github" ? "open-review.md" : "Open Review.md");
    setDownloaded(provider);
    window.setTimeout(() => setDownloaded(undefined), 1800);
  }
  return <div className="space-y-6">
    <ToggleRow checked={asBool(content.enabled, true)} description="Analyze user-authored Issues for this workspace or repository. Disabling it stops new Issue analysis without affecting PR review." label="Issue analysis" onChange={(value) => update("enabled", value)} />
    <IssueFormatTemplateLibrary content={content} detail={formatDetail} onApply={setContent} onCatalogChanged={onCatalogChanged} org={org} source={formatSource} templates={formatTemplates} />
    <div><p className="text-sm font-semibold text-[var(--ls-text)]">Format preset</p><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Each preset applies a complete required-section, response-section, collapse and item-budget contract. Refining any field turns it into a repository-owned custom format.</p><div className="mt-3 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">{Object.entries(issueTriagePresets).map(([key, preset]) => { const active = asString(content.preset, "engineering") === key; return <button aria-pressed={active} className={cn("luminous-focus rounded-[14px] border p-4 text-left transition", active ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)]" : "border-[var(--ls-line)] bg-[var(--ls-surface-muted)] hover:border-[var(--ls-line-strong)]")} key={key} onClick={() => choosePreset(key as keyof typeof issueTriagePresets)} type="button"><span className="flex items-center justify-between gap-2 text-sm font-semibold text-[var(--ls-text)]">{preset.label}{active ? <Check className="size-4 text-[var(--ls-accent)]" /> : null}</span><span className="mt-1 block text-xs leading-5 text-[var(--ls-text-secondary)]">{preset.detail}</span></button>; })}</div></div>
    <ChoiceGrid description="The analyzer reports absent sections as context gaps. It never rejects or rewrites the Issue." label="Required Issue sections" onToggle={(value) => toggle("required_issue_sections", required, value)} options={issueRequirementOptions} selected={required} />
    <ChoiceGrid description="The verdict table remains visible. Select which structured details appear below it." label="Analysis response sections" onToggle={(value) => toggle("response_sections", responses, value)} options={issueResponseOptions} selected={responses} />
    <div className="grid gap-4 lg:grid-cols-2"><SelectField help="Controls the stable report framework and the model response. Following the Issue language uses a conservative framework fallback when its natural language is ambiguous." label="Response language" onChange={(value) => update("language", value)} options={issueResponseLanguageOptions} value={asString(content.language, "inherit")} /><NumberField label="Maximum items per section" max={10} min={1} onChange={(value) => update("max_items_per_section", value)} value={asNumber(content.max_items_per_section, 6)} /></div>
    <div className="grid gap-3 sm:grid-cols-2"><ToggleRow checked={asBool(content.collapse_secondary, true)} description="Keep detailed risk, next steps and provenance in expandable blocks." label="Collapse secondary detail" onChange={(value) => update("collapse_secondary", value)} /><ToggleRow checked={asBool(content.link_file_references, true)} description="Turn evidence-backed repository paths into provider file links. Generic component names remain plain text." label="Link file references" onChange={(value) => update("link_file_references", value)} /><ToggleRow checked={asBool(content.reaction_feedback, true)} description="Invite 👍 or 👎 feedback when the provider can deliver it. Reactions never retrigger analysis; GitHub requires a polling integration rather than an App event." label="Reaction feedback" onChange={(value) => update("reaction_feedback", value)} /></div>
    <TextAreaField help="Trusted repository formatting requirements are appended to the Issue analyzer prompt. Use this for terminology, evidence expectations, risk conventions, and response constraints; Issue body text always remains untrusted input." label="Repository formatting requirements" maxLength={4000} onChange={(value) => updateCustomFormat("custom_guidance", value)} rows={5} value={asString(content.custom_guidance)} />
    <section className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div><div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><Eye className="size-4 text-[var(--ls-accent)]" />Repository Issue template preview</div><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">The exported provider files contain the same headings the analyzer validates. Repository guidance remains a trusted control-plane rule and is not exposed in the template.</p></div>
        <button className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs font-medium text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)] disabled:cursor-not-allowed disabled:opacity-45" disabled={!template} onClick={() => void copyTemplate()} type="button">{copied ? <Check className="size-3.5 text-[var(--ls-success-text)]" /> : <Copy className="size-3.5" />}{copied ? "Copied" : "Copy Markdown"}</button>
      </div>
      <pre className="mt-3 max-h-80 overflow-auto whitespace-pre-wrap rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-3 text-xs leading-5 text-[var(--ls-text-secondary)]">{template || "No required sections selected."}</pre>
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        <ProviderTemplateDownload destination=".github/ISSUE_TEMPLATE/open-review.md" downloaded={downloaded === "github"} disabled={!template} label="Download GitHub template" onClick={() => downloadTemplate("github")} />
        <ProviderTemplateDownload destination=".gitlab/issue_templates/Open Review.md" downloaded={downloaded === "gitlab"} disabled={!template} label="Download GitLab template" onClick={() => downloadTemplate("gitlab")} />
      </div>
      <p className="mt-3 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">Commit the downloaded file at the path shown above. Export never writes to the repository or bypasses provider branch protection.</p>
    </section>
  </div>;
}

function ProviderTemplateDownload({ destination, disabled, downloaded, label, onClick }: { destination: string; disabled: boolean; downloaded: boolean; label: string; onClick: () => void }) {
  return <button className="luminous-focus flex min-h-12 items-center justify-between gap-3 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-2 text-left disabled:cursor-not-allowed disabled:opacity-45" disabled={disabled} onClick={onClick} type="button"><span><span className="block text-xs font-medium text-[var(--ls-text)]">{downloaded ? "Downloaded" : label}</span><span className="mt-0.5 block break-all font-mono text-[10px] text-[var(--ls-text-tertiary)]">{destination}</span></span>{downloaded ? <Check className="size-4 shrink-0 text-[var(--ls-success-text)]" /> : <Download className="size-4 shrink-0 text-[var(--ls-accent)]" />}</button>;
}

function githubIssueTemplate(presetLabel: string, body: string, about: string) {
  if (!body) return "";
  return `---\nname: Open Review · ${presetLabel}\nabout: ${about}\ntitle: ''\nlabels: ''\nassignees: ''\n---\n\n${body}\n`;
}

function gitlabIssueTemplate(presetLabel: string, body: string, complete: string) {
  if (!body) return "";
  return `<!-- Open Review · ${presetLabel}. ${complete} -->\n\n${body}\n`;
}

function downloadTextFile(value: string, fileName: string) {
  const blob = new Blob([value], { type: "text/markdown;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = fileName;
  anchor.click();
  URL.revokeObjectURL(url);
}

const issueFormatTemplateKeys = [
  "preset",
  "language",
  "required_issue_sections",
  "response_sections",
  "collapse_secondary",
  "link_file_references",
  "reaction_feedback",
  "max_items_per_section",
  "custom_guidance",
] as const;

function IssueFormatTemplateLibrary({ content, detail, onApply, onCatalogChanged, org, source, templates }: { content: Record<string, unknown>; detail?: string; onApply: (next: Record<string, unknown>) => void; onCatalogChanged: () => void; org: string; source: DataSource; templates: IssueFormatTemplate[] }) {
  const [selectedID, setSelectedID] = useState("");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [pending, setPending] = useState<"create" | "update" | "archive">();
  const [notice, setNotice] = useState<Notice>();
  const selected = templates.find((item) => item.id === selectedID);
  const canManage = source === "live";

  function chooseTemplate(id: string) {
    setSelectedID(id);
    const item = templates.find((candidate) => candidate.id === id);
    setName(item?.name ?? "");
    setDescription(item?.description ?? "");
    setNotice(undefined);
  }

  function applySelected() {
    if (!selected) return;
    onApply({ ...content, ...selected.content, enabled: asBool(content.enabled, true) });
    setNotice({ tone: "success", text: `${selected.name} was applied to this unsaved configuration draft.` });
  }

  async function mutate(action: "create" | "update" | "archive") {
    if (!canManage || (action !== "archive" && name.trim().length < 2) || (action !== "create" && !selected)) return;
    setPending(action);
    setNotice(undefined);
    const endpoint = action === "create"
      ? `/api/tenants/${encodeURIComponent(org)}/issue-format-templates`
      : `/api/tenants/${encodeURIComponent(org)}/issue-format-templates/${encodeURIComponent(selected!.id)}${action === "archive" ? `?expected_revision=${selected!.revision}` : ""}`;
    try {
      const response = await fetch(endpoint, {
        method: action === "create" ? "POST" : action === "update" ? "PUT" : "DELETE",
        headers: action === "archive" ? undefined : { "Content-Type": "application/json" },
        body: action === "archive" ? undefined : JSON.stringify({
          name: name.trim(),
          description: description.trim(),
          expected_revision: action === "update" ? selected!.revision : 0,
          content: issueFormatTemplateContent(content),
        }),
      });
      const body = response.status === 204 ? undefined : await response.json() as IssueFormatTemplate | { error?: string };
      if (!response.ok) {
        throw new Error(body && "error" in body && body.error ? body.error : `Format operation failed (${response.status}).`);
      }
      if (action === "archive") {
        setSelectedID("");
        setName("");
        setDescription("");
        setNotice({ tone: "success", text: "Reusable format archived. Existing configuration revisions remain unchanged." });
      } else {
        const saved = body as IssueFormatTemplate;
        setSelectedID(saved.id);
        setName(saved.name);
        setDescription(saved.description);
        setNotice({ tone: "success", text: `${action === "create" ? "Created" : "Updated"} ${saved.name} revision ${saved.revision}.` });
      }
      onCatalogChanged();
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : "Reusable format could not be changed." });
    } finally {
      setPending(undefined);
    }
  }

  return (
    <section className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 sm:p-5">
      <div className="flex items-start gap-3">
        <span className="grid size-9 shrink-0 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><Library className="size-4" /></span>
        <div><h3 className="text-sm font-semibold text-[var(--ls-text)]">Reusable workspace formats</h3><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Save a named, versioned format once and apply it to any repository draft. Applying never publishes by itself.</p></div>
      </div>
      <div className="mt-4 grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto]">
        <label className="grid gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">Saved format<select className="luminous-focus h-10 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" onChange={(event) => chooseTemplate(event.target.value)} value={selectedID}><option value="">{templates.length ? "Choose a workspace format" : "No reusable formats yet"}</option>{templates.map((item) => <option key={item.id} value={item.id}>{item.name} · r{item.revision}</option>)}</select></label>
        <button className="luminous-focus mt-auto inline-flex h-10 items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-medium text-[var(--ls-text)] disabled:cursor-not-allowed disabled:opacity-45" disabled={!selected} onClick={applySelected} type="button"><BookOpen className="size-4" /> Apply to draft</button>
      </div>
      {selected ? <p className="mt-2 text-[11px] text-[var(--ls-text-tertiary)]">Revision {selected.revision} · {selected.content_sha256.slice(0, 12)} · updated by {selected.updated_by}</p> : null}
      <div className="mt-4 grid gap-3 sm:grid-cols-2">
        <label className="grid gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">Format name<input className="luminous-focus h-10 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" maxLength={80} onChange={(event) => setName(event.target.value)} placeholder="Security evidence contract" value={name} /></label>
        <label className="grid gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">Description<input className="luminous-focus h-10 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" maxLength={240} onChange={(event) => setDescription(event.target.value)} placeholder="When and why teams should use this format" value={description} /></label>
      </div>
      <div className="mt-3 flex flex-wrap gap-2">
        <button className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-medium text-white disabled:cursor-not-allowed disabled:opacity-45" disabled={!canManage || name.trim().length < 2 || Boolean(pending)} onClick={() => void mutate("create")} type="button">{pending === "create" ? <LoaderCircle className="size-3.5 animate-spin" /> : <Save className="size-3.5" />} Save current as new</button>
        <button className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs font-medium text-[var(--ls-text)] disabled:cursor-not-allowed disabled:opacity-45" disabled={!canManage || !selected || name.trim().length < 2 || Boolean(pending)} onClick={() => void mutate("update")} type="button">{pending === "update" ? <LoaderCircle className="size-3.5 animate-spin" /> : <RefreshCcw className="size-3.5" />} Update selected</button>
        <button className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[9px] border border-red-500/20 bg-red-500/[0.06] px-3 text-xs font-medium text-[var(--ls-critical-text)] disabled:cursor-not-allowed disabled:opacity-45" disabled={!canManage || !selected || Boolean(pending)} onClick={() => void mutate("archive")} type="button">{pending === "archive" ? <LoaderCircle className="size-3.5 animate-spin" /> : <Trash2 className="size-3.5" />} Archive</button>
      </div>
      {notice ? <p className={cn("mt-3 rounded-[10px] px-3 py-2 text-xs leading-5", notice.tone === "success" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : "bg-red-500/10 text-[var(--ls-critical-text)]")}>{notice.text}</p> : detail ? <p className="mt-3 text-xs leading-5 text-[var(--ls-warning-text)]">{detail}</p> : null}
    </section>
  );
}

function issueFormatTemplateContent(content: Record<string, unknown>) {
  return Object.fromEntries(issueFormatTemplateKeys.map((key) => [key, content[key]]));
}

function ChoiceGrid({ description, label, onToggle, options, selected }: { description: string; label: string; onToggle: (value: string) => void; options: string[]; selected: string[] }) {
  return <div><p className="text-sm font-semibold text-[var(--ls-text)]">{label}</p><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{description}</p><div className="mt-3 grid gap-2 sm:grid-cols-2 xl:grid-cols-3">{options.map((option) => { const active = selected.includes(option); return <button aria-pressed={active} className={cn("luminous-focus flex items-center justify-between rounded-[11px] border px-3 py-2.5 text-left text-xs font-medium transition", active ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-text)]" : "border-[var(--ls-line)] bg-[var(--ls-surface)] text-[var(--ls-text-secondary)]")} key={option} onClick={() => onToggle(option)} type="button"><span>{titleCase(option)}</span><span className={cn("grid size-4 place-items-center rounded-full border", active ? "border-[var(--ls-accent)] bg-[var(--ls-accent)] text-white" : "border-[var(--ls-line-strong)] text-transparent")}><Check className="size-2.5" /></span></button>; })}</div></div>;
}

const summarySections = [
  { key: "outcome", label: "Outcome", detail: "Author intent with an explicit evidence boundary." },
  { key: "scope", label: "Scope", detail: "Revision, changed-file and selected-risk scope." },
  { key: "risk", label: "Risk", detail: "Severity, trust boundary and blast-radius context." },
  { key: "acceptance", label: "Acceptance", detail: "Declared acceptance mapping, never inferred by the model." },
  { key: "invariants", label: "Invariants", detail: "Revision and governance properties retained by the run." },
  { key: "verification", label: "Verification", detail: "Observed checks versus author-declared evidence." },
  { key: "rollout", label: "Rollout", detail: "Declared phased-release plan, if present." },
  { key: "rollback", label: "Rollback", detail: "Declared recovery path, owner and data boundary." },
  { key: "provenance", label: "Provenance", detail: "Engine, rule, model route and exact revision identity." },
] as const;

function SummaryFields({ content, onChange }: { content: Record<string, unknown>; onChange: (key: string, value: unknown) => void }) {
  const selected = asStringArray(content.sections);
  const toggleSection = (key: string) => onChange("sections", selected.includes(key) ? selected.filter((item) => item !== key) : [...selected, key]);
  return <div className="space-y-6">
    <div><div className="flex items-end justify-between gap-4"><div><p className="text-sm font-semibold text-[var(--ls-text)]">Evidence sections</p><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">The merge-gate verdict and actionable finding index are always present. Select the optional, collapsible evidence sections that follow.</p></div><span className="rounded-full bg-[var(--ls-accent-soft)] px-2.5 py-1 text-xs font-medium text-[var(--ls-accent)]">{selected.length} selected</span></div><div className="mt-4 grid gap-3 sm:grid-cols-2">{summarySections.map((item) => { const active = selected.includes(item.key); return <button aria-pressed={active} className={cn("luminous-focus rounded-[14px] border p-4 text-left transition", active ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)]" : "border-[var(--ls-line)] bg-[var(--ls-surface-muted)] hover:border-[var(--ls-line-strong)]")} key={item.key} onClick={() => toggleSection(item.key)} type="button"><span className="flex items-center justify-between gap-3"><span className="text-sm font-medium text-[var(--ls-text)]">{item.label}</span><span className={cn("grid size-5 place-items-center rounded-full border", active ? "border-[var(--ls-accent)] bg-[var(--ls-accent)] text-white" : "border-[var(--ls-line-strong)] text-transparent")}><Check className="size-3" /></span></span><span className="mt-1.5 block text-xs leading-5 text-[var(--ls-text-secondary)]">{item.detail}</span></button>; })}</div></div>
    <div className="grid gap-4 lg:grid-cols-2"><NumberField label="Optional evidence character budget" max={20000} min={500} onChange={(value) => onChange("max_characters", value)} value={asNumber(content.max_characters, 4000)} /><section className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><Eye className="size-4 text-[var(--ls-accent)]" />Provider summary preview</div><p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">The next review will keep the verdict, gate and finding index visible, then render the selected detail sections until this optional budget is reached.</p><div className="mt-3 flex flex-wrap gap-1.5">{selected.length ? selected.map((section) => <span className="rounded-full bg-[var(--ls-surface)] px-2.5 py-1 text-[11px] font-medium text-[var(--ls-text-secondary)]" key={section}>{titleCase(section)}</span>) : <span className="text-xs text-[var(--ls-warning-text)]">Select at least one section before saving.</span>}</div></section></div>
    <div className="grid gap-3 sm:grid-cols-2"><ToggleRow checked={asBool(content.include_change_contract, true)} description="Include author-declared outcome, scope, risk, acceptance, rollout, rollback and provenance only in selected sections." label="Change contract evidence" onChange={(value) => onChange("include_change_contract", value)} /><ToggleRow checked={asBool(content.include_verification_evidence, true)} description="Include author-declared verification alongside the review's observed verification boundary." label="Verification evidence" onChange={(value) => onChange("include_verification_evidence", value)} /></div>
  </div>;
}

const lifecycleMessages = [
  { key: "success", label: "Pass", detail: "Added only when no actionable findings meet the configured publication threshold." },
  { key: "recommendation", label: "Recommendations", detail: "Added when findings are published but the configured merge gate still passes." },
  { key: "blocked", label: "Blocked", detail: "Added when findings meet the immutable merge-gate threshold." },
  { key: "failed", label: "Failure", detail: "Added when execution cannot produce trustworthy evidence." },
  { key: "needs_attention", label: "Needs attention", detail: "Added when a human decision is required before trustworthy findings or a merge conclusion can be published." },
  { key: "superseded", label: "Superseded", detail: "Added when a newer provider revision replaces this review." },
] as const;

const lifecycleMessageFallbacks: Record<(typeof lifecycleMessages)[number]["key"], string> = {
  success: "Review passed for revision {{head_sha}}.",
  recommendation: "Review completed with recommendations for revision {{head_sha}}.",
  blocked: "Merge gate blocked: review the actionable findings.",
  failed: "Review could not complete. Open the run detail for the safe error summary.",
  needs_attention: "Review requires human attention before trustworthy findings can be published.",
  superseded: "This review was superseded by a newer revision.",
};

function MessageFields({ content, onChange }: { content: Record<string, unknown>; onChange: (key: string, value: string) => void }) {
  const [previewKey, setPreviewKey] = useState<(typeof lifecycleMessages)[number]["key"]>("success");
  const selected = lifecycleMessages.find((item) => item.key === previewKey) ?? lifecycleMessages[0];
  const messageValue = (key: (typeof lifecycleMessages)[number]["key"]) =>
    asString(content[key], lifecycleMessageFallbacks[key]);
  const preview = renderMessagePreview(messageValue(selected.key));

  return <div className="space-y-5">
    <div className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]"><Languages className="mr-2 inline size-4 text-[var(--ls-accent)]" />The provider Check shows review progress without a timeline comment; only a final result is posted. Variables: <code>{"{{repository}}"}</code>, <code>{"{{review_number}}"}</code>, <code>{"{{head_sha}}"}</code>, <code>{"{{run_url}}"}</code>. Variable values are escaped; comments and system markers are rejected. <code>{"{{run_url}}"}</code> renders the safe console-evidence guidance until a public run URL is configured.</div>
    <div className="grid gap-4 lg:grid-cols-2">
      {lifecycleMessages.map((item) => <TextAreaField help={item.detail} key={item.key} label={`${item.label} message`} maxLength={2000} onChange={(value) => onChange(item.key, value)} rows={3} value={messageValue(item.key)} />)}
    </div>
    <section className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] p-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"><div><div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><Eye className="size-4 text-[var(--ls-accent)]" />Provider comment preview</div><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Preview is local only; saving creates a revision but never posts a provider comment.</p></div><select aria-label="Preview lifecycle message" className="luminous-focus h-9 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" onChange={(event) => setPreviewKey(event.target.value as (typeof lifecycleMessages)[number]["key"])} value={previewKey}>{lifecycleMessages.map((item) => <option key={item.key} value={item.key}>{item.label}</option>)}</select></div>
      <p className="mt-4 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3 py-3 font-mono text-sm leading-6 text-[var(--ls-text)]">{preview || "No extra copy is configured. The canonical review state remains visible."}</p>
    </section>
  </div>;
}

function ToggleRow({ checked, description, label, onChange, warning = false }: { checked: boolean; description: string; label: string; onChange: (value: boolean) => void; warning?: boolean }) {
  return <div className={cn("flex items-start justify-between gap-4 rounded-[14px] border p-4", warning ? "border-amber-500/25 bg-amber-500/[0.06]" : "border-[var(--ls-line)] bg-[var(--ls-surface-muted)]")}><div><p className="text-sm font-medium text-[var(--ls-text)]">{label}</p><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">{description}</p></div><Toggle checked={checked} label={label} onChange={onChange} /></div>;
}

function Toggle({ checked, label, onChange }: { checked: boolean; label: string; onChange: (value: boolean) => void }) {
  return <button aria-checked={checked} aria-label={label} className={cn("luminous-focus relative h-6 w-11 shrink-0 rounded-full transition", checked ? "bg-[var(--ls-accent)]" : "bg-[var(--ls-line-strong)]")} onClick={() => onChange(!checked)} role="switch" type="button"><span className={cn("absolute top-1 size-4 rounded-full bg-white shadow transition", checked ? "left-6" : "left-1")} /></button>;
}

function SelectField({ help, label, onChange, options, value }: { help?: string; label: string; onChange: (value: string) => void; options: SelectOption[]; value: string }) {
  return <label className="block"><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">{label}</span><select className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" onChange={(event) => onChange(event.target.value)} value={value}>{options.map((option) => { const normalized = typeof option === "string" ? { value: option, label: titleCase(option) } : option; return <option key={normalized.value} value={normalized.value}>{normalized.label}</option>; })}</select>{help ? <span className="mt-2 block text-xs leading-5 text-[var(--ls-text-tertiary)]">{help}</span> : null}</label>;
}

function NumberField({ label, max, min, onChange, value }: { label: string; max: number; min: number; onChange: (value: number) => void; value: number }) {
  return <label className="block max-w-sm"><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">{label}</span><input className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" max={max} min={min} onChange={(event) => onChange(Number(event.target.value))} type="number" value={value} /></label>;
}

function ListField({ help, label, onChange, placeholder, value }: { help?: string; label: string; onChange: (value: string[]) => void; placeholder: string; value: string[] }) {
  return <label className="block"><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">{label}</span><input className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]" onChange={(event) => onChange(event.target.value.split(",").map((item) => item.trim()).filter(Boolean))} placeholder={placeholder} value={value.join(", ")} />{help ? <span className="mt-2 block text-xs leading-5 text-[var(--ls-text-tertiary)]">{help}</span> : null}</label>;
}

function TextAreaField({ help, label, maxLength, onChange, rows, value }: { help?: string; label: string; maxLength?: number; onChange: (value: string) => void; rows: number; value: string }) {
  return <label className="block"><span className="mb-2 flex items-center justify-between gap-3 text-xs font-medium text-[var(--ls-text-secondary)]"><span>{label}</span>{maxLength ? <span className="font-mono text-[10px] font-normal text-[var(--ls-text-tertiary)]">{[...value].length}/{maxLength}</span> : null}</span><textarea className="luminous-focus w-full resize-y rounded-[12px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-2.5 font-mono text-sm leading-6 text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]" maxLength={maxLength} onChange={(event) => onChange(event.target.value)} rows={rows} value={value} />{help ? <span className="mt-2 block text-xs leading-5 text-[var(--ls-text-tertiary)]">{help}</span> : null}</label>;
}

function scopeButton(active: boolean) {
  return cn("luminous-focus rounded-[8px] px-3 py-1.5 text-xs font-medium transition", active ? "bg-[var(--ls-surface)] text-[var(--ls-text)] shadow-[var(--ls-shadow-control)]" : "text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]");
}

function stableJSON(value: Record<string, unknown>) {
  return JSON.stringify(value, Object.keys(value).sort());
}

function asBool(value: unknown, fallback = false) { return typeof value === "boolean" ? value : fallback; }
function asString(value: unknown, fallback = "") { return typeof value === "string" ? value : fallback; }
function asNumber(value: unknown, fallback: number) { return typeof value === "number" && Number.isFinite(value) ? value : fallback; }
function asStringArray(value: unknown) { return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : []; }
function asCategory(value: unknown) { const category = value && typeof value === "object" ? value as Record<string, unknown> : {}; return { enabled: asBool(category.enabled, true), minimum_severity: asString(category.minimum_severity, "medium") }; }
function titleCase(value: string) { return value.replaceAll("_", " ").replace(/\b\w/g, (character) => character.toUpperCase()); }
function renderMessagePreview(template: string) { return template.replaceAll("{{repository}}", "RainLib/open-review-platform").replaceAll("{{review_number}}", "42").replaceAll("{{head_sha}}", "a1b2c3d4").replaceAll("{{run_url}}", "Run evidence is available in the Open Review console."); }
