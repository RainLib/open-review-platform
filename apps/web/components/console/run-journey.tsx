"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  Activity,
  Check,
  LoaderCircle,
  Radio,
  RotateCw,
  ShieldAlert,
} from "lucide-react";

import { cn } from "@/lib/utils";
import type { RunEvent, RunState } from "@/lib/control-api";
import { displayRunState, formatTime } from "@/lib/format";

const stageOrder: RunState[] = [
  "acknowledged",
  "admitted",
  "preparing",
  "analyzing",
  "normalizing",
  "publishing",
  "completed",
];
const states = new Set<RunState>([
  ...stageOrder,
  "failed",
  "cancelled",
  "superseded",
  "needs_attention",
]);
const stageLabels: Record<RunState, string> = {
  acknowledged: "Received",
  admitted: "Admitted",
  preparing: "Preparing",
  analyzing: "Reviewing",
  normalizing: "Normalizing",
  publishing: "Publishing",
  completed: "Published",
  failed: "Failed",
  cancelled: "Cancelled",
  superseded: "Superseded",
  needs_attention: "Needs attention",
};

const reconnectDelay = (attempt: number) =>
  Math.min(5_000, 500 * 2 ** Math.min(attempt, 4));

function stateFromEvent(event: RunEvent): RunState | undefined {
  const candidate = event.event_type.replace(/^run\./, "");
  return states.has(candidate as RunState)
    ? (candidate as RunState)
    : undefined;
}

function signalDescription(event: RunEvent) {
  const details = Object.entries(event.payload)
    .filter(
      ([, value]) =>
        typeof value === "string" ||
        typeof value === "number" ||
        typeof value === "boolean",
    )
    .slice(0, 2)
    .map(([key, value]) => `${key.replaceAll("_", " ")}: ${String(value)}`);
  return (
    details.join(" · ") || "State transition recorded by the control plane."
  );
}

export function RunJourney({
  org,
  runID,
  initialEvents,
  initialRevision,
  initialState,
  streamEnabled,
}: {
  org: string;
  runID: string;
  initialEvents: RunEvent[];
  initialRevision: number;
  initialState: RunState;
  streamEnabled: boolean;
}) {
  const [events, setEvents] = useState(initialEvents);
  const [revision, setRevision] = useState(initialRevision);
  const [state, setState] = useState(initialState);
  const [connection, setConnection] = useState<
    "connecting" | "live" | "reconnecting" | "static"
  >(streamEnabled ? "connecting" : "static");
  const latestRevision = useRef(initialRevision);
  const reconnectAttempts = useRef(0);
  const [connectionEpoch, setConnectionEpoch] = useState(0);

  // A route refresh can deliver a newer durable snapshot while this client
  // component remains mounted. Merge it only when it moves the cursor forward:
  // a delayed server render must never roll the live journey back.
  useEffect(() => {
    if (initialRevision <= latestRevision.current) return;

    latestRevision.current = initialRevision;
    setRevision(initialRevision);
    setState(initialState);
    setEvents((current) => {
      const eventsByID = new Map(
        [...current, ...initialEvents].map((event) => [event.id, event]),
      );
      return [...eventsByID.values()]
        .sort((left, right) => left.revision - right.revision)
        .slice(-12);
    });
  }, [initialEvents, initialRevision, initialState]);

  useEffect(() => {
    if (!streamEnabled) return;

    let closed = false;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

    const eventSource = new EventSource(
      `/api/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/events?afterRevision=${latestRevision.current}`,
    );
    const scheduleReconnect = () => {
      eventSource.close();
      if (closed) return;

      const delay = reconnectDelay(reconnectAttempts.current);
      reconnectAttempts.current += 1;
      setConnection("reconnecting");
      reconnectTimer = setTimeout(() => {
        if (!closed) setConnectionEpoch((current) => current + 1);
      }, delay);
    };
    const onTransition = (message: Event) => {
      try {
        const event = JSON.parse(
          (message as MessageEvent<string>).data,
        ) as RunEvent;
        if (event.revision <= latestRevision.current) return;
        latestRevision.current = Math.max(
          latestRevision.current,
          event.revision,
        );
        setRevision(latestRevision.current);
        setEvents((current) =>
          current.some((candidate) => candidate.id === event.id)
            ? current
            : [...current, event].slice(-12),
        );
        const nextState = stateFromEvent(event);
        if (nextState) setState(nextState);
        setConnection("live");
      } catch {
        scheduleReconnect();
      }
    };

    eventSource.addEventListener("transition", onTransition);
    eventSource.onopen = () => {
      reconnectAttempts.current = 0;
      setConnection("live");
    };
    eventSource.onerror = scheduleReconnect;
    return () => {
      closed = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      eventSource.close();
    };
  }, [connectionEpoch, org, runID, streamEnabled]);

  const activeIndex = stageOrder.indexOf(state);
  const isTerminal = [
    "completed",
    "failed",
    "cancelled",
    "superseded",
    "needs_attention",
  ].includes(state);
  const orderedEvents = useMemo(
    () => [...events].sort((left, right) => left.revision - right.revision),
    [events],
  );

  return (
    <section className="grid gap-5 xl:grid-cols-[minmax(0,1.45fr)_minmax(300px,0.8fr)]">
      <div className="overflow-hidden rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
        <div className="flex flex-col gap-3 border-b border-[var(--ls-line)] px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-sm font-semibold text-[var(--ls-text)]">
              Execution journey
            </p>
            <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">
              Only durable stages and verified signals are shown. Private model
              reasoning is never displayed.
            </p>
          </div>
          <span
            aria-live="polite"
            className="flex items-center gap-1.5 text-xs text-[var(--ls-text-tertiary)]"
          >
            {connection === "live" ? (
              <Radio className="size-3.5 text-[var(--ls-success)]" />
            ) : connection === "reconnecting" ? (
              <RotateCw className="size-3.5 text-[var(--ls-warning)]" />
            ) : (
              <Activity className="size-3.5 text-[var(--ls-text-tertiary)]" />
            )}
            {connection === "live"
              ? "Live updates"
              : connection === "reconnecting"
                ? "Reconnecting"
                : "Snapshot"}
          </span>
        </div>
        <ol className="grid gap-0 px-5 py-6 sm:grid-cols-3 lg:grid-cols-7 lg:px-7">
          {stageOrder.map((stage, index) => {
            const complete =
              isTerminal && state === "completed" ? true : index < activeIndex;
            const current =
              stage === state ||
              (state === "needs_attention" && stage === "publishing");
            return (
              <li
                className="relative flex min-w-0 items-center gap-3 pb-4 last:pb-0 sm:pb-0 sm:pr-3 lg:block lg:pr-0"
                key={stage}
              >
                {index !== stageOrder.length - 1 ? (
                  <span className="absolute left-[11px] top-6 h-[calc(100%-10px)] w-px bg-[var(--ls-line)] sm:left-7 sm:top-[11px] sm:h-px sm:w-[calc(100%-16px)] lg:left-[calc(50%+11px)] lg:w-[calc(100%-22px)]" />
                ) : null}
                <span
                  className={cn(
                    "relative z-[1] grid size-[23px] shrink-0 place-items-center rounded-full border",
                    complete
                      ? "border-[var(--ls-success)] bg-[var(--ls-success)] text-white"
                      : current
                        ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)] shadow-[0_0_0_4px_color-mix(in_srgb,var(--ls-accent)_12%,transparent)]"
                        : "border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] text-[var(--ls-text-tertiary)]",
                  )}
                >
                  {complete ? (
                    <Check className="size-3.5" strokeWidth={3} />
                  ) : current && !isTerminal ? (
                    <LoaderCircle className="size-3.5 animate-spin motion-reduce:animate-none" />
                  ) : (
                    <span className="size-1.5 rounded-full bg-current" />
                  )}
                </span>
                <span
                  className={cn(
                    "text-xs lg:mt-3 lg:block lg:w-full lg:truncate lg:text-center",
                    current
                      ? "font-medium text-[var(--ls-text)]"
                      : complete
                        ? "text-[var(--ls-success-text)]"
                        : "text-[var(--ls-text-tertiary)]",
                  )}
                  title={`${stageLabels[stage]} (${displayRunState(stage)})`}
                >
                  {stageLabels[stage]}
                </span>
              </li>
            );
          })}
        </ol>
        {state === "failed" || state === "needs_attention" ? (
          <div className="mx-5 mb-5 flex gap-3 rounded-[12px] border border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,transparent)] p-3 text-xs leading-5 text-[var(--ls-warning-text)]">
            <ShieldAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-warning)]" />
            This run requires a human decision. Inspect its event evidence
            before retrying or changing policy.
          </div>
        ) : null}
      </div>

      <div className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
        <div className="flex items-center justify-between">
          <p className="text-sm font-semibold text-[var(--ls-text)]">Verified signals</p>
          <span className="font-mono text-xs text-[var(--ls-text-tertiary)]">r{revision}</span>
        </div>
        <div className="mt-4 space-y-0 divide-y divide-[var(--ls-line)]">
          {orderedEvents.length === 0 ? (
            <p className="py-8 text-center text-sm leading-6 text-[var(--ls-text-tertiary)]">
              The signal stream will add durable events as this run progresses.
            </p>
          ) : (
            orderedEvents.map((event) => (
              <div className="py-3" key={event.id}>
                <div className="flex items-center justify-between gap-3">
                  <p className="truncate text-xs font-medium text-[var(--ls-text-secondary)]">
                    {event.event_type
                      .replace(/^run\./, "")
                      .replaceAll("_", " ")}
                  </p>
                  <span className="shrink-0 text-[11px] text-[var(--ls-text-tertiary)]">
                    {formatTime(event.created_at)}
                  </span>
                </div>
                <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">
                  {signalDescription(event)}
                </p>
              </div>
            ))
          )}
        </div>
      </div>
    </section>
  );
}
