"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { LoaderCircle, RotateCcw } from "lucide-react";

import { Button } from "@/components/ui/button";
import type { ProviderIssueAnalysisState } from "@/lib/control-api";

export function ProviderIssueRetry({
  analysisID,
  enabled,
  deliveryFailure,
  skippedDelivery,
  org,
  revision,
  state,
}: {
  analysisID: string;
  enabled: boolean;
  deliveryFailure: boolean;
  skippedDelivery: boolean;
  org: string;
  revision: number;
  state: ProviderIssueAnalysisState;
}) {
  const router = useRouter();
  const [pending, setPending] = useState(false);
  const [retryKey, setRetryKey] = useState<string>();
  const [message, setMessage] = useState<string>();

  if (!enabled || (state !== "failed" && !deliveryFailure && !skippedDelivery)) return null;

  async function retry() {
    if (pending) return;
    const idempotencyKey = retryKey ?? `issue-retry:${crypto.randomUUID()}`;
    setRetryKey(idempotencyKey);
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/provider-issues/${encodeURIComponent(analysisID)}/retry`,
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
        attempt?: number;
        error?: string;
        replayed?: boolean;
      };
      if (!response.ok) {
        throw new Error(payload.error ?? "The Issue analysis retry was rejected.");
      }
      setMessage(
        payload.replayed
          ? `Retry attempt ${payload.attempt ?? ""} was already accepted.`.trim()
          : `Retry attempt ${payload.attempt ?? ""} queued from the retained Issue snapshot.`.trim(),
      );
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The Issue analysis retry could not be queued.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="mt-3">
      <Button disabled={pending} onClick={() => void retry()} size="sm" variant="outline">
        {pending ? (
          <>
            <LoaderCircle className="animate-spin motion-reduce:animate-none" />
            Queueing retry
          </>
        ) : (
          <>
            <RotateCcw />
            Retry retained snapshot
          </>
        )}
      </Button>
      <p className="mt-2 text-[11px] leading-4 text-[var(--ls-text-tertiary)]">
        Reuses revision r{revision} and its immutable model, prompt, and Issue-format snapshots.
      </p>
      {message ? (
        <p aria-live="polite" className="mt-2 text-[11px] leading-4 text-[var(--ls-text-secondary)]">
          {message}
        </p>
      ) : null}
    </div>
  );
}
