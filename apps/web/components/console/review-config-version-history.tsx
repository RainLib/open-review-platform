"use client";

import { Clock3, LoaderCircle } from "lucide-react";
import { useState } from "react";

import type { DataSource, ReviewConfigHistory, ReviewConfigSection, ReviewConfigView } from "@/lib/control-api";

type HistoryState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "ready"; history: ReviewConfigHistory }
  | { kind: "error"; detail: string };

// This is shared provenance UI for every review-policy section. It loads only
// after an operator asks for it, so the policy editor remains focused and the
// page does not expand into an unbounded audit log by default.
export function ReviewConfigVersionHistory({
  org,
  history,
  historyDetail,
  section,
  source,
  view,
}: {
  org: string;
  history?: ReviewConfigHistory;
  historyDetail?: string;
  section: ReviewConfigSection;
  source: DataSource;
  view: ReviewConfigView;
}) {
  const [state, setState] = useState<HistoryState>({ kind: "idle" });
  const [open, setOpen] = useState(false);

  async function toggle() {
    const nextOpen = !open;
    setOpen(nextOpen);
    if (!nextOpen || state.kind !== "idle") return;

    if (source === "demo") {
      if (history) setState({ kind: "ready", history });
      else setState({ kind: "error", detail: historyDetail ?? "Preview provenance is unavailable." });
      return;
    }

    setState({ kind: "loading" });
    const query = new URLSearchParams({ scope_kind: view.requested_scope_kind });
    if (view.requested_scope_ref) query.set("scope_ref", view.requested_scope_ref);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/review-config/${encodeURIComponent(section)}/history?${query.toString()}`,
      );
      const body = (await response.json()) as ReviewConfigHistory | { error?: string };
      if (!response.ok || !("versions" in body)) {
        throw new Error("error" in body && body.error ? body.error : `History could not be loaded (${response.status}).`);
      }
      setState({ kind: "ready", history: body });
    } catch (error) {
      setState({ kind: "error", detail: error instanceof Error ? error.message : "Configuration history could not be loaded." });
    }
  }

  return (
    <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5">
      <button
        aria-controls={`review-config-history-${section}`}
        aria-expanded={open}
        className="luminous-focus flex w-full items-center justify-between gap-3 rounded-[10px] text-left"
        disabled={source !== "live" && source !== "demo"}
        onClick={toggle}
        type="button"
      >
        <span className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]"><Clock3 className="size-4 text-[var(--ls-accent)]" /> Version history</span>
        <span className="rounded-full bg-[var(--ls-surface-muted)] px-2 py-1 text-[11px] text-[var(--ls-text-secondary)]">v{view.revision || "default"}</span>
      </button>
      <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">Immutable provenance stays compact until you open it.</p>

      {open ? (
        <div className="mt-4 border-t border-[var(--ls-line)] pt-4" id={`review-config-history-${section}`}>
          {state.kind === "loading" ? <p aria-live="polite" className="flex items-center gap-2 text-xs text-[var(--ls-text-secondary)]"><LoaderCircle className="size-4 animate-spin" /> Loading revision provenance…</p> : null}
          {state.kind === "error" ? <p className="rounded-[10px] bg-red-500/10 px-3 py-2.5 text-xs leading-5 text-[var(--ls-critical-text)]">{state.detail}</p> : null}
          {state.kind === "ready" ? <HistoryList history={state.history} /> : null}
        </div>
      ) : null}
    </section>
  );
}

function HistoryList({ history }: { history: ReviewConfigHistory }) {
  if (!history.versions.length) {
    return <p className="rounded-[10px] border border-dashed border-[var(--ls-line-strong)] px-3 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]">{history.inherited ? `This scope inherits ${history.origin_scope_kind} settings; no persisted revision exists at the requested scope.` : "This section still uses its built-in default; no persisted revision exists yet."}</p>;
  }

  return <>
    <p className="mb-3 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">{history.inherited ? `Showing effective ${history.origin_scope_kind} provenance.` : "Newest persisted revision first."} Existing review runs keep their admission snapshot.</p>
    <ol className="space-y-2">
      {history.versions.map((version) => (
        <li className="rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3" key={version.revision}>
          <div className="flex items-center justify-between gap-3"><span className="font-mono text-xs font-semibold text-[var(--ls-text)]">v{version.revision}</span><time className="shrink-0 text-[11px] text-[var(--ls-text-tertiary)]" dateTime={version.created_at}>{formatDate(version.created_at)}</time></div>
          <p className="mt-2 truncate font-mono text-[11px] text-[var(--ls-text-secondary)]" title={version.content_sha256}>{version.content_sha256}</p>
          <p className="mt-1 text-[11px] text-[var(--ls-text-tertiary)]">by {version.created_by}</p>
        </li>
      ))}
    </ol>
    <p className="mt-3 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">History exposes actor, time, scope, revision and content hash only. It never returns previous policy content, model references or secrets.</p>
  </>;
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" }).format(new Date(value));
}
