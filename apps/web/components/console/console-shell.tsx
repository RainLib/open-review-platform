"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Activity,
  BadgeCheck,
  Cable,
  ChevronDown,
  ClipboardList,
  Command,
  GitPullRequest,
  LayoutDashboard,
  ShieldCheck,
  Sparkles,
} from "lucide-react";

import { cn } from "@/lib/utils";

type NavigationItem = {
  href: string;
  label: string;
  icon: typeof LayoutDashboard;
};

const primaryNavigation = (org: string): NavigationItem[] => [
  { href: `/${org}/home`, label: "Overview", icon: LayoutDashboard },
  { href: `/${org}/reviews`, label: "Reviews", icon: GitPullRequest },
  { href: `/${org}/tasks`, label: "Work queue", icon: ClipboardList },
  { href: `/${org}/rules`, label: "Policy", icon: ShieldCheck },
];

const secondaryNavigation = (org: string): NavigationItem[] => [
  { href: `/${org}/connect`, label: "Connections", icon: Cable },
];

function NavigationGroup({
  label,
  items,
}: {
  label?: string;
  items: NavigationItem[];
}) {
  const pathname = usePathname();
  return (
    <div className="space-y-1">
      {label ? (
        <p className="px-3 pb-1 pt-5 text-[10px] font-semibold uppercase tracking-[0.16em] text-zinc-500">
          {label}
        </p>
      ) : null}
      {items.map((item) => {
        const active = pathname === item.href;
        const Icon = item.icon;
        return (
          <Link
            className={cn(
              "group flex items-center gap-3 rounded-xl px-3 py-2 text-sm transition-colors",
              active
                ? "bg-white/[0.08] text-white shadow-console-active"
                : "text-zinc-400 hover:bg-white/[0.045] hover:text-zinc-100",
            )}
            href={item.href}
            key={item.href}
          >
            <Icon
              className={cn(
                "size-4",
                active
                  ? "text-cyan-300"
                  : "text-zinc-500 group-hover:text-zinc-300",
              )}
            />
            {item.label}
          </Link>
        );
      })}
    </div>
  );
}

export function ConsoleShell({
  children,
  org,
}: {
  children: React.ReactNode;
  org: string;
}) {
  const title = org.charAt(0).toUpperCase() + org.slice(1);
  return (
    <div className="min-h-screen bg-console-canvas text-zinc-100">
      <aside className="fixed inset-y-0 hidden w-[244px] border-r border-white/[0.075] bg-console-rail px-3 py-4 lg:block">
        <Link
          className="mb-8 flex items-center gap-3 px-3"
          href={`/${org}/home`}
        >
          <span className="grid size-8 place-items-center rounded-[10px] bg-gradient-to-br from-cyan-300 to-indigo-500 text-console-on-accent shadow-lg shadow-cyan-500/10">
            <Command className="size-4" strokeWidth={2.5} />
          </span>
          <span className="text-sm font-semibold tracking-tight text-white">
            Open Review
          </span>
        </Link>
        <NavigationGroup items={primaryNavigation(org)} />
        <NavigationGroup
          label="Control plane"
          items={secondaryNavigation(org)}
        />
        <div className="absolute inset-x-3 bottom-4 rounded-2xl border border-white/[0.075] bg-white/[0.025] p-3">
          <div className="mb-2 flex items-center gap-2 text-xs font-medium text-zinc-200">
            <Sparkles className="size-3.5 text-cyan-300" />
            Evidence-first review
          </div>
          <p className="text-xs leading-5 text-zinc-500">
            Every decision retains its rule snapshot and exact revision.
          </p>
        </div>
      </aside>

      <main className="lg:pl-[244px]">
        <header className="sticky top-0 z-10 flex h-16 items-center justify-between border-b border-white/[0.075] bg-console-canvas/85 px-5 backdrop-blur-xl sm:px-8">
          <div className="flex items-center gap-2 text-sm">
            <span className="text-zinc-500">Workspace</span>
            <span className="text-zinc-700">/</span>
            <span className="font-medium text-zinc-200">{title}</span>
            <ChevronDown className="size-3.5 text-zinc-500" />
          </div>
          <div className="flex items-center gap-3">
            <span className="hidden items-center gap-2 text-xs text-zinc-500 sm:flex">
              <Activity className="size-3.5 text-emerald-300" />
              Control plane
            </span>
            <span className="grid size-8 place-items-center rounded-full border border-white/10 bg-gradient-to-br from-zinc-600 to-zinc-800 text-xs font-semibold text-zinc-200">
              RL
            </span>
          </div>
        </header>
        <div className="mx-auto max-w-[1500px] px-5 py-7 sm:px-8 lg:px-10">
          {children}
        </div>
      </main>
    </div>
  );
}

export function PageTitle({
  eyebrow,
  title,
  description,
}: {
  eyebrow?: string;
  title: string;
  description: string;
}) {
  return (
    <div className="mb-8">
      {eyebrow ? (
        <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.15em] text-cyan-300/80">
          {eyebrow}
        </p>
      ) : null}
      <h1 className="text-2xl font-semibold tracking-[-0.035em] text-white sm:text-3xl">
        {title}
      </h1>
      <p className="mt-2 max-w-2xl text-sm leading-6 text-zinc-400">
        {description}
      </p>
    </div>
  );
}

export function ImplementationNotice({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-3 rounded-2xl border border-cyan-300/10 bg-cyan-300/[0.045] px-4 py-3 text-sm text-zinc-300">
      <BadgeCheck className="mt-0.5 size-4 shrink-0 text-cyan-300" />
      <p className="leading-5">{children}</p>
    </div>
  );
}
