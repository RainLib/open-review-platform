import assert from "node:assert/strict";
import { test } from "node:test";

import { normalizeUiLanguage } from "./ui-language.ts";
import { workflowCopy, workflowStatus, workflowText } from "./workflow-copy.ts";
import { agentLinkedReviewOutcome } from "./agent-task-data.ts";

function placeholders(source) {
  return [...source.matchAll(/\{([A-Za-z][A-Za-z0-9_]*)\}/g)].map(match => match[1]).sort();
}

test("every Chinese catalog entry retains all named placeholders", () => {
  for (const [english, chinese] of Object.entries(workflowCopy)) {
    assert.equal(english, english.trim(), `Noncanonical source key: ${english}`);
    assert.ok(chinese.trim(), `Empty translation: ${english}`);
    assert.deepEqual(placeholders(chinese), placeholders(english), `Changed interpolation contract: ${english}`);
  }
});

test("interface copy changes language, preserving boundary spacing and empty input", () => {
  assert.equal(workflowText("zh-CN", "  Agent work\n"), "  Agent 任务\n");
  assert.equal(workflowText("en", "  Agent work\n"), "  Agent work\n");
  assert.equal(workflowText("zh-CN", "\n "), "\n ");
  assert.equal(workflowText("zh-CN", "\t"), "\t");
});

test("unknown requirements, code, hashes and prototype names pass through verbatim", () => {
  for (const source of ["Acceptance: retain A+B and $&", "59d409b71463f1e13c73657167a90f850ca871a1", "@openreview revise add CI", "constructor", "__proto__", "toString", "const x = '{count}';"]) {
    assert.equal(workflowText("zh-CN", source), source);
  }
});

test("named values are inserted once, without regex substitution or recursive interpolation", () => {
  const body = "<script>alert(1)</script> $& {id} 59d409b7";
  assert.equal(workflowText("zh-CN", "Temporary risk acceptance for issue {id}: {body}", { id: 27, body }), `Issue 27 的临时风险接受：${body}`);
  assert.equal(workflowText("en", "Version {version}", { version: "{version} $&" }), "Version {version} $&");
  assert.equal(workflowText("zh-CN", "Version {version}", {}), "版本 {version}");
  assert.equal(workflowText("en", "Version {version}", Object.create({ version: "inherited" })), "Version {version}");
});

test("states are display-only, and unsupported locales fall back to English", () => {
  assert.equal(workflowStatus("zh-CN", "awaiting_acceptance"), "等待验收");
  assert.equal(workflowStatus("en", "awaiting_acceptance"), "Awaiting acceptance");
  assert.equal(workflowStatus("zh-CN", "future_state"), "future state");
  assert.equal(workflowStatus("zh-CN", "constructor"), "constructor");
  assert.equal(normalizeUiLanguage("zh-CN"), "zh-CN");
  assert.equal(normalizeUiLanguage("zh-TW"), "en");
  assert.equal(normalizeUiLanguage(undefined), "en");
});

test("localized gate outcomes do not alter the source evidence or approval semantics", () => {
  const review = { state: "completed", merge_gate: { enabled: true, threshold: "high", conclusion: "failure", blocking_findings: 2, finding_count: 3 } };
  const before = structuredClone(review);
  assert.equal(agentLinkedReviewOutcome(review, "zh-CN").label, "合并门禁阻断");
  assert.match(agentLinkedReviewOutcome(review, "zh-CN").detail, /2/);
  assert.deepEqual(review, before);
  assert.equal(agentLinkedReviewOutcome(review).label, "Merge gate blocked");
});
