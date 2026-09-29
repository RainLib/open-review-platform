"use client";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { Gauge, LoaderCircle } from "lucide-react";

import type { UsageDashboard } from "@/lib/control-api";

export function UsageEntitlementForm({ dashboard, enabled, org }: { dashboard: UsageDashboard; enabled: boolean; org: string }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setMessage(undefined);
    const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/usage/entitlement`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        monthly_review_limit: Number(form.get("monthly_review_limit")),
        soft_warning_percent: Number(form.get("soft_warning_percent")),
      }),
    });
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setBusy(false);
    setMessage(response.ok ? "Quota policy updated. New review admissions use it immediately." : body.error ?? "Quota policy could not be updated.");
    if (response.ok) router.refresh();
  }

  return (
    <form className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]" onSubmit={submit}>
      <div className="flex items-start gap-3">
        <span className="grid size-9 place-items-center rounded-[11px] bg-[var(--ls-surface-muted)] text-[var(--ls-accent)]"><Gauge className="size-4" /></span>
        <div><h2 className="text-sm font-semibold text-[var(--ls-text)]">Admission quota</h2><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Use 0 for unlimited self-hosted operation. Reserved runs count immediately, preventing concurrent over-admission.</p></div>
      </div>
      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">Monthly reviews<input className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] disabled:opacity-45" defaultValue={dashboard.entitlement.monthly_review_limit} disabled={!enabled || busy} min="0" name="monthly_review_limit" required type="number" /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">Warning threshold (%)<input className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] disabled:opacity-45" defaultValue={dashboard.entitlement.soft_warning_percent} disabled={!enabled || busy} max="100" min="1" name="soft_warning_percent" required type="number" /></label>
      </div>
      <div className="mt-5 flex flex-wrap items-center gap-3">
        <button className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-xs font-semibold text-white disabled:opacity-45" disabled={!enabled || busy} type="submit">{busy ? <LoaderCircle className="size-4 animate-spin" /> : <Gauge className="size-4" />}Save quota</button>
        {message ? <p aria-live="polite" className="text-xs text-[var(--ls-text-secondary)]">{message}</p> : null}
      </div>
    </form>
  );
}
