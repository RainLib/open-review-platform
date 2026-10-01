package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/agentplan"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestAgentReviewRepairIsIdempotentBoundedAndRequiresFreshApproval(t *testing.T) {
	if os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL") == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	s, err := Open(ctx, os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	tenant, installation := uuid.New(), uuid.New()
	slug := "review-repair-" + tenant.String()[:8]
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Review repair fixture')`, tenant, slug)
	exec(`INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'owner','owner',true),($1,'reviewer','reviewer',true)`, tenant)
	exec(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,'team/repo','https://api.github.com','fixture','verified')`, installation, tenant, installation.String())
	workflow := domain.AgentWorkflowPolicy{Enabled: true, MaxRepairCycles: 2, MaxTaskAttempts: 3}
	_, err = s.SaveAgentTaskPolicy(ctx, "owner", slug, domain.AgentTaskPolicyInput{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "team/repo", Mode: "manual", DecisionBackend: "deterministic", Workflow: &workflow})
	if err != nil {
		t.Fatal(err)
	}
	issue := domain.AgentTaskIssueSnapshot{Title: "Fix bounded retry behavior in worker", Body: "Observed behavior: retries exceed the configured limit. Expected behavior: retry twice and retain the final failure.\n## Acceptance criteria\n- Retry exactly twice\n- Retain the final failure"}
	input := domain.AgentTaskInput{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "team/repo", OriginKind: "issue", OriginNumber: 21, Intent: "implement"}
	input.OriginRevision = domain.AgentIssueRevision(input.Provider, input.APIBaseURL, input.Repository, input.OriginNumber, issue.Title, issue.Body)
	issue.Revision = input.OriginRevision
	task, err := s.CreateAgentTask(ctx, "reviewer", slug, input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `UPDATE agent_task_acceptances SET state='superseded' WHERE task_id IN(SELECT id FROM agent_tasks WHERE tenant_id=$1)`, tenant)
	})
	snapshot := domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: strings.Repeat("a", 40), Issue: &issue}
	sections, err := (agentplan.Planner{}).Generate(ctx, task, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.GeneratedPlan = &sections
	task, err = s.RecordAgentTaskSourceSnapshot(ctx, task.ID, snapshot)
	if err != nil {
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
	// Delivery/callback checks are exercised by TestAgentWorkflowPlansDelivery...
	// Seed their already-verified terminal result to isolate the review repair.
	attemptID := uuid.New()
	head := strings.Repeat("b", 40)
	exec(`INSERT INTO agent_task_attempts(id,task_id,plan_id,task_revision,plan_revision,attempt,state,branch_name,head_sha,pull_request_number,pull_request_url,verification_profile_sha256,verification_output_sha256,finished_at) VALUES($1,$2,$3,$4,$5,1,'succeeded',$6,$7,22,'https://github.com/team/repo/pull/22',$8,$9,now())`, attemptID, task.ID, plan.ID, task.Revision, plan.Revision, task.ExecutionBranch, head, strings.Repeat("c", 64), strings.Repeat("d", 64))
	exec(`UPDATE agent_tasks SET state='completed' WHERE id=$1`, task.ID)
	attempt, err := scanAgentTaskAttempt(s.pool.QueryRow(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts WHERE id=$1`, attemptID))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = recordAgentDeliveryTx(ctx, tx, task, attempt); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	target, err := s.ClaimAgentWorkflow(ctx, "repair-monitor")
	if err != nil || target.Task.ID != task.ID {
		t.Fatalf("claim %+v %v", target, err)
	}
	event := domain.InboundEvent{Provider: task.Provider, APIBaseURL: task.APIBaseURL, InstallationExternalID: installation.String(), Repository: task.Repository, ReviewNumber: 22, CloneURL: "https://github.com/team/repo.git", HeadRef: task.ExecutionBranch, HeadSHA: head, BaseRef: "main", BaseSHA: snapshot.BaseSHA, DeliveryID: "repair-review-" + task.ID.String(), EventName: "agent_delivery", Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(), TriggerKind: "manual", ActorKind: "system", ActorSubject: "reviewer", IsDraft: true}
	if err = s.EnsureAgentRereview(ctx, *target, event); err != nil {
		t.Fatal(err)
	}
	var runID, jobID uuid.UUID
	if err = s.pool.QueryRow(ctx, `SELECT r.id,r.legacy_job_id FROM review_runs r JOIN review_jobs j ON j.id=r.legacy_job_id WHERE j.installation_id=$1`, installation).Scan(&runID, &jobID); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE review_runs SET state='completed' WHERE id=$1`, runID)
	exec(`UPDATE review_jobs SET state='succeeded' WHERE id=$1`, jobID)
	exec(`INSERT INTO review_merge_gate_decisions(run_id,enabled,threshold,conclusion,blocking_findings,finding_count,configuration_content_sha256,origin_scope_kind,origin_revision,evaluation_version) VALUES($1,true,'high','failure',1,1,$2,'default',0,'fixture')`, runID, strings.Repeat("f", 64))
	exec(`INSERT INTO review_findings(job_id,path,start_line,end_line,severity,category,body,suggestion,fingerprint,provider_marker) VALUES($1,'internal/worker.go',10,10,'high','correctness','Retry loop still retries three times','Bound the loop to two retries',$2,$3)`, jobID, "fixture-"+runID.String(), "fixture-marker-"+runID.String())
	if err = s.FinishAgentWorkflowObservation(ctx, "repair-monitor", *target, head, "open"); err != nil {
		t.Fatal(err)
	}
	detail, err = s.GetAgentTask(ctx, "owner", slug, task.ID)
	if err != nil || detail.Acceptance == nil || detail.Acceptance.RemediationTaskID == nil || detail.Acceptance.State != "checks_failed" {
		t.Fatalf("missing repair: %+v %v", detail.Acceptance, err)
	}
	childID := *detail.Acceptance.RemediationTaskID
	exec(`UPDATE agent_task_acceptances SET poll_after=now()-interval '1 second' WHERE task_id=$1`, task.ID)
	target, err = s.ClaimAgentWorkflow(ctx, "repair-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishAgentWorkflowObservation(ctx, "repair-monitor", *target, head, "open"); err != nil {
		t.Fatal(err)
	}
	var children int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_feedback_cycles WHERE source_review_run_id=$1`, runID).Scan(&children); err != nil || children != 1 {
		t.Fatalf("repair duplicated: %d %v", children, err)
	}
	source, err := s.LoadAgentTaskSourceTarget(ctx, childID)
	if err != nil || source.Feedback == nil || !source.Feedback.ExecutionValid() || source.Feedback.SourceReviewRunID == nil || *source.Feedback.SourceReviewRunID != runID || source.Task.ExecutionBranch != task.ExecutionBranch {
		t.Fatalf("review binding %+v %v", source, err)
	}
	binding := *source.Feedback
	feedback := domain.AgentTaskFeedbackSnapshot{CommentExternalID: binding.CommentExternalID, ActorExternalID: binding.ActorExternalID, Instruction: binding.SystemInstruction}
	bad := feedback
	bad.Instruction += " widened"
	badSnapshot := domain.AgentTaskSourceSnapshot{BaseRef: task.ExecutionBranch, BaseSHA: head, TargetBranch: "main", Feedback: &bad}
	if _, err = s.RecordAgentTaskSourceSnapshot(ctx, childID, badSnapshot); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("changed diagnostics accepted: %v", err)
	}
	repairedSource := domain.AgentTaskSourceSnapshot{BaseRef: task.ExecutionBranch, BaseSHA: head, TargetBranch: "main", Feedback: &feedback}
	generated, err := (agentplan.Planner{}).Generate(ctx, source.Task, repairedSource)
	if err != nil {
		t.Fatal(err)
	}
	repairedSource.GeneratedPlan = &generated
	child, err := s.RecordAgentTaskSourceSnapshot(ctx, childID, repairedSource)
	if err != nil || child.State != "awaiting_approval" {
		t.Fatalf("repair plan: %+v %v", child, err)
	}
	childDetail, err := s.GetAgentTask(ctx, "owner", slug, childID)
	if err != nil || len(childDetail.Plans) != 1 || len(childDetail.Plans[0].Sections.AcceptanceCriteria) != 4 || len(childDetail.Attempts) != 0 || childDetail.Feedback == nil || childDetail.Feedback.SourceReviewRunID == nil || *childDetail.Feedback.SourceReviewRunID != runID {
		t.Fatalf("repair lost requirements or self-approved: %+v %v", childDetail, err)
	}
	childPlan := childDetail.Plans[0]
	if _, err = s.ApproveAgentTaskPlan(ctx, "reviewer", slug, childID, childPlan.ID, domain.AgentTaskPlanApprovalInput{Revision: childPlan.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer repair approval: %v", err)
	}
	if _, err = s.ApproveAgentTaskPlan(ctx, "owner", slug, childID, childPlan.ID, domain.AgentTaskPlanApprovalInput{Revision: childPlan.Revision}); err != nil {
		t.Fatal(err)
	}
}
