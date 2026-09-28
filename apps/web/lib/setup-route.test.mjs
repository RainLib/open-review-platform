import assert from "node:assert/strict";
import { test } from "node:test";

import {
  laterSetupStep,
  restorableRequestedStep,
  setupRouteForCheckpoint,
  setupRouteForStep,
  setupStepFromSegment,
} from "./setup-route.ts";

test("every connection step has a refreshable route without changing the provider callback", () => {
  const steps = ["provider", "install", "repositories", "scope", "learning", "severity", "sync"];
  for (const provider of ["github", "gitlab"]) {
    for (const step of steps) {
      const path = setupRouteForStep(step, provider);
      assert.equal(setupStepFromSegment(path.split("/")[2]), step);
    }
    assert.equal(setupRouteForStep("install", provider), `/setup/${provider}`);
  }
  assert.equal(setupStepFromSegment("rules"), undefined);
  assert.equal(setupStepFromSegment("unknown"), undefined);
});

test("a deep link never promotes an unauthenticated or earlier browser draft", () => {
  assert.equal(restorableRequestedStep("provider", "sync", false), "provider");
  assert.equal(restorableRequestedStep("install", "sync", true), "install");
  assert.equal(restorableRequestedStep("install", "provider", true), "provider");
  assert.equal(restorableRequestedStep("severity", "repositories", true), "repositories");
  assert.equal(restorableRequestedStep("severity", "sync", true), "severity");
  assert.equal(restorableRequestedStep("learning", "severity", true, "severity"), "severity");
  assert.equal(restorableRequestedStep("scope", undefined, true), "scope");
  assert.equal(laterSetupStep("severity", "learning"), "severity");
  assert.equal(laterSetupStep("learning", "severity"), "severity");
});

test("verified baseline steps resolve to the same step URLs", () => {
  assert.equal(setupRouteForCheckpoint("review_scope"), "/setup/repositories");
  assert.equal(setupRouteForCheckpoint("review_scope", "scope"), "/setup/review-scope");
  assert.equal(setupRouteForCheckpoint("learning"), "/setup/learning");
  assert.equal(setupRouteForCheckpoint("severity"), "/setup/severity");
  assert.equal(setupRouteForCheckpoint("rules"), "/setup/rules");
});
