import assert from "node:assert/strict";
import { test } from "node:test";

import { blockingFindings, filterReviewFindings, findingPublicationState, validFindingSeverity, validFindingStatus } from "./review-findings.ts";

const findings = [
  { id: "a", severity: "critical", category: "Security", path: "src/auth.ts", body: "Token leak" },
  { id: "b", severity: "high", category: "Bug", path: "src/api.ts", body: "Crash", disposition: "resolved" },
  { id: "c", severity: "medium", category: "Bug", path: "src/api.ts", body: "Race" },
  { id: "d", severity: "low", category: "Style", path: "README.md", body: "Typo", disposition: "wont_fix" },
];

test("finding filters reject unsupported URL values", () => {
  assert.equal(validFindingSeverity("critical"), "critical");
  assert.equal(validFindingSeverity("blocking"), "blocking");
  assert.equal(validFindingStatus("resolved"), "resolved");
  assert.equal(validFindingStatus("deleted"), "all");
});

test("blocking is derived only from a retained enabled merge-gate threshold", () => {
  assert.equal(blockingFindings({ findings }), undefined);
  assert.equal(blockingFindings({ findings, merge_gate: { enabled: false, threshold: "high" } }), undefined);
  assert.deepEqual(blockingFindings({ findings, merge_gate: { enabled: true, threshold: "high" } }).map((finding) => finding.id), ["a", "b"]);
});

test("combined finding filters retain exact file and status semantics", () => {
  const filters = { severity: "all", status: "open", category: "Bug", file: "src/api.ts", query: "race" };
  assert.deepEqual(filterReviewFindings(findings, filters).map((finding) => finding.id), ["c"]);
  assert.deepEqual(filterReviewFindings(findings, { ...filters, severity: "blocking", status: "all", query: "" }).map((finding) => finding.id), []);
  assert.deepEqual(filterReviewFindings(findings, { ...filters, severity: "blocking", status: "all", query: "" }, findings.slice(0, 2)).map((finding) => finding.id), ["b"]);
});

test("finding publication requires a matching inline receipt with a confirmed timestamp", () => {
  const finding = { provider_marker: "open-review-platform:finding:exact" };
  const receipt = { receipt_kind: "inline_finding", stable_marker: finding.provider_marker };
  assert.equal(findingPublicationState({}, []), "untracked");
  assert.equal(findingPublicationState(finding, []), "unconfirmed");
  assert.equal(findingPublicationState(finding, [{ ...receipt, stable_marker: "another", published_at: "2026-09-27T00:00:00Z" }]), "unconfirmed");
  assert.equal(findingPublicationState(finding, [{ ...receipt, receipt_kind: "summary", published_at: "2026-09-27T00:00:00Z" }]), "unconfirmed");
  assert.equal(findingPublicationState(finding, [receipt]), "pending");
  assert.equal(findingPublicationState(finding, [{ ...receipt, last_error: "provider timeout" }]), "failed");
  assert.equal(findingPublicationState(finding, [{ ...receipt, last_error: "prior attempt failed", published_at: "2026-09-27T00:00:00Z" }]), "published");
});
