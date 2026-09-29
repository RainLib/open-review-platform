"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { LoaderCircle, OctagonX, RotateCcw } from "lucide-react";

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
import type { RunState } from "@/lib/control-api";

const terminalStates: RunState[] = [
  "completed",
  "failed",
  "cancelled",
  "superseded",
  "needs_attention",
];

export function RunControls({
  org,
  runID,
  revision,
  state,
  enabled,
  acknowledgementRecoveryAvailable = false,
}: {
  org: string;
  runID: string;
  revision: number;
  state: RunState;
  enabled: boolean;
  acknowledgementRecoveryAvailable?: boolean;
}) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [retryOpen, setRetryOpen] = useState(false);
  const [ackRetryOpen, setAckRetryOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [retryPending, setRetryPending] = useState(false);
  const [ackRetryPending, setAckRetryPending] = useState(false);
  const [retryKey, setRetryKey] = useState<string>();
  const [ackRetryKey, setAckRetryKey] = useState<string>();
  const [message, setMessage] = useState<string>();
  const unavailable = terminalStates.includes(state) || !enabled;
  const retryable =
    enabled && (state === "failed" || state === "needs_attention");

  async function cancelRun() {
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ revision }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      if (!response.ok)
        throw new Error(
          payload.error ?? "The cancellation request was rejected.",
        );
      setOpen(false);
      setMessage(
        "Cancellation requested. The control plane will publish the terminal state when it is safe to stop.",
      );
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The cancellation request could not be completed.",
      );
    } finally {
      setPending(false);
    }
  }

  async function retryRun() {
    setRetryPending(true);
    setMessage(undefined);
    const idempotencyKey = retryKey ?? crypto.randomUUID();
    setRetryKey(idempotencyKey);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/retry`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            expected_revision: revision,
            idempotency_key: idempotencyKey,
          }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
        replayed?: boolean;
        run?: { id?: string };
      };
      if (!response.ok) {
        throw new Error(payload.error ?? "The retry request was rejected.");
      }
      const nextRunID = payload.run?.id;
      setRetryOpen(false);
      setMessage(
        payload.replayed
          ? "The original retry request was already accepted. Opening its durable run."
          : "A new review run was queued for the same source revision.",
      );
      if (nextRunID) {
        router.push(`/${encodeURIComponent(org)}/tasks/${encodeURIComponent(nextRunID)}?tab=overview`);
      }
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The retry request could not be completed.",
      );
    } finally {
      setRetryPending(false);
    }
  }

  async function retryAcknowledgement() {
    setAckRetryPending(true);
    setMessage(undefined);
    const idempotencyKey = ackRetryKey ?? crypto.randomUUID();
    setAckRetryKey(idempotencyKey);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/acknowledgement/retry`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            expected_revision: revision,
            idempotency_key: idempotencyKey,
          }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
        attempt?: number;
        replayed?: boolean;
      };
      if (!response.ok) {
        throw new Error(payload.error ?? "The provider reply could not be retried.");
      }
      setAckRetryOpen(false);
      setMessage(
        payload.replayed
          ? "The same acknowledgement retry was already queued. Refresh for its latest state."
          : `Provider reply retry ${payload.attempt ?? ""} queued. Review execution remains held until the provider accepts it.`,
      );
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The provider reply could not be retried.",
      );
    } finally {
      setAckRetryPending(false);
    }
  }

  return (
    <div className="flex flex-col items-end gap-2">
      {enabled &&
      acknowledgementRecoveryAvailable &&
      state === "acknowledged" ? (
        <AlertDialog open={ackRetryOpen} onOpenChange={setAckRetryOpen}>
          <AlertDialogTrigger asChild>
            <Button disabled={ackRetryPending} size="sm" variant="outline">
              <RotateCcw data-icon="inline-start" />
              Retry provider reply
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Retry the progress reply?</AlertDialogTitle>
              <AlertDialogDescription>
                This is available only after the previous provider delivery has
                exhausted its retries. Restore the connection or its
                credentials first. Open Review will reuse the same comment
                marker; it will not start a new review or bypass the
                visible-reply gate.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={ackRetryPending}>Not now</AlertDialogCancel>
              <AlertDialogAction
                disabled={ackRetryPending}
                onClick={(event) => {
                  event.preventDefault();
                  void retryAcknowledgement();
                }}
              >
                {ackRetryPending ? (
                  <>
                    <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                    Queueing
                  </>
                ) : (
                  "Queue reply retry"
                )}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      ) : null}
      {retryable ? (
        <AlertDialog open={retryOpen} onOpenChange={setRetryOpen}>
          <AlertDialogTrigger asChild>
            <Button disabled={retryPending} size="sm" variant="outline">
              <RotateCcw data-icon="inline-start" />
              Retry review
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Retry this failed review?</AlertDialogTitle>
              <AlertDialogDescription>
                Open Review will keep this terminal result unchanged, then queue
                a new run for the same base and head commit. The new run records
                a fresh policy and configuration snapshot before it executes.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={retryPending}>Not now</AlertDialogCancel>
              <AlertDialogAction
                disabled={retryPending}
                onClick={(event) => {
                  event.preventDefault();
                  void retryRun();
                }}
              >
                {retryPending ? (
                  <>
                    <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                    Queueing
                  </>
                ) : (
                  "Queue retry"
                )}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      ) : null}
      <AlertDialog open={open} onOpenChange={setOpen}>
        <AlertDialogTrigger asChild>
          <Button disabled={unavailable} size="sm" variant="outline">
            <OctagonX data-icon="inline-start" />
            Cancel run
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Cancel this review run?</AlertDialogTitle>
            <AlertDialogDescription>
              Open Review records the cancellation request immediately. Comments
              or findings already published to the provider are not withdrawn,
              and the final terminal state arrives through the control plane.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={pending}>
              Keep running
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={pending}
              onClick={(event) => {
                event.preventDefault();
                void cancelRun();
              }}
            >
              {pending ? (
                <>
                  <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                  Requesting
                </>
              ) : (
                "Request cancellation"
              )}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {!enabled ? (
        <p className="max-w-60 text-right text-[11px] leading-4 text-[var(--ls-text-tertiary)]">
          Cancellation is enabled only for a live local-development
          control-plane session.
        </p>
      ) : null}
      {message ? (
        <p
          aria-live="polite"
          className="max-w-72 text-right text-[11px] leading-4 text-[var(--ls-warning-text)]"
        >
          {message}
        </p>
      ) : null}
    </div>
  );
}
