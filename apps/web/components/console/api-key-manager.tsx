"use client";

import { SectionDisclosure } from "./section-disclosure";

import { useWorkflowText } from "./ui-language-context";

import { FormEvent, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  BookOpen,
  Check,
  CircleAlert,
  Clipboard,
  Clock3,
  KeyRound,
  LoaderCircle,
  LockKeyhole,
  Plus,
  ShieldCheck,
  SquareTerminal,
  Trash2,
  X,
} from "lucide-react";

import { EnterpriseSettingsTabs } from "@/components/console/enterprise-settings-tabs";
import { CopyEvidenceButton } from "@/components/console/copy-evidence-button";
import { DataFreshness, PageState } from "@/components/console/page-state";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { useModalFocus } from "@/components/console/use-modal-focus";
import type { DataSource, WorkspaceAPIKey } from "@/lib/control-api";
import { cliEnvironmentTemplate } from "@/lib/public-control-plane-url";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

type Scope = WorkspaceAPIKey["scopes"][number];
type CreatedKey = { api_key: WorkspaceAPIKey; secret: string };
type APIKeyTab = "active" | "revoked" | "guide";

const scopeOptions: Array<{ value: Scope; label: string; detail: string }> = [
  { value: "reviews:create", label: "Create reviews", detail: "Submit a repository revision to the durable review workflow." },
  { value: "reviews:read", label: "Read review evidence", detail: "Read runs, findings, receipts, and immutable snapshots." },
  { value: "runs:cancel", label: "Cancel runs", detail: "Request cancellation for a non-terminal run in the allowed scope." },
];

export function APIKeyManager({
  apiKeys,
  detail,
  observedAt,
  org,
  publicAPIURL,
  source,
  tab,
}: {
  apiKeys: WorkspaceAPIKey[];
  detail?: string;
  observedAt: string;
  org: string;
  publicAPIURL?: string;
  source: DataSource;
  tab: APIKeyTab;
}) {
  const t = useWorkflowText();
  const router = useRouter();
  const [creating, setCreating] = useState(false);
  const [pending, setPending] = useState<"create" | string>();
  const [created, setCreated] = useState<CreatedKey>();
  const [copied, setCopied] = useState(false);
  const [confirmRevoke, setConfirmRevoke] = useState<string>();
  const [notice, setNotice] = useState<{ tone: "success" | "error"; text: string }>();
  const activeKeys = apiKeys.filter((item) => !item.revoked_at);
  const revokedKeys = apiKeys.filter((item) => Boolean(item.revoked_at));
  const visibleKeys = tab === "revoked" ? revokedKeys : activeKeys;
  const readable = source === "live" || source === "demo";
  const readOnly = source !== "live";

  async function createKey(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const scopes = scopeOptions
      .map(({ value }) => value)
      .filter((scope) => form.get(scope) === "on");
    const repositories = String(form.get("repositories") ?? "")
      .split(/[\n,]/)
      .map((repository) => repository.trim())
      .filter(Boolean);
    const lifetime = Number(form.get("lifetime") ?? 90);
    const expiresAt = lifetime > 0
      ? new Date(Date.now() + lifetime * 24 * 60 * 60 * 1000).toISOString()
      : undefined;
    if (scopes.length === 0) {
      setNotice({ tone: "error", text: "Select at least one action scope." });
      return;
    }
    setPending("create");
    setNotice(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/api-keys`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: String(form.get("name") ?? "").trim(),
          scopes,
          repositories,
          expires_at: expiresAt,
        }),
      });
      const body = await response.json() as CreatedKey | { error?: string };
      if (!response.ok || !("secret" in body)) {
        throw new Error("error" in body && body.error ? body.error : `Creation failed (${response.status}).`);
      }
      setCreated(body);
      setCreating(false);
      setNotice({ tone: "success", text: `${body.api_key.name} is active. Store the secret before closing the one-time view.` });
      router.refresh();
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : "The API key could not be created." });
    } finally {
      setPending(undefined);
    }
  }

  async function revoke(keyID: string) {
    setPending(keyID);
    setNotice(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/api-keys/${encodeURIComponent(keyID)}/revoke`, { method: "POST" });
      const body = await response.json().catch(() => ({})) as { error?: string };
      if (!response.ok) throw new Error(body.error ?? `Revocation failed (${response.status}).`);
      setConfirmRevoke(undefined);
      setNotice({ tone: "success", text: "The key was revoked. Future requests using it will be rejected." });
      router.refresh();
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : "The API key could not be revoked." });
    } finally {
      setPending(undefined);
    }
  }

  async function copySecret() {
    if (!created) return;
    await navigator.clipboard.writeText(created.secret);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  }

  function closeSecret() {
    setCreated(undefined);
    setCopied(false);
  }

  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">Enterprise control plane</p>
          <div className="mt-2 flex min-w-0 items-center gap-2"><h1 className="text-[32px] font-semibold tracking-[-0.045em] text-[var(--ls-text)]">API & CLI keys</h1><HelpHint label="API & CLI keys">Issue narrow machine credentials for CI and the Open Review CLI. Secret values are shown once and never stored in plaintext.</HelpHint></div>
        </div>
        <div className="flex items-center gap-3">
          <DataFreshness state={source === "live" ? "live" : source === "demo" ? "demo" : "unavailable"} />
          {tab === "active" ? <button className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-medium text-white disabled:opacity-45" disabled={source !== "live" || Boolean(created)} onClick={() => setCreating(true)} type="button"><Plus className="size-4" /> Create key</button> : null}
        </div>
      </header>

      <EnterpriseSettingsTabs active="api-keys" org={org} />

      <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="API key views">
        <KeyTab count={activeKeys.length} href={`/${org}/settings/api-keys?tab=active`} label="Active" selected={tab === "active"} />
        <KeyTab count={revokedKeys.length} href={`/${org}/settings/api-keys?tab=revoked`} label="Revoked & audit" selected={tab === "revoked"} />
        <KeyTab href={`/${org}/settings/api-keys?tab=guide`} label="Usage guide" selected={tab === "guide"} />
      </TabStateRouter>

      {notice ? <div aria-live="polite" className={cn("flex items-start gap-2 rounded-[12px] border px-4 py-3 text-sm", notice.tone === "success" ? "border-emerald-500/20 bg-emerald-500/[0.07] text-[var(--ls-success-text)]" : "border-red-500/20 bg-red-500/[0.07] text-[var(--ls-critical-text)]")}><CircleAlert className="mt-0.5 size-4 shrink-0" />{notice.text}</div> : null}

      {tab === "guide" ? <UsageGuide org={org} publicAPIURL={publicAPIURL} /> : !readable ? <PageState action={<button className="luminous-focus inline-flex h-9 items-center justify-center rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-xs font-semibold text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" onClick={() => router.refresh()} type="button">Refresh evidence</button>} detail={detail ?? "The API key control plane could not be loaded."} kind="unavailable" title="API key control plane unavailable" /> : (
        <>
          {readOnly ? <div className="rounded-[14px] border border-amber-500/25 bg-amber-500/[0.06] px-4 py-3 text-sm leading-6 text-[var(--ls-warning-text)]">Preview key records are read-only. No secret has been generated or retained here, and creation and revocation are disabled.</div> : null}
          <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
            <div className="flex flex-col gap-3 border-b border-[var(--ls-line)] px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex items-center gap-3"><span className="grid size-9 place-items-center rounded-[11px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><KeyRound className="size-4" /></span><div><h2 className="text-sm font-semibold text-[var(--ls-text)]">{tab === "revoked" ? "Revoked credentials" : "Active workspace keys"}</h2><p className="mt-0.5 text-xs text-[var(--ls-text-secondary)]">{visibleKeys.length} immutable credential record{visibleKeys.length === 1 ? "" : "s"}</p></div></div>
              <div className="inline-flex items-center gap-2 text-xs text-[var(--ls-text-tertiary)]"><ShieldCheck className="size-3.5 text-[var(--ls-success-text)]" />SHA-256 at rest · audited lifecycle</div>
            </div>
            {visibleKeys.length === 0 ? tab === "revoked" ? <EmptyRevoked /> : <EmptyKeys onCreate={() => setCreating(true)} /> : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[1020px] border-collapse text-left text-sm">
                  <thead className="bg-[var(--ls-surface-muted)] text-[11px] font-semibold uppercase tracking-[0.08em] text-[var(--ls-text-tertiary)]"><tr><th className="px-5 py-3">Name / prefix</th><th className="px-4 py-3">Caller</th><th className="px-4 py-3">Scopes</th><th className="px-4 py-3">Repositories</th><th className="px-4 py-3">Last used</th><th className="px-4 py-3">Expires</th><th className="px-4 py-3">Status</th><th className="px-5 py-3 text-right">Action</th></tr></thead>
                  <tbody className="divide-y divide-[var(--ls-line)]">{visibleKeys.map((key) => <KeyRow confirm={confirmRevoke === key.id} key={key.id} item={key} observedAt={observedAt} onCancel={() => setConfirmRevoke(undefined)} onConfirm={() => revoke(key.id)} onRevoke={() => setConfirmRevoke(key.id)} pending={pending === key.id} readOnly={readOnly} />)}</tbody>
                </table>
              </div>
            )}
          </section>
          <SectionDisclosure title={t("Credential safeguards")} >
            <Boundary icon={LockKeyhole} title="One-time plaintext">The control plane returns a secret only on creation. Closing that view permanently discards the retrievable value.</Boundary>
            <Boundary icon={ShieldCheck} title="Least privilege">Every request must satisfy both its action scope and exact repository allowlist. An empty repository list means all repositories in this workspace.</Boundary>
            <Boundary icon={Clock3} title="Expiry and revocation">Expired or revoked credentials are rejected before a workflow is admitted. Historical audit events retain only the visible prefix.</Boundary>
          </SectionDisclosure>
        </>
      )}

      {creating ? <CreateKeySheet busy={pending === "create"} onClose={() => setCreating(false)} onSubmit={createKey} /> : null}
      {created ? <OneTimeSecret copied={copied} creation={created} onClose={closeSecret} onCopy={copySecret} /> : null}
    </div>
  );
}

function KeyRow({ confirm, item, observedAt, onCancel, onConfirm, onRevoke, pending, readOnly }: { confirm: boolean; item: WorkspaceAPIKey; observedAt: string; onCancel: () => void; onConfirm: () => void; onRevoke: () => void; pending: boolean; readOnly: boolean }) {
  const status = item.revoked_at ? "Revoked" : item.expires_at && new Date(item.expires_at).getTime() <= new Date(observedAt).getTime() ? "Expired" : "Active";
  return <tr className="transition hover:bg-[var(--ls-surface-muted)]"><td className="px-5 py-4"><p className="font-medium text-[var(--ls-text)]">{item.name}</p><p className="mt-1 font-mono text-[11px] text-[var(--ls-text-tertiary)]">{item.prefix}••••</p></td><td className="max-w-48 px-4 py-4"><p className="truncate font-mono text-xs text-[var(--ls-text-secondary)]">{item.caller_subject}</p><p className="mt-1 text-[11px] text-[var(--ls-text-tertiary)]">by {item.created_by}</p></td><td className="px-4 py-4"><div className="flex max-w-56 flex-wrap gap-1">{item.scopes.map((scope) => <span className="rounded-full bg-[var(--ls-accent-soft)] px-2 py-1 text-[10px] font-medium text-[var(--ls-accent)]" key={scope}>{scope}</span>)}</div></td><td className="px-4 py-4 text-xs text-[var(--ls-text-secondary)]">{item.repositories.length ? <><span className="font-medium text-[var(--ls-text)]">{item.repositories.length}</span> selected</> : "All workspace repositories"}</td><td className="px-4 py-4 text-xs text-[var(--ls-text-secondary)]">{formatDate(item.last_used_at, "Never")}</td><td className="px-4 py-4 text-xs text-[var(--ls-text-secondary)]">{formatDate(item.expires_at, "Never")}</td><td className="px-4 py-4"><StatusPill status={status} /></td><td className="px-5 py-4 text-right">{readOnly ? <span className="text-xs text-[var(--ls-text-tertiary)]">Preview only</span> : status === "Active" ? confirm ? <span className="inline-flex items-center gap-1.5"><button className="luminous-focus rounded-[8px] px-2.5 py-1.5 text-xs text-[var(--ls-text-secondary)]" onClick={onCancel} type="button">Cancel</button><button className="luminous-focus inline-flex items-center gap-1.5 rounded-[8px] bg-red-500/10 px-2.5 py-1.5 text-xs font-medium text-[var(--ls-critical-text)]" disabled={pending} onClick={onConfirm} type="button">{pending ? <LoaderCircle className="size-3.5 animate-spin" /> : <Trash2 className="size-3.5" />}Confirm revoke</button></span> : <button className="luminous-focus rounded-[8px] px-2.5 py-1.5 text-xs font-medium text-[var(--ls-critical-text)] hover:bg-red-500/10" onClick={onRevoke} type="button">Revoke</button> : <span className="text-xs text-[var(--ls-text-tertiary)]">No actions</span>}</td></tr>;
}

function StatusPill({ status }: { status: "Active" | "Expired" | "Revoked" }) {
  const tone = status === "Active" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : status === "Expired" ? "bg-amber-500/10 text-[var(--ls-warning-text)]" : "bg-red-500/10 text-[var(--ls-critical-text)]";
  return <span className={cn("inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium", tone)}><span className="size-1.5 rounded-full bg-current" />{status}</span>;
}

function CreateKeySheet({ busy, onClose, onSubmit }: { busy: boolean; onClose: () => void; onSubmit: (event: FormEvent<HTMLFormElement>) => void }) {
  const dialogRef = useModalFocus<HTMLDivElement>(onClose);
  return <div aria-labelledby="create-key-title" aria-modal="true" className="fixed inset-0 z-50 flex justify-end bg-black/25 backdrop-blur-[2px]" ref={dialogRef} role="dialog"><form className="luminous-frosted flex h-full w-full max-w-xl flex-col border-l border-[var(--ls-line-strong)] shadow-[var(--ls-shadow-float)]" onSubmit={onSubmit}><div className="flex items-start justify-between border-b border-[var(--ls-line)] px-6 py-5"><div><p className="text-xs font-semibold uppercase tracking-[0.12em] text-[var(--ls-accent)]">Governed credential</p><div className="mt-2 flex min-w-0 items-center gap-2"><h2 className="text-xl font-semibold text-[var(--ls-text)]" id="create-key-title">Create API or CLI key</h2><HelpHint label="Create API or CLI key">Choose the smallest scope and shortest useful lifetime.</HelpHint></div></div><button aria-label="Close" className="luminous-focus grid size-9 place-items-center rounded-[10px] text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]" onClick={onClose} type="button"><X className="size-4" /></button></div><div className="flex-1 space-y-6 overflow-y-auto p-6"><label className="block"><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">Key name</span><input className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" data-dialog-initial-focus maxLength={80} name="name" placeholder="Release automation" required /></label><fieldset><legend className="text-xs font-medium text-[var(--ls-text-secondary)]">Action scopes</legend><div className="mt-2 divide-y divide-[var(--ls-line)] overflow-hidden rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface)]">{scopeOptions.map((scope, index) => <label className="flex cursor-pointer gap-3 p-4" key={scope.value}><input className="mt-1 size-4 accent-[var(--ls-accent)]" defaultChecked={index < 2} name={scope.value} type="checkbox" /><span><span className="block text-sm font-medium text-[var(--ls-text)]">{scope.label}</span><span className="mt-1 block text-xs leading-5 text-[var(--ls-text-secondary)]">{scope.detail}</span></span></label>)}</div></fieldset><label className="block"><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">Repository allowlist <span className="font-normal text-[var(--ls-text-tertiary)]">(optional)</span></span><textarea className="luminous-focus min-h-28 w-full resize-y rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-2.5 font-mono text-sm text-[var(--ls-text)]" name="repositories" placeholder={"RainLib/open-review-platform\nRainLib/platform-api"} /><span className="mt-1.5 block text-[11px] text-[var(--ls-text-tertiary)]">One owner/repository per line. Leave empty for all repositories in this workspace.</span></label><label className="block"><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">Lifetime</span><select className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" defaultValue="90" name="lifetime"><option value="30">30 days</option><option value="90">90 days</option><option value="365">1 year</option><option value="0">No expiry</option></select></label><div className="rounded-[12px] border border-amber-500/25 bg-amber-500/[0.06] p-4 text-xs leading-5 text-[var(--ls-text-secondary)]"><strong className="text-[var(--ls-warning-text)]">One-time display.</strong> The generated secret cannot be recovered. Losing it requires creating a replacement and revoking this key.</div></div><div className="flex justify-end gap-2 border-t border-[var(--ls-line)] px-6 py-4"><button className="luminous-focus h-10 rounded-[10px] px-4 text-sm text-[var(--ls-text-secondary)]" onClick={onClose} type="button">Cancel</button><button className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-medium text-white disabled:opacity-45" disabled={busy} type="submit">{busy ? <LoaderCircle className="size-4 animate-spin" /> : <KeyRound className="size-4" />}Create key</button></div></form></div>;
}

function OneTimeSecret({ copied, creation, onClose, onCopy }: { copied: boolean; creation: CreatedKey; onClose: () => void; onCopy: () => void }) {
  const dialogRef = useModalFocus<HTMLDivElement>(onClose, { closeOnEscape: false });
  return <div aria-labelledby="one-time-secret-title" aria-modal="true" className="fixed inset-0 z-[60] grid place-items-center bg-black/30 p-4 backdrop-blur-sm" ref={dialogRef} role="dialog"><section className="w-full max-w-2xl rounded-[24px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-raised)] p-6 shadow-[var(--ls-shadow-float)]"><span className="grid size-11 place-items-center rounded-[14px] bg-emerald-500/10 text-[var(--ls-success-text)]"><Check className="size-5" /></span><h2 className="mt-5 text-2xl font-semibold text-[var(--ls-text)]" id="one-time-secret-title">Key created — copy it now</h2><p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">This is the only time Open Review returns the full secret. It will not appear in the key list, logs, or audit events.</p><div className="mt-5 rounded-[14px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] p-4"><p className="text-[11px] font-semibold uppercase tracking-[0.1em] text-[var(--ls-text-tertiary)]">{creation.api_key.name}</p><div className="mt-2 flex items-center gap-3"><code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap font-mono text-sm text-[var(--ls-text)]">{creation.secret}</code><button className="luminous-focus inline-flex h-9 shrink-0 items-center gap-2 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs font-medium text-[var(--ls-text)]" data-dialog-initial-focus onClick={onCopy} type="button">{copied ? <Check className="size-3.5 text-[var(--ls-success-text)]" /> : <Clipboard className="size-3.5" />}{copied ? "Copied" : "Copy"}</button></div></div><div className="mt-5 rounded-[12px] border border-amber-500/25 bg-amber-500/[0.06] p-4 text-xs leading-5 text-[var(--ls-text-secondary)]">Store this value in your CI secret manager. Never place it in repository files, pull-request text, command history, or logs.</div><div className="mt-6 flex justify-end"><button className="luminous-focus h-10 rounded-[10px] bg-[var(--ls-accent)] px-5 text-sm font-medium text-white" onClick={onClose} type="button">I stored the key</button></div></section></div>;
}

function EmptyKeys({ onCreate }: { onCreate: () => void }) { return <div className="grid min-h-64 place-items-center p-8 text-center"><div><span className="mx-auto grid size-12 place-items-center rounded-[15px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><KeyRound className="size-5" /></span><h3 className="mt-4 text-base font-semibold text-[var(--ls-text)]">No machine credentials</h3><p className="mt-2 max-w-md text-sm leading-6 text-[var(--ls-text-secondary)]">Create a narrowly scoped key when CI or the CLI needs to submit or inspect a review.</p><button className="luminous-focus mt-4 min-h-6 text-sm font-medium text-[var(--ls-accent)]" onClick={onCreate} type="button">Create the first key</button></div></div>; }
function EmptyRevoked() { return <div className="grid min-h-64 place-items-center p-8 text-center"><div><span className="mx-auto grid size-12 place-items-center rounded-[15px] bg-[var(--ls-surface-muted)] text-[var(--ls-text-tertiary)]"><ShieldCheck className="size-5" /></span><h3 className="mt-4 text-base font-semibold text-[var(--ls-text)]">No revoked credentials</h3><p className="mt-2 max-w-md text-sm leading-6 text-[var(--ls-text-secondary)]">Revoked keys remain visible here as immutable lifecycle evidence and cannot be reactivated.</p></div></div>; }
function KeyTab({ count, href, label, selected }: { count?: number; href: string; label: string; selected: boolean }) { return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative inline-flex h-11 shrink-0 items-center gap-2 rounded-t-[10px] px-4 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={href} role="tab" tabIndex={selected ? 0 : -1}>{label}{count !== undefined ? <span className="rounded-full bg-[var(--ls-surface-muted)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)]">{count}</span> : null}{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>; }
function UsageGuide({ org, publicAPIURL }: { org: string; publicAPIURL?: string }) {
  const install = "go install github.com/RainLib/open-review-platform/cmd/openreview@latest";
  const configure = publicAPIURL ? cliEnvironmentTemplate(publicAPIURL, org) : undefined;
  return (
    <div className="grid gap-5 xl:grid-cols-[minmax(0,1.3fr)_minmax(320px,.7fr)]">
      <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-7">
        <div className="flex items-start gap-3">
          <span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><SquareTerminal className="size-5" /></span>
          <div>
            <div className="flex min-w-0 items-center gap-2"><h2 className="text-lg font-semibold text-[var(--ls-text)]">Open Review CLI</h2><HelpHint label="Open Review CLI">Install the open-source client, keep the secret in your CI vault, and submit only an existing PR or MR with exact base and head revisions.</HelpHint></div>
          </div>
        </div>
        <GuideCode label="Install" value={install} />
        {configure ? (
          <GuideCode label="Environment" value={configure} />
        ) : (
          <div className="mt-5 rounded-[12px] border border-amber-500/25 bg-amber-500/8 p-4 text-sm text-[var(--ls-text-secondary)]">
            Configure <code className="font-mono">OPEN_REVIEW_PUBLIC_API_URL</code> with the externally reachable HTTPS control-plane origin to enable a copyable environment example.
          </div>
        )}
        <div className="mt-5 rounded-[12px] border border-amber-500/25 bg-amber-500/[0.06] p-4 text-xs leading-5 text-[var(--ls-text-secondary)]"><strong className="text-[var(--ls-warning-text)]">Never paste a real key into a PR, issue, repository file, or support message.</strong> The CLI reads it only from <code className="font-mono">OPEN_REVIEW_API_KEY</code>.</div>
        <Link className="luminous-focus mt-5 inline-flex h-10 items-center rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white" href={`/${org}/cli-reviews?tab=quickstart`}>Open review quickstart</Link>
      </section>
      <aside className="space-y-4">
        <GuideCard icon={KeyRound} title="Minimum scopes" detail="Use reviews:create only for submitters, reviews:read for evidence readers, and runs:cancel only where cancellation is operationally required." />
        <GuideCard icon={ShieldCheck} title="Repository boundary" detail="Prefer an explicit owner/repository allowlist. Empty means every repository connected to this workspace." />
        <GuideCard icon={BookOpen} title="Rotation checklist" detail="Create a replacement, update the secret manager, verify one request, then revoke the old key and retain its audit receipt." />
      </aside>
    </div>
  );
}
function GuideCode({ label, value }: { label: string; value: string }) { return <div className="mt-5 overflow-hidden rounded-[13px] border border-slate-700 bg-[#101521]"><div className="flex items-center justify-between border-b border-slate-700 px-4 py-2.5"><span className="text-xs font-medium text-slate-300">{label}</span><CopyEvidenceButton label="Copy" value={value} /></div><pre className="overflow-x-auto p-4 text-xs leading-6 text-slate-200"><code>{value}</code></pre></div>; }
function GuideCard({ detail, icon: Icon, title }: { detail: string; icon: typeof KeyRound; title: string }) { return <section className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><Icon className="size-4 text-[var(--ls-accent)]" /><h3 className="mt-3 text-sm font-semibold text-[var(--ls-text)]">{title}</h3><p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">{detail}</p></section>; }
function Boundary({ children, icon: Icon, title }: { children: React.ReactNode; icon: typeof KeyRound; title: string }) { return <div className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><Icon className="size-4 text-[var(--ls-accent)]" />{title}</div><p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">{children}</p></div>; }
function formatDate(value: string | undefined, fallback: string) { if (!value) return fallback; return new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" }).format(new Date(value)); }
