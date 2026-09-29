import assert from "node:assert/strict";
import { test } from "node:test";

import { OIDC_ATTEMPT_COOKIE, oidcAttemptCookieName, oidcAttemptCookieToExpire } from "./oidc-attempt-cookie.ts";

test("OIDC attempts in separate tabs retain separate cookie names", () => {
  const first = oidcAttemptCookieName("A".repeat(43));
  const second = oidcAttemptCookieName("B".repeat(43));
  assert.match(first, /^open_review_oidc_attempt_[0-9a-f]{20}$/);
  assert.notEqual(first, second);
  assert.equal(first, oidcAttemptCookieName("A".repeat(43)));
});

test("callback input cannot select arbitrary cookie names", () => {
  for (const state of ["", "short", "A".repeat(44), "A".repeat(42) + ".", "A".repeat(42) + ";"]) {
    assert.equal(oidcAttemptCookieName(state), undefined);
  }
});

test("terminal callbacks retire only their own OIDC attempt", () => {
  const first = "A".repeat(43);
  const second = "B".repeat(43);
  assert.equal(
    oidcAttemptCookieToExpire(first, "first-attempt", "legacy-attempt", false),
    oidcAttemptCookieName(first),
  );
  assert.notEqual(oidcAttemptCookieName(first), oidcAttemptCookieName(second));
  assert.equal(
    oidcAttemptCookieToExpire(first, undefined, "legacy-attempt", true),
    OIDC_ATTEMPT_COOKIE,
  );
  assert.equal(
    oidcAttemptCookieToExpire(first, undefined, "other-tab-attempt", false),
    undefined,
  );
  assert.equal(
    oidcAttemptCookieToExpire("invalid.state", "untrusted", undefined, false),
    undefined,
  );
});
