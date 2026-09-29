"use client";

import { useMemo, useRef, useState } from "react";
import { CircleAlert, Eye, LoaderCircle, Settings2, X } from "lucide-react";
import { useRouter } from "next/navigation";

import type { IssueAutoCreatePolicy, IssueAutoCreatePreview } from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { useModalFocus } from "@/components/console/use-modal-focus";

type DraftPolicy = Omit<IssueAutoCreatePolicy, "repository_scopes" | "categories" | "labels"> & {
  repository_scopes: string;
  categories: string;
  labels: string;
};

function toDraft(policy: IssueAutoCreatePolicy): DraftPolicy {
  return {
    ...policy,
    // A newly created workspace has an intentionally incomplete policy. Older
    // control-plane responses encoded its optional lists as null, so preserve
    // the empty-state editor instead of crashing the whole Issue inbox.
    repository_scopes: (policy.repository_scopes ?? []).join(", "),
    categories: (policy.categories ?? []).join(", "),
    labels: (policy.labels ?? []).join(", "),
  };
}

function toPayload(draft: DraftPolicy): IssueAutoCreatePolicy {
  return {
    ...draft,
    target: "provider",
    repository_scopes: parseList(draft.repository_scopes),
    categories: parseList(draft.categories, true),
    labels: parseList(draft.labels),
  };
}

function parseList(value: string, lower = false) {
  return [...new Set(value.split(",").map((item) => (lower ? item.trim().toLowerCase() : item.trim())).filter(Boolean))];
}

export function IssueAutoCreatePolicyEditor({ enabled, org, policy }: { enabled: boolean; org: string; policy?: IssueAutoCreatePolicy }) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<DraftPolicy | undefined>(() => policy ? toDraft(policy) : undefined);
  const [preview, setPreview] = useState<IssueAutoCreatePreview>();
  const [busy, setBusy] = useState<"preview" | "save">();
  const [message, setMessage] = useState<string>();
  const canManage = enabled && policy?.can_manage === true;
  const canOpen = Boolean(policy) && (!enabled || canManage);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useModalFocus<HTMLDivElement>(() => setOpen(false), {
    active: open,
    returnFocusRef: triggerRef,
  });

  const ready = useMemo(() => {
    if (!draft) return false;
    return Boolean(
      draft.repository_scopes.trim() &&
      draft.title_template.trim() &&
      draft.body_template.trim() &&
      (draft.trigger_first_seen || draft.trigger_regressed || draft.repeat_occurrence_threshold > 0),
    );
  }, [draft]);

  function change<K extends keyof DraftPolicy>(key: K, value: DraftPolicy[K]) {
    setDraft((current) => current ? { ...current, [key]: value } : current);
    setMessage(undefined);
  }

  async function request(kind: "preview" | "save") {
    if (!draft || !ready || busy) return;
    setBusy(kind);
    setMessage(undefined);
    try {
      const payload = toPayload(draft);
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/issues/auto-create-policy${kind === "preview" ? "/preview" : ""}`,
        {
          method: kind === "preview" ? "POST" : "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        },
      );
      const body = await response.json().catch(() => ({})) as IssueAutoCreatePolicy | IssueAutoCreatePreview | { error?: string };
      if (!response.ok) {
        setMessage((body as { error?: string }).error ?? "The policy could not be updated.");
        return;
      }
      if (kind === "preview") {
        setPreview(body as IssueAutoCreatePreview);
        setMessage("Preview only: no external Issue has been created.");
      } else {
        const saved = body as IssueAutoCreatePolicy;
        setDraft(toDraft(saved));
        setPreview(undefined);
        setMessage(saved.enabled ? "Policy saved. Future matching aggregates are queued through the durable publisher." : "Policy saved and external Issue publication is stopped.");
        router.refresh();
      }
    } catch {
      setMessage("The control plane could not be reached. No provider Issue was created.");
    } finally {
      setBusy(undefined);
    }
  }

  if (!policy) return null;
  return (
    <>
      <button
        className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-medium text-[var(--ls-text)] shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-surface-muted)] disabled:cursor-not-allowed disabled:opacity-50"
        disabled={!canOpen}
        onClick={() => setOpen(true)}
        ref={triggerRef}
        title={canManage ? "Configure provider Issue automation" : enabled ? "Owner or admin role is required" : "Preview policy is read-only"}
        type="button"
      >
        <Settings2 className="size-4 text-[var(--ls-accent)]" /> Auto-create policy
      </button>
      {open && draft ? (
        <div aria-label="External Issue auto-create policy" aria-modal="true" className="fixed inset-0 z-50 flex items-end justify-center bg-slate-950/35 p-0 backdrop-blur-[2px] sm:items-center sm:p-5" ref={dialogRef} role="dialog">
          <div className="max-h-[94vh] w-full max-w-3xl overflow-y-auto rounded-t-[24px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-float)] sm:rounded-[24px]">
            <header className="sticky top-0 z-10 flex items-start justify-between gap-4 border-b border-[var(--ls-line)] bg-[var(--ls-surface)] px-5 py-4 sm:px-6">
              <div>
                <p className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--ls-accent)]">Issue automation</p>
                <h2 className="mt-1 text-xl font-semibold tracking-[-0.03em] text-[var(--ls-text)]">Create provider Issues with a bounded policy</h2>
                <p className="mt-1 text-sm text-[var(--ls-text-secondary)]">GitHub and GitLab issues are created only for matching future aggregates. One fingerprint retains one durable external Issue receipt.</p>
              </div>
              <button aria-label="Close policy editor" className="luminous-focus grid size-9 shrink-0 place-items-center rounded-[10px] text-[var(--ls-text-tertiary)] hover:bg-[var(--ls-surface-muted)]" onClick={() => setOpen(false)} type="button"><X className="size-4" /></button>
            </header>
            <fieldset className="space-y-5 px-5 py-5 sm:px-6 disabled:opacity-70" disabled={!enabled}>
              {!enabled ? <p className="rounded-[12px] border border-amber-500/20 bg-amber-500/[0.07] px-3.5 py-3 text-xs leading-5 text-[var(--ls-warning-text)]">Preview policy only. No external Issue can be created, including from a dry run.</p> : null}
              <label className="flex items-center justify-between gap-4 rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
                <span><span className="block text-sm font-semibold text-[var(--ls-text)]">Enable provider Issue creation</span><span className="mt-1 block text-xs leading-5 text-[var(--ls-text-secondary)]">Disabling this prevents queued receipts from making an external write.</span></span>
                <input checked={draft.enabled} className="size-5 accent-[var(--ls-accent)]" onChange={(event) => change("enabled", event.target.checked)} type="checkbox" />
              </label>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="Repository scope"><input className="policy-input" onChange={(event) => change("repository_scopes", event.target.value)} placeholder="RainLib/*, acme/service" value={draft.repository_scopes} /><Help>Required. Comma-separated exact names or glob scopes.</Help></Field>
                <Field label="Minimum severity"><select className="policy-input" onChange={(event) => change("minimum_severity", event.target.value as DraftPolicy["minimum_severity"])} value={draft.minimum_severity}><option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option><option value="critical">Critical</option></select><Help>Only this level and higher are eligible.</Help></Field>
                <Field label="Categories"><input className="policy-input" onChange={(event) => change("categories", event.target.value)} placeholder="security, bug (empty = all)" value={draft.categories} /><Help>Optional allow-list of review categories.</Help></Field>
                <Field label="Provider assignee"><input className="policy-input" onChange={(event) => change("assignee_external_id", event.target.value)} placeholder="GitHub login or GitLab numeric ID" value={draft.assignee_external_id ?? ""} /><Help>Applied when the connected provider supports it.</Help></Field>
              </div>
              <fieldset className="rounded-[14px] border border-[var(--ls-line)] p-4"><legend className="px-1 text-sm font-semibold text-[var(--ls-text)]">When to create</legend><div className="mt-2 grid gap-3 sm:grid-cols-2"><Checkbox checked={draft.trigger_first_seen} label="First matching occurrence" onChange={(value) => change("trigger_first_seen", value)} /><Checkbox checked={draft.trigger_regressed} label="A resolved issue regresses" onChange={(value) => change("trigger_regressed", value)} /><label className="flex items-center gap-3 text-sm text-[var(--ls-text-secondary)]"><input checked={draft.repeat_occurrence_threshold > 0} className="size-4 accent-[var(--ls-accent)]" onChange={(event) => change("repeat_occurrence_threshold", event.target.checked ? Math.max(2, draft.repeat_occurrence_threshold || 2) : 0)} type="checkbox" />At repeat threshold</label><input aria-label="Repeat occurrence threshold" className="policy-input h-9" disabled={draft.repeat_occurrence_threshold === 0} min={2} onChange={(event) => change("repeat_occurrence_threshold", Number(event.target.value) || 0)} type="number" value={draft.repeat_occurrence_threshold || ""} /></div></fieldset>
              <Field label="Labels"><input className="policy-input" onChange={(event) => change("labels", event.target.value)} placeholder="open-review, security" value={draft.labels} /><Help>Labels are sent with the provider Issue where supported.</Help></Field>
              <Field label="Issue title template"><input className="policy-input" onChange={(event) => change("title_template", event.target.value)} value={draft.title_template} /><Help>Variables include severity, severity_upper, category, repository, path, occurrence_count, and review_number.</Help></Field>
              <Field label="Issue body template"><textarea className="policy-input min-h-64 py-3 font-mono text-xs leading-5" onChange={(event) => change("body_template", event.target.value)} value={draft.body_template} /><Help>Markdown is supported. Use file_link and review_link for provider deep links; evidence, suggestion, run_id, head_sha, and issue_id provide traceability. A hidden stable marker is appended automatically.</Help></Field>
              {message ? <p aria-live="polite" className="flex items-start gap-2 rounded-[12px] bg-[var(--ls-surface-muted)] p-3 text-xs leading-5 text-[var(--ls-text-secondary)]"><CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />{message}</p> : null}
              {preview ? <section className="overflow-hidden rounded-[14px] border border-[var(--ls-line)]"><div className="flex items-center justify-between gap-3 border-b border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-4 py-3"><span className="text-sm font-semibold text-[var(--ls-text)]">30-day dry run</span><span className="text-xs text-[var(--ls-text-secondary)]">{preview.candidates.length} matching aggregate{preview.candidates.length === 1 ? "" : "s"}</span></div><ul className="divide-y divide-[var(--ls-line)]">{preview.candidates.slice(0, 5).map((item) => <li className="flex items-center justify-between gap-4 px-4 py-3 text-xs" key={item.issue_id}><span className="min-w-0"><span className="block truncate font-medium text-[var(--ls-text)]">{item.repository} · {item.path}</span><span className="mt-1 block text-[var(--ls-text-tertiary)]">{item.severity} · {item.category} · {item.occurrence_count} occurrences</span></span><span className={cn("shrink-0 rounded-full px-2 py-1", item.already_published ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : "bg-violet-500/10 text-[var(--ls-accent)]")}>{item.already_published ? "Already linked" : "Would qualify"}</span></li>)}</ul>{preview.candidates.length > 5 ? <p className="border-t border-[var(--ls-line)] px-4 py-3 text-xs text-[var(--ls-text-tertiary)]">Showing the first 5 of {preview.candidates.length} matching aggregates.</p> : null}</section> : null}
            </fieldset>
            <footer className="sticky bottom-0 flex flex-col-reverse gap-2 border-t border-[var(--ls-line)] bg-[var(--ls-surface)] px-5 py-4 sm:flex-row sm:justify-end sm:px-6"><button className="luminous-focus inline-flex h-10 items-center justify-center rounded-[10px] border border-[var(--ls-line-strong)] px-4 text-sm font-medium text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)] disabled:opacity-45" disabled={!enabled || !ready || Boolean(busy)} onClick={() => request("preview")} type="button">{busy === "preview" ? <LoaderCircle className="size-4 animate-spin" /> : <Eye className="size-4" />} Dry run</button><button className="luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white hover:bg-[var(--ls-accent-hover)] disabled:opacity-45" disabled={!enabled || !ready || Boolean(busy)} onClick={() => request("save")} type="button">{busy === "save" ? <LoaderCircle className="size-4 animate-spin" /> : null}Save policy</button></footer>
          </div>
        </div>
      ) : null}
    </>
  );
}

function Field({ children, label }: { children: React.ReactNode; label: string }) { return <label className="block text-xs font-medium text-[var(--ls-text-secondary)]"><span className="mb-1.5 block">{label}</span>{children}</label>; }
function Help({ children }: { children: React.ReactNode }) { return <span className="mt-1.5 block text-[11px] font-normal leading-4 text-[var(--ls-text-tertiary)]">{children}</span>; }
function Checkbox({ checked, label, onChange }: { checked: boolean; label: string; onChange: (next: boolean) => void }) { return <label className="flex items-center gap-3 text-sm text-[var(--ls-text-secondary)]"><input checked={checked} className="size-4 accent-[var(--ls-accent)]" onChange={(event) => onChange(event.target.checked)} type="checkbox" />{label}</label>; }
