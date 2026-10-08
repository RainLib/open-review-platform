package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestAgentTaskPlanIntegrityBindsSectionsAndLegacySummaryToHash(t *testing.T) {
	sections := domain.AgentTaskPlanSections{
		Objective:    "Correct the reported retry behavior without changing unrelated flows.",
		Scope:        "Only the worker retry path and focused regression tests.",
		Verification: "Run the focused test and the full Go suite.",
		Risks:        "Low; preserve task state transitions.",
		Unknowns:     "None after source inspection.",
	}
	summary := sections.Summary()
	digest := sha256.Sum256([]byte(summary))
	plan := domain.AgentTaskPlan{Sections: sections, Summary: summary, PlanSHA256: hex.EncodeToString(digest[:])}
	if err := validateAgentTaskPlanIntegrity(plan); err != nil {
		t.Fatalf("valid structured plan rejected: %v", err)
	}
	changed := plan
	changed.Sections.Scope = "Only an unrelated repository."
	if err := validateAgentTaskPlanIntegrity(changed); err == nil {
		t.Fatal("altered sections passed the approved summary boundary")
	}
	changed = plan
	changed.PlanSHA256 = strings.Repeat("0", 64)
	if err := validateAgentTaskPlanIntegrity(changed); err == nil {
		t.Fatal("altered approval hash passed the execution boundary")
	}
	legacy := domain.AgentTaskPlan{Summary: summary, PlanSHA256: plan.PlanSHA256}
	if err := validateAgentTaskPlanIntegrity(legacy); err != nil {
		t.Fatalf("legacy summary must remain readable: %v", err)
	}
}

func TestAgentTaskPlanPermissionsReflectSourceRoleAndSeparateApproval(t *testing.T) {
	detail := domain.AgentTaskDetail{
		Task:            domain.AgentTask{State: "awaiting_approval", SourceState: "ready", SourceBaseRef: "main", SourceBaseSHA: strings.Repeat("a", 40)},
		Plans:           []domain.AgentTaskPlan{{State: "awaiting_approval", CreatedBy: "owner"}},
		Classifications: []domain.AgentTaskClassification{{Decision: "requires_human", RiskLevel: "medium"}},
	}
	viewer := agentTaskPlanPermissions(detail, "viewer", "viewer", false)
	if viewer.CanCreatePlan || viewer.CanApprovePlan || viewer.CreateBlockReason != "reviewer_role_required" || viewer.ApproveBlockReason != "owner_admin_required" {
		t.Fatalf("viewer received plan actions: %#v", viewer)
	}
	reviewer := agentTaskPlanPermissions(detail, "reviewer", "reviewer", false)
	if !reviewer.CanCreatePlan || reviewer.CanApprovePlan || reviewer.ApproveBlockReason != "owner_admin_required" {
		t.Fatalf("reviewer actions incorrect: %#v", reviewer)
	}
	owner := agentTaskPlanPermissions(detail, "owner", "owner", false)
	if !owner.CanCreatePlan || owner.CanApprovePlan {
		t.Fatalf("author self-approval should be denied by default: %#v", owner)
	}
	detail.Classifications[0].RiskLevel = "high"
	owner = agentTaskPlanPermissions(detail, "owner", "owner", false)
	if owner.CanApprovePlan || owner.ApproveBlockReason != "separate_approver_required" {
		t.Fatalf("high-risk plan creator approved their own plan: %#v", owner)
	}
	admin := agentTaskPlanPermissions(detail, "admin", "another-admin", false)
	if !admin.CanApprovePlan {
		t.Fatalf("independent admin should be able to approve: %#v", admin)
	}
	owner = agentTaskPlanPermissions(detail, "owner", "owner", true)
	if !owner.CanApprovePlan {
		t.Fatalf("enabled self-approval still blocked: %#v", owner)
	}
	reviewer = agentTaskPlanPermissions(detail, "reviewer", "owner", true)
	if reviewer.CanApprovePlan {
		t.Fatal("self-approval opt-in widened reviewer role")
	}
	detail.Task.SourceState = "pending"
	admin = agentTaskPlanPermissions(detail, "admin", "another-admin", false)
	if admin.CanCreatePlan || admin.CreateBlockReason != "source_not_ready" {
		t.Fatalf("unfrozen source exposed plan creation: %#v", admin)
	}
}

func TestAgentSourceReadyCommentNeverOffersPlanForRejectedOrMissingContext(t *testing.T) {
	task := domain.AgentTask{SourceBaseSHA: strings.Repeat("a", 40)}
	for _, test := range []struct {
		decision string
		want     string
		noPlan   bool
	}{
		{decision: "requires_human", want: "bounded plan may now be prepared"},
		{decision: "needs_context", want: "blocked for missing context", noPlan: true},
		{decision: "rejected", want: "planning and execution are blocked", noPlan: true},
	} {
		classification := domain.AgentTaskClassification{Decision: test.decision, RiskLevel: "medium", NextAction: "review"}
		body, err := agentTaskSourceReadyBody(task, classification)
		if err != nil || !strings.Contains(body, test.want) || !strings.Contains(body, task.SourceBaseSHA) || !strings.Contains(body, "No coding Agent has started") {
			t.Fatalf("source decision %s produced unsafe body %q, error %v", test.decision, body, err)
		}
		if test.noPlan && strings.Contains(body, "bounded plan may now be prepared") {
			t.Fatalf("blocked source decision %s offered a plan", test.decision)
		}
	}
	if _, err := agentTaskSourceReadyBody(task, domain.AgentTaskClassification{Decision: "unknown"}); err == nil {
		t.Fatal("unknown source classification was published")
	}
}

func TestAgentPlanReadyCommentUsesCorrectApprovalSurface(t *testing.T) {
	plan := domain.AgentTaskPlan{Revision: 2, PlanSHA256: strings.Repeat("ab", 32)}
	issue := domain.AgentTask{ID: uuid.New(), OriginKind: "issue"}
	body := agentTaskPlanReadyBody(issue, plan)
	for _, expected := range []string{"Plan revision **2**", plan.PlanSHA256, "@openreview approve " + plan.PlanSHA256, "No coding Agent"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Issue plan comment missing %q: %s", expected, body)
		}
	}
	feedback := domain.AgentTask{ID: uuid.New(), OriginKind: "pull_request"}
	body = agentTaskPlanReadyBody(feedback, plan)
	if !strings.Contains(body, "approve this feedback plan in Open Review") || strings.Contains(body, "@openreview approve") {
		t.Fatalf("Draft PR/MR feedback comment offered an unsupported provider approval: %s", body)
	}
}

func TestAgentPlanApprovedCommentDoesNotClaimExecutionOrMerge(t *testing.T) {
	task := domain.AgentTask{ID: uuid.New(), OriginKind: "issue"}
	plan := domain.AgentTaskPlan{Revision: 3, PlanSHA256: strings.Repeat("cd", 32)}
	body := agentTaskPlanApprovedBody(task, plan)
	for _, expected := range []string{"Plan revision **3**", plan.PlanSHA256, "Execution is **queued**", "No coding result"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("approved plan status missing %q: %s", expected, body)
		}
	}
	if strings.Contains(body, "@openreview approve") || strings.Contains(body, "Draft PR ready") {
		t.Fatalf("approved plan status promised the wrong action: %s", body)
	}
}

func TestAgentTaskCommandAcknowledgementHasOptionalConsoleLink(t *testing.T) {
	task := domain.AgentTask{ID: uuid.New(), State: "received"}
	classification := domain.AgentTaskClassification{Decision: "requires_human", RiskLevel: "medium", NextAction: "await_plan_approval", Reasons: []string{"Acceptance criteria need an owner-reviewed plan."}}
	message := agentTaskCommandAcknowledgement(task, true, classification, "https://review.example.com/acme/agent-work?task="+task.ID.String())
	for _, expected := range []string{"Agent task received", "requires_human", "medium", "Open this Agent task in Open Review"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("acknowledgement missing %q: %s", expected, message)
		}
	}
	if !strings.Contains(message, "**Admission verdict:** `requires_human` · risk `medium` · next `await_plan_approval`") {
		t.Fatalf("expected explainable verdict, got %q", message)
	}
}

func TestAgentTaskCancellationCommentDoesNotPromiseProviderRollback(t *testing.T) {
	taskID := uuid.New()
	link := "https://review.example.com/acme/agent-work?task=" + taskID.String()
	message := agentTaskCancelledComment(taskID, link)
	for _, expected := range []string{"Agent task cancelled", taskID.String(), "late result callbacks cannot complete", "provider write already in flight", "check the repository", link} {
		if !strings.Contains(message, expected) {
			t.Fatalf("cancellation comment missing %q: %s", expected, message)
		}
	}
	if strings.Contains(message, "cannot publish a draft") || strings.Contains(message, "Draft PR was removed") {
		t.Fatalf("cancellation comment overclaims provider rollback: %s", message)
	}
}

func TestAgentTaskTerminalCommentsPreserveProviderUncertaintyAndPatchEvidence(t *testing.T) {
	attempt := domain.AgentTaskAttempt{
		BranchName: "agent/task-1", HeadSHA: strings.Repeat("a", 40),
		PullRequestURL: "https://github.com/acme/repo/pull/7",
		PatchSHA256:    strings.Repeat("b", 64), ChangedFileCount: 2, DiffBytes: 512,
	}
	status, message := agentTaskAdapterTerminalMessage("issue", attempt, domain.AgentTaskAdapterEvent{Kind: "completed", Summary: "Draft is ready"})
	if status != "completed" || !strings.Contains(message, attempt.PullRequestURL) || !strings.Contains(message, attempt.PatchSHA256) || !strings.Contains(message, "2 file(s), 512 diff byte(s)") || !strings.Contains(message, "not been reviewed, approved, or merged") {
		t.Fatalf("completed adapter comment lost bounded evidence: %s: %s", status, message)
	}
	status, message = agentTaskAdapterTerminalMessage("pull_request", attempt, domain.AgentTaskAdapterEvent{Kind: "completed", Summary: "Feedback was applied"})
	if status != "completed" || !strings.Contains(message, "same Draft PR/MR") || !strings.Contains(message, "not been re-reviewed") || !strings.Contains(message, "Draft-review settings") || !strings.Contains(message, attempt.PullRequestURL) || !strings.Contains(message, attempt.PatchSHA256) || strings.Contains(message, "reported a draft pull request") {
		t.Fatalf("feedback completion implied a new or reviewed Draft: %s: %s", status, message)
	}
	status, message = agentTaskAdapterTerminalMessage("issue", attempt, domain.AgentTaskAdapterEvent{Kind: "needs_attention", ErrorCode: "provider_timeout", Summary: "Draft response was lost"})
	if status != "needs-attention" || !strings.Contains(message, "may already have been accepted") || !strings.Contains(message, "Check the repository") || strings.Contains(message, "stopped before it could produce") {
		t.Fatalf("needs-attention comment overclaimed provider state: %s: %s", status, message)
	}
	status, message = agentTaskAdapterTerminalMessage("issue", attempt, domain.AgentTaskAdapterEvent{Kind: "failed", ErrorCode: "provider_timeout", Summary: "Push response was lost"})
	if status != "failed" || !strings.Contains(message, "Check for a branch or Draft PR/MR") {
		t.Fatalf("failed adapter comment omitted provider reconciliation: %s: %s", status, message)
	}
}

func TestAgentTaskCommandAcknowledgementDoesNotPromiseRejectedWork(t *testing.T) {
	task := domain.AgentTask{ID: uuid.New(), State: "rejected"}
	classification := domain.AgentTaskClassification{Decision: "rejected", RiskLevel: "critical", NextAction: "reject", Reasons: []string{"Unsafe instruction detected."}}
	message := agentTaskCommandAcknowledgement(task, true, classification, "")
	if !strings.Contains(message, "### Agent task rejected") || !strings.Contains(message, "Source resolution and planning will not continue") || strings.Contains(message, "will first capture") {
		t.Fatalf("rejected task acknowledgement promised unavailable work: %q", message)
	}
	message = agentTaskCommandAcknowledgement(domain.AgentTask{ID: uuid.New(), State: "received"}, true, domain.AgentTaskClassification{Decision: "needs_context", RiskLevel: "unknown", NextAction: "request_context"}, "")
	if !strings.Contains(message, "### Agent task needs context") || !strings.Contains(message, "planning remains blocked") || strings.Contains(message, "only then can an owner/admin approve") {
		t.Fatalf("under-specified task acknowledgement implied plan readiness: %q", message)
	}
}

func TestAgentTaskStatusCommentDoesNotClaimExecutionWithoutAnAttempt(t *testing.T) {
	task := domain.AgentTask{ID: uuid.New(), State: "received"}
	message := agentTaskStatusComment(task, domain.AgentTaskAttempt{}, nil, "https://review.example.com/acme/agent-work?task="+task.ID.String())
	for _, expected := range []string{"Agent task status", "No execution lease has been claimed", "Open task details in Open Review"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("status message missing %q: %s", expected, message)
		}
	}
}

func TestAgentAttemptAttentionCommentDistinguishesAttachedJob(t *testing.T) {
	withoutJob := agentTaskAttemptStatusBody("agent_executor_not_configured", "No adapter is deployed.", false)
	if !strings.Contains(withoutJob, "no coding action was performed") || strings.Contains(withoutJob, "provider write may already have started") {
		t.Fatalf("unattached attempt status is misleading: %q", withoutJob)
	}
	withJob := agentTaskAttemptStatusBody("agent_adapter_lease_expired", "The lease expired.", true)
	for _, evidence := range []string{"Coding or a provider write may already have started", "requested cancellation", "Check the repository branch and Draft"} {
		if !strings.Contains(withJob, evidence) {
			t.Fatalf("attached attempt status missing %q: %q", evidence, withJob)
		}
	}
	if strings.Contains(withJob, "no coding action was performed") {
		t.Fatalf("attached attempt falsely ruled out coding: %q", withJob)
	}
}

func TestAgentAttemptLeaseReclaimStopsAtAdapterAttachment(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	expired, live := now.Add(-time.Second), now.Add(time.Second)
	base := domain.AgentTaskAttempt{State: "running", Attempt: 1, LockedUntil: &expired}
	for _, tc := range []struct {
		name        string
		attempt     domain.AgentTaskAttempt
		maxAttempts int
		want        bool
	}{
		{name: "pre-adapter crash", attempt: base, maxAttempts: 2, want: true},
		{name: "attached reservation", attempt: func() domain.AgentTaskAttempt { item := base; item.AdapterJobID = "reserved-job"; return item }(), maxAttempts: 2},
		{name: "live lease", attempt: func() domain.AgentTaskAttempt { item := base; item.LockedUntil = &live; return item }(), maxAttempts: 2},
		{name: "retry budget exhausted", attempt: base, maxAttempts: 1},
		{name: "terminal attempt", attempt: func() domain.AgentTaskAttempt { item := base; item.State = "needs_attention"; return item }(), maxAttempts: 2},
		{name: "missing lease", attempt: func() domain.AgentTaskAttempt { item := base; item.LockedUntil = nil; return item }(), maxAttempts: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := canReclaimAgentAttempt(tc.attempt, tc.maxAttempts, now); got != tc.want {
				t.Fatalf("canReclaimAgentAttempt(%+v, %d)=%t, want %t", tc.attempt, tc.maxAttempts, got, tc.want)
			}
		})
	}
}

func TestAgentTaskStatusCommentShowsExactPendingApprovalContract(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	task := domain.AgentTask{ID: uuid.New(), State: "awaiting_approval", SourceState: "ready", SourceBaseRef: "main", SourceBaseSHA: strings.Repeat("a", 40)}
	plan := &domain.AgentTaskPlan{Revision: 3, State: "awaiting_approval", PlanSHA256: digest}
	message := agentTaskStatusComment(task, domain.AgentTaskAttempt{}, plan, "https://review.example.com/acme/agent-work?task="+task.ID.String())
	for _, expected := range []string{"Plan revision: **3**", digest, "@openreview approve " + digest, "After reviewing the full plan"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("pending approval status missing %q: %s", expected, message)
		}
	}
}

func TestAgentTaskDraftURLMatchesAdmittedGitLabOrigin(t *testing.T) {
	task := domain.AgentTask{Provider: domain.ProviderGitLab, APIBaseURL: "http://gitlab:8929/api/v4", Repository: "team/project"}
	if _, err := canonicalAgentResultURL(task, "http://gitlab:8929/team/project/-/merge_requests/7", 7, domain.AgentDraftURLPolicy{AllowGitLabHTTP: true}); err != nil {
		t.Fatal("local admitted GitLab draft URL was rejected")
	}
	for _, value := range []string{
		"http://other-gitlab:8929/team/project/-/merge_requests/7",
		"https://gitlab:8929/team/project/-/merge_requests/7",
		"http://gitlab:8929@other-gitlab:8929/team/project/-/merge_requests/7",
	} {
		if _, err := canonicalAgentResultURL(task, value, 7, domain.AgentDraftURLPolicy{AllowGitLabHTTP: true}); err == nil {
			t.Fatalf("unrelated GitLab draft URL was accepted: %s", value)
		}
	}
	github := domain.AgentTask{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "team/project"}
	if _, err := canonicalAgentResultURL(github, "http://github.com/team/project/pull/7", 7, domain.AgentDraftURLPolicy{}); err == nil {
		t.Fatal("GitHub plaintext draft URL was accepted")
	}
	if _, err := canonicalAgentResultURL(github, "https://github.com/other/project/pull/7", 7, domain.AgentDraftURLPolicy{}); err == nil {
		t.Fatal("another repository's GitHub PR was accepted")
	}
}

func TestAgentTaskDraftURLUsesDeploymentOwnedGitLabPublicBase(t *testing.T) {
	task := domain.AgentTask{Provider: domain.ProviderGitLab, APIBaseURL: "http://gitlab:8929/api/v4", Repository: "team/project"}
	policy := domain.AgentDraftURLPolicy{GitLabPublicBaseURL: "http://127.0.0.1:8929", GitLabPublicForAPIBaseURL: task.APIBaseURL, AllowGitLabHTTP: true}
	got, err := canonicalAgentResultURL(task, "http://gitlab:8929/team/project/-/merge_requests/7", 7, policy)
	if err != nil || got != "http://127.0.0.1:8929/team/project/-/merge_requests/7" {
		t.Fatalf("public draft URL = %q, %v", got, err)
	}
	if _, err := canonicalAgentResultURL(task, "http://attacker:8929/team/project/-/merge_requests/7", 7, policy); err == nil {
		t.Fatal("untrusted callback host was accepted")
	}
}
