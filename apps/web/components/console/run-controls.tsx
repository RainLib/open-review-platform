"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { LoaderCircle, OctagonX } from "lucide-react";

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
}: {
  org: string;
  runID: string;
  revision: number;
  state: RunState;
  enabled: boolean;
}) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();
  const unavailable = terminalStates.includes(state) || !enabled;

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

  return (
    <div className="flex flex-col items-end gap-2">
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
        <p className="max-w-60 text-right text-[11px] leading-4 text-zinc-500">
          Cancellation is enabled only for a live local-development
          control-plane session.
        </p>
      ) : null}
      {message ? (
        <p
          aria-live="polite"
          className="max-w-72 text-right text-[11px] leading-4 text-amber-100/80"
        >
          {message}
        </p>
      ) : null}
    </div>
  );
}
