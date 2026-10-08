"use client";

import { useWorkflowText } from "@/components/console/ui-language-context";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import {
  ArrowUpRight,
  Check,
  CircleAlert,
  FileLock2,
  LoaderCircle,
  ShieldCheck,
  Sparkles,
} from "lucide-react";

import type { RuleCatalogData, RuleCatalogEntry } from "@/lib/control-api";

type Message = { tone: "error" | "success"; text: string };

function installationLabel(entry: RuleCatalogEntry) {
  const installation = entry.installation;
  if (!installation) return undefined;
  return `v${installation.rule_version} · ${installation.rule_version_state.replaceAll("_", " ")}`;
}

export function RuleCatalogBrowser({
  catalog,
  org,
}: {
  catalog: RuleCatalogData;
  org: string;
}) {
  const t = useWorkflowText();
  const router = useRouter();
  const [pendingID, setPendingID] = useState<string>();
  const [message, setMessage] = useState<Message>();

  async function install(entry: RuleCatalogEntry) {
    if (!catalog.source || catalog.source !== "live" || !entry.can_install || entry.installation) {
      return;
    }
    setPendingID(entry.id);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/rule-catalog/${encodeURIComponent(entry.id)}/installations`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            version: entry.version,
            content_sha256: entry.content_sha256,
          }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
        replayed?: boolean;
      };
      if (!response.ok) {
        throw new Error(payload.error ?? t("The policy template could not be installed."));
      }
      setMessage({
        tone: "success",
        text: payload.replayed
          ? t("This exact catalog release is already installed as a governed draft.")
          : t("Installed as a draft. Request governed approval before publishing or binding it."),
      });
      router.refresh();
    } catch (error) {
      setMessage({
        tone: "error",
        text:
          error instanceof Error
            ? error.message
            : t("The policy template could not be installed."),
      });
    } finally {
      setPendingID(undefined);
    }
  }

  if (catalog.source === "unconfigured" || catalog.source === "unavailable") {
    return (
      <section className="grid min-h-72 place-items-center rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-8 text-center shadow-[var(--ls-shadow-control)]">
        <div className="max-w-xl">
          <span className="mx-auto grid size-12 place-items-center rounded-[15px] bg-amber-500/[0.12] text-[var(--ls-warning-text)]">
            <CircleAlert className="size-5" />
          </span>
          <h2 className="mt-5 text-xl font-semibold tracking-[-0.03em] text-[var(--ls-text)]">
            {t(" Catalog unavailable ")}</h2>
          <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">
            {catalog.detail ?? t("The control plane did not return a trusted policy catalog.")}
          </p>
        </div>
      </section>
    );
  }

  return (
    <section className="space-y-4">
      {catalog.source === "demo" ? (
        <div className="flex items-start gap-3 rounded-[14px] border border-violet-500/20 bg-violet-500/[0.07] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
          <CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />
          {catalog.detail}
        </div>
      ) : null}
      <div className="flex flex-col gap-3 rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex gap-3">
          <span className="grid size-9 shrink-0 place-items-center rounded-[11px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
            <FileLock2 className="size-4" />
          </span>
          <div>
            <p className="text-sm font-semibold text-[var(--ls-text)]">{t("Core catalog · release-pinned")}</p>
            <p className="mt-1 max-w-2xl text-xs leading-5 text-[var(--ls-text-secondary)]">
              {t(" Templates are compiled into this Open Review release. Installation creates a normal draft only; approvals, publication, and scoped bindings remain separate decisions. ")}</p>
          </div>
        </div>
        <span className="inline-flex w-fit items-center gap-1.5 rounded-full border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2.5 py-1 text-[11px] text-[var(--ls-text-secondary)]">
          <ShieldCheck className="size-3.5 text-[var(--ls-success-text)]" />
          {t(" Content verified ")}</span>
      </div>

      {message ? (
        <p
          aria-live="polite"
          className={message.tone === "error" ? "rounded-[12px] border border-rose-500/25 bg-rose-500/[0.06] px-4 py-3 text-sm text-[var(--ls-critical-text)]" : "rounded-[12px] border border-emerald-500/25 bg-emerald-500/[0.06] px-4 py-3 text-sm text-[var(--ls-success-text)]"}
        >
          {message.text}
        </p>
      ) : null}

      {catalog.entries.length === 0 ? (
        <div className="grid min-h-52 place-items-center rounded-[18px] border border-dashed border-[var(--ls-line-strong)] bg-[var(--ls-surface)] p-8 text-center">
          <div>
            <Sparkles className="mx-auto size-5 text-[var(--ls-text-tertiary)]" />
            <p className="mt-3 text-sm font-medium text-[var(--ls-text)]">{t("No catalog entries in this release")}</p>
          </div>
        </div>
      ) : (
        <div className="grid gap-4 xl:grid-cols-3">
          {catalog.entries.map((entry) => {
            const installed = entry.installation;
            const pending = pendingID === entry.id;
            return (
              <article className="flex min-h-72 flex-col rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]" key={entry.id}>
                <div className="flex items-start justify-between gap-3">
                  <span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
                    <ShieldCheck className="size-4" />
                  </span>
                  {installed ? (
                    <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/[0.1] px-2.5 py-1 text-[11px] font-medium text-[var(--ls-success-text)]">
                      <Check className="size-3" /> {t(" Installed ")}</span>
                  ) : (
                    <span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-[11px] font-medium text-[var(--ls-text-secondary)]">
                      {entry.rule_count} {entry.rule_count === 1 ? "rule" : "rules"}
                    </span>
                  )}
                </div>
                <h2 className="mt-5 text-base font-semibold tracking-[-0.02em] text-[var(--ls-text)]">{entry.title}</h2>
                <p className="mt-2 min-h-15 text-sm leading-6 text-[var(--ls-text-secondary)]">{entry.description}</p>
                <div className="mt-4 flex flex-wrap gap-1.5">
                  {entry.tags.map((tag) => <span className="rounded-full bg-[var(--ls-surface-muted)] px-2 py-1 text-[11px] text-[var(--ls-text-secondary)]" key={tag}>{tag}</span>)}
                </div>
                <details className="mt-4 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)]">
                  <summary className="luminous-focus cursor-pointer list-none px-3 py-2.5 text-xs font-medium text-[var(--ls-text-secondary)] [&::-webkit-details-marker]:hidden">
                    {t(" Version & provenance ")}</summary>
                  <dl className="space-y-2 border-t border-[var(--ls-line)] px-3 py-3 text-[11px] leading-5 text-[var(--ls-text-secondary)]">
                    <div className="flex justify-between gap-3"><dt>{t("Release")}</dt><dd className="font-mono text-[var(--ls-text)]">v{entry.version}</dd></div>
                    <div className="flex justify-between gap-3"><dt>{t("Source")}</dt><dd className="truncate font-mono text-[var(--ls-text)]" title={entry.origin}>{entry.origin}</dd></div>
                    <div className="flex justify-between gap-3"><dt>{t("Digest")}</dt><dd className="font-mono text-[var(--ls-text)]" title={entry.content_sha256}>{entry.content_sha256.slice(0, 12)}</dd></div>
                  </dl>
                </details>
                <div className="mt-auto pt-4">
                  {installed ? (
                    <Link className="luminous-focus inline-flex h-9 w-full items-center justify-center gap-2 rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-semibold text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" href={`/${encodeURIComponent(org)}/rules/${encodeURIComponent(installed.rule_set_id)}?tab=overview`}>
                      {t(" Open governed policy ")}<span className="text-[var(--ls-text-tertiary)]">{installationLabel(entry)}</span><ArrowUpRight className="size-3.5" />
                    </Link>
                  ) : (
                    <button className="luminous-focus inline-flex h-9 w-full items-center justify-center gap-2 rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white transition hover:bg-[var(--ls-accent-hover)] disabled:cursor-not-allowed disabled:opacity-45" disabled={!entry.can_install || pending} onClick={() => install(entry)} type="button">
                      {pending ? <LoaderCircle className="size-3.5 animate-spin" /> : <ShieldCheck className="size-3.5" />}
                      {catalog.source === "demo" ? t("Preview only") : entry.can_install ? t("Install as draft") : t("Admin approval required")}
                    </button>
                  )}
                </div>
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}
