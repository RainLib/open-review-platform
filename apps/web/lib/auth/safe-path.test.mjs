import assert from "node:assert/strict";
import { test } from "node:test";

import { safeInternalPath } from "./safe-path.ts";

test("keeps ordinary local routes and their filters", () => {
  assert.equal(safeInternalPath("/workspaces"), "/workspaces");
  assert.equal(
    safeInternalPath("/acme/issues?status=open&severity=high#results"),
    "/acme/issues?status=open&severity=high#results",
  );
});

test("rejects external and URL-normalized network paths", () => {
  for (const value of [
    "https://evil.example/path",
    "//evil.example/path",
    "///evil.example/path",
    "/\\evil.example/path",
    "/..//evil.example/path",
    "/%2e%2e//evil.example/path",
    "/workspaces\n//evil.example",
    "/workspaces\u0000",
    "/" + "a".repeat(2048),
  ]) {
    assert.equal(safeInternalPath(value), "/workspaces", value);
  }
});

test("every accepted value remains same-origin after repeated URL resolution", () => {
  for (const value of ["/acme/issues?next=%2Fworkspaces", "/a/../acme/connect", "/%2F/encoded"]) {
    const safe = safeInternalPath(value);
    const once = new URL(safe, "https://review.rainlib.com");
    const twice = new URL(safeInternalPath(safe), "https://review.rainlib.com");
    assert.equal(once.origin, "https://review.rainlib.com");
    assert.equal(twice.origin, once.origin);
  }
});
