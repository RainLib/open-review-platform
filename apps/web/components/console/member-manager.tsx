"use client";

import { SectionDisclosure } from "./section-disclosure";

import { useWorkflowText } from "./ui-language-context";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { Check, CircleAlert, Copy, Link2, LoaderCircle, ShieldCheck, UserPlus, Users, X } from "lucide-react";

import { PageState, RecoveryAction } from "@/components/console/page-state";
import type { DataSource, WorkspaceAccessRequest, WorkspaceInvitation, WorkspaceMember } from "@/lib/control-api";
import { HelpHint } from "@/components/console/help-hint";

const roles: Array<{ value: WorkspaceMember["role"]; label: string }> = [
  { value: "owner", label: "Owner" }, { value: "admin", label: "Admin" },
  { value: "rule_admin", label: "Rule admin" }, { value: "reviewer", label: "Reviewer" },
  { value: "viewer", label: "Viewer" }, { value: "billing_viewer", label: "Billing viewer" },
];
const inviteRoles = roles.filter((role) => role.value !== "owner");

function formatDate(value: string) {
  return new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" }).format(new Date(value));
}

export function MemberManager({ accessRequests, actorSubject, detail, org, members, invitations, enabled, source }: {
  accessRequests: WorkspaceAccessRequest[]; actorSubject?: string; detail?: string; org: string; members: WorkspaceMember[]; invitations: WorkspaceInvitation[]; enabled: boolean; source: DataSource;
}) {
  const t = useWorkflowText();
  const router = useRouter();
  const [busy, setBusy] = useState<string>();
  const [message, setMessage] = useState<string>();
  const [createdInvite, setCreatedInvite] = useState<{ subject: string; link: string }>();
  const canDecideAccess = members.some((member) => member.subject === actorSubject && member.active && member.role === "owner");

  async function decideAccess(item: WorkspaceAccessRequest, decision: "approve" | "reject") {
    setBusy(`access:${item.id}`); setMessage(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/access-requests/${encodeURIComponent(item.id)}/decision`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ decision, revision: item.revision }),
      });
      const body = (await response.json().catch(() => ({}))) as { error?: string };
      setMessage(response.ok ? `${item.subject} ${decision === "approve" ? "received Viewer access" : "was declined"}.` : body.error ?? "The access request could not be decided.");
      if (response.ok) router.refresh();
    } catch {
      setMessage("The access request could not be decided. Please retry.");
    } finally {
      setBusy(undefined);
    }
  }

  async function update(subject: string, role: WorkspaceMember["role"]) {
    setBusy(`member:${subject}`); setMessage(undefined);
    const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/members/${encodeURIComponent(subject)}`, {
      method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ role }),
    });
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setMessage(response.ok ? `Role updated for ${subject}.` : body.error ?? "The member could not be updated.");
    setBusy(undefined);
    if (response.ok) router.refresh();
  }

  async function createInvitation(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const subject = String(form.get("subject") ?? "").trim();
    setBusy("create"); setMessage(undefined); setCreatedInvite(undefined);
    const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/invitations`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ subject, role: String(form.get("role") ?? "reviewer"), expires_in_hours: Number(form.get("expires_in_hours") ?? 168) }),
    });
    const body = (await response.json().catch(() => ({}))) as { error?: string; token?: string };
    setBusy(undefined);
    if (!response.ok || !body.token) { setMessage(body.error ?? "The invitation could not be created."); return; }
    setCreatedInvite({ subject, link: `${window.location.origin}/invitations/accept?token=${encodeURIComponent(body.token)}` });
    setMessage("Invitation created. Copy the one-time link before leaving this page.");
    formElement.reset(); router.refresh();
  }

  async function revoke(invitation: WorkspaceInvitation) {
    setBusy(`revoke:${invitation.id}`); setMessage(undefined);
    const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/invitations/${encodeURIComponent(invitation.id)}/revoke`, { method: "POST" });
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setMessage(response.ok ? `Invitation for ${invitation.subject} revoked.` : body.error ?? "The invitation could not be revoked.");
    setBusy(undefined);
    if (response.ok) router.refresh();
  }

  async function setMemberActivation(member: WorkspaceMember) {
    const active = !member.active;
    setBusy(`activation:${member.subject}`); setMessage(undefined);
    const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/members/${encodeURIComponent(member.subject)}/activation`, {
      method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ active }),
    });
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setMessage(response.ok ? `${member.subject} ${active ? "reactivated" : "deactivated"}.` : body.error ?? "The member could not be updated.");
    setBusy(undefined);
    if (response.ok) router.refresh();
  }

  async function copyInvite() {
    if (!createdInvite) return;
    try { await navigator.clipboard.writeText(createdInvite.link); setMessage("Invitation link copied. It is shown only for this creation."); }
    catch { setMessage("Copy was unavailable. Select and copy the one-time link below."); }
  }

  if (source === "unavailable") {
    return <PageState detail={detail ?? "Membership and invitation records could not be loaded. No role or activation change was attempted."} kind="unavailable" title="Member control plane is temporarily unavailable" />;
  }

  if (source === "unconfigured") {
    return <PageState action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect?tab=installations`} variant="primary">Connect control plane</RecoveryAction>} detail={detail ?? "Connect the control plane before managing tenant members or issuing one-time invitations."} kind="first-use-empty" title="Member management needs a connected control plane" />;
  }

  return <div className="space-y-6">
    {message ? <div className="flex items-start gap-2 rounded-[12px] border border-[color:color-mix(in_srgb,var(--ls-accent)_26%,transparent)] bg-[var(--ls-accent-soft)] px-4 py-3 text-sm text-[var(--ls-text-secondary)]"><CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />{message}</div> : null}
    {source === "demo" ? <div className="rounded-[14px] border border-amber-500/25 bg-amber-500/[0.06] px-4 py-3 text-sm leading-6 text-[var(--ls-warning-text)]">Preview membership data is read-only. No invitation link is present, and invitation, role, and activation changes are disabled.</div> : null}

    <fieldset className="min-w-0 space-y-5" disabled={!enabled}>
<section className="overflow-hidden rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
      <div className="flex items-center justify-between border-b border-[var(--ls-line)] px-5 py-4"><div className="flex items-center gap-3"><span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><Users className="size-4" /></span><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Workspace members</h2><HelpHint label="Workspace members">Provider identities and Casdoor subjects resolve to these tenant roles.</HelpHint></div></div></div><span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">{members.length}</span></div>
      {members.length === 0 ? <p className="px-5 py-14 text-center text-sm text-[var(--ls-text-tertiary)]">No members are visible to this identity.</p> : members.map((member) => { const isCurrentSession = member.subject === actorSubject; return <div className="grid gap-3 border-b border-[var(--ls-line)] px-5 py-4 last:border-b-0 sm:grid-cols-[auto_minmax(0,1fr)_220px_auto] sm:items-center" key={member.subject}><span className={`grid size-9 place-items-center rounded-full ${member.active ? "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]" : "bg-amber-500/10 text-amber-600"}`}><ShieldCheck className="size-4" /></span><div className="min-w-0"><p className={`truncate text-sm font-medium ${member.active ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)]"}`}>{member.subject}</p><p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">{isCurrentSession ? "Current signed-in identity · role protected" : member.active ? "Tenant-scoped identity" : `Deactivated${member.deactivated_at ? ` · ${formatDate(member.deactivated_at)}` : ""}`}</p></div><div className="relative"><select aria-label={`Role for ${member.subject}`} className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3.5 text-sm text-[var(--ls-text)] outline-none" value={member.role} disabled={!enabled || !member.active || isCurrentSession || Boolean(busy)} title={isCurrentSession ? "Your current session role is protected." : undefined} onChange={(event) => update(member.subject, event.target.value as WorkspaceMember["role"])}>{roles.map((role) => <option key={role.value} value={role.value}>{role.label}</option>)}</select>{busy === `member:${member.subject}` ? <LoaderCircle className="pointer-events-none absolute right-8 top-3 size-4 animate-spin text-[var(--ls-accent)]" /> : null}</div><button className="luminous-focus inline-flex h-9 items-center justify-center gap-1.5 rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-semibold text-[var(--ls-text-secondary)] transition hover:border-[var(--ls-danger)] hover:text-[var(--ls-danger)] disabled:opacity-40" disabled={!enabled || isCurrentSession || Boolean(busy)} title={isCurrentSession ? "Your current session cannot deactivate itself." : undefined} onClick={() => setMemberActivation(member)} type="button">{busy === `activation:${member.subject}` ? <LoaderCircle className="size-3.5 animate-spin" /> : <X className="size-3.5" />}{isCurrentSession ? "Current session" : member.active ? "Deactivate" : "Reinstate"}</button></div>})}
    </section>
    <SectionDisclosure title={t("Access requests")} count={accessRequests.length} defaultOpen={accessRequests.length>0}>
      <div className="flex items-center justify-between border-b border-[var(--ls-line)] px-5 py-4"><div className="flex items-center gap-3"><span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><UserPlus className="size-4" /></span><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Access requests</h2><HelpHint label="Access requests">Only a workspace Owner can grant Viewer access. Requests do not create members until approved.</HelpHint></div></div></div><span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">{accessRequests.length}</span></div>
      {accessRequests.length === 0 ? <p className="px-5 py-8 text-center text-sm text-[var(--ls-text-tertiary)]">No access requests are waiting for review.</p> : accessRequests.map((item) => <div className="grid gap-3 border-b border-[var(--ls-line)] px-5 py-4 last:border-b-0 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center" key={item.id}><div className="min-w-0"><p className="break-all text-sm font-medium text-[var(--ls-text)]">{item.subject}</p><p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">Requested {formatDate(item.created_at)} · revision {item.revision}</p>{item.note ? <p className="mt-2 whitespace-pre-wrap break-words text-xs leading-5 text-[var(--ls-text-secondary)]">{item.note}</p> : null}</div>{canDecideAccess ? <div className="flex items-center gap-2"><button className="luminous-focus inline-flex h-9 items-center justify-center gap-1.5 rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white disabled:opacity-40" disabled={!enabled || Boolean(busy)} onClick={() => decideAccess(item, "approve")} type="button">{busy === `access:${item.id}` ? <LoaderCircle className="size-3.5 animate-spin" /> : <Check className="size-3.5" />}Approve Viewer</button><button className="luminous-focus inline-flex h-9 items-center justify-center gap-1.5 rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-semibold text-[var(--ls-text-secondary)] disabled:opacity-40" disabled={!enabled || Boolean(busy)} onClick={() => decideAccess(item, "reject")} type="button"><X className="size-3.5" />Decline</button></div> : <span className="text-xs text-[var(--ls-text-tertiary)]">Owner decision required</span>}</div>)}
    </SectionDisclosure>
    <SectionDisclosure title={t("Invite a workspace member")} >
      <div className="flex items-start gap-3"><span className="grid size-9 shrink-0 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><UserPlus className="size-4" /></span><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Invite a workspace member</h2><HelpHint label="Invite a workspace member">Invite a known Casdoor subject. The acceptance link is shown once, and the signed-in identity must match the invited subject.</HelpHint></div></div></div>
      <form className="mt-5 grid gap-3 sm:grid-cols-[minmax(0,1fr)_180px_150px_auto] sm:items-end" onSubmit={createInvitation}>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">Identity subject<input className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3.5 text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)]" name="subject" placeholder="Casdoor subject" required /></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">Role<select className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3.5 text-sm text-[var(--ls-text)] outline-none" defaultValue="reviewer" name="role">{inviteRoles.map((role) => <option key={role.value} value={role.value}>{role.label}</option>)}</select></label>
        <label className="text-xs font-medium text-[var(--ls-text-secondary)]">Expires in<select className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-3.5 text-sm text-[var(--ls-text)] outline-none" defaultValue="168" name="expires_in_hours"><option value="24">24 hours</option><option value="72">3 days</option><option value="168">7 days</option><option value="720">30 days</option></select></label>
        <button className="luminous-focus flex h-10 items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-5 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)] disabled:opacity-40" disabled={!enabled || Boolean(busy)} type="submit">{busy === "create" ? <LoaderCircle className="size-4 animate-spin" /> : <Link2 className="size-4" />}Create invite</button>
      </form>
      {createdInvite ? <div className="mt-4 rounded-[14px] border border-[color:color-mix(in_srgb,var(--ls-accent)_28%,transparent)] bg-[var(--ls-accent-soft)] p-3.5"><div className="flex items-center justify-between gap-3"><p className="text-xs font-semibold text-[var(--ls-text)]">One-time link for {createdInvite.subject}</p><button aria-label="Copy invitation link" className="luminous-focus inline-flex size-8 items-center justify-center rounded-[8px] text-[var(--ls-accent)] transition hover:bg-[var(--ls-surface)]" onClick={copyInvite} type="button"><Copy className="size-4" /></button></div><input aria-label="One-time invitation link" className="mt-2 h-9 w-full rounded-[8px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-2.5 font-mono text-xs text-[var(--ls-text-secondary)]" readOnly value={createdInvite.link} onFocus={(event) => event.currentTarget.select()} /></div> : null}
    </SectionDisclosure>

    <SectionDisclosure title={t("Pending invitations")} count={invitations.filter(item=>item.status==="pending").length}>
      <div className="flex items-center justify-between border-b border-[var(--ls-line)] px-5 py-4"><div className="flex items-center gap-3"><span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]"><Link2 className="size-4" /></span><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Pending invitations</h2><HelpHint label="Pending invitations">Invites become invalid after acceptance, expiry, or revocation.</HelpHint></div></div></div><span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">{invitations.filter((invitation) => invitation.status === "pending").length}</span></div>
      {invitations.length === 0 ? <p className="px-5 py-8 text-center text-sm text-[var(--ls-text-tertiary)]">No workspace invitations yet.</p> : invitations.map((invitation) => <div className="grid gap-3 border-b border-[var(--ls-line)] px-5 py-4 last:border-b-0 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center" key={invitation.id}><div className="min-w-0"><p className="truncate text-sm font-medium text-[var(--ls-text)]">{invitation.subject}</p><p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">{invitation.role.replace("_", " ")} · Expires {formatDate(invitation.expires_at)}</p></div><span className="w-fit rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs capitalize text-[var(--ls-text-secondary)]">{invitation.status}</span>{invitation.status === "pending" ? <button className="luminous-focus inline-flex h-9 items-center justify-center gap-1.5 rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-semibold text-[var(--ls-text-secondary)] transition hover:border-[var(--ls-danger)] hover:text-[var(--ls-danger)] disabled:opacity-40" disabled={!enabled || Boolean(busy)} onClick={() => revoke(invitation)} type="button">{busy === `revoke:${invitation.id}` ? <LoaderCircle className="size-3.5 animate-spin" /> : <X className="size-3.5" />}Revoke</button> : <span />}</div>)}
    </SectionDisclosure>


    </fieldset>
  </div>;
}
