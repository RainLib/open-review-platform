import { notFound, redirect } from "next/navigation";

import { getProviderIssueAnalysisData } from "@/lib/control-api";

export default async function ProviderIssueAnalysisPage({
  params,
}: {
  params: Promise<{ analysisId: string; org: string }>;
}) {
  const { analysisId, org } = await params;
  const data = await getProviderIssueAnalysisData(org, { selected: analysisId });

  if (!data.selected) {
    notFound();
  }

  redirect(
    `/${encodeURIComponent(org)}/provider-issues?selected=${encodeURIComponent(data.selected.id)}`,
  );
}
