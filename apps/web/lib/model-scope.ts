export type ModelScope = {
  scope_kind: "tenant" | "repository";
  scope_ref?: string;
  scope_provider?: string;
  scope_api_base_url?: string;
};

// A repository name is not unique across Git providers or self-managed hosts.
// Keep the full identity when navigating or deciding which proposals to show.
export function modelScopeQuery(scope: ModelScope): URLSearchParams {
  const query = new URLSearchParams();
  if (scope.scope_kind === "repository" && scope.scope_ref) {
    query.set("scope", "repository");
    query.set("repository", scope.scope_ref);
    if (scope.scope_provider) query.set("provider", scope.scope_provider);
    if (scope.scope_api_base_url) query.set("api_base_url", scope.scope_api_base_url);
  }
  return query;
}

export function sameModelScope(left: ModelScope, right: ModelScope): boolean {
  return left.scope_kind === right.scope_kind
    && (left.scope_ref ?? "") === (right.scope_ref ?? "")
    && (left.scope_provider ?? "") === (right.scope_provider ?? "")
    && (left.scope_api_base_url ?? "") === (right.scope_api_base_url ?? "");
}

type ModelProbeRoute = {
  origin_scope_kind: "default" | "tenant" | "repository";
  origin_scope_ref?: string;
  origin_scope_provider?: string;
  origin_scope_api_base_url?: string;
  content_sha256: string;
};

export function matchesModelProbeRoute(probe: ModelScope & { content_sha256: string }, route: ModelProbeRoute): boolean {
  if (route.origin_scope_kind === "default" || probe.content_sha256 !== route.content_sha256) return false;
  // Probes attest the effective route: an inherited workspace or legacy route
  // keeps its own origin identity even when opened through a qualified repository.
  const probeScope: ModelScope = {
    scope_kind: route.origin_scope_kind,
    scope_ref: route.origin_scope_ref,
    scope_provider: route.origin_scope_provider,
    scope_api_base_url: route.origin_scope_api_base_url,
  };
  return sameModelScope(probe, probeScope);
}
