import { getUiLanguage } from "@/lib/ui-language-server";
import { workflowStatus, workflowText } from "@/lib/workflow-copy";
import Link from "next/link";
import { ArrowLeft, BookOpen, Fingerprint, GitFork, History, SquareTerminal } from "lucide-react";
import { notFound } from "next/navigation";

import { RuleSetGovernanceActions } from "@/components/console/rule-set-governance-actions";
import { CopyEvidenceButton } from "@/components/console/copy-evidence-button";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { getConsoleData } from "@/lib/control-api";
import { formatTime } from "@/lib/format";
import { cn } from "@/lib/utils";

type DetailTab = "overview" | "version" | "provenance";
const tabs: { key: DetailTab; label: string }[] = [
  { key: "overview", label: "Overview" },
  { key: "version", label: "Version" },
  { key: "provenance", label: "Provenance" },
];

export default async function RuleDetailPage({ params, searchParams }: { params: Promise<{ org: string; ruleId: string }>; searchParams: Promise<{ tab?: string }> }) {
  const language = await getUiLanguage();
  const t = (source: string) => workflowText(language, source);
  const [{ org, ruleId }, query] = await Promise.all([params, searchParams]);
  const data = await getConsoleData(org);
  const ruleSet = data.ruleSets.find((item) => item.id === ruleId);
  if (!ruleSet) notFound();
  const tab = tabs.some((item) => item.key === query.tab) ? (query.tab as DetailTab) : "overview";
  const version = ruleSet.latest_version;

  return <div className="space-y-7">
    <Link className="luminous-focus inline-flex items-center gap-1.5 rounded text-sm font-medium text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]" href={`/${encodeURIComponent(org)}/rules?tab=enabled`}><ArrowLeft className="size-4" />{t("Rule library")}</Link>
    <div className="flex flex-col justify-between gap-5 lg:flex-row lg:items-end"><div><p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">{t("Immutable policy")}</p><h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">{ruleSet.name}</h1><p className="mt-2 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">{ruleSet.description || t("No description was provided for this rule set.")}</p></div><span className="inline-flex w-fit rounded-full bg-[var(--ls-surface-muted)] px-3 py-1.5 text-xs capitalize text-[var(--ls-text-secondary)]">{version ? workflowStatus(language, version.state) : t("No version")}</span></div>
    <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label={t("Rule details")}>{tabs.map((item) => <Link aria-current={item.key === tab ? "page" : undefined} aria-selected={item.key === tab} className={cn("luminous-focus relative h-11 shrink-0 rounded-t-[10px] px-4 py-3 text-sm font-medium", item.key === tab ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={`/${encodeURIComponent(org)}/rules/${encodeURIComponent(ruleId)}?tab=${item.key}`} key={item.key} role="tab" tabIndex={item.key === tab ? 0 : -1}>{t(item.label)}{item.key === tab ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>)}</TabStateRouter>
    {tab === "overview" ? <div className="grid gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(280px,.65fr)]"><section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-6 shadow-[var(--ls-shadow-control)]"><div className="flex items-center gap-3"><span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><BookOpen className="size-4" /></span><div><h2 className="text-base font-semibold text-[var(--ls-text)]">{t("Policy purpose")}</h2><p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">{t("Created by ")}{ruleSet.created_by}</p></div></div><p className="mt-5 text-sm leading-7 text-[var(--ls-text-secondary)]">{ruleSet.description || t("The policy owner has not recorded a purpose statement.")}</p><RuleSetGovernanceActions enabled={data.source === "live"} org={org} ruleSet={ruleSet} />{data.source === "live" && version?.state === "published" ? <ManualRuleCommand ruleSetID={ruleSet.id} /> : null}</section><aside className="space-y-4"><DetailCard icon={GitFork} label={t("Lifecycle")} value={version ? workflowStatus(language, version.state) : t("No version")} /><DetailCard icon={History} label={t("Latest update")} value={formatTime(ruleSet.updated_at, language)} /><DetailCard icon={Fingerprint} label={t("Content identity")} value={version ? version.content_sha256.slice(0, 16) : "Unavailable"} /></aside></div> : tab === "version" ? <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-6 shadow-[var(--ls-shadow-control)]"><h2 className="text-base font-semibold text-[var(--ls-text)]">{t("Latest immutable version")}</h2>{version ? <dl className="mt-5 grid gap-5 sm:grid-cols-2"><Fact label={t("Version")} value={`v${version.version}`} /><Fact label={t("Revision")} value={String(version.revision)} /><Fact label={t("State")} value={workflowStatus(language, version.state)} /><Fact label={t("Created by")} value={version.created_by} /><Fact label={t("Created")} value={formatTime(version.created_at, language)} /><Fact label={t("Updated")} value={formatTime(version.updated_at, language)} /><div className="sm:col-span-2"><Fact label={t("Content SHA-256")} value={version.content_sha256} mono /></div></dl> : <p className="mt-4 text-sm text-[var(--ls-text-secondary)]">{t("No immutable version is available.")}</p>}</section> : <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-6 shadow-[var(--ls-shadow-control)]"><h2 className="text-base font-semibold text-[var(--ls-text)]">{t("Provenance boundary")}</h2><p className="mt-2 max-w-2xl text-sm leading-6 text-[var(--ls-text-secondary)]">{t("This view reports only identities returned by the control plane. Test evidence and bindings remain in their dedicated governed views.")}</p><dl className="mt-5 grid gap-5 sm:grid-cols-2"><Fact label={t("Rule set ID")} value={ruleSet.id} mono /><Fact label={t("Rule version ID")} value={version?.id ?? "Unavailable"} mono /><Fact label={t("Rule-set author")} value={ruleSet.created_by} /><Fact label={t("Version author")} value={version?.created_by ?? "Unavailable"} /></dl></section>}
  </div>;
}

function DetailCard({ icon: Icon, label, value }: { icon: typeof BookOpen; label: string; value: string }) { return <div className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><Icon className="size-4 text-[var(--ls-accent)]" /><p className="mt-3 text-xs text-[var(--ls-text-tertiary)]">{label}</p><p className="mt-1 break-all text-sm font-medium capitalize text-[var(--ls-text)]">{value}</p></div>; }
async function ManualRuleCommand({ ruleSetID }: { ruleSetID: string }) {
  const language = await getUiLanguage();
  const t = (source: string) => workflowText(language, source); const command = `@openreview review --rule=${ruleSetID}`; return <section className="mt-5 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3.5"><div className="flex flex-wrap items-center justify-between gap-2"><p className="flex items-center gap-1.5 text-xs font-medium text-[var(--ls-text)]"><SquareTerminal className="size-3.5 text-[var(--ls-accent)]" /> {t(" Manual review command")}</p><CopyEvidenceButton label={t("Copy command")} value={command} /></div><code className="mt-2 block overflow-x-auto whitespace-nowrap rounded-[8px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-2.5 py-2 font-mono text-[11px] text-[var(--ls-text)]">{command}</code><p className="mt-2 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">{t("This only runs when the current repository and target branch have an active binding for this published rule set.")}</p></section>; }
function Fact({ label, mono, value }: { label: string; mono?: boolean; value: string }) { return <div><dt className="text-xs font-medium text-[var(--ls-text-tertiary)]">{label}</dt><dd className={cn("mt-1 break-all text-sm capitalize text-[var(--ls-text)]", mono && "font-mono text-xs normal-case")}>{value}</dd></div>; }
