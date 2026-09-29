"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { CalendarClock, LoaderCircle, X } from "lucide-react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import type { ReviewSchedule, RunState } from "@/lib/control-api";

const terminalStates: RunState[] = [
  "completed",
  "failed",
  "cancelled",
  "superseded",
  "needs_attention",
];

function localDateTime(value: Date) {
  const offset = value.getTimezoneOffset() * 60_000;
  return new Date(value.getTime() - offset).toISOString().slice(0, 16);
}

export function ScheduleReviewControl({
  org,
  runID,
  state,
  enabled,
}: {
  org: string;
  runID: string;
  state: RunState;
  enabled: boolean;
}) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [scheduledFor, setScheduledFor] = useState(() =>
    localDateTime(new Date(Date.now() + 10 * 60_000)),
  );
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();
  const available = enabled && terminalStates.includes(state);

  async function scheduleReview() {
    const parsed = new Date(scheduledFor);
    if (Number.isNaN(parsed.getTime())) {
      setMessage("Choose a valid future time.");
      return;
    }
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/schedules`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ scheduled_for: parsed.toISOString() }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
        schedule?: Pick<ReviewSchedule, "id">;
      };
      if (!response.ok) {
        throw new Error(payload.error ?? "The scheduled admission was rejected.");
      }
      setOpen(false);
      setMessage("Scheduled admission recorded. It will create a fresh run only at the selected time.");
      router.push(`/${encodeURIComponent(org)}/tasks?tab=scheduled`);
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The scheduled admission could not be created.",
      );
    } finally {
      setPending(false);
    }
  }

  if (!available) return null;
  return (
    <div className="flex flex-col items-end gap-2">
      <AlertDialog open={open} onOpenChange={setOpen}>
        <AlertDialogTrigger asChild>
          <Button size="sm" variant="outline">
            <CalendarClock data-icon="inline-start" />
            Schedule re-review
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Schedule this exact revision?</AlertDialogTitle>
            <AlertDialogDescription>
              Open Review will not scan or publish anything before this time. At admission it creates a fresh run and snapshots the policies then in effect, while keeping this source revision as provenance.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <label className="grid gap-2 text-sm font-medium text-[var(--ls-text)]">
            Admission time
            <input
              className="h-10 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] outline-none focus:border-[var(--ls-accent)]"
              onChange={(event) => setScheduledFor(event.target.value)}
              required
              type="datetime-local"
              value={scheduledFor}
            />
          </label>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={pending}>Not now</AlertDialogCancel>
            <AlertDialogAction
              disabled={pending}
              onClick={(event) => {
                event.preventDefault();
                void scheduleReview();
              }}
            >
              {pending ? (
                <>
                  <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                  Scheduling
                </>
              ) : (
                "Schedule admission"
              )}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {message ? (
        <p aria-live="polite" className="max-w-72 text-right text-[11px] leading-4 text-[var(--ls-warning-text)]">
          {message}
        </p>
      ) : null}
    </div>
  );
}

export function CancelReviewScheduleControl({
  org,
  schedule,
  enabled,
}: {
  org: string;
  schedule: Pick<ReviewSchedule, "id" | "revision" | "state">;
  enabled: boolean;
}) {
  const router = useRouter();
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();
  const cancellable = enabled && (schedule.state === "scheduled" || schedule.state === "blocked");

  async function cancelSchedule() {
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/review-schedules/${encodeURIComponent(schedule.id)}/cancel`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ expected_revision: schedule.revision }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as { error?: string };
      if (!response.ok) {
        throw new Error(payload.error ?? "The schedule could not be cancelled.");
      }
      setMessage("Scheduled admission cancelled.");
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The scheduled admission could not be cancelled.",
      );
    } finally {
      setPending(false);
    }
  }

  if (!cancellable) return null;
  return (
    <div className="flex flex-col items-end gap-1.5">
      <Button disabled={pending} onClick={() => void cancelSchedule()} size="sm" variant="ghost">
        {pending ? <LoaderCircle className="animate-spin motion-reduce:animate-none" /> : <X />}
        Cancel
      </Button>
      {message ? <p aria-live="polite" className="max-w-44 text-right text-[11px] leading-4 text-[var(--ls-warning-text)]">{message}</p> : null}
    </div>
  );
}
