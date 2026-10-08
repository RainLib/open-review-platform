package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/agentplan"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestAgentPreflightHoldIsFencedAuditedAndConsumesNoAttempts(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, isolatedQueueDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	tenant, installation := uuid.New(), uuid.New()
	slug := "preflight-" + tenant.String()[:8]
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Preflight fixture')`, tenant, slug)
	exec(`INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'owner','owner',true),($1,'reviewer','reviewer',true)`, tenant)
	exec(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,'team/repo','https://api.github.com','fixture','verified')`, installation, tenant, installation.String())
	workflow := domain.AgentWorkflowPolicy{Enabled: true, RequireCriterionEvidence: true, MaxRepairCycles: 2, MaxTaskAttempts: 3}
	if _, err = s.SaveAgentTaskPolicy(ctx, "owner", slug, domain.AgentTaskPolicyInput{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "team/repo", Mode: "manual", DecisionBackend: "deterministic", Workflow: &workflow}); err != nil {
		t.Fatal(err)
	}
	issue := domain.AgentTaskIssueSnapshot{Title: "Fix bounded retry status in the worker", Body: "Observed behavior: final failures disappear. Expected behavior: retain the final failure and retry exactly twice.\n## Acceptance criteria\n- Retry exactly twice\n- Show the final failure"}
	input := domain.AgentTaskInput{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "team/repo", OriginKind: "issue", OriginNumber: 18, Intent: "implement"}
	input.OriginRevision = domain.AgentIssueRevision(input.Provider, input.APIBaseURL, input.Repository, 18, issue.Title, issue.Body)
	issue.Revision = input.OriginRevision
	task, err := s.CreateAgentTask(ctx, "reviewer", slug, input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: strings.Repeat("a", 40), Issue: &issue}
	sections, err := (agentplan.Planner{}).Generate(ctx, task, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.GeneratedPlan = &sections
	if _, err = s.RecordAgentTaskSourceSnapshot(ctx, task.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	detail, err := s.GetAgentTask(ctx, "owner", slug, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan := detail.Plans[0]
	plan, err = s.ApproveAgentTaskPlan(ctx, "owner", slug, task.ID, plan.ID, domain.AgentTaskPlanApprovalInput{Revision: plan.Revision})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = s.GetAgentTask(ctx, "owner", slug, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.AgentTaskExecutionRequest{TaskID: task.ID, TaskRevision: detail.Task.Revision, PlanID: plan.ID, PlanRevision: plan.Revision, PlanSHA256: plan.PlanSHA256}
	stale := request
	stale.PlanSHA256 = strings.Repeat("b", 64)
	if err := s.HoldAgentTaskExecutionForReadiness(ctx, "worker", stale); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("wrong digest held approval: %v", err)
	}
	if err := s.HoldAgentTaskExecutionForReadiness(ctx, "worker", request); err != nil {
		t.Fatal(err)
	}
	if err := s.HoldAgentTaskExecutionForReadiness(ctx, "worker", request); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("duplicate hold changed revision: %v", err)
	}
	detail, err = s.GetAgentTask(ctx, "owner", slug, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.State != "needs_attention" || detail.Task.Revision != request.TaskRevision+1 || detail.ExecutionBlock == nil || len(detail.Attempts) != 0 || !detail.PlanPermissions.CanCreatePlan || detail.ExecutionBudget == nil || detail.ExecutionBudget.Used != 0 {
		t.Fatalf("held detail: %+v", detail)
	}
	var audits, used int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='agent_task.execution_preflight_blocked'`, tenant).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(sum(attempt),0) FROM agent_task_attempts WHERE task_id=$1`, task.ID).Scan(&used); err != nil || used != 0 {
		t.Fatalf("used=%d err=%v", used, err)
	}
	// Restoration requires an explicitly created and approved replacement plan.
	newPlan, err := s.CreateAgentTaskPlan(ctx, "reviewer", slug, task.ID, domain.AgentTaskPlanInput{Sections: &sections})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveAgentTaskPlan(ctx, "owner", slug, task.ID, newPlan.ID, domain.AgentTaskPlanApprovalInput{Revision: newPlan.Revision}); err != nil {
		t.Fatal(err)
	}
	if err := s.HoldAgentTaskExecutionForReadiness(ctx, "worker", request); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("stale hold overwrote new approval: %v", err)
	}
	detail, err = s.GetAgentTask(ctx, "owner", slug, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.State != "execution_queued" || detail.ExecutionBlock != nil {
		t.Fatalf("new approval retains stale block: %+v", detail.ExecutionBlock)
	}
	newRequest := domain.AgentTaskExecutionRequest{TaskID: task.ID, TaskRevision: detail.Task.Revision, PlanID: newPlan.ID, PlanRevision: newPlan.Revision, PlanSHA256: newPlan.PlanSHA256}
	attempt, err := s.ClaimAgentTaskAttempt(ctx, "worker", newRequest, time.Minute)
	if err != nil || attempt.Attempt != 1 {
		t.Fatalf("restored claim=%+v err=%v", attempt, err)
	}
	if err := s.HoldAgentTaskExecutionForReadiness(ctx, "worker", newRequest); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("held executing attempt: %v", err)
	}
}
