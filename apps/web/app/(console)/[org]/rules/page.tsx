import { getUiLanguage } from "@/lib/ui-language-server";
import { workflowStatus, workflowText } from "@/lib/workflow-copy";
import Link from "next/link";
import { ArrowRight, BookOpen, FileCheck2, Plus } from "lucide-react";

import { RuleCatalogBrowser } from "@/components/console/rule-catalog-browser";
import { PolicyPageHeader } from "@/components/console/policy-page-header";
import { RuleSetComposer } from "@/components/console/rule-set-composer";
import { RuleSetGovernanceActions } from "@/components/console/rule-set-governance-actions";
import { PageState, RecoveryAction } from "@/components/console/page-state";
import { getConsoleData, getRuleCatalog, type RuleSet } from "@/lib/control-api";
import { formatTime } from "@/lib/format";
import { cn } from "@/lib/utils";

type LibraryTab = "enabled" | "recommended" | "drafts" | "archived";

const libraryTabs: { key: LibraryTab; label: string }[] = [
  { key: "enabled", label: "Enabled" },
  { key: "recommended", label: "Recommended" },
  { key: "drafts", label: "Drafts" },
  { key: "archived", label: "Archived" },
];

function rulesForTab(ruleSets: RuleSet[], tab: LibraryTab) {
  if (tab === "enabled") return ruleSets.filter((set) => set.latest_version?.state === "published");
  if (tab === "drafts") return ruleSets.filter((set) => set.latest_version && ["draft", "in_review", "approved"].includes(set.latest_version.state));
  if (tab === "archived") return ruleSets.filter((set) => set.latest_version?.state === "retired");
  return [];
}

export default async function RulesPage({ params, searchParams }: { params: Promise<{ org: string }>; searchParams: Promise<{ tab?: string }> }) {
  const language = await getUiLanguage();
  const t = (source: string) => workflowText(language, source);
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const [data, catalog] = await Promise.all([getConsoleData(org), getRuleCatalog(org)]);
  const tab = libraryTabs.some((item) => item.key === query.tab) ? (query.tab as LibraryTab) : "enabled";
  const visible = rulesForTab(data.ruleSets, tab);
  const rulesAvailable = data.source === "live" || data.source === "demo";
  const rulesWritable = data.source === "live";

  return <div className="space-y-7">
    <PolicyPageHeader active="library" description={t("Create, review, publish, and retire immutable review policies without losing the exact version used by any historical run.")} eyebrow={t("Policy governance")} org={org} source={data.source} title={t("Rule library")} />
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="inline-flex min-w-0 max-w-full w-fit overflow-x-auto rounded-[11px] bg-[var(--ls-surface-muted)] p-1">
        {libraryTabs.map((item) => <Link aria-current={item.key === tab ? "page" : undefined} className={cn("luminous-focus shrink-0 rounded-[8px] px-3 py-2 text-xs font-medium transition", item.key === tab ? "bg-[var(--ls-surface)] text-[var(--ls-text)] shadow-[var(--ls-shadow-control)]" : "text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]")} href={`/${encodeURIComponent(org)}/rules?tab=${item.key}`} key={item.key}>{t(item.label)}</Link>)}
      </div>
      {rulesWritable ? <a className="luminous-focus inline-flex h-10 w-fit items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-medium text-white hover:bg-[var(--ls-accent-hover)]" href="#create-rule"><Plus className="size-4" />{t("Create rule")}</a> : null}
    </div>
    {!rulesAvailable ? <PageState action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect`} variant="primary">{t("Check connections")}</RecoveryAction>} detail={data.detail ?? t("The rule library could not be loaded. No policy absence or writable draft is inferred from this state.")} kind="unavailable" title={t("Rule library unavailable")} /> : <>
      {tab === "recommended" ? <RuleCatalogBrowser catalog={catalog} org={org} /> : visible.length === 0 ? <PageState action={tab === "archived" || !rulesWritable ? undefined : <RecoveryAction href="#create-rule" variant="primary">{t("Create draft")}</RecoveryAction>} detail={tab === "archived" ? t("Retired policies remain visible here for provenance when they exist.") : t("Create a governed draft below. It remains non-executable until independently approved and published.")} kind="first-use-empty" title={workflowText(language, "No {tab} policies", { tab: t(libraryTabs.find(item => item.key === tab)?.label ?? tab) })} /> : <div className="grid gap-4 lg:grid-cols-2">{visible.map((set) => <article className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]" key={set.id}><div className="flex items-start justify-between gap-3"><span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><BookOpen className="size-4" /></span><span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-[11px] font-medium capitalize text-[var(--ls-text-secondary)]">{set.latest_version ? workflowStatus(language, set.latest_version.state) : ""}</span></div><Link className="luminous-focus mt-5 inline-flex items-center gap-2 rounded text-base font-semibold text-[var(--ls-text)] hover:text-[var(--ls-accent)]" href={`/${encodeURIComponent(org)}/rules/${encodeURIComponent(set.id)}?tab=overview`}>{set.name}<ArrowRight className="size-4" /></Link><p className="mt-2 min-h-12 text-sm leading-6 text-[var(--ls-text-secondary)]">{set.description || t("No description was provided for this rule set.")}</p><div className="mt-5 flex items-center justify-between border-t border-[var(--ls-line)] pt-3 text-xs text-[var(--ls-text-tertiary)]"><span className="flex items-center gap-1.5"><FileCheck2 className="size-3.5" />{t("Updated ")}{formatTime(set.updated_at, language)}</span><span className="font-mono">{set.id.slice(0, 8)}</span></div><RuleSetGovernanceActions enabled={rulesWritable} org={org} ruleSet={set} /></article>)}</div>}
      <div><RuleSetComposer enabled={data.source === "live"} org={org} /></div>
    </>}
  </div>;
}
