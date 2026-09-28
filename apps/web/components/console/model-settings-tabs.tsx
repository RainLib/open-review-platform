import Link from "next/link";

import { TabStateRouter } from "@/components/console/tab-state-router";
import { cn } from "@/lib/utils";
import { modelScopeQuery, type ModelScope } from "@/lib/model-scope";

const tabs = [
  ["routes", "Routes"],
  ["credentials", "Credential references"],
  ["budgets", "Budgets"],
  ["history", "History"],
] as const;

export type ModelSettingsTab = (typeof tabs)[number][0];

export function ModelSettingsTabs({ active, org, scope }: { active: ModelSettingsTab; org: string; scope: ModelScope }) {
  const query = modelScopeQuery(scope);
  query.set("tab", active);

  return <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="Model governance">
    {tabs.map(([tab, label]) => {
      const selected = active === tab;
      const href = new URLSearchParams(query);
      href.set("tab", tab);
      return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative h-10 shrink-0 px-4 pt-2.5 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)]")} href={`/${org}/settings/models?${href.toString()}`} key={tab} role="tab" tabIndex={selected ? 0 : -1}>{label}{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 bg-[var(--ls-accent)]" /> : null}</Link>;
    })}
  </TabStateRouter>;
}
