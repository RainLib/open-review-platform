"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { CircleAlert, LoaderCircle, UserRoundCheck } from "lucide-react";

import type { IssueDetail, WorkspaceMember } from "@/lib/control-api";
import { cn } from "@/lib/utils";

type IssueAction = "assign" | "unassign" | "resolve" | "reopen" | "false_positive" | "suppression_cleared";

export function IssueActions({ issue, members, org }: { issue: IssueDetail; members: WorkspaceMember[]; org: string }) {
  const router = useRouter();
  const [revision, setRevision] = useState(issue.revision);
  const [status, setStatus] = useState(issue.status);
  const [assignee, setAssignee] = useState(issue.assignee_subject ?? "");
  const [selectedAssignee, setSelectedAssignee] = useState(issue.assignee_subject ?? "");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState<IssueAction>();
  const [message, setMessage] = useState<string>();
  const [messageTone, setMessageTone] = useState<"success" | "error">();

  async function mutate(action: IssueAction, extra?: { assignee_subject?: string }) {
    if (!issue.can_manage || busy) return;
    setBusy(action);
    setMessage(undefined);
    setMessageTone(undefined);
    try {
      const actionReason = action === "resolve" || action === "reopen" || action === "false_positive" || action === "suppression_cleared" ? reason : "";
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/issues/${encodeURIComponent(issue.id)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action, expected_revision: revision, reason: actionReason, ...extra }),
      });
      const body = (await response.json().catch(() => ({}))) as IssueDetail & { error?: string };
      if (!response.ok) {
        setMessage(body.error ?? "The issue could not be updated.");
        setMessageTone("error");
        return;
      }
      setRevision(body.revision);
      setStatus(body.status);
      setAssignee(body.assignee_subject ?? "");
      setSelectedAssignee(body.assignee_subject ?? "");
      setReason("");
      setMessage("Issue workflow updated and recorded in the audit timeline.");
      setMessageTone("success");
      router.refresh();
    } catch {
      setMessage("The issue update could not reach the control plane. Try again without changing the current revision.");
      setMessageTone("error");
    } finally {
      setBusy(undefined);
    }
  }

  const eligibleMembers = members.filter((member) => member.role !== "billing_viewer");
  const missingCurrentAssignee = assignee && !eligibleMembers.some((member) => member.subject === assignee);
  const disabled = !issue.can_manage || Boolean(busy);
  return (
    <section className="w-full max-w-xl rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-3 shadow-[var(--ls-shadow-control)]">
      <div className="flex flex-wrap items-center gap-2">
        {status === "suppressed" && issue.disposition_kind === "exception" ? (
          <button className="luminous-focus inline-flex h-9 cursor-not-allowed items-center justify-center rounded-[9px] border border-amber-500/20 bg-amber-500/[0.08] px-3 text-xs font-semibold text-[var(--ls-warning-text)]" disabled type="button">Governed exception active</button>
        ) : status === "suppressed" ? (
          <ActionButton busy={busy === "suppression_cleared"} disabled={disabled} label="Clear suppression" onClick={() => mutate("suppression_cleared")} primary />
        ) : status === "resolved" ? (
          <ActionButton busy={busy === "reopen"} disabled={disabled} label="Reopen" onClick={() => mutate("reopen")} primary />
        ) : (
          <ActionButton busy={busy === "resolve"} disabled={disabled} label="Resolve" onClick={() => mutate("resolve")} primary />
        )}
        <ActionButton busy={busy === "false_positive"} disabled={disabled || reason.trim().length < 3 || status === "suppressed"} label="False positive" onClick={() => mutate("false_positive")} />
        <span className="ml-auto text-[11px] text-[var(--ls-text-tertiary)]">Revision {revision}</span>
      </div>
      <label className="mt-3 block text-[11px] font-medium text-[var(--ls-text-tertiary)]">
        Decision evidence
        <textarea className="luminous-focus mt-1.5 min-h-16 w-full resize-y rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3 py-2 text-xs leading-5 text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]" onChange={(event) => setReason(event.target.value)} placeholder="Required for false positive; optional for resolution" value={reason} />
      </label>
      <div className="mt-3 grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
        <label className="sr-only" htmlFor="issue-assignee">Issue assignee</label>
        <select className="luminous-focus h-10 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs text-[var(--ls-text)]" disabled={disabled} id="issue-assignee" onChange={(event) => setSelectedAssignee(event.target.value)} value={selectedAssignee}>
          <option value="">Unassigned</option>
          {missingCurrentAssignee ? <option value={assignee}>{assignee} · current assignee</option> : null}
          {eligibleMembers.map((member) => <option key={member.subject} value={member.subject}>{member.subject} · {member.role.replaceAll("_", " ")}</option>)}
        </select>
        <button className="luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] px-3 text-xs font-medium text-[var(--ls-text)] disabled:cursor-not-allowed disabled:opacity-45" disabled={disabled || selectedAssignee === assignee} onClick={() => mutate(selectedAssignee ? "assign" : "unassign", { assignee_subject: selectedAssignee || undefined })} type="button">
          {busy === "assign" || busy === "unassign" ? <LoaderCircle className="size-3.5 animate-spin" /> : <UserRoundCheck className="size-3.5" />}
          Save owner
        </button>
      </div>
      <Link className="luminous-focus mt-3 inline-flex h-9 w-full items-center justify-center rounded-[9px] border border-[var(--ls-line-strong)] text-xs font-medium text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" href={`/${encodeURIComponent(org)}/rules/exceptions?issue=${encodeURIComponent(issue.id)}`}>
        {issue.exception_request?.effective_state === "pending" ? "View pending exception" : issue.exception_request?.effective_state === "approved" ? "View approved exception" : "Request governed exception"}
      </Link>
      {!issue.can_manage ? <p className="mt-3 text-xs text-[var(--ls-warning-text)]">Reviewer, rule admin, admin, or owner role is required.</p> : null}
      {message ? <p aria-live="polite" className={cn("mt-3 flex items-start gap-2 text-xs leading-5", messageTone === "success" ? "text-[var(--ls-success-text)]" : "text-[var(--ls-warning-text)]")}><CircleAlert className="mt-0.5 size-3.5 shrink-0" />{message}</p> : null}
    </section>
  );
}

function ActionButton({ busy, disabled, label, onClick, primary = false }: { busy: boolean; disabled: boolean; label: string; onClick: () => void; primary?: boolean }) {
  return <button className={cn("luminous-focus inline-flex h-9 items-center justify-center gap-2 rounded-[9px] px-3 text-xs font-semibold disabled:cursor-not-allowed disabled:opacity-45", primary ? "bg-[var(--ls-accent)] text-white" : "border border-[var(--ls-line-strong)] text-[var(--ls-text)]")} disabled={disabled} onClick={onClick} type="button">{busy ? <LoaderCircle className="size-3.5 animate-spin" /> : null}{label}</button>;
}
