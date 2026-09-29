import Link from "next/link";
import type { ReactNode } from "react";
import { ArrowUpRight, Command, GitPullRequest, ShieldCheck } from "lucide-react";

import { cn } from "@/lib/utils";

export function ProductMark({
  compact = false,
  variant = "dark",
}: {
  compact?: boolean;
  variant?: "dark" | "luminous";
}) {
  return (
    <Link className="inline-flex items-center gap-2.5" href="/">
      <span
        className={cn(
          "grid size-8 place-items-center rounded-[10px] text-white",
          variant === "luminous"
            ? "bg-[var(--ls-accent)] shadow-[var(--ls-shadow-control)]"
            : "bg-gradient-to-br from-cyan-200 via-teal-300 to-blue-500 text-slate-950 shadow-lg shadow-cyan-400/15",
        )}
      >
        <Command className="size-4" strokeWidth={2.7} />
      </span>
      {!compact ? (
        <span
          className={cn(
            "text-sm font-semibold tracking-[-0.03em]",
            variant === "luminous" ? "text-[var(--ls-text)]" : "text-white",
          )}
        >
          Open Review
        </span>
      ) : null}
    </Link>
  );
}

export function MarketingHeader({ className }: { className?: string }) {
  return (
    <header className={cn("relative z-20 mx-auto flex max-w-7xl items-center justify-between px-5 py-5 sm:px-8 lg:px-10", className)}>
      <ProductMark />
      <nav className="hidden items-center gap-6 text-sm text-slate-400 md:flex" aria-label="Primary navigation">
        <a className="transition hover:text-white" href="#product">
          Product
        </a>
        <a className="transition hover:text-white" href="#providers">
          Integrations
        </a>
        <a className="transition hover:text-white" href="#self-host">
          Self-host
        </a>
      </nav>
      <div className="flex items-center gap-2.5">
        <Link className="hidden rounded-xl px-3 py-2 text-sm font-medium text-slate-300 transition hover:bg-white/[0.06] hover:text-white sm:inline-flex" href="/sign-in">
          Sign in
        </Link>
        <Link className="inline-flex items-center gap-2 rounded-xl bg-white px-3.5 py-2 text-sm font-semibold text-slate-950 shadow-lg shadow-cyan-400/10 transition hover:-translate-y-0.5 hover:bg-cyan-100" href="/sign-in?next=%2Fsetup">
          Start setup <ArrowUpRight className="size-3.5" />
        </Link>
      </div>
    </header>
  );
}

export function ProviderBadge({
  icon,
  label,
  tone = "default",
}: {
  icon?: ReactNode;
  label: string;
  tone?: "default" | "private";
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-xs font-medium",
        tone === "private"
          ? "border-amber-200/15 bg-amber-100/[0.045] text-amber-100"
          : "border-white/10 bg-white/[0.035] text-slate-300",
      )}
    >
      {icon ?? <GitPullRequest className="size-3.5" />}
      {label}
    </span>
  );
}

export function SecurityCallout({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-start gap-3 rounded-2xl border border-cyan-200/10 bg-cyan-200/[0.045] px-4 py-3 text-sm leading-6 text-slate-300">
      <ShieldCheck className="mt-0.5 size-4 shrink-0 text-cyan-200" />
      <p>{children}</p>
    </div>
  );
}
