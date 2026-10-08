import { getUiLanguage } from "@/lib/ui-language-server";
import { workflowText } from "@/lib/workflow-copy";
import { ApprovalPolicyManager } from "@/components/console/approval-policy-manager";
import { EnterpriseSettingsTabs } from "@/components/console/enterprise-settings-tabs";
import { DataFreshness, PageState } from "@/components/console/page-state";
import { getWorkspaceApprovalPolicyData } from "@/lib/control-api";

export default async function ApprovalsPage({ params }: { params: Promise<{ org: string }> }) {
  const language = await getUiLanguage();
  const t = (source: string) => workflowText(language, source);
  const { org } = await params;
  const data = await getWorkspaceApprovalPolicyData(org);
  return <div className="space-y-7">
    <header><h1 className="text-[32px] font-semibold">{t("Approval settings")}</h1><p className="mt-2 text-sm text-[var(--ls-text-secondary)]">{t("Workspace approval rules require a different approver by default.")}</p><div className="mt-3"><DataFreshness language={language} detail={data.detail} state={data.source} /></div></header>
    <EnterpriseSettingsTabs active="approvals" org={org} />
    {data.policy ? <ApprovalPolicyManager initialPolicy={data.policy} key={data.policy.revision} org={org} /> : <PageState kind="unavailable" title={t("Approval settings unavailable")} detail={data.detail || t("Sign in and check the service connection, then reload.")} />}
  </div>;
}
