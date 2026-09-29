"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { ArrowRight, BadgeCheck, CircleAlert, LoaderCircle, LockKeyhole, Sparkles } from "lucide-react";

import { ProductMark } from "@/components/marketing/marketing-shell";
import { PublicThemeToggle } from "@/components/onboarding/luminous-public-frame";

export function InvitationAcceptance({ token }: { token: string }) {
  const router = useRouter();
  const [status, setStatus] = useState<"ready" | "submitting" | "accepted" | "failed">("ready");
  const [message, setMessage] = useState<string>();

  async function acceptInvitation() {
    setStatus("submitting");
    setMessage(undefined);
    const response = await fetch("/api/invitations/accept", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token }),
    });
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    if (!response.ok) {
      setStatus("failed");
      setMessage(body.error ?? "This invitation could not be accepted.");
      return;
    }
    setStatus("accepted");
    window.history.replaceState({}, "", "/workspaces");
    router.refresh();
  }

  return (
    <main className="min-h-screen p-3 sm:p-5">
      <div className="mx-auto grid min-h-[calc(100vh-1.5rem)] max-w-[1120px] overflow-hidden rounded-[28px] border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] shadow-[var(--ls-shadow-float)] lg:min-h-[min(720px,calc(100vh-2.5rem))] lg:grid-cols-[minmax(0,.85fr)_minmax(460px,1.15fr)]">
        <section className="relative hidden overflow-hidden border-r border-[var(--ls-line)] bg-[linear-gradient(155deg,color-mix(in_srgb,var(--ls-accent-soft)_65%,var(--ls-surface))_0%,var(--ls-surface)_58%,color-mix(in_srgb,var(--ls-accent-soft)_38%,var(--ls-surface))_100%)] p-10 lg:flex lg:flex-col">
          <ProductMark variant="luminous" />
          <div className="my-auto max-w-sm">
            <span className="grid size-12 place-items-center rounded-[16px] bg-[var(--ls-accent)] text-white shadow-[var(--ls-shadow-float)]"><Sparkles className="size-5" /></span>
            <h2 className="mt-8 text-[42px] font-semibold leading-[1.03] tracking-[-0.065em] text-[var(--ls-text)]">A governed path into better reviews.</h2>
            <p className="mt-6 text-base leading-7 text-[var(--ls-text-secondary)]">Your access is granted only after the control plane verifies this one-time invitation and your signed-in identity.</p>
          </div>
          <div className="rounded-[18px] border border-[color:color-mix(in_srgb,var(--ls-accent)_18%,var(--ls-line))] bg-[color:color-mix(in_srgb,var(--ls-surface)_74%,var(--ls-accent-soft))] p-4 text-sm text-[var(--ls-text-secondary)]"><LockKeyhole className="mb-3 size-4 text-[var(--ls-accent)]" />The invitation link does not grant access by itself. It must match the Casdoor subject selected by the workspace owner.</div>
        </section>
        <section className="flex min-h-full flex-col p-5 sm:p-8 lg:p-10">
          <header className="flex items-center justify-between"><ProductMark variant="luminous" /><PublicThemeToggle /></header>
          <div className="mx-auto flex w-full max-w-[420px] flex-1 flex-col justify-center py-12">
            <span className="grid size-12 place-items-center rounded-[16px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><BadgeCheck className="size-6" /></span>
            <p className="mt-7 text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Workspace invitation</p>
            <h1 className="mt-3 text-3xl font-semibold tracking-[-0.055em] text-[var(--ls-text)]">Join your team&apos;s review workspace</h1>
            <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">Confirming adds the currently signed-in identity with the role chosen by the workspace owner. The link cannot be reused.</p>
            {message ? <div className="mt-6 flex gap-3 rounded-[14px] border border-[color:color-mix(in_srgb,var(--ls-danger)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-danger)_8%,var(--ls-surface))] p-3.5 text-sm leading-5 text-[var(--ls-danger)]"><CircleAlert className="mt-0.5 size-4 shrink-0" />{message}</div> : null}
            {status === "accepted" ? <div className="mt-7"><div className="rounded-[16px] border border-[color:color-mix(in_srgb,var(--ls-accent)_26%,transparent)] bg-[var(--ls-accent-soft)] p-4 text-sm leading-6 text-[var(--ls-text-secondary)]">Invitation accepted. Your workspace is now available in the selector.</div><Link className="luminous-focus mt-4 flex h-11 items-center justify-center gap-2 rounded-[11px] bg-[var(--ls-accent)] px-5 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)]" href="/workspaces">Open workspaces <ArrowRight className="size-4" /></Link></div> : <button className="luminous-focus mt-8 flex h-11 w-full items-center justify-center gap-2 rounded-[11px] bg-[var(--ls-accent)] px-5 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)] disabled:opacity-50" disabled={status === "submitting"} onClick={acceptInvitation} type="button">{status === "submitting" ? <LoaderCircle className="size-4 animate-spin" /> : <BadgeCheck className="size-4" />}Accept invitation</button>}
            <Link className="luminous-focus mt-5 text-center text-sm text-[var(--ls-text-secondary)] transition hover:text-[var(--ls-text)]" href="/workspaces">Return to workspaces</Link>
          </div>
        </section>
      </div>
    </main>
  );
}
