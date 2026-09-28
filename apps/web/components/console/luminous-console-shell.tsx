"use client";

import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Bell,
  Building2,
  CircleAlert,
  ChevronDown,
  CircleGauge,
  Crosshair,
  GitPullRequest,
  ListChecks,
  MessagesSquare,
  LogOut,
  Monitor,
  Moon,
  Plus,
  Search,
  Settings2,
  ShieldCheck,
  SquareTerminal,
  Sparkles,
  Sun,
} from "lucide-react";

import type { AccessibleWorkspace } from "@/lib/control-api";
import { cn } from "@/lib/utils";

type ThemePreference = "system" | "light" | "dark";
const themeChangeEvent = "open-review-theme-change";

function readTheme(): ThemePreference {
  const stored = window.localStorage.getItem("open-review-theme");
  return stored === "light" || stored === "dark" || stored === "system"
    ? stored
    : "system";
}

function subscribeToTheme(onStoreChange: () => void) {
  window.addEventListener("storage", onStoreChange);
  window.addEventListener(themeChangeEvent, onStoreChange);
  return () => {
    window.removeEventListener("storage", onStoreChange);
    window.removeEventListener(themeChangeEvent, onStoreChange);
  };
}

const railItems = [
  { key: "home", label: "Cockpit", icon: CircleGauge },
  { key: "issues", label: "Issues", icon: ListChecks },
  { key: "reviews", label: "Pull requests", icon: GitPullRequest },
  { key: "agent-work", label: "Agent work", icon: SquareTerminal },
  { key: "rules", label: "Policy studio", icon: ShieldCheck },
  { key: "connect", label: "Operate", icon: Settings2 },
  { key: "settings/members", label: "Enterprise", icon: Building2 },
] as const;

const mobileItems = [railItems[2], railItems[1], railItems[3], railItems[4], railItems[5]];

export function LuminousConsoleShell({
  children,
  org,
  preview = false,
  workspaces,
}: {
  children: React.ReactNode;
  org: string;
  preview?: boolean;
  workspaces: AccessibleWorkspace[];
}) {
  const pathname = usePathname();
  const theme = useSyncExternalStore(subscribeToTheme, readTheme, () => "system");
  const [commandOpen, setCommandOpen] = useState(false);
  const [commandQuery, setCommandQuery] = useState("");
  const commandTriggerRef = useRef<HTMLButtonElement>(null);
  const commandReturnFocusRef = useRef<HTMLElement | null>(null);
  const title = org.charAt(0).toUpperCase() + org.slice(1);
  const visibleWorkspaces = workspaces.some((workspace) => workspace.slug === org)
    ? workspaces
    : [{ name: title, role: "member", slug: org }, ...workspaces];
  const commandItems = useMemo(() => commandItemsFor(org), [org]);
  const visibleCommandItems = commandItems.filter((item) =>
    `${item.label} ${item.description}`.toLocaleLowerCase().includes(commandQuery.trim().toLocaleLowerCase()),
  );

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLocaleLowerCase() === "k") {
        event.preventDefault();
        openCommandPalette();
      }
      if (event.key === "Escape" && commandOpen) closeCommandPalette();
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [commandOpen]);

  function cycleTheme() {
    const next = theme === "system" ? "light" : theme === "light" ? "dark" : "system";
    window.localStorage.setItem("open-review-theme", next);
    window.dispatchEvent(new Event(themeChangeEvent));
  }

  function openCommandPalette() {
    const active = document.activeElement;
    commandReturnFocusRef.current = active instanceof HTMLElement ? active : null;
    setCommandOpen(true);
  }

  function closeCommandPalette() {
    setCommandOpen(false);
    setCommandQuery("");
    // A command palette is a modal, not an overlay that drops keyboard users
    // back at the document start. Restore the invoking control after React
    // removes the dialog so the next Tab continues through the top bar.
    requestAnimationFrame(() =>
      (commandReturnFocusRef.current ?? commandTriggerRef.current)?.focus(),
    );
  }

  const ThemeIcon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor;

  return (
    <div className={cn("luminous-console", `luminous-theme-${theme}`)}>
      <a
        className="luminous-focus fixed left-3 top-3 z-[100] -translate-y-24 rounded-[10px] bg-[var(--ls-accent)] px-4 py-2 text-sm font-semibold text-white shadow-[var(--ls-shadow-float)] transition-transform focus:translate-y-0"
        href="#main-content"
      >
        Skip to main content
      </a>
      <header aria-hidden={commandOpen || undefined} className="luminous-frosted sticky top-0 z-40 flex h-14 items-center border-b border-[var(--ls-line)] px-3 sm:px-5">
        <Link
          className="luminous-focus flex min-w-0 items-center gap-2.5 rounded-lg"
          href={`/${org}/home`}
        >
          <span className="grid size-8 shrink-0 place-items-center rounded-xl bg-[var(--ls-accent)] text-white shadow-[var(--ls-shadow-control)]">
            <Sparkles className="size-4" strokeWidth={2.3} />
          </span>
          <span className="hidden text-sm font-semibold tracking-[-0.02em] text-[var(--ls-text)] sm:block">
            Open Review
          </span>
        </Link>

        <span className="mx-4 hidden h-6 w-px bg-[var(--ls-line)] sm:block" />
        <details className="group relative">
          <summary className="luminous-focus flex cursor-pointer list-none items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-[var(--ls-text)] transition hover:bg-[var(--ls-surface-muted)] [&::-webkit-details-marker]:hidden">
            <Building2 className="size-4 text-[var(--ls-text-secondary)]" />
            <span className="max-w-28 truncate">{title}</span>
            <ChevronDown className="size-3.5 text-[var(--ls-text-tertiary)] transition group-open:rotate-180" />
          </summary>
          <div className="luminous-frosted absolute left-0 top-[calc(100%+12px)] z-50 w-72 overflow-hidden rounded-[14px] border border-[var(--ls-line-strong)] p-1.5 shadow-[var(--ls-shadow-float)]">
            <p className="px-2.5 pb-2 pt-1.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-[var(--ls-text-tertiary)]">
              Workspaces
            </p>
            {visibleWorkspaces.map((workspace) => {
              const current = workspace.slug === org;
              return (
                <Link
                  aria-current={current ? "page" : undefined}
                  className={cn(
                    "luminous-focus flex items-center gap-3 rounded-[10px] px-2.5 py-2.5 text-sm transition",
                    current
                      ? "bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"
                      : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]",
                  )}
                  href={`/${workspace.slug}/home`}
                  key={workspace.slug}
                >
                  <span className="grid size-8 place-items-center rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface)]">
                    <Building2 className="size-4" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{workspace.name}</span>
                    <span className="block truncate text-xs text-[var(--ls-text-tertiary)]">
                      {workspace.slug} · {workspace.role}
                    </span>
                  </span>
                </Link>
              );
            })}
            <div className="mt-1 border-t border-[var(--ls-line)] pt-1">
              <Link
                className="luminous-focus flex items-center gap-3 rounded-[10px] px-2.5 py-2.5 text-sm text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
                href="/workspaces"
              >
                <Building2 className="size-4" /> All workspaces
              </Link>
              <Link
                className="luminous-focus flex items-center gap-3 rounded-[10px] px-2.5 py-2.5 text-sm font-medium text-[var(--ls-accent)] hover:bg-[var(--ls-accent-soft)]"
                href="/workspaces/new"
              >
                <Plus className="size-4" /> Create workspace
              </Link>
            </div>
          </div>
        </details>

        <nav aria-label="Primary domains" className="ml-6 hidden rounded-[10px] bg-[var(--ls-surface-muted)] p-1 lg:flex">
          <DomainLink active={pathname.startsWith(`/${org}/issues`) || pathname.startsWith(`/${org}/provider-issues`) || pathname.startsWith(`/${org}/findings`) || pathname.startsWith(`/${org}/reviews`) || pathname.startsWith(`/${org}/tasks`) || pathname.startsWith(`/${org}/cli-reviews`)} href={`/${org}/issues`}>
            Review
          </DomainLink>
          <DomainLink active={pathname.startsWith(`/${org}/rules`) || pathname.startsWith(`/${org}/review-config`)} href={`/${org}/review-config/general`}>
            Policy
          </DomainLink>
          <DomainLink active={pathname.startsWith(`/${org}/connect`) || pathname.startsWith(`/${org}/notifications`) || pathname.startsWith(`/${org}/audit`) || pathname.startsWith(`/${org}/settings`) || pathname.startsWith(`/${org}/agent-work`)} href={`/${org}/connect`}>
            Operate
          </DomainLink>
        </nav>

        <div className="ml-auto flex items-center gap-1.5">
          <button
            aria-haspopup="dialog"
            aria-label="Open command palette"
            className="luminous-focus hidden h-9 w-[min(34vw,360px)] items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text-tertiary)] shadow-[var(--ls-shadow-control)] md:flex"
            onClick={openCommandPalette}
            ref={commandTriggerRef}
            type="button"
          >
            <Search className="size-4" />
            <span className="truncate">Jump to a page or workspace…</span>
            <kbd className="ml-auto rounded border border-[var(--ls-line)] px-1.5 py-0.5 text-[10px]">⌘ K</kbd>
          </button>
          <button
            aria-label={`Theme: ${theme}. Activate to change theme.`}
            className="luminous-focus grid size-9 place-items-center rounded-[10px] text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
            onClick={cycleTheme}
            title={`Theme: ${theme}`}
            type="button"
          >
            <ThemeIcon className="size-4" />
          </button>
          <Link
            aria-label="Notifications"
            className="luminous-focus grid size-9 place-items-center rounded-[10px] text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
            href={`/${org}/notifications`}
            title="Notifications"
          >
            <Bell className="size-4" />
          </Link>
          <span className="grid size-8 place-items-center rounded-full bg-[var(--ls-accent-soft)] text-xs font-semibold text-[var(--ls-accent)]">
            RL
          </span>
          <form action="/api/auth/logout" method="post">
            <input name="next" type="hidden" value="/" />
            <button
              aria-label="Sign out"
              className="luminous-focus grid size-9 place-items-center rounded-[10px] text-[var(--ls-text-tertiary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
              type="submit"
            >
              <LogOut className="size-4" />
            </button>
          </form>
        </div>
      </header>

      <aside aria-hidden={commandOpen || undefined} className="luminous-frosted fixed inset-y-14 left-0 z-30 hidden w-16 border-r border-[var(--ls-line)] md:flex md:flex-col md:items-center md:gap-2 md:py-5">
        {railItems.map((item) => {
          const href = `/${org}/${item.key}`;
          const active = pathname.startsWith(href) || (item.key === "issues" && pathname.startsWith(`/${org}/provider-issues`));
          const Icon = item.icon;
          return (
            <Link
              aria-current={active ? "page" : undefined}
              aria-label={item.label}
              className={cn(
                "luminous-focus relative grid size-10 place-items-center rounded-[11px] transition",
                active
                  ? "bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"
                  : "text-[var(--ls-text-tertiary)] hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]",
              )}
              href={href}
              key={item.key}
              title={item.label}
            >
              {active ? <span className="absolute -left-3 h-5 w-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}
              <Icon className="size-[18px]" />
            </Link>
          );
        })}
      </aside>

      <main aria-hidden={commandOpen || undefined} className="pb-20 md:pl-16 md:pb-0" id="main-content" tabIndex={-1}>
        <div className="mx-auto max-w-[1600px] px-4 py-6 sm:px-7 lg:px-10 lg:py-8">
          {preview ? <PreviewModeNotice org={org} /> : null}
          {children}
        </div>
      </main>

      {commandOpen ? (
        <CommandPalette
          items={visibleCommandItems}
          onClose={closeCommandPalette}
          onQueryChange={setCommandQuery}
          query={commandQuery}
          workspaces={visibleWorkspaces}
        />
      ) : null}

      <nav aria-hidden={commandOpen || undefined} aria-label="Mobile navigation" className="luminous-frosted fixed inset-x-0 bottom-0 z-40 grid grid-cols-5 border-t border-[var(--ls-line)] px-2 pb-[max(.5rem,env(safe-area-inset-bottom))] pt-2 md:hidden">
        {mobileItems.map((item) => {
          const href = `/${org}/${item.key}`;
          const active = pathname.startsWith(href) || (item.key === "issues" && pathname.startsWith(`/${org}/provider-issues`));
          const Icon = item.icon;
          return (
            <Link
              aria-current={active ? "page" : undefined}
              className={cn(
                "luminous-focus flex flex-col items-center gap-1 rounded-[10px] py-1.5 text-[10px] font-medium",
                active ? "text-[var(--ls-accent)]" : "text-[var(--ls-text-tertiary)]",
              )}
              href={href}
              key={item.key}
            >
              <Icon className="size-[18px]" />
              {item.key === "reviews" ? "Review" : item.label}
            </Link>
          );
        })}
      </nav>
    </div>
  );
}

function PreviewModeNotice({ org }: { org: string }) {
  return (
    <aside
      className="mb-5 flex flex-col gap-3 rounded-[14px] border border-amber-500/25 bg-amber-500/[0.055] px-4 py-3.5 text-sm text-[var(--ls-text-secondary)] shadow-[var(--ls-shadow-control)] sm:flex-row sm:items-center sm:justify-between"
      role="status"
    >
      <div className="flex min-w-0 items-start gap-3">
        <CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-warning-text)]" />
        <p className="leading-5">
          <span className="font-semibold text-[var(--ls-text)]">Preview environment.</span>{" "}
          Pull requests, Issues, checks, and findings shown here are sample records. This deployment does not read from or write to GitHub or GitLab.
        </p>
      </div>
      <Link
        className="luminous-focus inline-flex shrink-0 items-center justify-center rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-1.5 text-xs font-semibold text-[var(--ls-text)] transition hover:bg-[var(--ls-surface-muted)]"
        href={`/${encodeURIComponent(org)}/connect`}
      >
        Open connections
      </Link>
    </aside>
  );
}

type CommandItem = {
  description: string;
  href: string;
  icon: typeof Search;
  label: string;
};

type CommandChoice = CommandItem & {
  group: "Pages" | "Workspaces" | "Workspace actions";
};

function commandItemsFor(org: string): CommandItem[] {
  const route = (path: string) => `/${encodeURIComponent(org)}${path}`;
  return [
    { label: "Review cockpit", description: "Prioritize current review operations", href: route("/home"), icon: CircleGauge },
    { label: "Issues", description: "Open, regressed, critical, and assigned issue evidence", href: route("/issues"), icon: ListChecks },
    { label: "Provider Issue triage", description: "AI analysis for user-authored GitHub and GitLab Issues", href: route("/provider-issues"), icon: MessagesSquare },
    { label: "Finding explorer", description: "Cross-run finding feedback and trends", href: route("/findings"), icon: Crosshair },
    { label: "Pull requests", description: "Review decisions and revision-bound evidence", href: route("/reviews"), icon: GitPullRequest },
    { label: "Work queue", description: "Running and needs-attention review runs", href: route("/tasks?tab=running"), icon: CircleGauge },
		{ label: "Agent work", description: "Issue-to-PR admission, classification, plans, and approvals", href: route("/agent-work"), icon: SquareTerminal },
		{ label: "CLI reviews", description: "Review admission and reproducible CLI evidence", href: route("/cli-reviews"), icon: SquareTerminal },
		{ label: "Review commands", description: "Provider comment commands and Console shortcuts", href: route("/review-commands"), icon: SquareTerminal },
		{ label: "Review settings", description: "Scope, prompts, summaries, and lifecycle messages", href: route("/review-config/general"), icon: ShieldCheck },
    { label: "Policy library", description: "Published policies, drafts, and provenance", href: route("/rules"), icon: ShieldCheck },
    { label: "Policy approvals", description: "Independently decide immutable content changes", href: route("/rules/approvals"), icon: ShieldCheck },
    { label: "Policy exceptions", description: "Time-bounded governed risk acceptance", href: route("/rules/exceptions"), icon: ShieldCheck },
    { label: "Connections", description: "Git provider installations and verification", href: route("/connect"), icon: Settings2 },
    { label: "Notifications", description: "Destinations, routes, and delivery receipts", href: route("/notifications"), icon: Bell },
    { label: "Audit", description: "Immutable workspace control-plane events", href: route("/audit"), icon: Settings2 },
    { label: "Usage", description: "Reservation, settlement, and reconciliation", href: route("/usage"), icon: CircleGauge },
    { label: "Enterprise settings", description: "Members, SSO, models, keys, data, and health", href: route("/settings/members"), icon: Building2 },
  ];
}

function CommandPalette({
  items,
  onClose,
  onQueryChange,
  query,
  workspaces,
}: {
  items: CommandItem[];
  onClose: () => void;
  onQueryChange: (query: string) => void;
  query: string;
  workspaces: AccessibleWorkspace[];
}) {
  const dialogRef = useRef<HTMLElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const workspaceItems = useMemo<CommandItem[]>(
    () =>
      workspaces.map((workspace) => ({
        description: `${workspace.slug} · ${workspace.role}`,
        href: `/${encodeURIComponent(workspace.slug)}/home`,
        icon: Building2,
        label: `Switch to ${workspace.name}`,
      })),
    [workspaces],
  );
  const workspaceActions = useMemo<CommandItem[]>(
    () => [
      {
        description: "See every workspace you can access",
        href: "/workspaces",
        icon: Building2,
        label: "All workspaces",
      },
      {
        description: "Create a separate workspace boundary",
        href: "/workspaces/new",
        icon: Plus,
        label: "Create workspace",
      },
    ],
    [],
  );
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const visibleWorkspaces = workspaceItems.filter((workspace) =>
    `${workspace.label} ${workspace.description}`
      .toLocaleLowerCase()
      .includes(normalizedQuery),
  );
  const visibleWorkspaceActions = workspaceActions.filter((action) =>
    `${action.label} ${action.description}`
      .toLocaleLowerCase()
      .includes(normalizedQuery),
  );
  const choices = useMemo<CommandChoice[]>(
    () => [
      ...items.map((item) => ({ ...item, group: "Pages" as const })),
      ...visibleWorkspaces.map((item) => ({ ...item, group: "Workspaces" as const })),
      ...visibleWorkspaceActions.map((item) => ({ ...item, group: "Workspace actions" as const })),
    ],
    [items, visibleWorkspaceActions, visibleWorkspaces],
  );
  const [activeIndex, setActiveIndex] = useState(0);
  const highlightedIndex = Math.min(
    activeIndex,
    Math.max(choices.length - 1, 0),
  );

  function navigate(choice: CommandChoice | undefined) {
    if (!choice) return;
    window.location.assign(choice.href);
  }

  useEffect(() => {
    searchRef.current?.focus();
  }, []);

  function trapFocus(event: React.KeyboardEvent<HTMLElement>) {
    if (event.key !== "Tab") return;
    const focusable = Array.from(
      dialogRef.current?.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ) ?? [],
    ).filter((element) => !element.hasAttribute("hidden"));
    if (!focusable.length) return;
    const current = document.activeElement as HTMLElement | null;
    const index = current ? focusable.indexOf(current) : -1;
    const next = event.shiftKey
      ? index <= 0 ? focusable.length - 1 : index - 1
      : index === focusable.length - 1 ? 0 : index + 1;
    event.preventDefault();
    focusable[next]?.focus();
  }

  function moveActive(direction: 1 | -1) {
    if (!choices.length) return;
    setActiveIndex((current) => {
      const bounded = Math.min(current, choices.length - 1);
      return (bounded + direction + choices.length) % choices.length;
    });
  }

  function onSearchKeyDown(event: React.KeyboardEvent<HTMLInputElement>) {
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        moveActive(1);
        return;
      case "ArrowUp":
        event.preventDefault();
        moveActive(-1);
        return;
      case "Home":
        if (!choices.length) return;
        event.preventDefault();
        setActiveIndex(0);
        return;
      case "End":
        if (!choices.length) return;
        event.preventDefault();
        setActiveIndex(choices.length - 1);
        return;
      case "Enter":
        if (!choices.length) return;
        event.preventDefault();
        navigate(choices[highlightedIndex]);
        return;
      default:
        return;
    }
  }

  const pageChoices = choices.filter((choice) => choice.group === "Pages");
  const workspaceChoices = choices.filter(
    (choice) => choice.group === "Workspaces",
  );
  const workspaceActionChoices = choices.filter(
    (choice) => choice.group === "Workspace actions",
  );

  return (
    <div className="fixed inset-0 z-50 bg-black/20 p-4 backdrop-blur-sm" role="presentation" onMouseDown={onClose}>
      <section
        aria-label="Workspace command palette"
        aria-modal="true"
        className="luminous-frosted mx-auto mt-[max(8vh,4rem)] w-full max-w-2xl overflow-hidden rounded-[20px] border border-[var(--ls-line-strong)] shadow-[var(--ls-shadow-float)]"
        onKeyDown={trapFocus}
        onMouseDown={(event) => event.stopPropagation()}
        ref={dialogRef}
        role="dialog"
      >
        <div className="flex items-center gap-3 border-b border-[var(--ls-line)] px-4 py-3">
          <Search className="size-4 shrink-0 text-[var(--ls-text-tertiary)]" />
          <label className="sr-only" htmlFor="workspace-command-search">Search pages and workspaces</label>
          <input
            aria-activedescendant={
              choices[highlightedIndex]
                ? commandOptionID(choices[highlightedIndex])
                : undefined
            }
            aria-autocomplete="list"
            aria-controls="workspace-command-options"
            aria-expanded="true"
            autoFocus
            className="h-8 min-w-0 flex-1 bg-transparent text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)]"
            id="workspace-command-search"
            onChange={(event) => {
              setActiveIndex(0);
              onQueryChange(event.target.value);
            }}
            onKeyDown={onSearchKeyDown}
            placeholder="Search pages and workspaces…"
            ref={searchRef}
            role="combobox"
            value={query}
          />
          <kbd className="rounded border border-[var(--ls-line)] px-1.5 py-0.5 text-[10px] text-[var(--ls-text-tertiary)]">Esc</kbd>
        </div>
        <div className="max-h-[min(66vh,560px)] overflow-y-auto p-2" id="workspace-command-options" role="listbox">
          {choices.length ? (
            <>
              {pageChoices.length ? (
                <CommandGroup label="Pages">
                  {pageChoices.map((item) => (
                    <CommandPaletteLink
                      active={choices.indexOf(item) === highlightedIndex}
                      item={item}
                      key={item.href}
                      onNavigate={onClose}
                    />
                  ))}
                </CommandGroup>
              ) : null}
              {workspaceChoices.length ? (
                <CommandGroup label="Workspaces">
                  {workspaceChoices.map((item) => (
                    <CommandPaletteLink
                      active={choices.indexOf(item) === highlightedIndex}
                      item={item}
                      key={item.href}
                      onNavigate={onClose}
                    />
                  ))}
                </CommandGroup>
              ) : null}
              {workspaceActionChoices.length ? (
                <CommandGroup label="Workspace actions">
                  {workspaceActionChoices.map((item) => (
                    <CommandPaletteLink
                      active={choices.indexOf(item) === highlightedIndex}
                      item={item}
                      key={item.href}
                      onNavigate={onClose}
                    />
                  ))}
                </CommandGroup>
              ) : null}
            </>
          ) : (
            <p className="px-3 py-7 text-center text-sm text-[var(--ls-text-secondary)]">
              No pages or workspaces match this search.
            </p>
          )}
        </div>
      </section>
    </div>
  );
}

function CommandGroup({ children, label }: { children: React.ReactNode; label: string }) {
  return <section className="py-1"><p className="px-3 pb-1.5 pt-2 text-[10px] font-semibold uppercase tracking-[.14em] text-[var(--ls-text-tertiary)]">{label}</p>{children}</section>;
}

function commandOptionID(item: Pick<CommandChoice, "group" | "href">) {
  const group = item.group.toLocaleLowerCase().replaceAll(/[^a-zA-Z0-9_-]/g, "-");
  const href = item.href.replaceAll(/[^a-zA-Z0-9_-]/g, "-");
  return `workspace-command-${group}-${href}`;
}

function CommandPaletteLink({
  active,
  item,
  onNavigate,
}: {
  active: boolean;
  item: CommandChoice;
  onNavigate: () => void;
}) {
  const Icon = item.icon;
  return (
    <Link
      aria-selected={active}
      className={cn(
        "luminous-focus flex items-center gap-3 rounded-[11px] px-3 py-2.5 text-sm text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]",
        active && "bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]",
      )}
      href={item.href}
      id={commandOptionID(item)}
      onClick={onNavigate}
      role="option"
    >
      <span className="grid size-8 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
        <Icon className="size-4" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block font-medium text-[var(--ls-text)]">{item.label}</span>
        <span className="mt-0.5 block truncate text-xs text-[var(--ls-text-tertiary)]">{item.description}</span>
      </span>
    </Link>
  );
}

function DomainLink({
  active,
  children,
  href,
}: {
  active: boolean;
  children: React.ReactNode;
  href: string;
}) {
  return (
    <Link
      aria-current={active ? "page" : undefined}
      className={cn(
        "luminous-focus rounded-[8px] px-5 py-1.5 text-xs font-medium transition",
        active
          ? "bg-[var(--ls-surface)] text-[var(--ls-text)] shadow-[var(--ls-shadow-control)]"
          : "text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]",
      )}
      href={href}
    >
      {children}
    </Link>
  );
}
