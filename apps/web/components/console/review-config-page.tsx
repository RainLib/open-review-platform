import {
  getIssueFormatTemplates,
  getReviewConfigData,
  getReviewConfigHistory,
  type IssueFormatTemplateData,
  type ReviewConfigSection,
} from "@/lib/control-api";

import { ReviewConfigEditor } from "./review-config-editor";

export async function ReviewConfigPage({
  org,
  section,
  scopeKind,
  scopeRef,
  scopeProvider = "",
  scopeAPIBaseURL = "",
}: {
  org: string;
  section: ReviewConfigSection;
  scopeKind: "tenant" | "repository";
  scopeRef: string;
  scopeProvider?: string;
  scopeAPIBaseURL?: string;
}) {
  const [data, historyData, formatData] = await Promise.all([
    getReviewConfigData(org, section, scopeKind, scopeRef, scopeProvider, scopeAPIBaseURL),
    getReviewConfigHistory(org, section, scopeKind, scopeRef, scopeProvider, scopeAPIBaseURL),
    section === "issue-triage"
      ? getIssueFormatTemplates(org)
      : Promise.resolve<IssueFormatTemplateData>({ source: "live", templates: [] }),
  ]);

  return (
    <ReviewConfigEditor
      config={data.config}
      detail={data.detail}
      history={historyData.history}
      historyDetail={historyData.detail}
      issueFormatDetail={formatData.detail}
      issueFormatSource={formatData.source}
      issueFormatTemplates={formatData.templates}
      key={`${section}:${scopeKind}:${scopeRef}:${scopeProvider}:${scopeAPIBaseURL}:${data.config?.content_sha256 ?? data.source}`}
      org={org}
      section={section}
      source={data.source}
    />
  );
}
