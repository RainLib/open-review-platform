"use client";

import { type FormEvent, useState } from "react";
import { LoaderCircle, UserPlus } from "lucide-react";

const slugPattern = /^[a-z0-9][a-z0-9-]{1,62}$/;

export function RequestAccessForm({ defaultSlug = "" }: { defaultSlug?: string }) {
  const [open, setOpen] = useState(false);
  const [slug, setSlug] = useState(slugPattern.test(defaultSlug) ? defaultSlug : "");
  const [note, setNote] = useState("");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState("");
  const [received, setReceived] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!slugPattern.test(slug) || new TextEncoder().encode(note.trim()).length > 1000) {
      setMessage("Use a valid workspace URL slug and a note under 1,000 bytes.");
      return;
    }
    setPending(true);
    setMessage("");
    try {
      const response = await fetch("/api/workspace-access-requests", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ slug, note: note.trim() }),
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as { error?: string };
        setMessage(body.error ?? "The request could not be sent. Please try again.");
        return;
      }
      setReceived(true);
      setMessage("Request received. If this workspace exists, its owner can review it in Members. This does not grant access until approved.");
    } catch {
      setMessage("The request could not be sent. Please try again.");
    } finally {
      setPending(false);
    }
  }

  if (!open) {
    return <button className="luminous-focus inline-flex h-9 items-center justify-center gap-2 rounded-[9px] bg-[var(--ls-accent)] px-3.5 text-xs font-semibold text-white transition hover:bg-[var(--ls-accent-hover)]" onClick={() => setOpen(true)} type="button"><UserPlus className="size-3.5" />Request access</button>;
  }

  return <form className="mx-auto mt-3 grid max-w-md gap-3 text-left" onSubmit={submit}>
    <label className="text-xs font-medium text-[var(--ls-text-secondary)]">Workspace URL slug
      <input autoComplete="off" className="luminous-focus mt-1.5 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" disabled={pending || received} maxLength={63} onChange={(event) => setSlug(event.target.value.toLowerCase().trim())} placeholder="my-team" required value={slug} />
    </label>
    <label className="text-xs font-medium text-[var(--ls-text-secondary)]">Note to the owner (optional)
      <textarea className="luminous-focus mt-1.5 min-h-20 w-full resize-y rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-2 text-sm text-[var(--ls-text)]" disabled={pending || received} maxLength={1000} onChange={(event) => setNote(event.target.value)} placeholder="Why do you need access?" value={note} />
    </label>
    <p className="text-xs leading-5 text-[var(--ls-text-tertiary)]">For privacy, the result does not confirm whether a workspace exists. An owner decides whether to grant Viewer access.</p>
    {message ? <p aria-live="polite" className={`text-xs leading-5 ${received ? "text-[var(--ls-success-text)]" : "text-[var(--ls-critical-text)]"}`}>{message}</p> : null}
    {!received ? <button className="luminous-focus inline-flex h-9 items-center justify-center gap-2 rounded-[9px] bg-[var(--ls-accent)] px-4 text-xs font-semibold text-white disabled:opacity-50" disabled={pending} type="submit">{pending ? <LoaderCircle className="size-3.5 animate-spin" /> : <UserPlus className="size-3.5" />}Send request</button> : null}
  </form>;
}
