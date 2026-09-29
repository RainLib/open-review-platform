import { Fingerprint } from "lucide-react";
import { PolicyPageHeader } from "@/components/console/policy-page-header";
import { RuleApprovalManager } from "@/components/console/rule-approval-manager";
import { getRuleApprovalData } from "@/lib/control-api";

export default async function RuleApprovalsPage({ params }: { params: Promise<{ org: string }> }) {
  const { org } = await params;
  const data = await getRuleApprovalData(org);
  return <div className="space-y-7"><PolicyPageHeader active="approvals" description="Decide one immutable content hash. Requesters cannot approve their own change, and publication stays locked until quorum is met." eyebrow="Independent governance" org={org} source={data.source} title="Approval queue" /><div className="flex items-start gap-3 rounded-[14px] border border-violet-500/20 bg-violet-500/[0.07] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]"><Fingerprint className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />Every decision is bound to the displayed SHA-256. A changed payload requires a new version and approval request.</div><RuleApprovalManager approvals={data.approvals} enabled={data.source === "live"} org={org} /></div>;
}
