"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { CircleAlert, RefreshCw, ShieldCheck } from "lucide-react";

import type { UsageDashboard } from "@/lib/control-api";
import { cn } from "@/lib/utils";

const timestamp = new Intl.DateTimeFormat("en", {
  dateStyle: "medium",
  timeStyle: "short",
  timeZone: "UTC",
});

type Report = UsageDashboard["reconciliation"] & {
  repaired_runs: number;
  adjustments: number;
  reconciled_at: string;
  reconciled_by: string;
};

export function UsageReconciliationPanel({
  dashboard,
  enabled,
  org,
}: {
  dashboard: UsageDashboard;
  enabled: boolean;
  org: string;
}) {
  const router = useRouter();
  const [reason, setReason] = useState("");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();
  const [failed, setFailed] = useState(false);
  const status = dashboard.reconciliation;
  const clean = status.state === "clean";

  async function reconcile(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setFailed(false);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/usage/reconcile`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            period_start: dashboard.period_start.slice(0, 10),
            reason,
            idempotency_key: `console-${crypto.randomUUID()}`,
          }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as
        | Report
        | { error?: string };
      if (!response.ok) {
        throw new Error(
          "error" in payload && payload.error
            ? payload.error
            : "Usage reconciliation failed.",
        );
      }
      const report = payload as Report;
      setMessage(
        report.repaired_runs > 0
          ? `Repaired ${report.repaired_runs} run(s) and appended ${report.adjustments} immutable adjustment event(s).`
          : `Checked ${report.scanned_runs} run(s); no drift required repair.`,
      );
      setReason("");
      router.refresh();
    } catch (error) {
      setFailed(true);
      setMessage(
        error instanceof Error
          ? error.message
          : "Usage reconciliation failed.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-start">
        <div className="flex gap-3">
          <span
            className={cn(
              "grid size-10 shrink-0 place-items-center rounded-[12px]",
              clean
                ? "bg-emerald-500/10 text-[var(--ls-success-text)]"
                : "bg-amber-500/10 text-[var(--ls-warning-text)]",
            )}
          >
            {clean ? (
              <ShieldCheck className="size-4" />
            ) : (
              <CircleAlert className="size-4" />
            )}
          </span>
          <div>
            <h2 className="text-base font-semibold text-[var(--ls-text)]">
              Usage reconciliation
            </h2>
            <p className="mt-1 max-w-2xl text-xs leading-5 text-[var(--ls-text-secondary)]">
              Review-run state is authoritative. Reconciliation repairs mutable
              reservations and appends adjustment evidence; it never updates or
              deletes an existing ledger event.
            </p>
          </div>
        </div>
        <span
          className={cn(
            "w-fit rounded-full px-2.5 py-1 text-[10px] font-semibold uppercase",
            clean
              ? "bg-emerald-500/10 text-[var(--ls-success-text)]"
              : status.state === "drift"
                ? "bg-amber-500/10 text-[var(--ls-warning-text)]"
                : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]",
          )}
        >
          {status.state === "drift"
            ? `${status.drifted_runs} drifted`
            : status.state}
        </span>
      </div>

      <dl className="mt-5 grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
        <Fact label="Runs scanned" value={status.scanned_runs} />
        <Fact label="Missing reservations" value={status.missing_reservations} />
        <Fact label="State mismatches" value={status.state_mismatches} />
        <Fact label="Missing ledger proof" value={status.missing_ledger_proofs} />
      </dl>

      <div className="mt-5 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
        Period: {timestamp.format(new Date(status.period_start))} UTC —{" "}
        {timestamp.format(new Date(status.period_end))} UTC
        {status.last_reconciled_at ? (
          <span className="block text-[var(--ls-text-tertiary)]">
            Last reconciled {timestamp.format(new Date(status.last_reconciled_at))} UTC
            {status.last_reconciled_by
              ? ` by ${status.last_reconciled_by}`
              : ""}
          </span>
        ) : null}
      </div>

      <form className="mt-5 grid gap-3" onSubmit={reconcile}>
        <label className="grid gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">
          Operational reason
          <input
            className="luminous-focus h-10 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3 text-sm text-[var(--ls-text)] disabled:cursor-not-allowed disabled:opacity-60"
            disabled={!enabled || pending}
            maxLength={500}
            minLength={3}
            onChange={(event) => setReason(event.target.value)}
            placeholder="For example: monthly ledger integrity check"
            required
            value={reason}
          />
        </label>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-[11px] leading-5 text-[var(--ls-text-tertiary)]">
            Owner or Admin only. This action does not create invoices, alter
            entitlement limits, or call a billing provider.
          </p>
          <button
            className="luminous-focus inline-flex h-10 shrink-0 items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-xs font-semibold text-white disabled:cursor-not-allowed disabled:opacity-60"
            disabled={!enabled || pending}
            type="submit"
          >
            <RefreshCw className={cn("size-3.5", pending && "animate-spin")} />
            {pending ? "Reconciling…" : "Reconcile period"}
          </button>
        </div>
      </form>
      {message ? (
        <p
          aria-live="polite"
          className={cn(
            "mt-4 rounded-[10px] px-3 py-2 text-xs leading-5",
            failed
              ? "bg-red-500/[0.07] text-[var(--ls-critical-text)]"
              : "bg-emerald-500/[0.07] text-[var(--ls-success-text)]",
          )}
          role="status"
        >
          {message}
        </p>
      ) : null}
    </section>
  );
}

function Fact({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-[11px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-3">
      <dt className="text-[10px] text-[var(--ls-text-tertiary)]">{label}</dt>
      <dd className="mt-1 text-sm font-semibold text-[var(--ls-text)]">{value}</dd>
    </div>
  );
}
