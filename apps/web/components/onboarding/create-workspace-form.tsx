"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import {
  ArrowRight,
  Building2,
  Database,
  KeyRound,
  LoaderCircle,
  ShieldCheck,
} from "lucide-react";

import type { DeploymentProfile } from "@/lib/deployment-profile";

function slugFromName(value: string) {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63);
}

const inputClassName =
  "luminous-focus h-11 w-full rounded-xl border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none transition placeholder:text-[var(--ls-text-tertiary)] focus:border-[var(--ls-accent)] focus:ring-4 focus:ring-[color:color-mix(in_srgb,var(--ls-accent)_14%,transparent)] disabled:cursor-not-allowed disabled:opacity-55";

export function CreateWorkspaceForm({
  deployment,
}: {
  deployment: DeploymentProfile;
}) {
  const router = useRouter();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();

  async function createWorkspace(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch("/api/tenants", {
        body: JSON.stringify({ name, slug }),
        headers: { "Content-Type": "application/json" },
        method: "POST",
      });
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
        slug?: string;
      };
      if (!response.ok) {
        throw new Error(payload.error ?? "The workspace could not be created.");
      }
      const workspace = payload.slug ?? slug;
      router.replace(
        `/setup?tenant=${encodeURIComponent(workspace)}&next=${encodeURIComponent(`/${workspace}/home`)}`,
      );
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The workspace could not be created.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <form className="mt-8 space-y-5" onSubmit={createWorkspace}>
      <label className="block space-y-2 text-sm font-medium text-[var(--ls-text)]">
        Workspace name
        <input
          autoComplete="organization"
          className={inputClassName}
          disabled={pending}
          onChange={(event) => {
            const nextName = event.target.value;
            setName(nextName);
            if (!slugEdited) setSlug(slugFromName(nextName));
          }}
          placeholder="Platform engineering"
          required
          value={name}
        />
      </label>
      <label className="block space-y-2 text-sm font-medium text-[var(--ls-text)]">
        Workspace slug
        <span className="block text-xs font-normal leading-5 text-[var(--ls-text-tertiary)]">
          Used in URLs. Lowercase letters, numbers, and hyphens only.
        </span>
        <div className="flex overflow-hidden rounded-xl border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] focus-within:border-[var(--ls-accent)] focus-within:ring-4 focus-within:ring-[color:color-mix(in_srgb,var(--ls-accent)_14%,transparent)]">
          <span className="flex items-center border-r border-[var(--ls-line)] px-3 text-sm text-[var(--ls-text-tertiary)]">
            /
          </span>
          <input
            className="h-11 min-w-0 flex-1 bg-transparent px-3.5 text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)] disabled:cursor-not-allowed disabled:opacity-55"
            disabled={pending}
            onChange={(event) => {
              setSlugEdited(true);
              setSlug(slugFromName(event.target.value));
            }}
            placeholder="platform-engineering"
            required
            value={slug}
          />
        </div>
      </label>
      <section aria-labelledby="workspace-deployment-boundary" className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
        <div className="flex items-start gap-3">
          <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-surface)] text-[var(--ls-accent)]"><ShieldCheck className="size-4" /></span>
          <div>
            <h3 className="text-sm font-semibold text-[var(--ls-text)]" id="workspace-deployment-boundary">Deployment boundary</h3>
            <dl className="mt-2 grid gap-x-4 gap-y-1 text-xs leading-5 sm:grid-cols-[auto_1fr]">
              <dt className="text-[var(--ls-text-tertiary)]">Hosting</dt>
              <dd className="font-medium text-[var(--ls-text)]">{deployment.mode === "cloud" ? "Open Review Cloud" : "Self-hosted"}</dd>
              <dt className="text-[var(--ls-text-tertiary)]">Region</dt>
              <dd className="font-medium text-[var(--ls-text)]">{deployment.region}</dd>
              <dt className="text-[var(--ls-text-tertiary)]">Identity</dt>
              <dd className="font-medium text-[var(--ls-text)]">{deployment.identity}</dd>
            </dl>
          </div>
        </div>
        {deployment.identityState === "needs_configuration" ? (
          <p className="mt-3 rounded-[10px] border border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_10%,var(--ls-surface))] px-3 py-2 text-xs leading-5 text-[var(--ls-warning-text)]">
            An operator must configure Casdoor/OIDC before this workspace can be accessed outside a local development preview.
          </p>
        ) : null}
      </section>
      <section className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
        <div className="flex items-start gap-3">
          <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-surface)] text-[var(--ls-accent)]"><Database className="size-4" /></span>
          <div>
            <p className="text-sm font-semibold text-[var(--ls-text)]">Deployment-owned boundary</p>
            <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Region, Git endpoint, and retention infrastructure remain controlled by this deployment. They are not silently inferred from this browser.</p>
          </div>
        </div>
        <div className="mt-3 flex items-start gap-3 border-t border-[var(--ls-line)] pt-3">
          <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-surface)] text-[var(--ls-accent)]"><KeyRound className="size-4" /></span>
          <div>
            <p className="text-sm font-semibold text-[var(--ls-text)]">Organization identity</p>
            <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Your current identity becomes the first owner. Casdoor/OIDC mapping and SSO enforcement are configured after the workspace exists.</p>
          </div>
        </div>
      </section>
      <div className="flex items-start gap-3 rounded-xl border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 text-sm leading-6 text-[var(--ls-text-secondary)]">
        <ShieldCheck className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />
        You become the workspace owner. Source-control credentials are connected
        in the next step and remain deployment-owned.
      </div>
      <button
        className="luminous-focus inline-flex w-full items-center justify-center gap-2 rounded-xl bg-[var(--ls-accent)] px-4 py-3 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)] disabled:cursor-not-allowed disabled:opacity-55"
        disabled={pending}
        type="submit"
      >
        {pending ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Building2 className="size-4" />}
        {pending ? "Creating workspace…" : "Create and connect Git"}
        {!pending ? <ArrowRight className="size-4" /> : null}
      </button>
      {message ? (
        <p aria-live="polite" className="rounded-[12px] border border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_10%,var(--ls-surface))] p-3 text-sm leading-6 text-[var(--ls-warning-text)]">
          {message}
        </p>
      ) : null}
    </form>
  );
}
