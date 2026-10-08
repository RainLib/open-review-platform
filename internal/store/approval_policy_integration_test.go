package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestWorkspaceSelfApprovalOptInRetainsRoleRevisionVotesAndAudit(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, isolatedQueueDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	tenant, other, installation, set, version := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	slug := "approval-" + tenant.String()[:8]
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := s.pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Approval'),($3,$4,'Other')`, tenant, slug, other, slug+"-other")
	exec(`INSERT INTO memberships(tenant_id,subject,role) VALUES($1,'owner','owner'),($1,'admin','admin'),($1,'reviewer','reviewer'),($1,'viewer','viewer')`, tenant)
	policy, err := s.GetWorkspaceApprovalPolicy(ctx, "owner", slug)
	if err != nil || policy.AllowAgentPlanSelfApproval || policy.AllowRuleSelfApproval || policy.Revision != 1 || !policy.CanUpdate {
		t.Fatalf("default policy=%+v err=%v", policy, err)
	}
	on, off := true, false
	input := domain.WorkspaceApprovalPolicyInput{AllowAgentPlanSelfApproval: &on, AllowRuleSelfApproval: &on, ExpectedRevision: 1}
	for _, actor := range []string{"admin", "reviewer", "viewer", "outsider"} {
		if _, err := s.SaveWorkspaceApprovalPolicy(ctx, actor, slug, input); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s changed policy: %v", actor, err)
		}
	}
	if _, err := s.GetWorkspaceApprovalPolicy(ctx, "owner", slug+"-other"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant policy leaked: %v", err)
	}
	exec(`INSERT INTO rule_sets(id,tenant_id,name,created_by) VALUES($1,$2,'Approval fixture','owner')`, set, tenant)
	exec(`INSERT INTO rule_versions(id,rule_set_id,version,state,rules,content_sha256,created_by) VALUES($1,$2,1,'draft','[]',$3,'owner')`, version, set, strings.Repeat("c", 64))
	request, err := s.RequestRuleApproval(ctx, "owner", slug, set, 1, domain.RuleApprovalRequestInput{RequiredApprovals: 2})
	if err != nil {
		t.Fatal(err)
	}
	decision := domain.RuleApprovalDecisionInput{Decision: "approved"}
	if _, err := s.DecideRuleApproval(ctx, "owner", slug, request.ID, decision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("default self-vote: %v", err)
	}
	policy, err = s.SaveWorkspaceApprovalPolicy(ctx, "owner", slug, input)
	if err != nil || policy.Revision != 2 {
		t.Fatalf("opt-in %+v %v", policy, err)
	}
	if _, err := s.SaveWorkspaceApprovalPolicy(ctx, "owner", slug, input); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale settings write: %v", err)
	}
	list, err := s.ListRuleApprovalRequests(ctx, "owner", slug, 10)
	if err != nil || !list[0].CanDecide {
		t.Fatalf("self-vote capability: %+v %v", list, err)
	}
	if _, err := s.DecideRuleApproval(ctx, "viewer", slug, request.ID, decision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer self-vote: %v", err)
	}
	request, err = s.DecideRuleApproval(ctx, "owner", slug, request.ID, decision)
	if err != nil || request.State != "pending" {
		t.Fatalf("vote threshold bypassed %+v %v", request, err)
	}
	if _, err := s.DecideRuleApproval(ctx, "owner", slug, request.ID, decision); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate vote: %v", err)
	}
	request, err = s.DecideRuleApproval(ctx, "admin", slug, request.ID, decision)
	if err != nil || request.State != "approved" {
		t.Fatalf("second vote %+v %v", request, err)
	}
	exec(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,'team/repo','https://api.github.com','fixture','verified')`, installation, tenant, installation.String())
	if _, err = s.SaveAgentTaskPolicy(ctx, "owner", slug, domain.AgentTaskPolicyInput{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "team/repo", Mode: "manual", DecisionBackend: "deterministic"}); err != nil {
		t.Fatal(err)
	}
	issue := domain.AgentTaskIssueSnapshot{Title: "Fix bounded retry status in the worker", Body: "Observed behavior: final failures disappear. Expected behavior: retain the final failure and retry exactly twice."}
	taskInput := domain.AgentTaskInput{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "team/repo", OriginKind: "issue", OriginNumber: 1, Intent: "implement"}
	taskInput.OriginRevision = domain.AgentIssueRevision(taskInput.Provider, taskInput.APIBaseURL, taskInput.Repository, 1, issue.Title, issue.Body)
	issue.Revision = taskInput.OriginRevision
	task, err := s.CreateAgentTask(ctx, "owner", slug, taskInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordAgentTaskSourceSnapshot(ctx, task.ID, domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: strings.Repeat("a", 40), Issue: &issue}); err != nil {
		t.Fatal(err)
	}
	plan, err := s.CreateAgentTaskPlan(ctx, "owner", slug, task.ID, domain.AgentTaskPlanInput{Summary: "Fix only the bounded retry state transition. Verify focused retry tests and unchanged final failure reporting. No credential or permission changes."})
	if err != nil {
		t.Fatal(err)
	}
	// Revoking the opt-in takes effect even for plans created while it was enabled.
	input.AllowAgentPlanSelfApproval = &off
	input.AllowRuleSelfApproval = &off
	input.ExpectedRevision = 2
	if _, err = s.SaveWorkspaceApprovalPolicy(ctx, "owner", slug, input); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveAgentTaskPlan(ctx, "owner", slug, task.ID, plan.ID, domain.AgentTaskPlanApprovalInput{Revision: plan.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked author approval: %v", err)
	}
	input.AllowAgentPlanSelfApproval = &on
	input.ExpectedRevision = 3
	if _, err = s.SaveWorkspaceApprovalPolicy(ctx, "owner", slug, input); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveAgentTaskPlan(ctx, "reviewer", slug, task.ID, plan.ID, domain.AgentTaskPlanApprovalInput{Revision: plan.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer approval: %v", err)
	}
	if _, err := s.ApproveAgentTaskPlan(ctx, "owner", slug, task.ID, plan.ID, domain.AgentTaskPlanApprovalInput{Revision: plan.Revision + 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale plan: %v", err)
	}
	if _, err := s.ApproveAgentTaskPlan(ctx, "owner", slug, task.ID, plan.ID, domain.AgentTaskPlanApprovalInput{Revision: plan.Revision}); err != nil {
		t.Fatal(err)
	}
	var audits, dispatches int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action IN ('agent_task.plan_approved','rule_approval.decided') AND metadata->>'self_approval'='true' AND metadata->>'approval_policy_revision' IN ('2','4')`, tenant).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("self approval evidence %d %v", audits, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.execute.requested'`, task.ID).Scan(&dispatches); err != nil || dispatches != 1 {
		t.Fatalf("exact dispatch %d %v", dispatches, err)
	}
	// Verified Issue commands obey the same opt-in and exact digest guard.
	exec(`INSERT INTO provider_actor_mappings(tenant_id,provider,external_id,subject) VALUES($1,'github','provider-owner','owner')`, tenant)
	taskInput.OriginNumber = 2
	taskInput.OriginRevision = domain.AgentIssueRevision(taskInput.Provider, taskInput.APIBaseURL, taskInput.Repository, 2, issue.Title, issue.Body)
	issue.Revision = taskInput.OriginRevision
	commandTask, err := s.CreateAgentTask(ctx, "owner", slug, taskInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordAgentTaskSourceSnapshot(ctx, commandTask.ID, domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: strings.Repeat("a", 40), Issue: &issue}); err != nil {
		t.Fatal(err)
	}
	commandPlan, err := s.CreateAgentTaskPlan(ctx, "owner", slug, commandTask.ID, domain.AgentTaskPlanInput{Summary: plan.Summary})
	if err != nil {
		t.Fatal(err)
	}
	event := domain.AgentTaskCommandEvent{Provider: domain.ProviderGitHub, APIBaseURL: taskInput.APIBaseURL, Repository: taskInput.Repository, InstallationExternalID: installation.String(), IssueNumber: 2, IssueRevision: issue.Revision, IssueTitle: issue.Title, IssueBody: issue.Body, ActorExternalID: "provider-owner", CommentExternalID: "100", DeliveryID: uuid.NewString(), Body: "@openreview approve " + commandPlan.PlanSHA256}
	input.AllowAgentPlanSelfApproval = &off
	input.ExpectedRevision = 4
	if _, err = s.SaveWorkspaceApprovalPolicy(ctx, "owner", slug, input); err != nil {
		t.Fatal(err)
	}
	if outcome, err := s.ProcessAgentTaskCommand(ctx, event, "approve", event.Body); err != nil || outcome.Accepted || !strings.Contains(outcome.Reason, "self-approval") {
		t.Fatalf("disabled command self-approval %+v %v", outcome, err)
	}
	input.AllowAgentPlanSelfApproval = &on
	input.ExpectedRevision = 5
	if _, err = s.SaveWorkspaceApprovalPolicy(ctx, "owner", slug, input); err != nil {
		t.Fatal(err)
	}
	event.DeliveryID = uuid.NewString()
	event.Body = "@openreview approve " + strings.Repeat("0", 64)
	if outcome, err := s.ProcessAgentTaskCommand(ctx, event, "approve", event.Body); err != nil || outcome.Accepted {
		t.Fatalf("stale command digest %+v %v", outcome, err)
	}
	event.DeliveryID = uuid.NewString()
	event.Body = "@openreview approve " + commandPlan.PlanSHA256
	if outcome, err := s.ProcessAgentTaskCommand(ctx, event, "approve", event.Body); err != nil || !outcome.Accepted {
		t.Fatalf("enabled command self-approval %+v %v", outcome, err)
	}
	if outcome, err := s.ProcessAgentTaskCommand(ctx, event, "approve", event.Body); err != nil || !outcome.Duplicate {
		t.Fatalf("command replay %+v %v", outcome, err)
	}
}
