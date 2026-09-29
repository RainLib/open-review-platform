import assert from "node:assert/strict";
import test from "node:test";

import { matchesModelProbeRoute, modelScopeQuery, sameModelScope } from "./model-scope.ts";

const gitlab = {
  scope_kind: "repository",
  scope_ref: "team/service",
  scope_provider: "gitlab",
  scope_api_base_url: "https://gitlab.internal/api/v4",
};

test("same-name repositories on different providers and instances cannot share approvals or probes", () => {
  assert.equal(sameModelScope(gitlab, { ...gitlab }), true);
  assert.equal(sameModelScope(gitlab, { ...gitlab, scope_provider: "github" }), false);
  assert.equal(sameModelScope(gitlab, { ...gitlab, scope_api_base_url: "https://gitlab.com/api/v4" }), false);
  assert.equal(sameModelScope(gitlab, { ...gitlab, scope_ref: "team/other-service" }), false);
});

test("legacy unqualified receipts only match legacy scope", () => {
  const legacy = { scope_kind: "repository", scope_ref: "team/service" };
  assert.equal(sameModelScope(gitlab, legacy), false);
  assert.equal(sameModelScope(legacy, { ...legacy, scope_provider: "", scope_api_base_url: "" }), true);
});

test("tab navigation retains the full self-managed repository identity", () => {
  for (const tab of ["routes", "credentials", "budgets", "history"]) {
    const query = modelScopeQuery(gitlab);
    query.set("tab", tab);
    const navigation = new URL(`/acme/settings/models?${query}`, "https://console.example");
    assert.equal(navigation.searchParams.get("scope"), "repository");
    assert.equal(navigation.searchParams.get("repository"), "team/service");
    assert.equal(navigation.searchParams.get("provider"), "gitlab");
    assert.equal(navigation.searchParams.get("api_base_url"), "https://gitlab.internal/api/v4");
    assert.equal(navigation.searchParams.get("tab"), tab);
  }
});

test("workspace navigation removes prior repository fields", () => {
  assert.equal(modelScopeQuery({ ...gitlab, scope_kind: "tenant" }).toString(), "");
  assert.equal(sameModelScope({ scope_kind: "tenant" }, { scope_kind: "tenant", scope_ref: "", scope_provider: "", scope_api_base_url: "" }), true);
});

test("inherited repository model health uses workspace-origin receipts", () => {
  const route = { origin_scope_kind: "tenant", content_sha256: "current-route" };
  const probe = { scope_kind: "tenant", content_sha256: "current-route", state: "queued" };
  assert.equal(matchesModelProbeRoute(probe, route), true);
  assert.equal(matchesModelProbeRoute({ ...gitlab, content_sha256: "current-route" }, route), false);
});

test("qualified repository legacy fallback retains the unqualified origin", () => {
  const route = { origin_scope_kind: "repository", origin_scope_ref: gitlab.scope_ref, content_sha256: "legacy-route" };
  assert.equal(matchesModelProbeRoute({ scope_kind: "repository", scope_ref: gitlab.scope_ref, content_sha256: "legacy-route" }, route), true);
  assert.equal(matchesModelProbeRoute({ ...gitlab, content_sha256: "legacy-route" }, route), false);
});

test("probe health cannot leak from an older route or another repository instance", () => {
  const route = { origin_scope_kind: "repository", origin_scope_ref: gitlab.scope_ref, origin_scope_provider: gitlab.scope_provider, origin_scope_api_base_url: gitlab.scope_api_base_url, content_sha256: "current-route" };
  assert.equal(matchesModelProbeRoute({ ...gitlab, content_sha256: "current-route" }, route), true);
  assert.equal(matchesModelProbeRoute({ ...gitlab, content_sha256: "old-route" }, route), false);
  assert.equal(matchesModelProbeRoute({ ...gitlab, scope_api_base_url: "https://gitlab.com/api/v4", content_sha256: "current-route" }, route), false);
  assert.equal(matchesModelProbeRoute({ ...gitlab, content_sha256: "current-route" }, { ...route, origin_scope_kind: "default" }), false);
});
