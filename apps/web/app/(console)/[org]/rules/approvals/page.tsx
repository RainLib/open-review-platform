import { getUiLanguage } from "@/lib/ui-language-server";
import { workflowText } from "@/lib/workflow-copy";
import { Fingerprint } from "lucide-react";
import { PolicyPageHeader } from "@/components/console/policy-page-header";
import { RuleApprovalManager } from "@/components/console/rule-approval-manager";
import { getRuleApprovalData } from "@/lib/control-api";

export default async function RuleApprovalsPage({ params }: { params: Promise<{ org: string }> }) {
  const language = await getUiLanguage();
  const t = (source: string) => workflowText(language, source);
  const { org } = await params;
  const data = await getRuleApprovalData(org);
  return <div className="space-y-7"><PolicyPageHeader active="approvals" description={t("Decide one immutable content hash. Requester decisions follow workspace self-approval settings; publication stays locked until quorum is met.")} eyebrow={t("Approval governance")} org={org} source={data.source} title={t("Approval queue")} /><div className="flex items-start gap-3 rounded-[14px] border border-violet-500/20 bg-violet-500/[0.07] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]"><Fingerprint className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />{t("Every decision is bound to the displayed SHA-256. A changed payload requires a new version and approval request.")}</div><RuleApprovalManager approvals={data.approvals} enabled={data.source === "live"} org={org} /></div>;
}
