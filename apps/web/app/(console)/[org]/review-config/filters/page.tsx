import { ReviewConfigPage } from "@/components/console/review-config-page";

export default async function Page({ params, searchParams }: { params: Promise<{ org: string }>; searchParams: Promise<{ scope?: string; repository?: string; provider?: string; api_base_url?: string }> }) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const repository = query.repository?.trim() ?? "";
  return <ReviewConfigPage org={org} scopeKind={query.scope === "repository" && repository ? "repository" : "tenant"} scopeRef={repository} scopeProvider={query.provider?.trim()} scopeAPIBaseURL={query.api_base_url?.trim()} section="filters" />;
}
