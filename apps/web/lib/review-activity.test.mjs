import assert from "node:assert/strict";
import { test } from "node:test";

import { filterRunEvents, isRunEvent, mergeRunEvents, visibleRunEventPayload } from "./review-activity.ts";

const event = (id, revision, overrides = {}) => ({
  id, run_id: "run-1", revision, event_type: "run.completed", actor_kind: "worker",
  payload: {}, created_at: "2026-09-27T00:00:00Z", ...overrides,
});

test("same-revision events survive replay while duplicate event IDs do not multiply", () => {
  const merged = mergeRunEvents([event("a", 3)], [event("b", 3, { created_at: "2026-09-27T00:00:01Z" }), event("a", 3)]);
  assert.deepEqual(merged.map(({ id }) => id), ["a", "b"]);
});

test("event filter only searches event and actor metadata, not arbitrary payload", () => {
  const events = [event("a", 2, { actor_kind: "provider", event_type: "run.admitted", payload: { secret: "needle" } }), event("b", 3)];
  assert.deepEqual(filterRunEvents(events, "admitted", "provider").map(({ id }) => id), ["a"]);
  assert.deepEqual(filterRunEvents(events, "needle", "all"), []);
  assert.deepEqual(filterRunEvents(events, "3", "all").map(({ id }) => id), ["b"]);
});

test("stream event validation rejects cross-run and malformed event payloads", () => {
  assert.equal(isRunEvent(event("a", 1), "run-1"), true);
  assert.equal(isRunEvent(event("a", 1), "run-2"), false);
  assert.equal(isRunEvent(event("a", 1, { payload: [] }), "run-1"), false);
  assert.equal(isRunEvent(event("a", 1, { revision: "1" }), "run-1"), false);
});

test("event metadata uses a bounded allowlist and omits unknown or nested fields", () => {
  assert.deepEqual(visibleRunEventPayload({ token_ref: "secret", trigger: "x".repeat(161), nested: { value: "y" } }), {
    trigger: "x".repeat(160), "Additional metadata": "2 non-display fields omitted",
  });
});
