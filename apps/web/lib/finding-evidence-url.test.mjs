import assert from "node:assert/strict";
import { test } from "node:test";

import { findingEvidenceURL } from "./finding-evidence-url.ts";

test("finding evidence targets the matching run and finding card", () => {
  const runID = "9a4f63d1-8e14-47b4-a43e-5f8a99f2ee60";
  const findingID = "e94ba97a-f046-4bc7-b239-6d2613e5296b";
  assert.equal(
    findingEvidenceURL("acme-team", runID, findingID),
    `/acme-team/reviews/${runID}?tab=findings#finding-${findingID}`,
  );
});

test("unlinked or malformed legacy findings do not advertise a run link", () => {
  assert.equal(findingEvidenceURL("acme", undefined, "e94ba97a-f046-4bc7-b239-6d2613e5296b"), undefined);
  assert.equal(findingEvidenceURL("acme", "not-a-run", "e94ba97a-f046-4bc7-b239-6d2613e5296b"), undefined);
  assert.equal(findingEvidenceURL("acme", "9a4f63d1-8e14-47b4-a43e-5f8a99f2ee60", "not-a-finding"), undefined);
  assert.equal(findingEvidenceURL("..", "9a4f63d1-8e14-47b4-a43e-5f8a99f2ee60", "e94ba97a-f046-4bc7-b239-6d2613e5296b"), undefined);
});
