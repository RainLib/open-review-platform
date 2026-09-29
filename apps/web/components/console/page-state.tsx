import Link from "next/link";
import type { ComponentType, ReactNode } from "react";
import {
  Ban,
  CircleAlert,
  Clock3,
  Database,
  FilterX,
  LoaderCircle,
  RefreshCw,
  ShieldAlert,
} from "lucide-react";

import { cn } from "@/lib/utils";
import type { UiLanguage } from "@/lib/ui-language";

export type PageStateKind =
  | "loading"
  | "first-use-empty"
  | "filtered-empty"
  | "partial"
  | "permission"
  | "stale"
  | "superseded"
  | "unavailable";

type StatePresentation = {
  icon: ComponentType<{ className?: string }>;
  iconClassName: string;
  surfaceClassName: string;
};

const presentation: Record<PageStateKind, StatePresentation> = {
  loading: {
    icon: LoaderCircle,
    iconClassName: "animate-spin text-[var(--ls-accent)]",
    surfaceClassName: "border-[var(--ls-line)] bg-[var(--ls-surface)]",
  },
  "first-use-empty": {
    icon: Database,
    iconClassName: "text-[var(--ls-accent)]",
    surfaceClassName: "border-dashed border-[var(--ls-line-strong)] bg-[var(--ls-surface)]",
  },
  "filtered-empty": {
    icon: FilterX,
    iconClassName: "text-[var(--ls-text-tertiary)]",
    surfaceClassName: "border-dashed border-[var(--ls-line-strong)] bg-[var(--ls-surface)]",
  },
  partial: {
    icon: CircleAlert,
    iconClassName: "text-[var(--ls-warning-text)]",
    surfaceClassName: "border-amber-500/25 bg-amber-500/[0.045]",
  },
  permission: {
    icon: ShieldAlert,
    iconClassName: "text-[var(--ls-warning-text)]",
    surfaceClassName: "border-amber-500/25 bg-amber-500/[0.045]",
  },
  stale: {
    icon: Clock3,
    iconClassName: "text-[var(--ls-warning-text)]",
    surfaceClassName: "border-amber-500/25 bg-amber-500/[0.045]",
  },
  superseded: {
    icon: RefreshCw,
    iconClassName: "text-[var(--ls-text-secondary)]",
    surfaceClassName: "border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)]",
  },
  unavailable: {
    icon: Ban,
    iconClassName: "text-[var(--ls-critical-text)]",
    surfaceClassName: "border-red-500/20 bg-red-500/[0.04]",
  },
};

export function PageState({
  action,
  children,
  className,
  detail,
  kind,
  title,
}: {
  action?: ReactNode;
  children?: ReactNode;
  className?: string;
  detail: ReactNode;
  kind: PageStateKind;
  title: string;
}) {
  const state = presentation[kind];
  const Icon = state.icon;
  const liveMessage = kind === "loading" ? "Loading" : undefined;

  return (
    <section
      aria-busy={kind === "loading" || undefined}
      aria-live={liveMessage ? "polite" : undefined}
      className={cn(
        "grid min-h-56 place-items-center rounded-[18px] border p-8 text-center shadow-[var(--ls-shadow-control)]",
        state.surfaceClassName,
        className,
      )}
    >
      <div className="max-w-xl">
        <Icon aria-hidden="true" className={cn("mx-auto size-6", state.iconClassName)} />
        <h2 className="mt-3 text-base font-semibold text-[var(--ls-text)]">{title}</h2>
        <div className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">{detail}</div>
        {children ? <div className="mt-4">{children}</div> : null}
        {action ? <div className="mt-5 flex flex-wrap justify-center gap-2">{action}</div> : null}
      </div>
    </section>
  );
}

export function RecoveryAction({
  children,
  href,
  variant = "secondary",
}: {
  children: ReactNode;
  href: string;
  variant?: "primary" | "secondary";
}) {
  return (
    <Link
      className={cn(
        "luminous-focus inline-flex h-9 items-center justify-center rounded-[9px] px-3.5 text-xs font-semibold transition",
        variant === "primary"
          ? "bg-[var(--ls-accent)] text-white hover:bg-[var(--ls-accent-hover)]"
          : "border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]",
      )}
      href={href}
    >
      {children}
    </Link>
  );
}

export function DataFreshness({
  detail,
  language = "en",
  state,
}: {
  detail?: string;
  language?: UiLanguage;
  state: "live" | "demo" | "partial" | "stale" | "unavailable" | "unconfigured";
}) {
  const label = language === "zh-CN"
    ? ({ live: "控制面实时数据", demo: "只读预览", partial: "控制面数据不完整", stale: "证据已过期", unconfigured: "控制面未配置", unavailable: "控制面不可用" } as const)[state]
    : state === "live"
    ? "Live control-plane data"
    : state === "demo"
      ? "Read-only preview"
      : state === "partial"
        ? "Partial control-plane data"
      : state === "stale"
          ? "Stale evidence"
          : state === "unconfigured"
            ? "Control plane not configured"
          : "Control plane unavailable";
  const tone = state === "live"
    ? "bg-emerald-500/10 text-[var(--ls-success-text)]"
    : state === "demo"
      ? "bg-sky-500/10 text-[var(--ls-accent)]"
    : state === "unavailable"
      ? "bg-red-500/10 text-[var(--ls-critical-text)]"
      : "bg-amber-500/10 text-[var(--ls-warning-text)]";

  return (
    <span className="inline-flex max-w-full flex-wrap items-center gap-x-2 gap-y-1" title={detail}>
      <span className={cn("inline-flex shrink-0 items-center gap-2 rounded-full px-3 py-1.5 text-xs", tone)}>
        <span className="size-1.5 rounded-full bg-current" />
        {label}
      </span>
      {detail ? (
        <span className="basis-full text-xs leading-5 text-[var(--ls-text-tertiary)] sm:basis-auto sm:max-w-80 sm:text-right">
          {detail}
        </span>
      ) : null}
    </span>
  );
}
