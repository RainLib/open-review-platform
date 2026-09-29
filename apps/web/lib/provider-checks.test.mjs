import assert from "node:assert/strict";
import { test } from "node:test";

import { groupProviderChecks, prioritizeProviderChecks, providerCheckState, reviewChecksCount, safeProviderCheckURL, validReviewChecksView } from "./provider-checks.ts";

test("own and legacy provider checks cannot count as independent CI", () => {
  const own = { name: "Open Review / Analysis", state: "success", origin: "open_review" };
  const legacy = { name: "Open Review / Analysis", state: "success" };
  const unknown = { name: "Pipeline #31", state: "success", origin: "unclassified" };
  const external = { name: "CI / go", state: "failure", origin: "independent" };
  const grouped = groupProviderChecks([own, legacy, unknown, external]);
  assert.deepEqual(grouped.independent, [external]);
  assert.deepEqual(grouped.other, [own, legacy, unknown]);
});

test("actionable independent checks appear before successful ones without mutating provider order", () => {
  const checks = [
    { name: "build", state: "success", origin: "independent" },
    { name: "lint", state: "failed", origin: "independent" },
    { name: "unit", state: "running", origin: "independent" },
    { name: "integration", state: "failed", origin: "independent" },
  ];
  assert.deepEqual(prioritizeProviderChecks(checks).map((check) => check.name), ["lint", "integration", "unit", "build"]);
  assert.equal(checks[0].name, "build");
});

test("checks navigation and provider states fail closed on unknown input", () => {
  assert.equal(validReviewChecksView("provider-ci"), "provider-ci");
  assert.equal(validReviewChecksView("open-review"), "open-review");
  assert.equal(validReviewChecksView("legacy"), "all");
  assert.equal(providerCheckState("success"), "success");
  assert.equal(providerCheckState("succeeded"), "success");
  assert.equal(providerCheckState("TIMED_OUT"), "failed");
  assert.equal(providerCheckState("cancelled"), "cancelled");
  assert.equal(providerCheckState("in_progress"), "running");
  assert.equal(providerCheckState("pending"), "queued");
  assert.equal(providerCheckState("neutral"), "unknown");
});

test("check links must use a navigable HTTP URL without embedded credentials", () => {
  assert.equal(safeProviderCheckURL("https://github.com/acme/repo/actions/runs/1"), "https://github.com/acme/repo/actions/runs/1");
  assert.equal(safeProviderCheckURL("http://gitlab.local/acme/repo/-/pipelines/1"), "http://gitlab.local/acme/repo/-/pipelines/1");
  assert.equal(safeProviderCheckURL("javascript:alert(1)"), undefined);
  assert.equal(safeProviderCheckURL("https://user:secret@example.com/run"), undefined);
});

test("check counts exclude publication receipts and mismatched provider revisions", () => {
  const evidence = { run: { head_sha: "abc" }, stages: [{ id: "stage" }], merge_gate: { conclusion: "success" }, receipts: [{ id: "receipt" }], provider_checks: { head_sha: "def", checks: [{ origin: "independent" }] } };
  assert.deepEqual(reviewChecksCount(evidence), { all: 2, openReview: 2, independent: 0, unclassified: 0 });
  evidence.provider_checks.head_sha = "ABC";
  evidence.provider_checks.checks.push({ origin: "open_review" });
  assert.deepEqual(reviewChecksCount(evidence), { all: 4, openReview: 3, independent: 1, unclassified: 0 });
});
