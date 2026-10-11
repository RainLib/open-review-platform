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
	for _, origin := range []string{"issue", "campaign"} {
		t.Run(origin, func(t *testing.T) { testAgentInternalRepair(t, "review", origin) })
	}
}
func TestAgentAcceptanceRejectionPreparesRepairWithoutManualFeedbackBudget(t *testing.T) {
	for _, origin := range []string{"issue", "campaign"} {
		t.Run(origin, func(t *testing.T) { testAgentInternalRepair(t, "acceptance", origin) })
	}
}
func TestAgentCIFailurePreparesOnlyDiagnosedCodeRepair(t *testing.T) {
	for _, origin := range []string{"issue", "campaign"} {
		t.Run(origin, func(t *testing.T) { testAgentInternalRepair(t, "ci", origin) })
	}
}
func testAgentInternalRepair(t *testing.T, kind, origin string) {
	if os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL") == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	s, err := Open(ctx, isolatedQueueDatabase(t))
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
	exec(`INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'owner','owner',true),($1,'reviewer','reviewer',true),($1,'campaign-author','admin',true)`, tenant)
	exec(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,'team/repo','https://api.github.com','fixture','verified')`, installation, tenant, installation.String())
	workflow := domain.AgentWorkflowPolicy{Enabled: true, MaxRepairCycles: 2, MaxTaskAttempts: 3}
	if origin == "campaign" {
		workflow.RequireCriterionEvidence = true
		workflow.RequiredChecks = []string{"go tests"}
	}
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
	if origin == "campaign" {
		_, _ = s.pool.Exec(ctx, `UPDATE agent_tasks SET state='cancelled' WHERE id=$1`, task.ID)
		exec(`INSERT INTO provider_repository_inventory(installation_id,external_id,name) VALUES($1,'repo','team/repo')`, installation)
		c, e := s.CreateAgentCampaign(ctx, "campaign-author", slug, domain.AgentCampaignInput{IdempotencyKey: uuid.NewString(), Title: "Campaign repair fixture", Mode: "docs", Requirements: issue.Body, AcceptanceCriteria: []string{"Retry exactly twice", "Retain the final failure"}, Paths: []string{"README.md", "internal/**"}, Repositories: []domain.AgentCampaignRepository{{InstallationID: installation, Repository: "team/repo"}}, Concurrency: 1})
		if e != nil {
			t.Fatal(e)
		}
		_, ct, e := s.ClaimAgentCampaignScan(ctx, "campaign-repair-scan")
		if e != nil {
			t.Fatal(e)
		}
		receipt := domain.AgentCampaignScan{BaseRef: "main", BaseSHA: snapshot.BaseSHA, Complete: true, FilesScanned: 1, Files: []domain.AgentCampaignFile{{Path: "README.md", SHA256: strings.Repeat("1", 64)}}}
		ct.Scan = receipt
		b := CampaignBinding(c, ct)
		snapshot = domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: receipt.BaseSHA, Campaign: &b}
		generated, e := (agentplan.Planner{}).Generate(ctx, ct.PlanningTask, snapshot)
		if e != nil {
			t.Fatal(e)
		}
		snapshot.GeneratedPlan = &generated
		if e = s.FinishAgentCampaignScan(ctx, "campaign-repair-scan", ct, receipt, snapshot, ""); e != nil {
			t.Fatal(e)
		}
		d, e := s.GetAgentTask(ctx, "owner", slug, ct.PlanningTask.ID)
		if e != nil {
			t.Fatal(e)
		}
		task = d.Task
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
	if kind != "review" {
		exec(`UPDATE review_merge_gate_decisions SET conclusion='success',blocking_findings=0 WHERE run_id=$1`, runID)
		checks := `[{"name":"go tests","state":"success","origin":"independent"}]`
		if kind == "ci" {
			checks = `[{"name":"go tests","state":"failure","origin":"independent","diagnostics":"--- FAIL: TestRetry: expected two retries, got three","failure_class":"code"}]`
		}
		exec(`INSERT INTO review_provider_check_observations(run_id,head_sha,state,checks,observed_at) VALUES($1,$2,'observed',$3,now())`, runID, head, checks)
	}
	if kind == "ci" {
		for _, checks := range []string{
			`[{"name":"go tests","state":"failure","origin":"independent","diagnostics":"connection refused","failure_class":"infrastructure"}]`,
			`[{"name":"go tests","state":"pending","origin":"independent","diagnostics":"test failed","failure_class":"code"}]`,
			`[{"name":"go tests","state":"failure","origin":"independent"}]`,
		} {
			exec(`UPDATE review_provider_check_observations SET checks=$2 WHERE run_id=$1`, runID, checks)
			if err = s.FinishAgentWorkflowObservation(ctx, "repair-monitor", *target, head, "open"); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_feedback_cycles WHERE parent_task_id=$1`, task.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("unrepairable CI started repair: %d %v", count, err)
			}
			exec(`UPDATE agent_task_acceptances SET poll_after=now()-interval '1 second' WHERE task_id=$1`, task.ID)
			target, err = s.ClaimAgentWorkflow(ctx, "repair-monitor")
			if err != nil {
				t.Fatal(err)
			}
		}
		exec(`UPDATE review_provider_check_observations SET checks='[{"name":"go tests","state":"failure","origin":"independent","diagnostics":"--- FAIL: TestRetry: expected two retries, got three","failure_class":"code"}]' WHERE run_id=$1`, runID)
	}

	if err = s.FinishAgentWorkflowObservation(ctx, "repair-monitor", *target, head, "open"); err != nil {
		t.Fatal(err)
	}
	if kind == "acceptance" {
		d, loadErr := s.GetAgentTask(ctx, "owner", slug, task.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		decision := domain.AgentTaskAcceptanceInput{Revision: d.Acceptance.Revision, HeadSHA: head, Decision: "changes_requested", Reason: "Observed behavior: final failure message is missing. Expected behavior: retain the original worker error and verify it with a regression test."}
		rejectedDecision := decision
		if _, err = s.DecideAgentTaskAcceptance(ctx, "reviewer", slug, task.ID, rejectedDecision); !errors.Is(err, ErrForbidden) {
			t.Fatalf("reviewer requested changes: %v", err)
		}
		if _, err = s.DecideAgentTaskAcceptance(ctx, "owner", slug, task.ID, decision); err != nil {
			t.Fatal(err)
		}
	}

	detail, err = s.GetAgentTask(ctx, "owner", slug, task.ID)
	if err != nil || detail.Acceptance == nil || detail.Acceptance.RemediationTaskID == nil || (kind != "acceptance" && detail.Acceptance.State != "checks_failed") {
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
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_feedback_cycles WHERE parent_task_id=$1`, task.ID).Scan(&children); err != nil || children != 1 {
		t.Fatalf("repair duplicated: %d %v", children, err)
	}
	source, err := s.LoadAgentTaskSourceTarget(ctx, childID)
	if err != nil || source.Feedback == nil || !source.Feedback.ExecutionValid() || source.Feedback.InternalRepairKind != kind || (kind != "acceptance" && (source.Feedback.SourceReviewRunID == nil || *source.Feedback.SourceReviewRunID != runID)) || source.Task.ExecutionBranch != task.ExecutionBranch {
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
	if err != nil || len(childDetail.Plans) != 1 || len(childDetail.Plans[0].Sections.AcceptanceCriteria) != 2 || len(childDetail.Attempts) != 0 || childDetail.Feedback == nil || childDetail.Feedback.InternalRepairKind != kind || childDetail.Plans[0].Sections.SourceRequirements != issue.Body {
		t.Fatalf("repair lost requirements or self-approved: %+v %v", childDetail, err)
	}
	if origin == "campaign" {
		binding, e := s.loadCampaignBinding(ctx, childDetail.Task)
		if e != nil || binding == nil || !binding.Valid() {
			t.Fatalf("repair lost campaign path scope: %+v %v", binding, e)
		}
	}
	childPlan := childDetail.Plans[0]
	if _, err = s.ApproveAgentTaskPlan(ctx, "reviewer", slug, childID, childPlan.ID, domain.AgentTaskPlanApprovalInput{Revision: childPlan.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer repair approval: %v", err)
	}
	if _, err = s.ApproveAgentTaskPlan(ctx, "owner", slug, childID, childPlan.ID, domain.AgentTaskPlanApprovalInput{Revision: childPlan.Revision}); err != nil {
		t.Fatal(err)
	}
}
