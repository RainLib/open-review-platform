"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { CircleAlert, Radio, RotateCw } from "lucide-react";

import type { RunEvent } from "@/lib/control-api";
import { formatTime } from "@/lib/format";
import { filterRunEvents, isRunEvent, mergeRunEvents, visibleRunEventPayload } from "@/lib/review-activity";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

type Connection = "connecting" | "live" | "reconnecting" | "offline" | "snapshot";

function latestRevision(events: RunEvent[]) {
  return events.reduce((latest, event) => Math.max(latest, event.revision), 0);
}

export function ReviewActivityTimeline({ initialEvents, org, runID, streamEnabled, query = "", actor = "all" }: {
  initialEvents: RunEvent[];
  org: string;
  runID: string;
  streamEnabled: boolean;
  query?: string;
  actor?: string;
}) {
  const router = useRouter();
  const [streamedEvents, setStreamedEvents] = useState<RunEvent[]>([]);
  const [connection, setConnection] = useState<Connection>(streamEnabled ? "connecting" : "snapshot");
  const [refreshSuggested, setRefreshSuggested] = useState(false);
  const latest = useRef(latestRevision(initialEvents));
  const reconnectAttempts = useRef(0);
  const [connectionEpoch, setConnectionEpoch] = useState(0);
  const events = useMemo(() => mergeRunEvents(initialEvents, streamedEvents), [initialEvents, streamedEvents]);

  useEffect(() => {
    latest.current = Math.max(latest.current, latestRevision(initialEvents));
  }, [initialEvents]);

  useEffect(() => {
    if (!streamEnabled) return;
    let closed = false;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
    let stableTimer: ReturnType<typeof setTimeout> | undefined;
    // A revision may contain more than one immutable event. Replay the last
    // revision and deduplicate by event ID instead of treating it as an event cursor.
    const afterRevision = Math.max(0, latest.current - 1);
    const source = new EventSource(`/api/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/events?afterRevision=${afterRevision}`);
    const reconnect = () => {
      if (closed || reconnectTimer) return;
      source.close();
      if (stableTimer) clearTimeout(stableTimer);
      reconnectAttempts.current += 1;
      if (reconnectAttempts.current > 5) {
        setConnection("offline");
        setRefreshSuggested(true);
        return;
      }
      setConnection("reconnecting");
      reconnectTimer = setTimeout(() => {
        if (!closed) setConnectionEpoch((current) => current + 1);
      }, Math.min(8_000, 500 * 2 ** (reconnectAttempts.current - 1)));
    };
    source.onopen = () => {
      setConnection("live");
      // A 200 response that immediately emits an application error is not a
      // recovered stream. Reset the backoff only after a stable interval.
      stableTimer = setTimeout(() => { reconnectAttempts.current = 0; }, 15_000);
    };
    source.addEventListener("transition", (message) => {
      try {
        const parsed: unknown = JSON.parse((message as MessageEvent<string>).data);
        if (!isRunEvent(parsed, runID)) {
          setRefreshSuggested(true);
          return;
        }
        if (parsed.revision > latest.current + 1) setRefreshSuggested(true);
        latest.current = Math.max(latest.current, parsed.revision);
        setStreamedEvents((current) => current.some((event) => event.id === parsed.id)
          ? current : mergeRunEvents(current, [parsed]));
      } catch {
        setRefreshSuggested(true);
      }
    });
    source.onerror = reconnect;
    return () => {
      closed = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (stableTimer) clearTimeout(stableTimer);
      source.close();
    };
  }, [connectionEpoch, org, runID, streamEnabled]);

  const actors = useMemo(() => [...new Set(events.map((event) => event.actor_kind))].sort(), [events]);
  const selectedActor = actor === "all" || actors.includes(actor) ? actor : "all";
  const term = query.slice(0, 80);
  const filtered = useMemo(() => [...filterRunEvents(events, term, selectedActor)].reverse(), [events, term, selectedActor]);
  const action = `/${encodeURIComponent(org)}/reviews/${encodeURIComponent(runID)}`;
  const label = connection === "live" ? "Live event stream" : connection === "connecting" ? "Connecting to event stream"
    : connection === "reconnecting" ? "Reconnecting · showing retained snapshot"
      : connection === "offline" ? "Offline · showing retained snapshot" : "Retained preview snapshot";

  return <div className="space-y-5">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div><div className="flex min-w-0 items-center gap-2"><h2 className="text-lg font-semibold text-[var(--ls-text)]">Run activity</h2><HelpHint label="Run activity">Immutable run transitions, newest first. Model reasoning and credentials are not shown.</HelpHint></div></div>
      <div className="flex items-center gap-2"><span aria-live="polite" className={cn("inline-flex items-center gap-2 rounded-full px-3 py-1.5 text-xs font-medium", connection === "live" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]")}><Radio className="size-3.5" />{label}</span><button className="luminous-focus inline-flex h-9 items-center gap-1.5 rounded-[9px] border border-[var(--ls-line)] px-3 text-xs font-medium text-[var(--ls-accent)] hover:bg-[var(--ls-accent-soft)]" onClick={() => { setRefreshSuggested(false); reconnectAttempts.current = 0; setConnection(streamEnabled ? "connecting" : "snapshot"); setConnectionEpoch((current) => current + 1); router.refresh(); }} type="button"><RotateCw className="size-3.5" />Refresh snapshot</button></div>
    </div>
    {refreshSuggested ? <div className="flex gap-2 rounded-[10px] border border-amber-500/20 bg-amber-500/[0.06] p-3 text-xs leading-5 text-[var(--ls-warning-text)]"><CircleAlert className="mt-0.5 size-4 shrink-0" /><span>A newer revision or interrupted stream was observed. Refresh the durable snapshot to reconcile the complete timeline; an open stream alone does not prove completeness.</span></div> : null}
    <form action={action} className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_170px_auto]" method="get"><input name="tab" type="hidden" value="activity" /><label className="sr-only" htmlFor="review-activity-search">Search event or actor</label><input className="luminous-focus h-10 min-w-0 rounded-[9px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]" defaultValue={term} id="review-activity-search" maxLength={80} name="q" placeholder="Search event or actor" type="search" /><label className="sr-only" htmlFor="review-activity-actor">Actor type</label><select className="luminous-focus h-10 rounded-[9px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" defaultValue={selectedActor} id="review-activity-actor" name="actor"><option value="all">All actors</option>{actors.map((kind) => <option key={kind} value={kind}>{kind.replaceAll("_", " ")}</option>)}</select><button className="luminous-focus h-10 rounded-[9px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white" type="submit">Filter</button></form>
    <p className="text-xs text-[var(--ls-text-secondary)]">Showing {filtered.length} of {events.length} retained events. Search excludes raw payload values.</p>
    {filtered.length ? <ol className="border-l border-[var(--ls-line-strong)] pl-6">{filtered.map((event, index) => {
      const payload = visibleRunEventPayload(event.payload);
      return <li className="relative border-b border-[var(--ls-line)] py-5 first:pt-0 last:border-0" key={event.id}><span className={cn("absolute -left-[29px] top-6 size-2.5 rounded-full bg-[var(--ls-accent)] ring-4 ring-[var(--ls-surface)]", index === 0 && "top-1")} /><div className="flex flex-wrap items-start justify-between gap-2"><div><h3 className="text-sm font-semibold text-[var(--ls-text)]">{event.event_type.replaceAll("_", " ")}</h3><p className="mt-1 text-xs text-[var(--ls-text-secondary)]">Revision {event.revision} · {event.actor_kind}{event.actor_subject ? ` · ${event.actor_subject}` : ""}</p></div><time className="text-xs text-[var(--ls-text-tertiary)]" dateTime={event.created_at}>{formatTime(event.created_at)}</time></div>{Object.keys(payload).length ? <details className="mt-3 rounded-[9px] bg-[var(--ls-surface-muted)]"><summary className="luminous-focus cursor-pointer px-3 py-2 text-xs font-medium text-[var(--ls-accent)]">Event metadata</summary><pre className="max-h-64 overflow-auto border-t border-[var(--ls-line)] p-3 font-mono text-[11px] leading-5 text-[var(--ls-text-secondary)]">{JSON.stringify(payload, null, 2)}</pre></details> : null}</li>;
    })}</ol> : <div className="grid min-h-48 place-items-center rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-6 text-center"><div><CircleAlert className="mx-auto size-5 text-[var(--ls-text-tertiary)]" /><p className="mt-3 text-sm font-semibold text-[var(--ls-text)]">{events.length ? "No matching events" : "No retained events"}</p><p className="mt-1 text-xs text-[var(--ls-text-secondary)]">{events.length ? "Adjust the event or actor filter." : "No durable transition events are attached to this run yet."}</p></div></div>}
  </div>;
}
