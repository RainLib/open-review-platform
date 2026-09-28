import Link from "next/link";
import { BellRing, GitBranch, PlugZap, Send } from "lucide-react";

import { TabStateRouter } from "@/components/console/tab-state-router";
import { DataFreshness } from "@/components/console/page-state";
import type { DataSource } from "@/lib/control-api";
import { cn } from "@/lib/utils";

export type OperateSection = "connections" | "destinations" | "routing" | "deliveries";
const sections = [
  { key: "connections", label: "Connections", href: "connect?tab=installations", icon: PlugZap },
  { key: "destinations", label: "Destinations", href: "notifications?tab=destinations", icon: BellRing },
  { key: "routing", label: "Event routing", href: "notifications?tab=routing", icon: GitBranch },
  { key: "deliveries", label: "Delivery logs", href: "notifications?tab=deliveries", icon: Send },
] as const;

export function OperatePageHeader({ active, description, detail, eyebrow, org, source, title }: { active: OperateSection; description: string; detail?: string; eyebrow: string; org: string; source: DataSource; title: string }) {
  return <div className="min-w-0 space-y-6"><div className="flex min-w-0 flex-col justify-between gap-5 lg:flex-row lg:items-end"><div className="min-w-0"><p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">{eyebrow}</p><h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">{title}</h1><p className="mt-2 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">{description}</p></div><DataFreshness detail={detail} state={source} /></div><TabStateRouter className="flex w-full min-w-0 max-w-full gap-1 overflow-x-auto overscroll-x-contain border-b border-[var(--ls-line)]" label="Operate sections">{sections.map((section) => { const Icon = section.icon; const selected = active === section.key; return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative inline-flex h-12 shrink-0 items-center gap-2 rounded-t-[10px] px-3.5 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={`/${org}/${section.href}`} key={section.key} role="tab" tabIndex={selected ? 0 : -1}><Icon className={cn("size-4", selected && "text-[var(--ls-accent)]")} />{section.label}{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>; })}</TabStateRouter></div>;
}
