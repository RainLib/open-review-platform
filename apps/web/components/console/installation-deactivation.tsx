"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { LoaderCircle, Power } from "lucide-react";

import { Button } from "@/components/ui/button";

// Deactivation is intentionally a separate explicit confirmation. It stops
// future provider admission but keeps historic evidence readable; deleting an
// integration must never erase the review and audit trail that it created.
export function InstallationDeactivation({
  active,
  enabled,
  installationID,
  org,
  provider,
  repositoryScope,
}: {
  active: boolean;
  enabled: boolean;
  installationID: string;
  org: string;
  provider: "github" | "gitlab";
  repositoryScope: string;
}) {
  const router = useRouter();
  const [confirming, setConfirming] = useState(false);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();

  if (!active) return null;

  async function deactivate() {
    if (!enabled || pending) return;
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationID)}`,
        { method: "DELETE" },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      if (!response.ok) {
        throw new Error(
          payload.error ?? "The provider connection could not be deactivated.",
        );
      }
      setConfirming(false);
      setMessage(
        "Connection deactivated. New webhooks, CLI requests, and reviews can no longer use this installation; existing evidence remains available.",
      );
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The provider connection could not be deactivated.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="rounded-[18px] border border-red-500/25 bg-red-500/[0.055] p-5">
      <div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-critical-text)]">
        <Power className="size-4" />
        Deactivate connection
      </div>
      <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
        Stop future review admission for this {provider === "github" ? "GitHub App" : "GitLab"} scope. Historical reviews, webhooks, and audit evidence are retained.
      </p>
      <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
        This does not uninstall the integration at {provider === "github" ? "GitHub" : "GitLab"} or revoke its provider-side grant. Remove that access separately in the provider if it is no longer needed.
        {provider === "gitlab" ? " An OAuth credential stored by Open Review is revoked locally when no other active scope uses it." : null}
      </p>
      {!confirming ? (
        <Button
          className="mt-4"
          disabled={!enabled}
          onClick={() => setConfirming(true)}
          size="sm"
          type="button"
          variant="destructive"
        >
          <Power />
          Deactivate
        </Button>
      ) : (
        <div className="mt-4 rounded-[12px] border border-red-500/20 bg-[var(--ls-surface)] p-3.5">
          <p className="text-xs leading-5 text-[var(--ls-text-secondary)]">
            Confirm stopping new provider events for <span className="font-mono text-[var(--ls-text)]">{repositoryScope}</span>. A new signed provider authorization is required to connect it again.
          </p>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button
              disabled={!enabled || pending}
              onClick={() => void deactivate()}
              size="sm"
              type="button"
              variant="destructive"
            >
              {pending ? (
                <>
                  <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                  Deactivating
                </>
              ) : (
                <>
                  <Power />
                  Confirm deactivation
                </>
              )}
            </Button>
            <Button
              disabled={!enabled || pending}
              onClick={() => setConfirming(false)}
              size="sm"
              type="button"
              variant="outline"
            >
              Cancel
            </Button>
          </div>
        </div>
      )}
      {message ? (
        <p
          aria-live="polite"
          className="mt-3 text-xs leading-5 text-[var(--ls-text-secondary)]"
        >
          {message}
        </p>
      ) : null}
      {!enabled ? (
        <p className="mt-3 text-xs leading-5 text-[var(--ls-text-tertiary)]">
          Live control-plane data is required before an installation can be
          deactivated.
        </p>
      ) : null}
    </section>
  );
}
