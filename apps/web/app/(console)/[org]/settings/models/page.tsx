import { ModelRouteEditor } from "@/components/console/model-route-editor";
import { ModelRouteGovernanceView } from "@/components/console/model-route-governance-view";
import { getModelProbeData, getReviewConfigChangeRequests, getReviewConfigData, getReviewConfigHistory } from "@/lib/control-api";
import type { ModelScope } from "@/lib/model-scope";

type ModelTab = "routes" | "credentials" | "budgets" | "history";

export default async function Page({ params, searchParams }: { params: Promise<{ org: string }>; searchParams: Promise<{ scope?: string; repository?: string; provider?: string; api_base_url?: string; tab?: string }> }) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const repository = query.repository?.trim() ?? "";
  const scopeKind = query.scope === "repository" && repository ? "repository" : "tenant";
  const scopeProvider = scopeKind === "repository" ? query.provider?.trim() ?? "" : "";
  const scopeAPIBaseURL = scopeKind === "repository" ? query.api_base_url?.trim() ?? "" : "";
  const requestedScope: ModelScope = { scope_kind: scopeKind, scope_ref: scopeKind === "repository" ? repository : "", scope_provider: scopeProvider, scope_api_base_url: scopeAPIBaseURL };
  const tab: ModelTab = query.tab === "credentials" || query.tab === "budgets" || query.tab === "history" ? query.tab : "routes";
  const [data, probes, changes] = await Promise.all([
    getReviewConfigData(org, "models", scopeKind, repository, scopeProvider, scopeAPIBaseURL),
    getModelProbeData(org, scopeKind, repository, scopeProvider, scopeAPIBaseURL),
    getReviewConfigChangeRequests(org),
  ]);
  const history = tab === "history" && (data.source === "live" || data.source === "demo")
    ? await getReviewConfigHistory(org, "models", scopeKind, repository, scopeProvider, scopeAPIBaseURL)
    : undefined;
  const historyUsesReadModel = tab === "history" && (data.source === "live" || data.source === "demo");
  if (tab !== "routes") return <ModelRouteGovernanceView config={data.config} detail={historyUsesReadModel ? history?.detail ?? data.detail : data.detail} history={history?.history} org={org} requestedScope={requestedScope} source={historyUsesReadModel ? history?.source ?? data.source : data.source} tab={tab} />;
  return <ModelRouteEditor changes={changes.requests} changesDetail={changes.detail} changesSource={changes.source} config={data.config} detail={data.detail} key={`${scopeKind}:${repository}:${scopeProvider}:${scopeAPIBaseURL}:${data.config?.revision ?? 0}:${data.config?.content_sha256 ?? data.source}`} org={org} probes={probes.probes} probeDetail={probes.detail} probeSource={probes.source} requestedScope={requestedScope} source={data.source} />;
}
