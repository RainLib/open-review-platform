import { ShieldOff } from "lucide-react";

import { PolicyPageHeader } from "@/components/console/policy-page-header";
import { RuleExceptionManager } from "@/components/console/rule-exception-manager";
import { getIssueDetailData, getRuleExceptionData } from "@/lib/control-api";

export default async function RuleExceptionsPage({ params, searchParams }: { params: Promise<{ org: string }>; searchParams: Promise<{ issue?: string }> }) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const [data, issueData] = await Promise.all([
    getRuleExceptionData(org),
    query.issue ? getIssueDetailData(org, query.issue) : Promise.resolve(undefined),
  ]);
  const issue = issueData?.issue;
  const sourceIssue = issue && issue.api_base_url && issue.can_manage && (issue.status === "open" || issue.status === "regressed")
    ? { id: issue.id, revision: issue.revision, provider: issue.provider, apiBaseURL: issue.api_base_url, repository: issue.repository, bodyPreview: issue.body_preview }
    : undefined;
  return <div className="space-y-7">
    <PolicyPageHeader active="exceptions" description="Request a time-bounded exception to one exact published rule key, require an independent decision, and retain every applied exception in future review snapshots." eyebrow="Risk acceptance" org={org} source={data.source} title="Policy exceptions" />
    <div className="flex items-start gap-3 rounded-[14px] border border-amber-500/20 bg-amber-500/[0.08] px-4 py-3 text-xs leading-5 text-[var(--ls-warning-text)]"><ShieldOff className="mt-0.5 size-4 shrink-0" />Exceptions never edit a published policy. Approval affects only future admissions; expiry and revocation stop new use while historical snapshots remain unchanged. The expiry reconciler automatically restores a linked suppressed Issue and records its timeline.</div>
    {query.issue && !sourceIssue ? <div className="rounded-[14px] border border-red-500/20 bg-red-500/[0.08] px-4 py-3 text-xs text-[var(--ls-critical-text)]">The source issue is unavailable, already resolved or suppressed, or your role cannot request a governed exception.</div> : null}
    <RuleExceptionManager enabled={data.source === "live"} exceptions={data.exceptions} org={org} ruleSets={data.ruleSets} sourceIssue={sourceIssue} />
  </div>;
}
