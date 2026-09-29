import assert from "node:assert/strict";
import { test } from "node:test";

import { reviewDuration } from "./review-duration.ts";

test("review duration distinguishes a live run from missing terminal timing", () => {
  assert.equal(reviewDuration({ state: "acknowledged" }), "Not started");
  assert.equal(reviewDuration({ state: "analyzing", started_at: "2026-09-25T00:00:00Z" }), "In progress");
  assert.equal(reviewDuration({ state: "completed", started_at: "2026-09-25T00:00:00Z" }), "Timing unavailable");
});

test("review duration renders bounded elapsed time and rejects inconsistent evidence", () => {
  const started_at = "2026-09-25T00:00:00Z";
  assert.equal(reviewDuration({ state: "completed", started_at, finished_at: "2026-09-25T00:00:00.500Z" }), "<1s");
  assert.equal(reviewDuration({ state: "completed", started_at, finished_at: "2026-09-25T00:01:21Z" }), "1m 21s");
  assert.equal(reviewDuration({ state: "completed", started_at, finished_at: "2026-09-25T01:02:03Z" }), "1h 2m");
  assert.equal(reviewDuration({ state: "completed", started_at, finished_at: "2026-09-24T23:59:59Z" }), "Timing unavailable");
});
