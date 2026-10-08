import { getUiLanguage } from "@/lib/ui-language-server";
import { workflowText } from "@/lib/workflow-copy";
import { Activity } from "lucide-react";
import { PolicyPageHeader } from "@/components/console/policy-page-header";
import { FindingFeedbackDashboardView } from "@/components/console/finding-feedback-dashboard";
import { getFindingFeedbackDashboard } from "@/lib/control-api";

export default async function RuleInsightsPage({ params }: { params: Promise<{ org: string }> }) {
  const language = await getUiLanguage();
  const t = (source: string) => workflowText(language, source);
  const { org } = await params;
  const data = await getFindingFeedbackDashboard(org);
  return <div className="space-y-7"><PolicyPageHeader active="insights" description={t("Measure useful and false-positive signals from real review conversations while preserving repository and immutable rule-snapshot attribution.")} eyebrow={t("Measured quality")} org={org} source={data.source} title={t("Finding feedback")} /><div className="flex items-start gap-3 rounded-[14px] border border-violet-500/20 bg-violet-500/[0.07] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]"><Activity className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />{t("Feedback is improvement evidence only. It never edits a published rule or changes a completed merge decision automatically.")}</div><FindingFeedbackDashboardView data={data} enabled={data.source === "live"} org={org} /></div>;
}
