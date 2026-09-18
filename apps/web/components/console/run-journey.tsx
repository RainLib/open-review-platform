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

  useEffect(() => {
    if (!streamEnabled) return;

    const eventSource = new EventSource(
      `/api/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/events?afterRevision=${latestRevision.current}`,
    );
    const onTransition = (message: Event) => {
      try {
        const event = JSON.parse(
          (message as MessageEvent<string>).data,
        ) as RunEvent;
        if (event.revision < latestRevision.current) return;
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
        setConnection("reconnecting");
      }
    };

    eventSource.addEventListener("transition", onTransition);
    eventSource.onopen = () => setConnection("live");
    eventSource.onerror = () => setConnection("reconnecting");
    return () => eventSource.close();
  }, [org, runID, streamEnabled]);

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
      <div className="overflow-hidden rounded-[24px] border border-white/[0.075] bg-console-surface">
        <div className="flex flex-col gap-3 border-b border-white/[0.075] px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-sm font-medium text-zinc-100">
              Execution journey
            </p>
            <p className="mt-1 text-xs text-zinc-500">
              Only durable stages and verified signals are shown. Private model
              reasoning is never displayed.
            </p>
          </div>
          <span
            aria-live="polite"
            className="flex items-center gap-1.5 text-xs text-zinc-500"
          >
            {connection === "live" ? (
              <Radio className="size-3.5 text-emerald-300" />
            ) : connection === "reconnecting" ? (
              <RotateCw className="size-3.5 text-amber-300" />
            ) : (
              <Activity className="size-3.5 text-zinc-500" />
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
                  <span className="absolute left-[11px] top-6 h-[calc(100%-10px)] w-px bg-white/[0.09] sm:left-7 sm:top-[11px] sm:h-px sm:w-[calc(100%-16px)] lg:left-[calc(50%+11px)] lg:w-[calc(100%-22px)]" />
                ) : null}
                <span
                  className={cn(
                    "relative z-[1] grid size-[23px] shrink-0 place-items-center rounded-full border",
                    complete
                      ? "border-emerald-300/50 bg-emerald-300 text-console-on-success"
                      : current
                        ? "border-cyan-300/50 bg-cyan-300/15 text-cyan-200 shadow-console-stage-current"
                        : "border-white/10 bg-console-inset text-zinc-600",
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
                      ? "font-medium text-zinc-100"
                      : complete
                        ? "text-emerald-200/80"
                        : "text-zinc-500",
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
          <div className="mx-5 mb-5 flex gap-3 rounded-xl border border-amber-300/15 bg-amber-300/[0.05] p-3 text-xs leading-5 text-amber-100/75">
            <ShieldAlert className="mt-0.5 size-4 shrink-0 text-amber-300" />
            This run requires a human decision. Inspect its event evidence
            before retrying or changing policy.
          </div>
        ) : null}
      </div>

      <div className="rounded-[24px] border border-white/[0.075] bg-console-surface p-5">
        <div className="flex items-center justify-between">
          <p className="text-sm font-medium text-zinc-100">Verified signals</p>
          <span className="font-mono text-xs text-zinc-500">r{revision}</span>
        </div>
        <div className="mt-4 space-y-0 divide-y divide-white/[0.06]">
          {orderedEvents.length === 0 ? (
            <p className="py-8 text-center text-sm leading-6 text-zinc-500">
              The signal stream will add durable events as this run progresses.
            </p>
          ) : (
            orderedEvents.map((event) => (
              <div className="py-3" key={event.id}>
                <div className="flex items-center justify-between gap-3">
                  <p className="truncate text-xs font-medium text-zinc-300">
                    {event.event_type
                      .replace(/^run\./, "")
                      .replaceAll("_", " ")}
                  </p>
                  <span className="shrink-0 text-[11px] text-zinc-600">
                    {formatTime(event.created_at)}
                  </span>
                </div>
                <p className="mt-1 text-xs leading-5 text-zinc-500">
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
