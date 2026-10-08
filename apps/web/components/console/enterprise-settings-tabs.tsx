"use client";

import { useWorkflowText } from "@/components/console/ui-language-context";
import Link from "next/link";

import { cn } from "@/lib/utils";

const tabs = [
  ["members", "Members"],
  ["approvals", "Approvals"],
  ["sso", "SSO"],
  ["models", "Models & BYOK"],
  ["api-keys", "API & CLI keys"],
  ["data", "Data governance"],
  ["health", "Platform health"],
] as const;

export type EnterpriseSettingsTab = (typeof tabs)[number][0];

export function EnterpriseSettingsTabs({ active, org }: { active: EnterpriseSettingsTab; org: string }) {
  const t = useWorkflowText();
  return (
    <nav aria-label={t("Enterprise settings")} className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]">
      {tabs.map(([path, label]) => {
        const selected = path === active;
        return (
          <Link
            aria-current={selected ? "page" : undefined}
            className={cn(
              "luminous-focus relative h-11 shrink-0 px-4 pt-3 text-sm font-medium",
              selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)]",
            )}
            href={`/${org}/settings/${path}`}
            key={path}
          >
            {t(label)}
            {selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 bg-[var(--ls-accent)]" /> : null}
          </Link>
        );
      })}
    </nav>
  );
}
