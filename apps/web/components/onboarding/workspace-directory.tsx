"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import {
  ArrowRight,
  Building2,
  CircleAlert,
  CircleHelp,
  Command,
  Plus,
  Search,
  ShieldCheck,
} from "lucide-react";

import type {
  AccessibleWorkspaceState,
  DataSource,
} from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { ProductMark } from "@/components/marketing/marketing-shell";
import { ProviderMark } from "@/components/providers/provider-icons";
import { PageState, RecoveryAction } from "@/components/console/page-state";
import { PublicThemeToggle } from "@/components/onboarding/luminous-public-frame";
import { RequestAccessForm } from "@/components/onboarding/request-access-form";

type DirectoryFilter = "all" | "ready" | "setup";

function statusPresentation(status: AccessibleWorkspaceState["initialization"]["status"]) {
  switch (status) {
    case "ready":
      return {
        label: "Ready",
        tone: "bg-[color:color-mix(in_srgb,var(--ls-success)_12%,transparent)] text-[var(--ls-success-text)]",
        dot: "bg-[var(--ls-success)]",
      };
    case "access_denied":
      return {
        label: "No access",
        tone: "bg-amber-500/10 text-[var(--ls-warning-text)]",
        dot: "bg-[var(--ls-warning)]",
      };
    case "needs_connection":
      return {
        label: "Connect Git",
        tone: "bg-[color:color-mix(in_srgb,var(--ls-warning)_12%,transparent)] text-[var(--ls-warning-text)]",
        dot: "bg-[var(--ls-warning)]",
      };
    case "verifying_connection":
      return {
        label: "Verifying access",
        tone: "bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]",
        dot: "bg-[var(--ls-accent)]",
      };
    case "connection_failed":
      return {
        label: "Needs attention",
        tone: "bg-red-500/10 text-[var(--ls-critical-text)]",
        dot: "bg-[var(--ls-critical)]",
      };
    case "needs_setup":
      return {
        label: "Setup incomplete",
        tone: "bg-[color:color-mix(in_srgb,var(--ls-warning)_12%,transparent)] text-[var(--ls-warning-text)]",
        dot: "bg-[var(--ls-warning)]",
      };
    default:
      return {
        label: "State unavailable",
        tone: "bg-[var(--ls-surface-muted)] text-[var(--ls-text-tertiary)]",
        dot: "bg-[var(--ls-text-tertiary)]",
      };
  }
}

function workspaceHref(workspace: AccessibleWorkspaceState) {
  if (workspace.initialization.status === "ready") {
    return `/${encodeURIComponent(workspace.slug)}/home`;
  }
  if (
    workspace.initialization.status === "unavailable" ||
    workspace.initialization.status === "access_denied"
  ) {
    return undefined;
  }
  return `/setup?tenant=${encodeURIComponent(workspace.slug)}&next=${encodeURIComponent(`/${workspace.slug}/home`)}`;
}

export function WorkspaceDirectory({
  detail,
  needsSignIn,
  notice,
  requestedSlug,
  source,
  workspaces,
}: {
  detail?: string;
  needsSignIn?: boolean;
  notice?: "access_denied";
  requestedSlug?: string;
  source: DataSource;
  workspaces: AccessibleWorkspaceState[];
}) {
  const [filter, setFilter] = useState<DirectoryFilter>("all");
  const [query, setQuery] = useState("");
  const searchInput = useRef<HTMLInputElement>(null);

  useEffect(() => {
    function focusWorkspaceSearch(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLocaleLowerCase() === "k") {
        event.preventDefault();
        searchInput.current?.focus();
      }
    }
    window.addEventListener("keydown", focusWorkspaceSearch);
    return () => window.removeEventListener("keydown", focusWorkspaceSearch);
  }, []);
  const counts = useMemo(
    () => ({
      all: workspaces.length,
      ready: workspaces.filter((workspace) => workspace.initialization.status === "ready").length,
      setup: workspaces.filter((workspace) =>
        [
          "needs_connection",
          "verifying_connection",
          "connection_failed",
          "needs_setup",
        ].includes(workspace.initialization.status),
      ).length,
    }),
    [workspaces],
  );
  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return workspaces.filter((workspace) => {
      const matchesFilter =
        filter === "all" ||
        (filter === "ready" && workspace.initialization.status === "ready") ||
        (filter === "setup" &&
          [
            "needs_connection",
            "verifying_connection",
            "connection_failed",
            "needs_setup",
          ].includes(workspace.initialization.status));
      return (
        matchesFilter &&
        (!normalized ||
          workspace.name.toLowerCase().includes(normalized) ||
          workspace.slug.toLowerCase().includes(normalized))
      );
    });
  }, [filter, query, workspaces]);
  const directoryAvailable = source === "live" || source === "demo";

  return (
    <main className="min-h-screen p-3 sm:p-5">
      <div className="mx-auto grid min-h-[calc(100vh-1.5rem)] max-w-[1480px] overflow-hidden rounded-[28px] border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] shadow-[var(--ls-shadow-float)] lg:grid-cols-[220px_minmax(0,1fr)]">
        <aside className="hidden border-r border-[var(--ls-line)] bg-[color:color-mix(in_srgb,var(--ls-surface)_92%,var(--ls-accent-soft))] px-4 py-6 lg:flex lg:flex-col">
          <ProductMark variant="luminous" />
          <nav aria-label="Workspace access" className="mt-10 space-y-1.5">
            <Link className="luminous-focus flex items-center gap-3 rounded-[10px] bg-[var(--ls-accent-soft)] px-3 py-2.5 text-sm font-medium text-[var(--ls-accent)]" href="/workspaces">
              <Building2 className="size-4" /> Workspaces
            </Link>
            <Link className="luminous-focus flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-sm text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]" href="/#self-host">
              <CircleHelp className="size-4" /> Help
            </Link>
          </nav>
          <div className="mt-auto rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-3.5">
            <ShieldCheck className="size-4 text-[var(--ls-accent)]" />
            <p className="mt-2 text-xs font-semibold text-[var(--ls-text)]">Access-bound workspaces</p>
            <p className="mt-1 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">Setup must finish before a workspace can open its Console.</p>
          </div>
        </aside>

        <section className="min-w-0">
          <header className="flex h-16 items-center gap-3 border-b border-[var(--ls-line)] px-5 sm:px-7">
            <div className="lg:hidden"><ProductMark compact variant="luminous" /></div>
            {directoryAvailable ? <label className="luminous-focus ml-auto flex h-10 min-w-0 max-w-[440px] flex-1 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text-secondary)] shadow-[var(--ls-shadow-control)]">
              <Search className="size-4 shrink-0 text-[var(--ls-text-tertiary)]" />
              <span className="sr-only">Search workspaces</span>
              <input className="min-w-0 flex-1 bg-transparent text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)]" onChange={(event) => setQuery(event.target.value)} placeholder="Search workspaces…" ref={searchInput} value={query} />
              <kbd className="hidden rounded border border-[var(--ls-line)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)] sm:inline">⌘ K</kbd>
            </label> : <span className="ml-auto" />}
            <PublicThemeToggle />
            <span className="grid size-8 shrink-0 place-items-center rounded-full bg-[var(--ls-accent-soft)] text-xs font-semibold text-[var(--ls-accent)]">RL</span>
          </header>

          <div className="mx-auto max-w-5xl px-5 py-8 sm:px-9 sm:py-12">
            <div className="flex flex-col justify-between gap-5 sm:flex-row sm:items-end">
              <div>
                <p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Workspace directory</p>
                <h1 className="mt-3 text-3xl font-semibold tracking-[-0.055em] text-[var(--ls-text)] sm:text-4xl">Your workspaces</h1>
                <p className="mt-2 max-w-xl text-sm leading-6 text-[var(--ls-text-secondary)]">Open a ready Console, or continue the specific setup step that is still blocking review access.</p>
              </div>
              {directoryAvailable ? <Link className="luminous-focus inline-flex h-10 shrink-0 items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)]" href="/workspaces/new">
                <Plus className="size-4" /> Create workspace
              </Link> : null}
            </div>

            {source === "demo" ? (
              <div className="mt-7 flex items-start gap-3 rounded-[14px] border border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_10%,var(--ls-surface))] p-4 text-sm leading-6 text-[var(--ls-warning-text)]">
                <CircleAlert className="mt-0.5 size-4 shrink-0" />
                {detail ?? "Workspace state is not backed by the live control plane."}
              </div>
            ) : null}

            {!directoryAvailable ? (
              <PageState
                action={needsSignIn ? (
                  <form action="/api/auth/logout" method="post">
                    <input name="next" type="hidden" value="/sign-in?next=%2Fworkspaces" />
                    <button className="luminous-focus inline-flex h-9 items-center justify-center rounded-[9px] bg-[var(--ls-accent)] px-3.5 text-xs font-semibold text-white transition hover:bg-[var(--ls-accent-hover)]" type="submit">Sign in again</button>
                  </form>
                ) : <RecoveryAction href="/workspaces">Retry loading</RecoveryAction>}
                className="mt-8"
                detail={detail ?? "The control plane could not verify your workspace access. Retry when the connection is available."}
                kind={needsSignIn ? "permission" : "unavailable"}
                title={needsSignIn ? "Your session needs renewal" : "Workspace directory unavailable"}
              />
            ) : <>

            {notice === "access_denied" ? (
              <PageState
                action={
                  <RecoveryAction href="/workspaces">Switch workspace</RecoveryAction>
                }
                className="mt-7 min-h-0 py-7"
                detail={source === "live" ? "Your account is signed in, but it cannot access the workspace you tried to open. Choose one below or request access. The request will not confirm whether that workspace exists." : "This preview cannot verify access to the workspace you tried to open. Choose an available workspace below."}
                kind="permission"
                title="That workspace is not available to your account"
              >{source === "live" ? <RequestAccessForm defaultSlug={requestedSlug} key={requestedSlug} /> : null}</PageState>
            ) : null}

            <div className="mt-8 flex flex-wrap items-center gap-2">
              {([
                ["all", "All"],
                ["ready", "Ready"],
                ["setup", "Needs setup"],
              ] as const).map(([value, label]) => (
                <button className={cn("luminous-focus inline-flex h-8 items-center gap-1.5 rounded-full px-3 text-xs font-medium transition", filter === value ? "bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]" : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]")} key={value} onClick={() => setFilter(value)} type="button">
                  {label}<span className="rounded-full bg-[var(--ls-surface)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)]">{counts[value]}</span>
                </button>
              ))}
            </div>

            <div className="mt-5 overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
              <div className="hidden grid-cols-[minmax(0,1.15fr)_140px_120px_150px_20px] gap-4 border-b border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-5 py-3 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--ls-text-tertiary)] md:grid">
                <span>Workspace</span><span>Git provider</span><span>Role</span><span>Review access</span><span />
              </div>
              {filtered.length ? (
                filtered.map((workspace) => {
                  const presentation = statusPresentation(workspace.initialization.status);
                  const href = workspaceHref(workspace);
                  const provider = workspace.initialization.connection?.provider;
                  const content = <>
                    <span className="col-span-2 flex min-w-0 items-center gap-3 md:col-span-1"><span className="grid size-10 shrink-0 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-sm font-semibold text-[var(--ls-accent)]">{workspace.name.slice(0, 1).toUpperCase()}</span><span className="min-w-0 flex-1"><span className="block truncate text-sm font-semibold text-[var(--ls-text)]">{workspace.name}</span><span className="mt-1 block truncate text-xs text-[var(--ls-text-tertiary)]">{workspace.slug}<span className="capitalize md:hidden"> · {workspace.role.replaceAll("_", " ")}</span></span></span></span>
                    <span className="inline-flex min-w-0 items-center gap-2 text-xs text-[var(--ls-text-secondary)]">{provider ? <ProviderMark className={cn("size-4 shrink-0", provider === "github" && "text-[var(--ls-text)]")} provider={provider} /> : <CircleHelp className="size-4 shrink-0 text-[var(--ls-text-tertiary)]" />}<span className="truncate">{provider === "github" ? "GitHub" : provider === "gitlab" ? "GitLab" : "Not connected"}</span></span>
                    <span className="hidden text-xs capitalize text-[var(--ls-text-secondary)] md:block">{workspace.role.replaceAll("_", " ")}</span>
                    <span className={cn("inline-flex w-fit items-center gap-1.5 rounded-full px-2.5 py-1 text-[11px] font-medium", presentation.tone)}><span className={cn("size-1.5 rounded-full", presentation.dot)} />{presentation.label}</span>
                    {href ? <ArrowRight className="hidden size-4 text-[var(--ls-text-tertiary)] transition group-hover:translate-x-0.5 group-hover:text-[var(--ls-accent)] md:block" /> : <span className="hidden md:block" />}
                  </>;
                  return href ? (
                    <Link className="luminous-focus group grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-[var(--ls-line)] px-5 py-4 transition last:border-b-0 hover:bg-[var(--ls-surface-muted)] md:grid-cols-[minmax(0,1.15fr)_140px_120px_150px_20px] md:gap-4" href={href} key={workspace.slug}>{content}</Link>
                  ) : (
                    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-[var(--ls-line)] px-5 py-4 opacity-70 last:border-b-0 md:grid-cols-[minmax(0,1.15fr)_140px_120px_150px_20px] md:gap-4" key={workspace.slug}>{content}</div>
                  );
                })
              ) : (
                <PageState
                  action={query ? <RecoveryAction href="/workspaces">Clear search</RecoveryAction> : <RecoveryAction href="/workspaces/new" variant="primary">Create workspace</RecoveryAction>}
                  className="m-4 min-h-60"
                  detail={query ? "Try a different workspace name or clear the current search." : "Create an organization boundary, then connect source control before opening its Console."}
                  kind={query ? "filtered-empty" : "first-use-empty"}
                  title={query ? "No matching workspaces" : "No accessible workspace"}
                >{!query && notice !== "access_denied" && source === "live" ? <RequestAccessForm /> : null}</PageState>
              )}
            </div>

            <div className="mt-5 flex items-center gap-2 text-xs text-[var(--ls-text-tertiary)]"><Command className="size-3.5" />Workspace identity, memberships, and setup state are resolved again whenever you open a workspace.</div>
            </>}
          </div>
        </section>
      </div>
    </main>
  );
}
