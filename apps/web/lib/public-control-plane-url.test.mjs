import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { test } from "node:test";

import {
  cliEnvironmentTemplate,
  cliReviewTemplate,
  resolvePublicControlPlaneURL,
} from "./public-control-plane-url.ts";

test("public CLI origin uses the explicit API URL or the public app origin", () => {
  assert.equal(resolvePublicControlPlaneURL({ publicAPIURL: "https://api.rainlib.com/", appURL: "https://review.rainlib.com" }), "https://api.rainlib.com");
  assert.equal(resolvePublicControlPlaneURL({ appURL: "https://review.rainlib.com" }), "https://review.rainlib.com");
  assert.equal(resolvePublicControlPlaneURL({ appURL: "http://127.0.0.1:3110" }), "http://127.0.0.1:3110");
  assert.equal(resolvePublicControlPlaneURL({ publicAPIURL: "https://review.corp.internal" }), "https://review.corp.internal");
});

test("placeholder, internal service, and unsafe CLI URLs do not produce copyable commands", () => {
  for (const value of ["https://review.example.com", "http://control-api:8080", "https://control-api:8080", "https://user:pass@review.rainlib.com", "https://review.rainlib.com/v1", "https://review.rainlib.com/?key=x", "javascript:alert(1)"]) {
    assert.equal(resolvePublicControlPlaneURL({ publicAPIURL: value }), undefined, value);
  }
});

test("CLI template guards every required value without shell redirection placeholders or embedded secrets", () => {
  const command = cliReviewTemplate({
    publicAPIURL: "https://review.rainlib.com",
    org: "rainlib-open-review",
    installationID: "550e8400-e29b-41d4-a716-446655440000",
    repository: "RainLib/open-review-platform",
  });
  assert.match(command, /OPEN_REVIEW_API_URL='https:\/\/review\.rainlib\.com'/);
  assert.match(command, /OPEN_REVIEW_INSTALLATION='550e8400-e29b-41d4-a716-446655440000'/);
  assert.match(command, /OPEN_REVIEW_REPOSITORY='RainLib\/open-review-platform'/);
  assert.match(command, /\$\{OPEN_REVIEW_API_KEY:\?Set OPEN_REVIEW_API_KEY/);
  assert.match(command, /\$\{OPEN_REVIEW_PR_NUMBER:\?Set OPEN_REVIEW_PR_NUMBER/);
  assert.match(command, /--number "\$OPEN_REVIEW_PR_NUMBER"/);
  assert.doesNotMatch(command, /<PR_OR_MR_NUMBER>|review\.example\.com|OPEN_REVIEW_API_KEY='[^']+'/);
  const parsed = spawnSync("sh", ["-n"], { input: command, encoding: "utf8" });
  assert.equal(parsed.status, 0, parsed.stderr);
});

test("shell quoting preserves unusual tenant names without evaluating them", () => {
  const environment = cliEnvironmentTemplate("https://review.rainlib.com", "rain'lib");
  assert.match(environment, /OPEN_REVIEW_TENANT='rain'\\''lib'/);
});
