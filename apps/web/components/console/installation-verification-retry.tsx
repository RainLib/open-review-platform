"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { LoaderCircle, RotateCw } from "lucide-react";

import { Button } from "@/components/ui/button";

// A failed or grandfathered connection is deliberately verified through the
// control plane. The browser only requests durable work; provider credentials
// stay with the worker that claims the probe.
export function InstallationVerificationRetry({
  active,
  healthState,
  installationID,
  org,
  verificationState,
}: {
  active: boolean;
  healthState?: "live" | "degraded" | "critical" | "stale" | "configured_only";
  installationID: string;
  org: string;
  verificationState: "legacy" | "pending" | "checking" | "verified" | "failed";
}) {
  const router = useRouter();
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();

  const isLegacy = verificationState === "legacy";
  const isVerifiedButUnhealthy = verificationState === "verified" && healthState !== undefined && healthState !== "live";
  if (!active || (!isLegacy && verificationState !== "failed" && !isVerifiedButUnhealthy)) return null;

  async function retry() {
    if (pending) return;
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationID)}/verification`,
        { method: "POST" },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      if (!response.ok) {
        throw new Error(
          payload.error ?? "The verification retry was rejected.",
        );
      }
      setMessage(
        isVerifiedButUnhealthy
          ? "Provider recheck queued. Existing webhook and CLI admission remains eligible while the worker refreshes the read-only health receipt."
          : isLegacy
            ? "Re-verification queued. New webhook and CLI admission remains held until the worker records a live provider result."
            : "Verification queued. Admission remains blocked until the worker records a live provider result.",
      );
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The verification retry could not be queued.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="mt-3">
      <Button
        disabled={pending}
        onClick={() => void retry()}
        size="sm"
        variant="outline"
      >
        {pending ? (
          <>
            <LoaderCircle className="animate-spin motion-reduce:animate-none" />
            Queueing check
          </>
        ) : (
          <>
            <RotateCw />
            {isVerifiedButUnhealthy
              ? "Recheck provider access"
              : isLegacy
                ? "Re-verify read-only access"
                : "Retry read-only check"}
          </>
        )}
      </Button>
      {message ? (
        <p
          aria-live="polite"
          className="mt-2 text-[11px] leading-4 text-[var(--ls-text-secondary)]"
        >
          {message}
        </p>
      ) : null}
    </div>
  );
}
