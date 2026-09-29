import assert from "node:assert/strict";
import { test } from "node:test";

import { reviewEmptyState } from "./review-empty-state.ts";

const data = (source = "live", all = 2) => ({ source, counts: { active: 0, attention: 0, completed: all, all } });

test("empty current view links to All rather than clearing an absent search", () => {
  const state = reviewEmptyState("my team", "active", {}, data());
  assert.equal(state.title, "No pull requests in this view");
  assert.deepEqual(state.action, { href: "/my%20team/reviews?view=all", label: "View all reviews" });
});

test("search and stale cursor each have a distinct recovery route", () => {
  assert.equal(reviewEmptyState("acme", "attention", { q: "failure" }, data()).action.href, "/acme/reviews?view=attention");
  assert.equal(reviewEmptyState("acme", "attention", { cursor: "stale" }, data()).action.label, "Return to first page");
});

test("first use points to the manual review guide, while unavailable data stays unavailable", () => {
  const firstUse = reviewEmptyState("acme", "active", {}, data("live", 0));
  assert.equal(firstUse.kind, "first-use-empty");
  assert.equal(firstUse.action.href, "#manual-review-guide");
  const unavailable = reviewEmptyState("acme", "active", {}, data("unavailable", 0));
  assert.equal(unavailable.kind, "unavailable");
  assert.equal(unavailable.action.href, "/acme/connect");
});

test("a count/list contradiction is not described as first use", () => {
  const state = reviewEmptyState("acme", "all", {}, data("live", 2));
  assert.equal(state.kind, "unavailable");
  assert.equal(state.title, "Review list needs attention");
});
