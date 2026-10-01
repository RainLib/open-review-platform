import type {
  AgentTask,
  AgentTaskClassification,
  AgentTaskData,
  AgentTaskDetail,
  AgentTaskPlan,
  AgentTaskPlanSections,
  AgentTaskPolicy,
  DataSource,
  PlatformHealthData,
  ProviderInstallation,
  ReviewConfigData,
} from "@/lib/control-api";

// A model verdict is advisory evidence, not a fourth deterministic admission
// check. Keep it separate so the Console cannot imply it granted execution.
export function agentAdmissionEvidence(evaluation: AgentTaskClassification["evaluation"]) {
  return {
    hardChecks: evaluation.filter((stage) =>
      stage.stage === "judge" || stage.stage === "evaluate" || stage.stage === "verify"),
    modelAdvisory: evaluation.find((stage) => stage.stage === "model"),
  };
}

type AgentTaskSettledResults = {
  policies: PromiseSettledResult<{ agent_task_policies: AgentTaskPolicy[] }>;
  tasks: PromiseSettledResult<{ agent_tasks: AgentTask[]; next_cursor?: string }>;
  installations: PromiseSettledResult<{ installations: ProviderInstallation[] }>;
  selected: PromiseSettledResult<AgentTaskDetail | undefined>;
};

// A failed attempt keeps its approved plan as evidence, but must expose a
// fresh plan-revision path. Only the latest pending revision is actionable.
export function agentPlanActions(
  taskState: AgentTask["state"],
  latestPlanState?: AgentTaskPlan["state"],
) {
  return {
    canCreate: taskState === "received" || taskState === "awaiting_approval" || taskState === "needs_attention",
    canApprove: taskState === "awaiting_approval" && latestPlanState === "awaiting_approval",
  };
}

// Plan rows are immutable evidence. A pending plan may outlive the task's
// approval window (for example after an Issue comment cancels the task), so
// never present its stored state as an actionable current request.
export function agentPlanHistoricalNotice(
  taskState: AgentTask["state"],
  latestPlanState?: AgentTaskPlan["state"],
): string | undefined {
  if (latestPlanState !== "awaiting_approval" || agentPlanActions(taskState, latestPlanState).canApprove) {
    return undefined;
  }
  if (taskState === "cancelled") {
    return "This task was cancelled. The plan is retained as evidence and can no longer be approved.";
  }
  return `This plan is retained as evidence. A task in ${taskState.replaceAll("_", " ")} cannot approve this revision.`;
}

// Poll only while the control plane can advance a task without another human
// action. Waiting for a plan/approval and terminal states stay quiet.
export function agentTaskNeedsLiveRefresh(tasks: AgentTask[], selected?: AgentTask): boolean {
 if (selected?.workflow?.enabled && selected.state === "completed") return true;
  const active = (task: AgentTask) =>
    task.state === "execution_queued" || task.state === "executing" ||
    (task.state === "received" && task.source_state === "pending");
  return tasks.some(active) || (selected !== undefined && active(selected));
}

// A terminal run without an immutable gate decision is not a passing review.
// The Agent handoff must show the admitted decision, not infer it from a green
// external check or today's mutable repository settings.
export function agentLinkedReviewOutcome(review: AgentTaskDetail["linked_reviews"][number]) {
  if (review.state !== "completed") {
    return {
      label: `Review ${review.state.replaceAll("_", " ")}`,
      detail: review.state === "failed" || review.state === "needs_attention"
        ? "No trustworthy passing conclusion was published. Open the review for recovery guidance."
        : "Wait for the exact-revision review result before acting on findings or a merge gate.",
      tone: "neutral" as const,
    };
  }
  const gate = review.merge_gate;
  if (!gate) {
    return {
      label: "Review complete · gate unknown",
      detail: "No immutable merge-gate decision was retained; this does not prove the Draft is clear to merge.",
      tone: "neutral" as const,
    };
  }
  if (!gate.enabled) {
    return {
      label: "Review complete · advisory only",
      detail: `${gate.finding_count} recorded finding(s). Blocking was off for this revision; human approval and branch protection still apply.`,
      tone: "neutral" as const,
    };
  }
  if (gate.conclusion === "failure") {
    return {
      label: "Merge gate blocked",
      detail: `${gate.blocking_findings} finding(s) meet the ${gate.threshold} threshold. Open the review to inspect evidence, then update the Draft or request an approved Agent feedback cycle.`,
      tone: "danger" as const,
    };
  }
  return {
    label: "Review gate passed",
    detail: `${gate.finding_count} recorded finding(s); none block at the ${gate.threshold} threshold. Human approval and branch protection still apply.`,
    tone: "success" as const,
  };
}

export function agentTaskOriginGuidance(originKind: AgentTask["origin_kind"]) {
  if (originKind === "pull_request") {
    return {
      resource: "Agent Draft feedback",
      command: "@openreview revise <feedback>",
      sourcePending: "Verifying the original feedback comment, current Agent Draft head, and exact base commit. Planning stays unavailable until this evidence is captured.",
      sourceFailure: "Open Review could not complete the provider acknowledgement, verify the Agent Draft and original feedback comment, or finish the decision check. Planning and execution remain blocked. Retry re-reads the current Draft and comment; it cannot reuse stale feedback.",
      recovery: "Address the stated reason, then post a new revise command on the current Agent-created Draft. Editing an old comment does not reauthorize this task.",
      planSource: "Wait for the original feedback comment, Agent Draft head, and exact base commit to be verified before writing a plan.",
      planClassification: "This Agent Draft feedback classification does not permit a coding plan. Review the admission evidence first.",
    };
  }
  return {
    resource: "Issue",
    command: "@openreview implement",
    sourcePending: "Resolving the provider Issue, default branch, and exact commit. Planning stays unavailable until this evidence is captured.",
    sourceFailure: "Open Review could not complete the provider acknowledgement, source verification, or decision check. Planning and execution remain blocked. Retry will restore the acknowledgement first if needed, then re-read the current Issue and base commit.",
    recovery: "Update the provider Issue and submit a new implementation request for its current revision.",
    planSource: "Wait for the provider Issue and exact base commit to be verified before writing a plan.",
    planClassification: "This Issue classification does not permit a coding plan. Review the admission evidence first.",
  };
}

// Mirror the control plane's repository-scope syntax for presentation only.
// Admission still performs its own provider-qualified authorization check.
function installationScopeAllows(scope: string, repository: string): boolean {
  const target = repository.trim().replace(/^\/+|\/+$/g, "");
  if (!target) return false;
  return scope.split(",").some((raw) => {
    const candidate = raw.trim().replace(/^\/+|\/+$/g, "");
    if (candidate === "*/*" || candidate === target) return true;
    if (!candidate.endsWith("/*")) return false;
    const group = candidate.slice(0, -2);
    return group !== "" && target.startsWith(`${group}/`);
  });
}

export function agentInstallationForPolicy(
  policy: AgentTaskPolicy,
  installations: ProviderInstallation[],
): ProviderInstallation | undefined {
  return installations.find((installation) =>
    installation.provider === policy.provider &&
    installation.api_base_url.replace(/\/$/, "") === policy.api_base_url.replace(/\/$/, "") &&
    installationScopeAllows(installation.repository_scope, policy.repository),
  );
}

// A task freezes its installation ID. A second installation may have the
// same provider, host and repository scope, but its health and review settings
// do not prove this task's original connection is still usable.
export function agentInstallationForTask(
  task: AgentTask,
  installations: ProviderInstallation[],
): ProviderInstallation | undefined {
  return installations.find((installation) =>
    installation.id === task.installation_id &&
    installation.active &&
    installation.verification_state === "verified" &&
    installation.provider === task.provider &&
    installation.api_base_url.replace(/\/$/, "") === task.api_base_url.replace(/\/$/, "") &&
    installationScopeAllows(installation.repository_scope, task.repository),
  );
}

export function agentHasManualPolicyOnVerifiedInstallation(data: AgentTaskData): boolean {
  if (!data.availability.installations || !data.availability.policies) return false;
  return data.policies.some((policy) => policy.mode === "manual" &&
    agentInstallationForPolicy(policy, data.installations) !== undefined);
}

export function agentRepositoryAdmissionReadiness(
  data: AgentTaskData,
  selectedTask?: AgentTask,
  exact?: { source: "live" | "demo" | "unconfigured" | "unavailable"; policy?: AgentTaskPolicy },
): { status: string; detail: string; attention: boolean; policy?: AgentTaskPolicy } {
  if (selectedTask) {
    if (exact?.source !== "live") {
      return { status: "Policy unknown", detail: "The selected task's exact repository policy could not be read. Another repository's Manual policy does not apply.", attention: true };
    }
    const policy = exact.policy;
    if (!policy || policy.mode !== "manual") {
      return { status: policy?.mode === "suggest" ? "Suggestion only" : "Not enabled", detail: "This exact repository is not in Manual mode; new Issue implementation requests cannot be admitted.", attention: true, policy };
    }
    return { status: "Manual enabled", detail: "This task's exact repository policy allows explicit Issue implementation requests; plan approval and the executor remain separate gates.", attention: false, policy };
  }
  if (!data.availability.installations || !data.availability.policies) {
    return { status: "Unknown", detail: "Repository policies or verified connections could not be loaded.", attention: true };
  }
  if (agentHasManualPolicyOnVerifiedInstallation(data)) {
    return { status: "Manual enabled", detail: "At least one verified repository allows explicit Issue implementation requests. Select a task to inspect its exact policy.", attention: false };
  }
  if (data.policies.length >= 100) {
    return { status: "Check repository", detail: "The overview is limited to 100 policies. Select a task for an exact repository lookup.", attention: true };
  }
  return { status: "Not enabled", detail: "Enable Manual mode on a verified repository before requesting Issue implementation.", attention: true };
}

// This is a configuration check for the Agent-created Draft, not evidence that
// a webhook was delivered or that a review/feedback cycle completed.
export function agentDraftReviewReadiness(
  installation: ProviderInstallation | undefined,
  general: ReviewConfigData | undefined,
  filters: ReviewConfigData | undefined,
  targetBranch?: string,
): { status: string; detail: string; attention: boolean; connectionAction: boolean; filtersAction?: boolean } {
  if (!installation) {
    return { status: "Connection unknown", detail: "Select a repository with a verified provider installation before checking Draft review admission.", attention: true, connectionAction: true };
  }
  if (!installation.automatic_reviews) {
    return { status: "Automatic reviews off", detail: "Enable automatic reviews on this provider connection for Agent-created Drafts.", attention: true, connectionAction: true };
  }
  if (installation.author_scope === "mine") {
    return { status: "Author scope limited", detail: "This connection reviews only the OAuth user's PRs. An Agent-created Draft has a provider-bot author; choose all authors to admit it automatically.", attention: true, connectionAction: true };
  }
  if (general?.source !== "live" || !general.config || filters?.source !== "live" || !filters.config) {
    return { status: "Policy unknown", detail: "Effective review settings or filters could not be loaded. Do not assume the Draft will enter review.", attention: true, connectionAction: false };
  }
  const content = general.config.content;
  const trigger = typeof content.trigger_mode === "string" ? content.trigger_mode : content.automatic_review === false ? "manual" : "automatic";
  if (trigger !== "automatic" || content.automatic_review === false) {
    return { status: "Manual reviews only", detail: "Automatic webhook admission is disabled for this repository. An explicit review command would still be needed.", attention: true, connectionAction: false };
  }
  if (content.review_drafts !== true) {
    return { status: "Drafts excluded", detail: "Enable Review drafts so the Agent-created Draft PR/MR starts a review before it is marked ready.", attention: true, connectionAction: false };
  }
  if (content.rereview_on_push === false) {
    return { status: "Push re-review off", detail: "Enable Re-review on push so an Agent fix can receive another review on its new head commit.", attention: true, connectionAction: false };
  }
  const filterContent = filters.config.content;
  const requiredLabels = Array.isArray(filterContent.required_labels) ? filterContent.required_labels : [];
  if (requiredLabels.length > 0) {
    return { status: "Check required labels", detail: "Review admission requires PR/MR labels. Verify the Agent-created Draft receives them; otherwise the webhook will be skipped.", attention: true, connectionAction: false, filtersAction: true };
  }
  const excludedAuthors = Array.isArray(filterContent.exclude_authors) ? filterContent.exclude_authors : [];
  if (excludedAuthors.length > 0) {
    return { status: "Check author filter", detail: "This repository excludes selected authors. Verify the Agent Draft's provider-bot author is not excluded before relying on automatic review.", attention: true, connectionAction: false, filtersAction: true };
  }
  const branch = targetBranch?.trim();
  if (!branch) {
    return { status: "Branch unverified", detail: "Select an Agent task with a captured target branch to verify that its Draft can enter automatic review.", attention: true, connectionAction: false, filtersAction: true };
  }
  // The Go decoder defaults an omitted target_branches field to ["main"]. An
  // explicitly stored empty array is different: it admits no target branch.
  const targetBranches = Array.isArray(filterContent.target_branches) ? filterContent.target_branches : ["main"];
  if (!targetBranches.some((pattern) => pattern.trim() === branch)) {
    if (targetBranches.some((pattern) => /[*?]/.test(pattern))) {
      return { status: "Branch filter unverified", detail: `The target branch ${branch} may match a wildcard filter. Confirm it in Filters; this UI does not evaluate the backend's glob rules.`, attention: true, connectionAction: false, filtersAction: true };
    }
    return { status: "Target branch excluded", detail: `The target branch ${branch} is not listed in the review admission filter. Add it or choose an admitted target branch.`, attention: true, connectionAction: false, filtersAction: true };
  }
  return { status: "Core policy configured", detail: "Known Draft, push re-review, author and exact target-branch settings allow automatic admission. Webhook delivery and an actual review cycle still require live verification.", attention: false, connectionAction: false };
}

// Installation verification is an admission record, not evidence that a
// provider-calling worker can still read the repository. Keep those two
// signals distinct in the Agent Work readiness checklist.
export function agentProviderReadiness(
  data: AgentTaskData,
  health: Pick<PlatformHealthData, "source" | "providers">,
  selectedTask?: AgentTask,
  exact?: { source: DataSource; installation?: ProviderInstallation },
): { status: string; detail: string; attention: boolean } {
  if (selectedTask) {
    if (exact && exact.source !== "live") {
      return { status: "Connection unknown", detail: "The selected task's exact installation could not be read. Do not infer its state from another connection or the bounded overview.", attention: true };
    }
    const installation = agentInstallationForTask(selectedTask, exact ? exact.installation ? [exact.installation] : [] : data.installations);
    if (!installation) {
      return { status: "Exact connection unavailable", detail: exact ? "The selected task's original installation is absent, inactive, unverified, or outside the current repository scope. Another connection cannot prove this task is ready." : "The selected task's original verified installation is absent, inactive, outside the current repository scope, or beyond the loaded list. Another connection cannot prove this task is ready.", attention: true };
    }
    if (health.source !== "live") {
      return { status: "Health unknown", detail: "The task's original installation is verified, but its current provider access could not be sampled.", attention: true };
    }
    const probe = health.providers.find((provider) => provider.installation_id === installation.id);
    if (probe?.state === "live") {
      return { status: "Read access observed", detail: "The selected task's exact installation passed a fresh read-only probe. Provider write access is not proven by this check.", attention: false };
    }
    if (probe?.state === "critical") {
      return { status: "Probe failed", detail: "The selected task's installation failed its latest provider access probe. Check its credential and endpoint before continuing.", attention: true };
    }
    if (probe?.state === "degraded") {
      return { status: "Needs attention", detail: "The selected task's installation has degraded provider access or capabilities.", attention: true };
    }
    if (probe?.state === "stale") {
      return { status: "Probe stale", detail: "The selected task's installation has no fresh read-only access result.", attention: true };
    }
    return { status: "Probe unavailable", detail: "The selected task's installation has no fresh read-only provider access result.", attention: true };
  }
  if (!data.availability.installations) {
    return { status: "Unknown", detail: "Provider installations could not be loaded. Retry before admitting Issue work.", attention: true };
  }
  if (data.installations.length === 0) {
    return { status: "Not connected", detail: "Connect and verify a GitHub or GitLab installation with repository access.", attention: true };
  }
  if (health.source !== "live") {
    return { status: "Health unknown", detail: "An installation is verified, but current provider access could not be sampled.", attention: true };
  }
  const ids = new Set(data.installations.map((installation) => installation.id));
  const probes = health.providers.filter((provider) => ids.has(provider.installation_id));
  const live = probes.filter((provider) => provider.state === "live").length;
  const critical = probes.some((provider) => provider.state === "critical");
  const degraded = probes.some((provider) => provider.state === "degraded");
  if (live > 0 && live === data.installations.length) {
    return { status: "Read access observed", detail: "All verified installations passed a fresh read-only probe. Provider write access is not proven by this check.", attention: false };
  }
  if (live > 0) {
    return { status: `${live}/${data.installations.length} access observed`, detail: "Only some verified installations passed a fresh read-only probe. Inspect the selected repository before requesting an Agent task.", attention: true };
  }
  if (critical) {
    return { status: "Probe failed", detail: "A verified installation failed its latest provider access probe. Check its credential and endpoint health before requesting work.", attention: true };
  }
  if (degraded) {
    return { status: "Needs attention", detail: "Provider access or an advertised capability is degraded. Inspect the selected installation before requesting work.", attention: true };
  }
  if (probes.some((provider) => provider.state === "stale")) {
    return { status: "Probe stale", detail: "The last read-only provider probe is outside its freshness window. Recheck the connection before requesting work.", attention: true };
  }
  return { status: "Probe unavailable", detail: "An installation is verified, but no fresh read-only provider access result is available.", attention: true };
}

// An empty queue is actionable only after the installation and policy reads
// have succeeded. A failed read must never masquerade as missing setup.
export function agentTaskEmptyQueueStage(data: AgentTaskData):
  "connection_unavailable" | "connect" | "policy_unavailable" | "check_repository" | "enable" | "request" {
  if (!data.availability.installations) return "connection_unavailable";
  if (data.installations.length === 0) return "connect";
  if (!data.availability.policies) return "policy_unavailable";
  if (!agentHasManualPolicyOnVerifiedInstallation(data)) {
    // The overview is bounded to 100 policies. A Manual policy beyond that
    // page cannot be ruled out until the selected repository is read exactly.
    return data.policies.length >= 100 ? "check_repository" : "enable";
  }
  return "request";
}

// A pre-push checkpoint belongs to one execution attempt, not merely to the
// task. Never present an older commit as evidence for a later retry.
export function agentAttemptEvidence(
  attempts: AgentTaskDetail["attempts"],
  checkpoints: AgentTaskDetail["publication_checkpoints"] = [],
) {
  return attempts.map((attempt) => ({
    attempt,
    checkpoint: checkpoints?.find(
      (checkpoint) => checkpoint.attempt_id === attempt.id && checkpoint.attempt_number === attempt.attempt,
    ),
  }));
}

export function agentPlanSectionsValid(sections: AgentTaskPlanSections): boolean {
  const encoder = new TextEncoder();
  const byteLength = (value: string) => encoder.encode(value).length;
  const requirements: [keyof Pick<AgentTaskPlanSections, "objective" | "scope" | "verification" | "risks" | "unknowns">, number][] = [
    ["objective", 20], ["scope", 10], ["verification", 10],
    ["risks", 3], ["unknowns", 3],
  ];
  if (requirements.some(([key, minimum]) => {
    const value = sections[key].trim();
    return byteLength(value) < minimum || byteLength(value) > 4000 || value.includes("\0");
  })) return false;
  if ((sections.acceptance_criteria?.length ?? 0) > 20 || sections.acceptance_criteria?.some(value => byteLength(value.trim()) < 3 || byteLength(value) > 1000 || value.includes("\0"))) return false;
  const summary = `## Objective\n${sections.objective.trim()}\n\n## Scope and impact\n${sections.scope.trim()}\n\n## Verification\n${sections.verification.trim()}\n\n## Risks\n${sections.risks.trim()}\n\n## Unknowns\n${sections.unknowns.trim()}`;
  const acceptance = sections.acceptance_criteria?.length
    ? `\n\n## Acceptance criteria\n${sections.acceptance_criteria.map(value => `- ${value.trim()}`).join("\n")}` : "";
  const totalBytes = byteLength(summary + acceptance);
  return totalBytes >= 20 && totalBytes <= 12000;
}

// One failing provider/read endpoint must not hide independent task evidence.
// Availability is explicit so a missing response is never rendered as an
// authoritative empty list or used as a policy revision for a mutation.
export function assembleAgentTaskData(
  results: AgentTaskSettledResults,
  selectedTaskRequested: boolean,
  taskCursor?: string,
): AgentTaskData {
  const availability = {
    policies: results.policies.status === "fulfilled" && Array.isArray(results.policies.value.agent_task_policies),
    tasks: results.tasks.status === "fulfilled" && Array.isArray(results.tasks.value.agent_tasks) &&
      (results.tasks.value.next_cursor === undefined ||
        (typeof results.tasks.value.next_cursor === "string" && results.tasks.value.next_cursor.length > 0)),
    installations: results.installations.status === "fulfilled" && Array.isArray(results.installations.value.installations),
    selected: !selectedTaskRequested || (results.selected.status === "fulfilled" && Boolean(
      results.selected.value?.task &&
      Array.isArray(results.selected.value.plans) &&
      Array.isArray(results.selected.value.classifications) &&
      Array.isArray(results.selected.value.attempts),
    )),
  };
  const failed = [
    !availability.installations && "provider connections",
    !availability.policies && "repository policies",
    !availability.tasks && "task queue",
    !availability.selected && "selected task detail",
  ].filter((name): name is string => Boolean(name));
  const anyAvailable = availability.installations || availability.policies ||
    availability.tasks || (selectedTaskRequested && availability.selected);
  const installations = availability.installations && results.installations.status === "fulfilled"
    ? results.installations.value.installations.filter(
        (installation) => installation.active && installation.verification_state === "verified",
      )
    : [];
  return {
    source: failed.length === 0 ? "live" : anyAvailable ? "partial" : "unavailable",
    installations,
    policies: availability.policies && results.policies.status === "fulfilled" ? results.policies.value.agent_task_policies : [],
    tasks: availability.tasks && results.tasks.status === "fulfilled" ? results.tasks.value.agent_tasks : [],
    taskCursor,
    nextTaskCursor: availability.tasks && results.tasks.status === "fulfilled" ? results.tasks.value.next_cursor : undefined,
    selected: availability.selected && results.selected.status === "fulfilled" ? results.selected.value : undefined,
    selectedTaskRequested,
    availability,
    detail: failed.length ? `Could not load ${failed.join(", ")}. Available sections remain visible; refresh to retry.` : undefined,
  };
}
