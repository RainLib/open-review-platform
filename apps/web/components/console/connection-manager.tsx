"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { CheckCircle2, KeyRound, LoaderCircle, Plus, ShieldCheck } from "lucide-react";

import { Button } from "@/components/ui/button";
import type { ProviderInstallation } from "@/lib/control-api";

type Provider = "github" | "gitlab";

type ConnectionForm = {
  provider: Provider;
  externalID: string;
  repositoryScope: string;
};

const inputClassName =
  "h-9 w-full rounded-xl border border-white/10 bg-black/20 px-3 text-sm text-zinc-100 outline-none transition-colors placeholder:text-zinc-600 focus:border-cyan-300/50 focus:ring-2 focus:ring-cyan-300/10 disabled:cursor-not-allowed disabled:opacity-50";

const providerDefaults: Record<
  Provider,
  Pick<ConnectionForm, "repositoryScope">
> = {
  github: {
    repositoryScope: "owner/*",
  },
  gitlab: {
    repositoryScope: "group/*",
  },
};

function initialForm(): ConnectionForm {
  return { provider: "github", externalID: "", ...providerDefaults.github };
}

export function ConnectionManager({
  enabled,
  installations,
  org,
}: {
  enabled: boolean;
  installations: ProviderInstallation[];
  org: string;
}) {
  const router = useRouter();
  const [form, setForm] = useState<ConnectionForm>(initialForm);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();
  const credentialRef =
    form.provider === "github" ? "github-app" : "gitlab-token";

  function selectProvider(provider: Provider) {
    setForm((current) => ({
      ...current,
      provider,
      ...providerDefaults[provider],
    }));
    setMessage(undefined);
  }

  async function createInstallation(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!enabled || pending) return;

    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        "/api/tenants/" + encodeURIComponent(org) + "/installations",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            provider: form.provider,
            external_id: form.externalID,
            repository_scope: form.repositoryScope,
            credential_ref: credentialRef,
          }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      if (!response.ok) {
        throw new Error(
          payload.error ?? "The provider installation was not accepted.",
        );
      }
      setForm(initialForm());
      setMessage(
        "Connection recorded. Send a signed provider test delivery to verify the complete webhook path.",
      );
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The provider installation could not be created.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="rounded-2xl border border-white/[0.075] bg-console-surface p-5 sm:p-6">
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-start">
        <div>
          <div className="flex items-center gap-2 text-sm font-medium text-zinc-100">
            <span className="grid size-8 place-items-center rounded-xl bg-cyan-300/[0.08] text-cyan-200">
              <KeyRound className="size-4" />
            </span>
            Provider installations
          </div>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-zinc-500">
            Register a provider identity and repository scope. This form only
            sends a supported credential alias; it never accepts or displays a
            provider secret.
          </p>
        </div>
        <span className="inline-flex w-fit items-center gap-1.5 rounded-full border border-emerald-300/15 bg-emerald-300/[0.06] px-2.5 py-1 text-[11px] font-medium text-emerald-200">
          <ShieldCheck className="size-3.5" />
          Credential-safe
        </span>
      </div>

      <form className="mt-6 grid gap-4 lg:grid-cols-2" onSubmit={createInstallation}>
        <label className="space-y-1.5 text-xs font-medium text-zinc-400">
          Provider
          <select
            className={inputClassName}
            disabled={!enabled || pending}
            onChange={(event) => selectProvider(event.target.value as Provider)}
            value={form.provider}
          >
            <option value="github">GitHub App</option>
            <option value="gitlab">GitLab</option>
          </select>
        </label>
        <label className="space-y-1.5 text-xs font-medium text-zinc-400">
          {form.provider === "github"
            ? "GitHub App installation ID"
            : "GitLab integration identity"}
          <input
            className={inputClassName}
            disabled={!enabled || pending}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                externalID: event.target.value,
              }))
            }
            placeholder={form.provider === "github" ? "12345678" : "group-bot"}
            required
            value={form.externalID}
          />
        </label>
        <label className="space-y-1.5 text-xs font-medium text-zinc-400">
          Repository scope
          <input
            className={inputClassName}
            disabled={!enabled || pending}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                repositoryScope: event.target.value,
              }))
            }
            placeholder="owner/*"
            required
            value={form.repositoryScope}
          />
        </label>
        <p className="rounded-xl border border-white/[0.06] bg-black/15 px-3 py-2.5 text-xs leading-5 text-zinc-500">
          The provider API endpoint is selected from trusted deployment
          configuration. It cannot be changed from this browser.
        </p>
        <div className="flex flex-col justify-end gap-2 lg:col-span-2 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-xs leading-5 text-zinc-500">
            {form.provider === "github"
              ? "Uses the deployment-mounted GitHub App key to mint short-lived installation tokens."
              : "Uses the deployment-configured GitLab credential; no GitLab token is sent from this browser."}
          </p>
          <Button disabled={!enabled || pending} size="sm" type="submit">
            {pending ? (
              <>
                <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                Saving
              </>
            ) : (
              <>
                <Plus />
                Add connection
              </>
            )}
          </Button>
        </div>
      </form>
      {!enabled ? (
        <p className="mt-4 rounded-xl border border-amber-300/15 bg-amber-300/[0.045] px-3 py-2 text-xs leading-5 text-amber-100/80">
          Enable a local development control-plane bridge to write here.
          Production writes require the Casdoor session bridge.
        </p>
      ) : null}
      {message ? (
        <p
          aria-live="polite"
          className="mt-4 text-xs leading-5 text-cyan-100/85"
        >
          {message}
        </p>
      ) : null}

      <div className="mt-6 border-t border-white/[0.06] pt-4">
        <p className="text-xs font-medium uppercase tracking-[0.12em] text-zinc-500">
          Registered connections
        </p>
        {installations.length === 0 ? (
          <p className="mt-3 text-sm text-zinc-500">
            No provider installation is visible to this tenant yet.
          </p>
        ) : (
          <ul className="mt-3 grid gap-2 lg:grid-cols-2">
            {installations.map((installation) => (
              <li
                className="flex items-center justify-between gap-3 rounded-xl border border-white/[0.06] bg-black/15 px-3 py-2.5"
                key={installation.id}
              >
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-zinc-200">
                    {installation.provider === "github" ? "GitHub" : "GitLab"}{" "}
                    · {installation.repository_scope}
                  </p>
                  <p className="mt-0.5 truncate text-xs text-zinc-500">
                    {installation.api_base_url} · {installation.external_id}
                  </p>
                </div>
                {installation.active ? (
                  <CheckCircle2 className="size-4 shrink-0 text-emerald-300" />
                ) : (
                  <span className="text-xs text-zinc-500">Inactive</span>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
