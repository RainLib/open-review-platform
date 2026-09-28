"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { CheckCheck, LoaderCircle, UserRoundCheck } from "lucide-react";

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
import type { ReviewIntervention } from "@/lib/control-api";

// This is intentionally not a retry control. Acknowledging a queue item keeps
// the terminal run's evidence unchanged; retrying remains an explicit new-run
// admission in RunControls.
export function ReviewInterventionControls({
  compact = false,
  enabled,
  intervention,
  org,
  runID,
}: {
  compact?: boolean;
  enabled: boolean;
  intervention?: Pick<ReviewIntervention, "revision" | "state" | "assignee_subject">;
  org: string;
  runID: string;
}) {
  const router = useRouter();
  const [resolveOpen, setResolveOpen] = useState(false);
  const [pending, setPending] = useState<"claim" | "resolve">();
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState<string>();

  if (!intervention || intervention.state === "resolved") return null;
  const activeIntervention = intervention;

  async function mutate(action: "claim" | "resolve") {
    setPending(action);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/intervention/${action}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(
            action === "resolve"
              ? { expected_revision: activeIntervention.revision, reason: reason.trim() }
              : { expected_revision: activeIntervention.revision },
          ),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as { error?: string };
      if (!response.ok) throw new Error(payload.error ?? "The intervention could not be updated.");
      setMessage(
        action === "claim"
          ? "Intervention claimed. The terminal evidence remains unchanged."
          : "Intervention acknowledged and removed from the active queue. The terminal evidence remains retained.",
      );
      setResolveOpen(false);
      router.refresh();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "The intervention could not be updated.");
    } finally {
      setPending(undefined);
    }
  }

  const claimed = activeIntervention.state === "claimed";
  return (
    <div className={compact ? "flex flex-col items-end gap-1.5" : "flex flex-wrap items-center gap-2"}>
      <span className="rounded-full bg-amber-500/10 px-2 py-1 text-[11px] font-medium text-[var(--ls-warning-text)]">
        {claimed ? `Claimed${activeIntervention.assignee_subject ? ` · ${activeIntervention.assignee_subject}` : ""}` : "Unclaimed"}
      </span>
      {!claimed ? (
        <Button disabled={!enabled || pending !== undefined} onClick={() => void mutate("claim")} size="sm" variant="outline">
          {pending === "claim" ? <LoaderCircle className="animate-spin motion-reduce:animate-none" /> : <UserRoundCheck data-icon="inline-start" />}
          Claim
        </Button>
      ) : null}
      <AlertDialog open={resolveOpen} onOpenChange={setResolveOpen}>
        <AlertDialogTrigger asChild>
          <Button disabled={!enabled || pending !== undefined} size="sm" variant="outline">
            <CheckCheck data-icon="inline-start" />
            {compact ? "Acknowledge" : "Acknowledge and close"}
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Acknowledge this intervention?</AlertDialogTitle>
            <AlertDialogDescription>
              This only removes the human task from the active queue. It does not mark the failed review as passed, alter its evidence, or start a retry.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <label className="grid gap-2 text-sm font-medium text-[var(--ls-text)]">
            Why is no further review action required?
            <textarea
              className="min-h-24 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] p-3 text-sm font-normal text-[var(--ls-text)] outline-none focus:border-[var(--ls-accent)]"
              maxLength={2000}
              onChange={(event) => setReason(event.target.value)}
              placeholder="For example: provider outage is tracked externally; rerun will be initiated after recovery."
              value={reason}
            />
          </label>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={pending !== undefined}>Keep in queue</AlertDialogCancel>
            <AlertDialogAction
              disabled={pending !== undefined || reason.trim().length < 3}
              onClick={(event) => {
                event.preventDefault();
                void mutate("resolve");
              }}
            >
              {pending === "resolve" ? <><LoaderCircle className="animate-spin motion-reduce:animate-none" />Saving</> : "Acknowledge queue item"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {message ? <p aria-live="polite" className={compact ? "max-w-60 text-right text-[11px] leading-4 text-[var(--ls-warning-text)]" : "basis-full text-[11px] leading-4 text-[var(--ls-warning-text)]"}>{message}</p> : null}
    </div>
  );
}
