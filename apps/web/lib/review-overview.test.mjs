import assert from "node:assert/strict";
import { test } from "node:test";

import { observedSeverity, providerObservation, reviewConfigurationEvidence, selectedScopeCount, validReviewOverviewView } from "./review-overview.ts";

test("configuration provenance requires every distinct governed section", () => {
  const snapshots = ["general", "categories", "filters", "prompts", "summary", "messages", "models", "issue-triage"].map((section) => ({ section }));
  assert.deepEqual(reviewConfigurationEvidence(snapshots), { retained: 8, expected: 8, complete: true });
  const missingPrompts = snapshots.filter((snapshot) => snapshot.section !== "prompts");
  assert.deepEqual(reviewConfigurationEvidence([...missingPrompts, snapshots[0]]), { retained: 7, expected: 8, complete: false });
  assert.deepEqual(reviewConfigurationEvidence([]), { retained: 0, expected: 8, complete: false });
});

test("overview subviews accept only stable slugs", () => {
  assert.equal(validReviewOverviewView("risk"), "risk");
  assert.equal(validReviewOverviewView("verification"), "verification");
  assert.equal(validReviewOverviewView("unexpected"), "overview");
});

test("risk is observed severity, not an inferred low risk", () => {
  assert.equal(observedSeverity([]), "none retained");
  assert.equal(observedSeverity([{ severity: "medium" }, { severity: "high" }]), "high");
});

test("provider observation fails closed for missing and mismatched revisions", () => {
  const evidence = { run: { head_sha: "abc" } };
  assert.equal(providerObservation(evidence), "not observed");
  assert.equal(providerObservation({ ...evidence, provider_checks: { head_sha: "def", state: "observed", observed_at: "2026-01-01", stale: false, truncated: false } }), "not observed");
  assert.equal(providerObservation({ ...evidence, provider_checks: { head_sha: "ABC", state: "observed", observed_at: "2026-01-01", stale: false, truncated: true } }), "partial");
  assert.equal(providerObservation({ ...evidence, provider_checks: { head_sha: "abc", state: "observed", observed_at: "2026-01-01", stale: true, truncated: false } }), "stale");
});

test("scope count stays unknown without a retained plan", () => {
  assert.equal(selectedScopeCount({}), undefined);
  assert.equal(selectedScopeCount({ execution_plan: { selected_paths: ["a.ts", "b.ts"] } }), 2);
});
