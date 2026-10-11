"use client";

import { Fragment, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
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
  Languages,
  ListChecks,
  MessagesSquare,
  LogOut,
  Monitor,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Search,
  Settings2,
  ShieldCheck,
  SquareTerminal,
  Sparkles,
  Sun,
} from "lucide-react";

import type { AccessibleWorkspace } from "@/lib/control-api";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { uiLanguageCookie, type UiLanguage } from "@/lib/ui-language";
import { useUiLanguage, useUiText } from "@/components/console/ui-language-context";

type ThemePreference = "system" | "light" | "dark";
const themeChangeEvent = "open-review-theme-change";
const sidebarChangeEvent = "open-review-sidebar-change";

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

function readSidebarExpanded() {
  return window.localStorage.getItem("open-review-sidebar") !== "collapsed";
}

function subscribeToSidebar(onStoreChange: () => void) {
  window.addEventListener("storage", onStoreChange);
  window.addEventListener(sidebarChangeEvent, onStoreChange);
  return () => {
    window.removeEventListener("storage", onStoreChange);
    window.removeEventListener(sidebarChangeEvent, onStoreChange);
  };
}

const railItems = [
  { key: "home", label: "cockpit", icon: CircleGauge },
  { key: "issues", label: "issues", icon: ListChecks },
  { key: "reviews", label: "pullRequests", icon: GitPullRequest },
  { key: "agent-work", label: "agentWork", icon: SquareTerminal },
  { key: "agent-campaigns", label: "agentCampaigns", icon: SquareTerminal },
  { key: "rules", label: "policyStudio", icon: ShieldCheck },
  { key: "connect", label: "operate", icon: Settings2 },
  { key: "settings/members", label: "enterprise", icon: Building2 },
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
  const language = useUiLanguage();
  const t = useUiText();
  const theme = useSyncExternalStore(subscribeToTheme, readTheme, () => "system");
  const sidebarExpanded = useSyncExternalStore(
    subscribeToSidebar,
    readSidebarExpanded,
    () => true,
  );
  const [commandOpen, setCommandOpen] = useState(false);
  const [commandQuery, setCommandQuery] = useState("");
  const commandTriggerRef = useRef<HTMLButtonElement>(null);
  const commandReturnFocusRef = useRef<HTMLElement | null>(null);
  const title = org.charAt(0).toUpperCase() + org.slice(1);
  const visibleWorkspaces = workspaces.some((workspace) => workspace.slug === org)
    ? workspaces
    : [{ name: title, role: "member", slug: org }, ...workspaces];
  const commandItems = useMemo(() => commandItemsFor(org, language), [org, language]);
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

  function toggleSidebar() {
    window.localStorage.setItem(
      "open-review-sidebar",
      sidebarExpanded ? "collapsed" : "expanded",
    );
    window.dispatchEvent(new Event(sidebarChangeEvent));
  }

  function changeLanguage(next: string) {
    if (next !== "en" && next !== "zh-CN") return;
    document.cookie = `${uiLanguageCookie}=${encodeURIComponent(next)}; Path=/; Max-Age=31536000; SameSite=Lax${location.protocol === "https:" ? "; Secure" : ""}`;
    window.location.reload();
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
        {t("skip")}
      </a>
      <header aria-hidden={commandOpen || undefined} className="luminous-frosted sticky top-0 z-40 flex h-14 items-center border-b border-[var(--ls-line)] px-3 sm:px-5">
        <Link
          className="luminous-focus flex min-w-0 shrink-0 items-center gap-2.5 rounded-lg"
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
        <details className="group relative min-w-0">
          <summary className="luminous-focus flex min-w-0 cursor-pointer list-none items-center gap-2 rounded-lg px-1 py-1.5 text-sm text-[var(--ls-text)] transition hover:bg-[var(--ls-surface-muted)] sm:px-2 [&::-webkit-details-marker]:hidden">
            <Building2 className="hidden size-4 shrink-0 text-[var(--ls-text-secondary)] sm:block" />
            <span className="min-w-0 max-w-28 truncate">{title}</span>
            <ChevronDown className="size-3.5 shrink-0 text-[var(--ls-text-tertiary)] transition group-open:rotate-180" />
          </summary>
          <div className="luminous-frosted absolute left-0 top-[calc(100%+12px)] z-50 w-[min(18rem,calc(100vw-3.5rem))] overflow-hidden rounded-[14px] border border-[var(--ls-line-strong)] p-1.5 shadow-[var(--ls-shadow-float)]">
            <p className="px-2.5 pb-2 pt-1.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-[var(--ls-text-tertiary)]">
              {t("workspaces")}
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
                <Building2 className="size-4" /> {t("allWorkspaces")}
              </Link>
              <Link
                className="luminous-focus flex items-center gap-3 rounded-[10px] px-2.5 py-2.5 text-sm font-medium text-[var(--ls-accent)] hover:bg-[var(--ls-accent-soft)]"
                href="/workspaces/new"
              >
                <Plus className="size-4" /> {t("createWorkspace")}
              </Link>
            </div>
          </div>
        </details>



        <div className="ml-auto flex shrink-0 items-center gap-0 sm:gap-1.5">
          <label className="luminous-focus flex h-9 items-center gap-1 rounded-[10px] px-2 text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]" title={t("language")}>
            <Languages aria-hidden="true" className="size-4" />
            <span className="sr-only">{t("language")}</span>
            <select
              aria-label={t("language")}
              className="max-w-16 cursor-pointer bg-transparent text-xs font-medium text-[var(--ls-text)] outline-none"
              onChange={(event) => changeLanguage(event.target.value)}
              value={language}
            >
              <option value="en">EN</option>
              <option value="zh-CN">中文</option>
            </select>
          </label>
          <button
            aria-haspopup="dialog"
            aria-label={t("search")}
            className="luminous-focus hidden h-9 w-[min(34vw,360px)] items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text-tertiary)] shadow-[var(--ls-shadow-control)] md:flex"
            onClick={openCommandPalette}
            ref={commandTriggerRef}
            type="button"
          >
            <Search className="size-4" />
            <span className="truncate">{t("search")}</span>
            <kbd className="ml-auto rounded border border-[var(--ls-line)] px-1.5 py-0.5 text-[10px]">⌘ K</kbd>
          </button>
          <button
            aria-label={`${t("theme")}: ${theme}`}
            className="luminous-focus grid size-9 place-items-center rounded-[10px] text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
            onClick={cycleTheme}
            title={`${t("theme")}: ${theme}`}
            type="button"
          >
            <ThemeIcon className="size-4" />
          </button>
          <Link
            aria-label={t("notifications")}
            className="luminous-focus grid size-9 place-items-center rounded-[10px] text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
            href={`/${org}/notifications`}
            title={t("notifications")}
          >
            <Bell className="size-4" />
          </Link>
          <span className="grid size-8 place-items-center rounded-full bg-[var(--ls-accent-soft)] text-xs font-semibold text-[var(--ls-accent)]">
            RL
          </span>
          <form action="/api/auth/logout" method="post">
            <input name="next" type="hidden" value="/" />
            <button
              aria-label={t("signOut")}
              className="luminous-focus grid size-9 place-items-center rounded-[10px] text-[var(--ls-text-tertiary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
              type="submit"
            >
              <LogOut className="size-4" />
            </button>
          </form>
        </div>
      </header>

      <aside
        aria-hidden={commandOpen || undefined}
        className={cn(
          "luminous-frosted fixed inset-y-14 left-0 z-30 hidden border-r border-[var(--ls-line)] transition-[width] duration-200 md:flex md:flex-col md:gap-2 md:px-3 md:py-5",
          sidebarExpanded ? "w-56" : "w-16",
        )}
      >
        <Tooltip open={sidebarExpanded ? false : undefined}>
          <TooltipTrigger asChild>
            <button
              aria-controls="console-sidebar-navigation"
              aria-expanded={sidebarExpanded}
              aria-label={sidebarExpanded ? t("collapse") : t("expand")}
              className={cn(
                "luminous-focus flex h-10 items-center rounded-[11px] text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]",
                sidebarExpanded ? "justify-between px-3" : "justify-center",
              )}
              onClick={toggleSidebar}
              type="button"
            >
              {sidebarExpanded ? <span className="text-xs font-semibold uppercase tracking-wider">{t("navigation")}</span> : null}
              {sidebarExpanded ? <PanelLeftClose className="size-[18px]" /> : <PanelLeftOpen className="size-[18px]" />}
            </button>
          </TooltipTrigger>
          <TooltipContent side="right" sideOffset={12}>{t("expand")}</TooltipContent>
        </Tooltip>
        <nav aria-label="Workspace navigation" className="flex flex-col gap-2" id="console-sidebar-navigation">
          {railItems.map((item, index) => {
            const href = `/${org}/${item.key}`;
            const active = pathname.startsWith(href) || (item.key === "issues" && pathname.startsWith(`/${org}/provider-issues`));
            const Icon = item.icon;
            return (
              <Fragment key={item.key}>
                {sidebarExpanded && (index === 1 || index === 5) ? <p className="px-3 pb-1 pt-4 text-[10px] font-medium tracking-wide text-[var(--ls-text-tertiary)]">{index === 1 ? (language === "zh-CN" ? "工作区" : "Workspace") : (language === "zh-CN" ? "管理" : "Manage")}</p> : null}
              <Tooltip open={sidebarExpanded ? false : undefined}>
                <TooltipTrigger asChild>
                  <Link
                    aria-current={active ? "page" : undefined}
                    aria-label={t(item.label)}
                    className={cn(
                      "luminous-focus relative flex h-10 items-center rounded-[11px] transition",
                      sidebarExpanded ? "gap-3 px-3" : "justify-center",
                      active
                        ? "bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"
                        : "text-[var(--ls-text-tertiary)] hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]",
                    )}
                    href={href}
                  >
                    {active ? <span className="absolute -left-3 h-5 w-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}
                    <Icon className="size-[18px] shrink-0" />
                    {sidebarExpanded ? <span className="truncate text-sm font-medium">{t(item.label)}</span> : null}
                  </Link>
                </TooltipTrigger>
                <TooltipContent side="right" sideOffset={12}>{t(item.label)}</TooltipContent>
              </Tooltip>
              </Fragment>
            );
          })}
        </nav>
      </aside>

      <main
        aria-hidden={commandOpen || undefined}
        className={cn("pb-20 transition-[padding] duration-200 md:pb-0", sidebarExpanded ? "md:pl-56" : "md:pl-16")}
        id="main-content"
        tabIndex={-1}
      >
        <div className="mx-auto max-w-[1440px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
          {preview ? <PreviewModeNotice org={org} /> : null}
          {children}
        </div>
      </main>

      {commandOpen ? (
        <CommandPalette
          items={visibleCommandItems}
          language={language}
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
              {item.key === "reviews" ? t("review") : t(item.label)}
            </Link>
          );
        })}
      </nav>
    </div>
  );
}

function PreviewModeNotice({ org }: { org: string }) {
  const t = useUiText();
  return (
    <aside
      className="mb-5 flex flex-col gap-3 rounded-[14px] border border-amber-500/25 bg-amber-500/[0.055] px-4 py-3.5 text-sm text-[var(--ls-text-secondary)] shadow-[var(--ls-shadow-control)] sm:flex-row sm:items-center sm:justify-between"
      role="status"
    >
      <div className="flex min-w-0 items-start gap-3">
        <CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-warning-text)]" />
        <p className="leading-5">
          <span className="font-semibold text-[var(--ls-text)]">{t("preview")}</span>{" "}
          {t("previewDetail")}
        </p>
      </div>
      <Link
        className="luminous-focus inline-flex shrink-0 items-center justify-center rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-1.5 text-xs font-semibold text-[var(--ls-text)] transition hover:bg-[var(--ls-surface-muted)]"
        href={`/${encodeURIComponent(org)}/connect`}
      >
        {t("openConnections")}
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

function commandItemsFor(org: string, language: UiLanguage): CommandItem[] {
  const route = (path: string) => `/${encodeURIComponent(org)}${path}`;
  const label = (en: string, zh: string) => language === "zh-CN" ? zh : en;
  return [
    { label: label("Review cockpit", "审核概览"), description: label("Prioritize current review operations", "优先处理当前审核任务"), href: route("/home"), icon: CircleGauge },
    { label: label("Issues", "问题"), description: label("Open, regressed, critical, and assigned issue evidence", "待处理、复发、严重及分配给我的问题"), href: route("/issues"), icon: ListChecks },
    { label: label("Provider Issue triage", "Issue 分析"), description: label("AI analysis for user-authored GitHub and GitLab Issues", "分析用户创建的 GitHub 和 GitLab Issue"), href: route("/provider-issues"), icon: MessagesSquare },
    { label: label("Finding explorer", "发现项"), description: label("Cross-run finding feedback and trends", "跨运行查看发现项与反馈趋势"), href: route("/findings"), icon: Crosshair },
    { label: label("Pull requests", "合并请求"), description: label("Review decisions and revision-bound evidence", "审核结论和版本关联证据"), href: route("/reviews"), icon: GitPullRequest },
    { label: label("Work queue", "工作队列"), description: label("Running and needs-attention review runs", "执行中及需要人工处理的审核"), href: route("/tasks?tab=running"), icon: CircleGauge },
    { label: label("Agent work", "Agent 任务"), description: label("Issue-to-PR admission, classification, plans, and approvals", "Issue 到 PR 的准入、分类、计划与审批"), href: route("/agent-work"), icon: SquareTerminal },
    { label: label("CLI reviews", "CLI 审核"), description: label("Review admission and reproducible CLI evidence", "CLI 审核准入与可复现证据"), href: route("/cli-reviews"), icon: SquareTerminal },
    { label: label("Review commands", "审核命令"), description: label("Provider comment commands and Console shortcuts", "平台评论命令和控制台快捷操作"), href: route("/review-commands"), icon: SquareTerminal },
    { label: label("Review settings", "审核设置"), description: label("Scope, prompts, summaries, and lifecycle messages", "审核范围、提示词、摘要和状态消息"), href: route("/review-config/general"), icon: ShieldCheck },
    { label: label("Policy library", "策略库"), description: label("Published policies, drafts, and provenance", "已发布策略、草稿和来源"), href: route("/rules"), icon: ShieldCheck },
    { label: label("Policy approvals", "策略审批"), description: label("Independently decide immutable content changes", "独立审批不可变内容变更"), href: route("/rules/approvals"), icon: ShieldCheck },
    { label: label("Policy exceptions", "策略例外"), description: label("Time-bounded governed risk acceptance", "有期限的风险接受"), href: route("/rules/exceptions"), icon: ShieldCheck },
    { label: label("Connections", "连接"), description: label("Git provider installations and verification", "Git 平台安装与验证"), href: route("/connect"), icon: Settings2 },
    { label: label("Notifications", "通知"), description: label("Destinations, routes, and delivery receipts", "通知目标、路由和送达回执"), href: route("/notifications"), icon: Bell },
    { label: label("Audit", "审计"), description: label("Immutable workspace control-plane events", "工作空间不可变控制事件"), href: route("/audit"), icon: Settings2 },
    { label: label("Usage", "用量"), description: label("Reservation, settlement, and reconciliation", "预留、结算与对账"), href: route("/usage"), icon: CircleGauge },
    { label: label("Enterprise settings", "企业设置"), description: label("Members, SSO, models, keys, data, and health", "成员、单点登录、模型、密钥、数据及健康状态"), href: route("/settings/members"), icon: Building2 },
  ];
}

function CommandPalette({
  items,
  language,
  onClose,
  onQueryChange,
  query,
  workspaces,
}: {
  items: CommandItem[];
  language: UiLanguage;
  onClose: () => void;
  onQueryChange: (query: string) => void;
  query: string;
  workspaces: AccessibleWorkspace[];
}) {
  const zh = language === "zh-CN";
  const dialogRef = useRef<HTMLElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const workspaceItems = useMemo<CommandItem[]>(
    () =>
      workspaces.map((workspace) => ({
        description: `${workspace.slug} · ${workspace.role}`,
        href: `/${encodeURIComponent(workspace.slug)}/home`,
        icon: Building2,
        label: zh ? `切换到 ${workspace.name}` : `Switch to ${workspace.name}`,
      })),
    [workspaces, zh],
  );
  const workspaceActions = useMemo<CommandItem[]>(
    () => [
      {
        description: zh ? "查看可以访问的全部工作空间" : "See every workspace you can access",
        href: "/workspaces",
        icon: Building2,
        label: zh ? "全部工作空间" : "All workspaces",
      },
      {
        description: zh ? "创建独立的工作空间" : "Create a separate workspace boundary",
        href: "/workspaces/new",
        icon: Plus,
        label: zh ? "创建工作空间" : "Create workspace",
      },
    ],
    [zh],
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
        aria-label={zh ? "工作空间命令面板" : "Workspace command palette"}
        aria-modal="true"
        className="luminous-frosted mx-auto mt-[max(8vh,4rem)] w-full max-w-2xl overflow-hidden rounded-[20px] border border-[var(--ls-line-strong)] shadow-[var(--ls-shadow-float)]"
        onKeyDown={trapFocus}
        onMouseDown={(event) => event.stopPropagation()}
        ref={dialogRef}
        role="dialog"
      >
        <div className="flex items-center gap-3 border-b border-[var(--ls-line)] px-4 py-3">
          <Search className="size-4 shrink-0 text-[var(--ls-text-tertiary)]" />
          <label className="sr-only" htmlFor="workspace-command-search">{zh ? "搜索页面和工作空间" : "Search pages and workspaces"}</label>
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
            placeholder={zh ? "搜索页面和工作空间…" : "Search pages and workspaces…"}
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
                <CommandGroup label={zh ? "页面" : "Pages"}>
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
                <CommandGroup label={zh ? "工作空间" : "Workspaces"}>
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
                <CommandGroup label={zh ? "工作空间操作" : "Workspace actions"}>
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
              {zh ? "没有匹配的页面或工作空间。" : "No pages or workspaces match this search."}
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
