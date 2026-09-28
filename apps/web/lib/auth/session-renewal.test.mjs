import assert from "node:assert/strict";
import { test } from "node:test";

import { sessionRenewalPath, shouldRedirectExistingSession } from "./safe-path.ts";

test("a rejected session resumes the exact safe workspace route", () => {
  assert.equal(
    sessionRenewalPath("/acme/issues?status=open"),
    "/sign-in?error=session&next=%2Facme%2Fissues%3Fstatus%3Dopen",
  );
  assert.equal(
    sessionRenewalPath("//attacker.example"),
    "/sign-in?error=session&next=%2Fworkspaces",
  );
});

test("a rejected cookie does not immediately redirect out of sign-in", () => {
  assert.equal(shouldRedirectExistingSession("session"), false);
  assert.equal(shouldRedirectExistingSession(undefined), true);
  assert.equal(shouldRedirectExistingSession("state"), false);
});
