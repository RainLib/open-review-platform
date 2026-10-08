import { getUiLanguage } from "@/lib/ui-language-server";
import { workflowText } from "@/lib/workflow-copy";
import Link from "next/link";
import {
  Activity,
  BookOpen,
  CheckCheck,
  FlaskConical,
  GitFork,
  ShieldOff,
  Sparkles,
} from "lucide-react";

import { TabStateRouter } from "@/components/console/tab-state-router";
import { DataFreshness } from "@/components/console/page-state";
import { cn } from "@/lib/utils";

export type PolicySection =
  | "library"
  | "discovery"
  | "bindings"
  | "approvals"
  | "test-lab"
  | "exceptions"
  | "insights";

const sections = [
  { key: "library", label: "Library", path: "rules?tab=enabled", icon: BookOpen },
  { key: "discovery", label: "Discovery", path: "rules/discovery", icon: Sparkles },
  { key: "bindings", label: "Bindings", path: "rules/bindings", icon: GitFork },
  { key: "approvals", label: "Approvals", path: "rules/approvals", icon: CheckCheck },
  { key: "test-lab", label: "Test Lab", path: "rules/test-lab", icon: FlaskConical },
  { key: "exceptions", label: "Exceptions", path: "rules/exceptions", icon: ShieldOff },
  { key: "insights", label: "Insights", path: "rules/insights", icon: Activity },
] as const;

export async function PolicyPageHeader({
  active,
  description,
  eyebrow,
  org,
  source,
  title,
}: {
  active: PolicySection;
  description: string;
  eyebrow: string;
  org: string;
  source: string;
  title: string;
}) {
  const language = await getUiLanguage();
  const t = (source: string) => workflowText(language, source);
  return (
    <div className="space-y-6">
      <div className="flex flex-col justify-between gap-5 lg:flex-row lg:items-end">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">{t(eyebrow)}</p>
          <h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">{t(title)}</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">{t(description)}</p>
        </div>
        <DataFreshness language={language} state={source === "live" ? "live" : source === "demo" ? "demo" : "unavailable"} />
      </div>
      <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label={t("Policy governance sections")}>
        {sections.map((section) => {
          const Icon = section.icon;
          const selected = active === section.key;
          return (
            <Link
              aria-current={selected ? "page" : undefined}
              aria-selected={selected}
              className={cn(
                "luminous-focus relative inline-flex h-12 shrink-0 items-center gap-2 rounded-t-[10px] px-3.5 text-sm font-medium transition",
                selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]",
              )}
              href={`/${encodeURIComponent(org)}/${section.path}`}
              key={section.key}
              role="tab"
              tabIndex={selected ? 0 : -1}
            >
              <Icon className={cn("size-4", selected && "text-[var(--ls-accent)]")} />
              {t(section.label)}
              {selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}
            </Link>
          );
        })}
      </TabStateRouter>
    </div>
  );
}
