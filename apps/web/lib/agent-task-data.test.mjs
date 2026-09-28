import assert from "node:assert/strict";
import { test } from "node:test";

import { agentAdmissionEvidence, agentAttemptEvidence, agentDraftReviewReadiness, agentHasManualPolicyOnVerifiedInstallation, agentInstallationForPolicy, agentInstallationForTask, agentLinkedReviewOutcome, agentPlanActions, agentPlanHistoricalNotice, agentPlanSectionsValid, agentProviderReadiness, agentRepositoryAdmissionReadiness, agentTaskEmptyQueueStage, agentTaskNeedsLiveRefresh, agentTaskOriginGuidance, assembleAgentTaskData } from "./agent-task-data.ts";

test("Agent review handoff distinguishes a blocked gate, advisory review and missing decision", () => {
  const review = { state: "completed", merge_gate: { enabled: true, threshold: "high", conclusion: "failure", blocking_findings: 1, finding_count: 2 } };
  assert.equal(agentLinkedReviewOutcome(review).label, "Merge gate blocked");
  assert.match(agentLinkedReviewOutcome(review).detail, /approved Agent feedback cycle/);
  assert.equal(agentLinkedReviewOutcome({ ...review, state: "failed" }).tone, "neutral");
  assert.equal(agentLinkedReviewOutcome({ ...review, merge_gate: undefined }).label, "Review complete · gate unknown");
  assert.equal(agentLinkedReviewOutcome({ ...review, merge_gate: { ...review.merge_gate, enabled: false } }).label, "Review complete · advisory only");
  assert.equal(agentLinkedReviewOutcome({ ...review, merge_gate: { ...review.merge_gate, conclusion: "success", blocking_findings: 0 } }).label, "Review gate passed");
});

test("policy editing selects an installation with the exact provider, host and repository scope", () => {
  const policy = { provider: "gitlab", api_base_url: "https://git.example.test/api/v4", repository: "team/service" };
  const installations = [
    { id: "wrong-provider", provider: "github", api_base_url: policy.api_base_url, repository_scope: "team/*" },
    { id: "wrong-host", provider: "gitlab", api_base_url: "https://other.example.test/api/v4", repository_scope: "team/*" },
    { id: "wrong-scope", provider: "gitlab", api_base_url: policy.api_base_url, repository_scope: "other/*" },
    { id: "authorized", provider: "gitlab", api_base_url: `${policy.api_base_url}/`, repository_scope: "team/*" },
  ];
  assert.equal(agentInstallationForPolicy(policy, installations)?.id, "authorized");
  assert.equal(agentInstallationForPolicy({ ...policy, repository: "other/service" }, installations)?.id, "wrong-scope");
  assert.equal(agentInstallationForPolicy({ ...policy, repository: "private/service" }, installations), undefined);
});

test("Agent Draft handoff reports each independent automatic review gate without claiming a completed cycle", () => {
  const installation = { automatic_reviews: true, author_scope: "all" };
  const general = { source: "live", config: { content: { trigger_mode: "automatic", automatic_review: true, review_drafts: true, rereview_on_push: true } } };
  const filters = { source: "live", config: { content: { required_labels: [], target_branches: ["main"] } } };
  const check = (connection = installation, settings = general, metadata = filters, branch = "main") => agentDraftReviewReadiness(connection, settings, metadata, branch);
  assert.equal(check().status, "Core policy configured");
  assert.match(check().detail, /live verification/);
  assert.equal(agentDraftReviewReadiness(undefined, general, filters).status, "Connection unknown");
  assert.equal(check({ ...installation, automatic_reviews: false }).status, "Automatic reviews off");
  assert.equal(check({ ...installation, author_scope: "mine" }).status, "Author scope limited");
  assert.equal(agentDraftReviewReadiness(installation, undefined, filters).status, "Policy unknown");
  assert.equal(check(installation, general, { ...filters, source: "demo" }).status, "Policy unknown");
  assert.equal(check(installation, { ...general, config: { content: { ...general.config.content, trigger_mode: "manual" } } }).status, "Manual reviews only");
  assert.equal(check(installation, { ...general, config: { content: { ...general.config.content, review_drafts: false } } }).status, "Drafts excluded");
  assert.equal(check(installation, { ...general, config: { content: { ...general.config.content, rereview_on_push: false } } }).status, "Push re-review off");
  assert.equal(check(installation, general, { ...filters, config: { content: { required_labels: ["agent-reviewed"] } } }).status, "Check required labels");
  assert.equal(agentDraftReviewReadiness(installation, general, filters).status, "Branch unverified");
  assert.equal(check(installation, general, filters, "release").status, "Target branch excluded");
  assert.equal(check(installation, general, { ...filters, config: { content: { target_branches: ["release/*"] } } }, "release/v2").status, "Branch filter unverified");
  assert.equal(check(installation, general, { ...filters, config: { content: { target_branches: ["release/v2"] } } }, "release/v2").status, "Core policy configured");
  assert.equal(check(installation, general, { ...filters, config: { content: { required_labels: [] } } }, "main").status, "Core policy configured");
  assert.equal(check(installation, general, { ...filters, config: { content: { target_branches: [] } } }, "main").status, "Target branch excluded");
  assert.equal(check(installation, general, { ...filters, config: { content: { target_branches: ["main"], exclude_authors: ["open-review-bot"] } } }).status, "Check author filter");
  assert.equal(check(installation, general, filters, "release").filtersAction, true);
});

test("selected Agent task uses only its exact repository policy, never another repository's Manual setting", () => {
  const selectedTask = { provider: "github", api_base_url: "https://api.github.com", repository: "RainLib/target" };
  const otherPolicy = { mode: "manual", provider: "github", api_base_url: "https://api.github.com", repository: "RainLib/other" };
  const data = { policies: [otherPolicy], installations: [{ provider: "github", api_base_url: "https://api.github.com", repository_scope: "RainLib/*" }], availability: { installations: true, policies: true } };
  assert.equal(agentRepositoryAdmissionReadiness(data).status, "Manual enabled");
  assert.equal(agentRepositoryAdmissionReadiness(data, selectedTask, { source: "unavailable" }).status, "Policy unknown");
  assert.equal(agentRepositoryAdmissionReadiness(data, selectedTask, { source: "live" }).status, "Not enabled");
  assert.equal(agentRepositoryAdmissionReadiness(data, selectedTask, { source: "live", policy: { ...selectedTask, mode: "disabled" } }).status, "Not enabled");
  assert.equal(agentRepositoryAdmissionReadiness(data, selectedTask, { source: "live", policy: { ...selectedTask, mode: "suggest" } }).status, "Suggestion only");
  assert.equal(agentRepositoryAdmissionReadiness(data, selectedTask, { source: "live", policy: { ...selectedTask, mode: "manual" } }).status, "Manual enabled");
});

test("Agent task recovery guidance follows the Issue or Agent Draft feedback origin", () => {
  const issue = agentTaskOriginGuidance("issue");
  const feedback = agentTaskOriginGuidance("pull_request");
  assert.equal(issue.command, "@openreview implement");
  assert.match(issue.sourceFailure, /re-read the current Issue/);
  assert.match(issue.planClassification, /Issue classification/);
  assert.equal(feedback.command, "@openreview revise <feedback>");
  assert.match(feedback.sourceFailure, /Agent Draft and original feedback comment/);
  assert.match(feedback.recovery, /current Agent-created Draft/);
  assert.doesNotMatch(feedback.recovery + feedback.planSource + feedback.planClassification, /@openreview implement|provider Issue/);
});

test("Jev advice is distinct from deterministic admission checks", () => {
  const evaluation = [
    { stage: "judge", outcome: "passed", summary: "No hard rejection.", signals: [] },
    { stage: "evaluate", outcome: "requires_human", summary: "Plan required.", signals: [] },
    { stage: "verify", outcome: "passed", summary: "Source frozen.", signals: [] },
    { stage: "model", outcome: "advisory", summary: "Bounded choice only.", signals: ["jev", "plan", "confidence:91"] },
  ];
  const result = agentAdmissionEvidence(evaluation);
  assert.deepEqual(result.hardChecks.map((stage) => stage.stage), ["judge", "evaluate", "verify"]);
  assert.deepEqual(result.modelAdvisory?.signals, ["jev", "plan", "confidence:91"]);
  assert.equal(agentAdmissionEvidence(evaluation.slice(0, 3)).modelAdvisory, undefined);
  assert.equal(agentAdmissionEvidence([...evaluation, { stage: "unexpected", outcome: "passed", summary: "", signals: [] }]).hardChecks.length, 3);
});

test("publication checkpoints stay attached to their exact execution attempt", () => {
  const attempts = [
    { id: "attempt-new", attempt: 2, state: "running" },
    { id: "attempt-old", attempt: 1, state: "needs_attention" },
  ];
  const checkpoints = [
    { attempt_id: "attempt-old", attempt_number: 1, head_sha: "old-head", verification_profile_sha256: "old-profile" },
    { attempt_id: "attempt-new", attempt_number: 1, head_sha: "wrong-number", verification_profile_sha256: "wrong-profile" },
  ];
  const evidence = agentAttemptEvidence(attempts, checkpoints);
  assert.equal(evidence[0].checkpoint, undefined);
  assert.equal(evidence[1].checkpoint?.head_sha, "old-head");
  assert.equal(evidence[1].checkpoint?.verification_profile_sha256, "old-profile");
  assert.deepEqual(agentAttemptEvidence([], checkpoints), []);
});

test("failed execution offers a new plan revision without reapproving the old one", () => {
  assert.deepEqual(agentPlanActions("needs_attention", "approved"), { canCreate: true, canApprove: false });
  assert.deepEqual(agentPlanActions("awaiting_approval", "awaiting_approval"), { canCreate: true, canApprove: true });
  assert.deepEqual(agentPlanActions("execution_queued", "approved"), { canCreate: false, canApprove: false });
  assert.deepEqual(agentPlanActions("completed", "approved"), { canCreate: false, canApprove: false });
});

test("pending plan history is not presented as actionable after cancellation", () => {
  assert.equal(agentPlanHistoricalNotice("awaiting_approval", "awaiting_approval"), undefined);
  assert.match(agentPlanHistoricalNotice("cancelled", "awaiting_approval"), /cancelled.*no longer be approved/);
  assert.match(agentPlanHistoricalNotice("needs_attention", "awaiting_approval"), /cannot approve this revision/);
  assert.equal(agentPlanHistoricalNotice("completed", "approved"), undefined);
});

test("Agent task live refresh follows only autonomous progress", () => {
  const task = (state, source_state = "ready") => ({ state, source_state });
  assert.equal(agentTaskNeedsLiveRefresh([task("received", "pending")]), true);
  assert.equal(agentTaskNeedsLiveRefresh([task("execution_queued")]), true);
  assert.equal(agentTaskNeedsLiveRefresh([task("executing")]), true);
  assert.equal(agentTaskNeedsLiveRefresh([task("awaiting_approval"), task("completed")]), false);
  assert.equal(agentTaskNeedsLiveRefresh([task("needs_attention")]), false);
  assert.equal(agentTaskNeedsLiveRefresh([], task("executing")), true);
});

test("structured Agent plans require each approval boundary before submission", () => {
  const sections = {
    objective: "Correct the reported retry behavior without changing unrelated flows.",
    scope: "Only the review worker and its focused tests.",
    verification: "Run the focused worker test and full Go suite.",
    risks: "Low; no data migration.",
    unknowns: "None after source review.",
  };
  assert.equal(agentPlanSectionsValid(sections), true);
  assert.equal(agentPlanSectionsValid({ ...sections, verification: "" }), false);
  assert.equal(agentPlanSectionsValid({ ...sections, objective: "short" }), false);
  assert.equal(agentPlanSectionsValid({ ...sections, risks: "x".repeat(4001) }), false);
  assert.equal(agentPlanSectionsValid({ ...sections, risks: "风险".repeat(667) }), false);
});

const ok = (value) => ({ status: "fulfilled", value });
const failed = { status: "rejected", reason: new Error("provider unavailable") };

function settled(overrides = {}) {
  return {
    policies: ok({ agent_task_policies: [{ id: "policy-1", revision: 3 }] }),
    tasks: ok({ agent_tasks: [{ id: "task-1", state: "needs_attention" }] }),
    installations: ok({ installations: [
      { id: "verified", active: true, verification_state: "verified" },
      { id: "revoked", active: false, verification_state: "verified" },
    ] }),
    selected: ok(undefined),
    ...overrides,
  };
}

test("complete Agent reads retain only verified active installations", () => {
  const data = assembleAgentTaskData(settled(), false);
  assert.equal(data.source, "live");
  assert.deepEqual(data.installations.map((item) => item.id), ["verified"]);
  assert.equal(data.policies.length, 1);
  assert.equal(data.tasks.length, 1);
});

test("Agent readiness separates verified installation from current provider access", () => {
  const data = assembleAgentTaskData(settled({ installations: ok({ installations: [
    { id: "github", active: true, verification_state: "verified" },
    { id: "gitlab", active: true, verification_state: "verified" },
    { id: "failed", active: true, verification_state: "failed" },
  ] }) }), false);
  const live = { installation_id: "github", state: "live" };
  const critical = { installation_id: "gitlab", state: "critical" };
  assert.equal(agentProviderReadiness(data, { source: "live", providers: [live, critical] }).status, "1/2 access observed");
  assert.equal(agentProviderReadiness(data, { source: "live", providers: [live, critical] }).attention, true);
  assert.equal(agentProviderReadiness(data, { source: "live", providers: [live, { ...critical, state: "live" }] }).status, "Read access observed");
  assert.equal(agentProviderReadiness(data, { source: "live", providers: [live, { ...critical, state: "live" }] }).attention, false);
  assert.equal(agentProviderReadiness(data, { source: "live", providers: [critical] }).status, "Probe failed");
  assert.equal(agentProviderReadiness(data, { source: "live", providers: [{ ...critical, state: "degraded" }] }).status, "Needs attention");
  assert.equal(agentProviderReadiness(data, { source: "live", providers: [{ ...critical, state: "stale" }] }).status, "Probe stale");
  assert.equal(agentProviderReadiness(data, { source: "live", providers: [] }).status, "Probe unavailable");
  assert.equal(agentProviderReadiness(data, { source: "unavailable", providers: [] }).status, "Health unknown");
  assert.equal(agentProviderReadiness({ ...data, installations: [] }, { source: "live", providers: [live] }).status, "Not connected");
  assert.equal(agentProviderReadiness({ ...data, availability: { ...data.availability, installations: false } }, { source: "live", providers: [live] }).status, "Unknown");
});

test("selected Agent task checks its frozen installation, not an overlapping healthy connection", () => {
  const task = {
    installation_id: "original", provider: "github", api_base_url: "https://api.github.com",
    repository: "RainLib/open-review-platform",
  };
  const original = {
    id: "original", active: true, verification_state: "verified", provider: "github",
    api_base_url: "https://api.github.com", repository_scope: "RainLib/*",
  };
  const other = { ...original, id: "other" };
  const data = { availability: { installations: true }, installations: [other, original] };
  const health = { source: "live", providers: [
    { installation_id: "other", state: "live" },
    { installation_id: "original", state: "critical" },
  ] };
  assert.equal(agentInstallationForTask(task, data.installations)?.id, "original");
  assert.equal(agentProviderReadiness(data, health, task).status, "Probe failed");
  assert.equal(agentProviderReadiness(data, health, task).attention, true);
  assert.equal(agentProviderReadiness(data, { ...health, providers: [{ installation_id: "other", state: "live" }] }, task).status, "Probe unavailable");
  assert.equal(agentProviderReadiness(data, { ...health, providers: [{ installation_id: "original", state: "live" }] }, task).status, "Read access observed");
  assert.equal(agentProviderReadiness(data, health).status, "1/2 access observed");

  for (const changed of [
    { ...original, active: false },
    { ...original, verification_state: "failed" },
    { ...original, repository_scope: "Other/*" },
    { ...original, api_base_url: "https://enterprise.example/api/v3" },
  ]) {
    const changedData = { ...data, installations: [other, changed] };
    assert.equal(agentInstallationForTask(task, changedData.installations), undefined);
    assert.equal(agentProviderReadiness(changedData, health, task).status, "Exact connection unavailable");
  }
  assert.equal(agentProviderReadiness({ ...data, installations: [other] }, health, task).status, "Exact connection unavailable");
  assert.equal(agentProviderReadiness(data, { source: "unavailable", providers: [] }, task).status, "Health unknown");
  assert.equal(agentProviderReadiness({ ...data, installations: [other] }, health, task, { source: "live", installation: original }).status, "Probe failed");
  assert.equal(agentProviderReadiness(data, health, task, { source: "live" }).status, "Exact connection unavailable");
  assert.equal(agentProviderReadiness(data, health, task, { source: "unavailable" }).status, "Connection unknown");
  assert.equal(agentProviderReadiness({ ...data, availability: { installations: false } }, { source: "live", providers: [{ installation_id: "original", state: "live" }] }, task, { source: "live", installation: original }).status, "Read access observed");
});

test("Agent task history retains the requested cursor and only a validated next cursor", () => {
  const page = assembleAgentTaskData(settled({ tasks: ok({ agent_tasks: [{ id: "older-task" }], next_cursor: "next-task" }) }), false, "current-task");
  assert.equal(page.source, "live");
  assert.equal(page.taskCursor, "current-task");
  assert.equal(page.nextTaskCursor, "next-task");
  assert.equal(page.tasks[0].id, "older-task");

  const malformed = assembleAgentTaskData(settled({ tasks: ok({ agent_tasks: [], next_cursor: 42 }) }), false, "current-task");
  assert.equal(malformed.source, "partial");
  assert.equal(malformed.availability.tasks, false);
  assert.equal(malformed.nextTaskCursor, undefined);
});

test("empty Agent queue follows verified connection and Manual policy prerequisites", () => {
  const noConnection = assembleAgentTaskData(settled({
    tasks: ok({ agent_tasks: [] }), policies: ok({ agent_task_policies: [] }),
    installations: ok({ installations: [] }),
  }), false);
  assert.equal(agentTaskEmptyQueueStage(noConnection), "connect");
  const noPolicy = { ...noConnection, installations: [{ id: "verified", provider: "github", api_base_url: "https://api.github.com", repository_scope: "RainLib/*" }] };
  assert.equal(agentTaskEmptyQueueStage(noPolicy), "enable");
  assert.equal(agentTaskEmptyQueueStage({ ...noPolicy, policies: [{ mode: "suggest" }] }), "enable");
  const manualPolicy = { mode: "manual", provider: "github", api_base_url: "https://api.github.com", repository: "RainLib/open-review-platform" };
  assert.equal(agentTaskEmptyQueueStage({ ...noPolicy, policies: [manualPolicy] }), "request");
  assert.equal(agentHasManualPolicyOnVerifiedInstallation({ ...noPolicy, policies: [manualPolicy] }), true);
  assert.equal(agentTaskEmptyQueueStage({ ...noPolicy, policies: [{ ...manualPolicy, repository: "Other/project" }] }), "enable");
  assert.equal(agentTaskEmptyQueueStage({ ...noPolicy, policies: [{ ...manualPolicy, provider: "gitlab" }] }), "enable");
  assert.equal(agentTaskEmptyQueueStage({ ...noPolicy, policies: [{ ...manualPolicy, api_base_url: "https://gitlab.example/api/v4" }] }), "enable");
  assert.equal(agentTaskEmptyQueueStage({ ...noPolicy, availability: { ...noPolicy.availability, policies: false } }), "policy_unavailable");
  assert.equal(agentTaskEmptyQueueStage({ ...noPolicy, availability: { ...noPolicy.availability, installations: false } }), "connection_unavailable");
  assert.equal(agentTaskEmptyQueueStage({ ...noPolicy, policies: Array.from({ length: 100 }, (_, index) => ({ mode: "disabled", repository: `repo-${index}` })) }), "check_repository");
});

test("manual-policy readiness honors exact, wildcard, and comma-separated verified scopes", () => {
  const base = assembleAgentTaskData(settled({ policies: ok({ agent_task_policies: [] }), tasks: ok({ agent_tasks: [] }), installations: ok({ installations: [] }) }), false);
  const policy = { mode: "manual", provider: "gitlab", api_base_url: "https://gitlab.example/api/v4", repository: "team/sub/project" };
  const withScope = (scope) => ({ ...base, policies: [policy], installations: [{ provider: "gitlab", api_base_url: "https://gitlab.example/api/v4/", repository_scope: scope }] });
  for (const scope of ["team/sub/project", "team/*", "*/*", "other/repo, team/sub/*"]) {
    assert.equal(agentHasManualPolicyOnVerifiedInstallation(withScope(scope)), true, scope);
  }
  for (const scope of ["", "*", "team", "other/*", "team2/*"]) {
    assert.equal(agentHasManualPolicyOnVerifiedInstallation(withScope(scope)), false, scope);
  }
});

test("provider inventory failure does not hide policies or task evidence", () => {
  const data = assembleAgentTaskData(settled({ installations: failed }), false);
  assert.equal(data.source, "partial");
  assert.equal(data.availability.installations, false);
  assert.equal(data.availability.policies, true);
  assert.equal(data.availability.tasks, true);
  assert.equal(data.tasks[0].id, "task-1");
  assert.match(data.detail, /provider connections/);
});

test("missing policy revision is distinguished from an empty policy list", () => {
  const data = assembleAgentTaskData(settled({ policies: failed }), false);
  assert.equal(data.source, "partial");
  assert.equal(data.availability.policies, false);
  assert.equal(data.tasks[0].id, "task-1");
});

test("malformed list payloads cannot masquerade as authoritative empty data", () => {
  const data = assembleAgentTaskData(settled({ tasks: ok({ wrong_field: [] }) }), false);
  assert.equal(data.source, "partial");
  assert.equal(data.availability.tasks, false);
  assert.equal(data.policies.length, 1);
});

test("a stale selected task does not hide the queue", () => {
  const data = assembleAgentTaskData(settled({ selected: failed }), true);
  assert.equal(data.source, "partial");
  assert.equal(data.selectedTaskRequested, true);
  assert.equal(data.availability.selected, false);
  assert.equal(data.tasks[0].id, "task-1");
});

test("a successful but incomplete detail response remains unavailable", () => {
  const data = assembleAgentTaskData(settled({ selected: ok({ task: { id: "task-1" } }) }), true);
  assert.equal(data.source, "partial");
  assert.equal(data.availability.selected, false);
  assert.equal(data.selected, undefined);
});

test("all failed reads cannot be presented as a healthy empty Agent workspace", () => {
  const data = assembleAgentTaskData(settled({ policies: failed, tasks: failed, installations: failed, selected: failed }), false);
  assert.equal(data.source, "unavailable");
  assert.equal(data.availability.tasks, false);
  assert.equal(data.availability.policies, false);
});
