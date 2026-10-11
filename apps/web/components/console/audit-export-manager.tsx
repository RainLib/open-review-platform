"use client";

import type { FormEvent } from "react";
import { useMemo, useState } from "react";
import Link from "next/link";
import { Download, FileArchive, LoaderCircle, ShieldCheck } from "lucide-react";
import { useRouter } from "next/navigation";

import type { DataGovernanceJob, DataSource } from "@/lib/control-api";
import { HelpHint } from "@/components/console/help-hint";

const dateFormatter = new Intl.DateTimeFormat("en", {
  dateStyle: "medium",
  timeStyle: "short",
  timeZone: "UTC",
});

const stateLabel: Record<DataGovernanceJob["state"], string> = {
  requested: "Requested",
  awaiting_approval: "Awaiting approval",
  queued: "Queued",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
  rejected: "Rejected",
};

export function AuditExportManager({
  detail,
  endDate,
  jobs,
  org,
  source,
  startDate,
}: {
  detail?: string;
  endDate: string;
  jobs: DataGovernanceJob[];
  org: string;
  source: DataSource;
  startDate: string;
}) {
  const router = useRouter();
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<
    { tone: "error" | "success"; text: string } | undefined
  >();
  const auditJobs = useMemo(
    () => jobs.filter((job) => job.kind === "export" && job.data_classes.includes("audit")),
    [jobs],
  );
	const writable = source === "live";

  async function requestExport(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
	if (pending || !writable) return;
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const start = String(form.get("start") ?? "");
    const end = String(form.get("end") ?? "");
    const reason = String(form.get("reason") ?? "").trim();
    const startAt = new Date(`${start}T00:00:00.000Z`);
    const endAt = new Date(`${end}T00:00:00.000Z`);
    if (!start || !end || Number.isNaN(startAt.getTime()) || Number.isNaN(endAt.getTime()) || startAt >= endAt) {
      setMessage({ tone: "error", text: "Choose a valid UTC range with an end date after the start date." });
      return;
    }
    if (reason.length < 3) {
      setMessage({ tone: "error", text: "Provide an operational reason of at least three characters." });
      return;
    }
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/data-governance/jobs`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            kind: "export",
            scope_kind: "audit_range",
            scope_ref: `${startAt.toISOString()}/${endAt.toISOString()}`,
            data_classes: ["audit"],
            idempotency_key: `audit-export-${crypto.randomUUID()}`,
            reason,
          }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as { error?: string };
      if (!response.ok) throw new Error(payload.error ?? "The audit export request could not be created.");
      setMessage({
        tone: "success",
        text: "Encrypted audit export queued. A download appears only after the executor records a completion receipt.",
      });
      formElement.reset();
      router.refresh();
    } catch (error) {
      setMessage({
        tone: "error",
        text: error instanceof Error ? error.message : "The audit export request could not be created.",
      });
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="space-y-5">
      <section className="grid gap-5 rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] lg:grid-cols-[minmax(0,1fr)_300px]">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">Governed disclosure</p>
          <div className="mt-2 flex min-w-0 items-center gap-2"><h2 className="text-xl font-semibold tracking-[-0.035em] text-[var(--ls-text)]">Request an encrypted audit export</h2><HelpHint label="Request an encrypted audit export">Exports are durable, tenant-scoped operations. They record the requester and range, encrypt the generated artifact, require an executor receipt, and audit every authenticated download.</HelpHint></div>
          {source === "live" || source === "demo" ? (
            <form className="mt-5 grid gap-3 sm:grid-cols-2" onSubmit={requestExport}>
              <fieldset className="contents" disabled={!writable}>
                <DateField defaultValue={startDate} label="Start date (UTC)" name="start" />
                <DateField defaultValue={endDate} label="End date (UTC)" name="end" />
                <label className="sm:col-span-2">
                <span className="text-xs font-medium text-[var(--ls-text-secondary)]">Operational reason</span>
                <input className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" name="reason" placeholder="Compliance evidence request" required />
                </label>
                <button className="luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white disabled:opacity-45 sm:col-span-2 sm:justify-self-end" disabled={pending || !writable} type="submit">
                {pending ? <LoaderCircle className="size-4 animate-spin" /> : <FileArchive className="size-4" />}
                {pending ? "Queuing export…" : writable ? "Queue encrypted export" : "Preview only"}
                </button>
              </fieldset>
            </form>
          ) : (
            <div className="mt-5 rounded-[12px] border border-amber-500/25 bg-amber-500/[0.06] p-4 text-sm leading-6 text-[var(--ls-warning-text)]">
              {detail ?? "Audit export requires a live control-plane connection and an owner or administrator role."}
            </div>
          )}
          {message ? <p aria-live="polite" className={`mt-4 text-sm leading-6 ${message.tone === "success" ? "text-[var(--ls-success-text)]" : "text-[var(--ls-critical-text)]"}`}>{message.text}</p> : null}
        </div>
        <aside className="rounded-[15px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 text-xs leading-5 text-[var(--ls-text-secondary)]">
          <ShieldCheck className="size-5 text-[var(--ls-accent)]" />
          <p className="mt-3 font-semibold text-[var(--ls-text)]">No instant download</p>
          <p className="mt-1.5">The browser never composes export contents. A separate trusted executor creates an encrypted, expiring artifact after this request is accepted.</p>
          <Link className="luminous-focus mt-4 inline-flex min-h-6 items-center font-medium text-[var(--ls-accent)] hover:text-[var(--ls-accent-hover)]" href={`/${encodeURIComponent(org)}/settings/data?tab=jobs`}>Open all governance jobs</Link>
        </aside>
      </section>

      <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
        <div className="flex items-center justify-between border-b border-[var(--ls-line)] px-5 py-4"><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Audit export jobs</h2><HelpHint label="Audit export jobs">Newest first · a completion state alone does not expose an artifact.</HelpHint></div></div><span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">{auditJobs.length} jobs</span></div>
        {auditJobs.length ? <div className="divide-y divide-[var(--ls-line)]">{auditJobs.map((job) => <AuditExportRow job={job} key={job.id} org={org} />)}</div> : <div className="grid min-h-44 place-items-center p-8 text-center"><div><FileArchive className="mx-auto size-5 text-[var(--ls-text-tertiary)]" /><p className="mt-3 text-sm text-[var(--ls-text-secondary)]">No governed audit exports yet.</p></div></div>}
      </section>
    </div>
  );
}

function DateField({ defaultValue, label, name }: { defaultValue: string; label: string; name: string }) {
  return <label><span className="text-xs font-medium text-[var(--ls-text-secondary)]">{label}</span><input className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" defaultValue={defaultValue} name={name} required type="date" /></label>;
}

function AuditExportRow({ job, org }: { job: DataGovernanceJob; org: string }) {
  const range = (job.scope_ref ?? "").split("/");
  const started = range.length === 2 ? `${dateFormatter.format(new Date(range[0]))} – ${dateFormatter.format(new Date(range[1]))}` : "Recorded audit range";
  return <article className="flex flex-col justify-between gap-3 px-5 py-4 sm:flex-row sm:items-center"><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><p className="text-sm font-medium text-[var(--ls-text)]">{started}</p><span className="rounded-full bg-[var(--ls-surface-muted)] px-2 py-0.5 text-[10px] font-semibold text-[var(--ls-text-secondary)]">{stateLabel[job.state]}</span></div><p className="mt-1 truncate text-xs text-[var(--ls-text-secondary)]">{job.reason} · requested by {job.requested_by}</p>{job.error_message ? <p className="mt-1 text-xs text-[var(--ls-critical-text)]">{job.error_code}: {job.error_message}</p> : null}</div>{job.artifact_ready ? <a className="luminous-focus inline-flex h-9 shrink-0 items-center justify-center gap-1.5 rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white" href={`/api/tenants/${encodeURIComponent(org)}/data-governance/jobs/${encodeURIComponent(job.id)}/artifact`}><Download className="size-3.5" />Download</a> : <span className="shrink-0 text-xs text-[var(--ls-text-tertiary)]">{job.state === "failed" ? "Review failure receipt" : "Artifact pending"}</span>}</article>;
}
