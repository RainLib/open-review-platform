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

func TestAgentWorkflowPlansDeliveryRereviewAndRequirementAcceptance(t *testing.T) {
	for _, origin := range []string{"issue", "campaign"} {
		t.Run(origin, func(t *testing.T) { agentWorkflowClosureFixture(t, origin) })
	}
}
func agentWorkflowClosureFixture(t *testing.T, origin string) {
	if os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL") == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("requires an isolated PostgreSQL database")
	}
	ctx := context.Background()
	s, err := Open(ctx, isolatedQueueDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	tenant, installation := uuid.New(), uuid.New()
	slug := "workflow-" + tenant.String()[:8]
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Workflow fixture')`, tenant, slug)
	exec(`INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'owner','owner',true),($1,'reviewer','reviewer',true),($1,'campaign-author','admin',true)`, tenant)
	exec(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,'team/repo','https://api.github.com','fixture','verified')`, installation, tenant, installation.String())
	workflow := domain.AgentWorkflowPolicy{Enabled: true, RequireCriterionEvidence: true, MaxRepairCycles: 2, MaxTaskAttempts: 3, RequiredChecks: []string{"CI / tests"}}
	policyInput := domain.AgentTaskPolicyInput{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "team/repo", Mode: "manual", DecisionBackend: "deterministic", Workflow: &workflow, MaxFeedbackCycles: 2}
	policy, err := s.SaveAgentTaskPolicy(ctx, "owner", slug, policyInput)
	if err != nil {
		t.Fatal(err)
	}
	issue := domain.AgentTaskIssueSnapshot{Title: "Fix bounded retry status in the worker", Body: "Observed behavior: final failures disappear. Expected behavior: retain the final failure and retry exactly twice.\n## Acceptance criteria\n- Retry exactly twice\n- Show the final failure"}
	input := domain.AgentTaskInput{Provider: policyInput.Provider, APIBaseURL: policyInput.APIBaseURL, Repository: policyInput.Repository, OriginKind: "issue", OriginNumber: 18, Intent: "implement"}
	input.OriginRevision = domain.AgentIssueRevision(input.Provider, input.APIBaseURL, input.Repository, input.OriginNumber, issue.Title, issue.Body)
	issue.Revision = input.OriginRevision
	task, err := s.CreateAgentTask(ctx, "reviewer", slug, input)
	var campaignID uuid.UUID
	var campaignTarget domain.AgentCampaignTarget
	if origin == "campaign" {
		// The unused Issue task stays separate; campaign requests have no Issue number.
		_, _ = s.pool.Exec(ctx, `UPDATE agent_tasks SET state='cancelled' WHERE id=$1`, task.ID)
		exec(`INSERT INTO provider_repository_inventory(installation_id,external_id,name) VALUES($1,'repo','team/repo')`, installation)
		c, e := s.CreateAgentCampaign(ctx, "campaign-author", slug, domain.AgentCampaignInput{IdempotencyKey: uuid.NewString(), Title: "Verify campaign closure", Mode: "docs", Requirements: issue.Body, AcceptanceCriteria: []string{"Retry exactly twice", "Show the final failure"}, Paths: []string{"README.md"}, Repositories: []domain.AgentCampaignRepository{{InstallationID: installation, Repository: "team/repo"}}, Concurrency: 1})
		if e != nil {
			t.Fatal(e)
		}
		campaignID = c.ID
		_, campaignTarget, e = s.ClaimAgentCampaignScan(ctx, "campaign-scan")
		if e != nil {
			t.Fatal(e)
		}
		task = campaignTarget.PlanningTask
		task.Workflow = workflow
	}

	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `UPDATE agent_task_acceptances SET state='superseded' WHERE task_id=$1`, task.ID)
	})
	if !task.Workflow.Enabled {
		t.Fatal("workflow was not frozen on admission")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE agent_tasks SET workflow='{}' WHERE id=$1`, task.ID); err == nil && origin == "issue" {
		t.Fatal("workflow contract could be widened/changed")
	}
	snapshot := domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: strings.Repeat("a", 40), Issue: &issue}
	if origin == "campaign" {
		campaignTarget.Scan = domain.AgentCampaignScan{BaseRef: snapshot.BaseRef, BaseSHA: snapshot.BaseSHA, Complete: true, FilesScanned: 1, Files: []domain.AgentCampaignFile{{Path: "README.md", SHA256: strings.Repeat("1", 64)}}}
		b := CampaignBinding(domain.AgentCampaign{ID: campaignID, RequestSHA256: strings.Split(task.OriginRevision, ":")[2], Input: domain.AgentCampaignInput{Mode: "docs", Requirements: issue.Body, AcceptanceCriteria: []string{"Retry exactly twice", "Show the final failure"}, Paths: []string{"README.md"}}}, campaignTarget)
		snapshot.Issue = nil
		snapshot.Campaign = &b
	}
	planSections, err := (agentplan.Planner{}).Generate(ctx, task, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.GeneratedPlan = &planSections
	if origin == "campaign" {
		err = s.FinishAgentCampaignScan(ctx, "campaign-scan", campaignTarget, campaignTarget.Scan, snapshot, "")
		if err == nil {
			d, e := s.GetAgentTask(ctx, "owner", slug, task.ID)
			err = e
			task = d.Task
		}
	} else {
		task, err = s.RecordAgentTaskSourceSnapshot(ctx, task.ID, snapshot)
	}
	if err != nil || task.State != "awaiting_approval" {
		t.Fatalf("automatic plan: %s %v", task.State, err)
	}
	detail, err := s.GetAgentTask(ctx, "owner", slug, task.ID)
	if err != nil || len(detail.Plans) != 1 || len(detail.Plans[0].Sections.AcceptanceCriteria) != 2 {
		t.Fatalf("generated plan: %+v %v", detail.Plans, err)
	}
	// Repository edits cannot enable or expand a previously admitted task.
	disabled := domain.AgentWorkflowPolicy{}
	policyInput.Workflow = &disabled
	policyInput.Revision = policy.Revision
	if _, err = s.SaveAgentTaskPolicy(ctx, "owner", slug, policyInput); err != nil {
		t.Fatal(err)
	}
	plan := detail.Plans[0]
	weakPlan := planSections
	weakPlan.AcceptanceCriteria = weakPlan.AcceptanceCriteria[:1]
	if _, err = s.CreateAgentTaskPlan(ctx, "reviewer", slug, task.ID, domain.AgentTaskPlanInput{Sections: &weakPlan}); !errors.Is(err, ErrInvalidAgentTaskPlan) {
		t.Fatalf("original requirements weakened: %v", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE agent_task_requirements SET criteria='["Weakened criterion"]' WHERE task_id=$1`, task.ID); err == nil {
		t.Fatal("verified criteria mutable")
	}
	if _, err = s.ApproveAgentTaskPlan(ctx, "reviewer", slug, task.ID, plan.ID, domain.AgentTaskPlanApprovalInput{Revision: plan.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer approval: %v", err)
	}
	plan, err = s.ApproveAgentTaskPlan(ctx, "owner", slug, task.ID, plan.ID, domain.AgentTaskPlanApprovalInput{Revision: plan.Revision})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = s.GetAgentTask(ctx, "owner", slug, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := s.ClaimAgentTaskAttempt(ctx, "fixture-executor", domain.AgentTaskExecutionRequest{TaskID: task.ID, TaskRevision: detail.Task.Revision, PlanID: plan.ID, PlanRevision: plan.Revision, PlanSHA256: plan.PlanSHA256}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	jobID := "fixture-job-" + attempt.ID.String()
	if err = s.AttachAgentTaskAdapterJob(ctx, attempt.ID, "fixture-executor", jobID); err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimAgentTaskAdapterStart(ctx, attempt.ID, jobID); err != nil {
		t.Fatal(err)
	}
	event := domain.AgentTaskAdapterEvent{AttemptID: attempt.ID, AdapterJobID: jobID, DeliveryID: "fixture-completed-" + attempt.ID.String(), Kind: "completed", Summary: "Fixture delivery", BranchName: task.ExecutionBranch, HeadSHA: strings.Repeat("b", 40), PullRequestNumber: 19, PullRequestURL: "https://github.com/team/repo/pull/19", PatchSHA256: strings.Repeat("c", 64), ChangedFileCount: 1, DiffBytes: 123}
	if _, _, err = s.RecordAgentTaskAdapterEvent(ctx, event, time.Minute); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("unverified delivery allowed: %v", err)
	}
	event.VerificationProfileSHA256 = strings.Repeat("d", 64)
	event.VerificationOutputSHA256 = strings.Repeat("e", 64)
	event.VerificationOutputBytes = 45
	event.VerificationCriteria = []domain.AgentCriterionResult{{Criterion: "Retry exactly twice", Status: "passed", Evidence: "TestRetryLimits passed"}, {Criterion: "Show the final failure", Status: "passed", Evidence: "TestFinalFailure passed"}}
	checkpoint := event
	checkpoint.Kind = "publication_checkpoint"
	checkpoint.Summary = ""
	checkpoint.DeliveryID = "fixture-checkpoint-" + attempt.ID.String()
	checkpoint.PullRequestURL = ""
	checkpoint.PullRequestNumber = 0
	missingProof := checkpoint
	missingProof.VerificationCriteria = nil
	if _, _, err = s.RecordAgentTaskAdapterEvent(ctx, missingProof, time.Minute); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("unproved requirements published: %v", err)
	}
	if _, _, err = s.RecordAgentTaskAdapterEvent(ctx, checkpoint, time.Minute); err != nil {
		t.Fatal(err)
	}
	tampered := event
	tampered.VerificationCriteria = append([]domain.AgentCriterionResult(nil), event.VerificationCriteria...)
	tampered.VerificationCriteria[0].Evidence = "Different unbound evidence"
	if _, _, err = s.RecordAgentTaskAdapterEvent(ctx, tampered, time.Minute); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("changed criterion proof accepted: %v", err)
	}
	if _, _, err = s.RecordAgentTaskAdapterEvent(ctx, event, time.Minute); err != nil {
		t.Fatal(err)
	}
	target, err := s.ClaimAgentWorkflow(ctx, "fixture-monitor")
	if err != nil || target.Task.ID != task.ID {
		t.Fatalf("delivery target: %+v %v", target, err)
	}
	if _, err = s.ClaimAgentWorkflow(ctx, "competing-monitor"); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("concurrent delivery lease was duplicated: %v", err)
	}
	if err = s.FinishAgentWorkflowObservation(ctx, "wrong-monitor", *target, event.HeadSHA, "open"); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("foreign worker overwrote delivery: %v", err)
	}
	reviewEvent := domain.InboundEvent{Provider: task.Provider, APIBaseURL: task.APIBaseURL, InstallationExternalID: installation.String(), Repository: task.Repository, ReviewNumber: 19, CloneURL: "https://github.com/team/repo.git", HeadRef: task.ExecutionBranch, HeadSHA: event.HeadSHA, BaseRef: "main", BaseSHA: snapshot.BaseSHA, DeliveryID: "fixture-rereview-" + task.ID.String(), EventName: "agent_delivery", Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(), TriggerKind: "manual", ActorKind: "system", ActorSubject: "reviewer", IsDraft: true}
	if err = s.EnsureAgentRereview(ctx, *target, reviewEvent); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureAgentRereview(ctx, *target, reviewEvent); err != nil {
		t.Fatal(err)
	}
	var runID uuid.UUID
	var runs int
	if err = s.pool.QueryRow(ctx, `SELECT count(*),min(r.id::text)::uuid FROM review_runs r JOIN review_jobs j ON j.id=r.legacy_job_id WHERE j.installation_id=$1 AND j.head_sha=$2`, installation, event.HeadSHA).Scan(&runs, &runID); err != nil || runs != 1 {
		t.Fatalf("rereview idempotency: %d %v", runs, err)
	}
	exec(`UPDATE review_runs SET state='completed',finished_at=now() WHERE id=$1`, runID)
	exec(`UPDATE review_jobs SET state='succeeded' WHERE id=(SELECT legacy_job_id FROM review_runs WHERE id=$1)`, runID)
	exec(`INSERT INTO review_merge_gate_decisions(run_id,enabled,threshold,conclusion,blocking_findings,finding_count,configuration_content_sha256,origin_scope_kind,origin_revision,evaluation_version) VALUES($1,true,'high','success',0,0,$2,'default',0,'fixture')`, runID, strings.Repeat("f", 64))
	observe := func(head, state string) {
		t.Helper()
		if err := s.FinishAgentWorkflowObservation(ctx, "fixture-monitor", *target, head, state); err != nil {
			t.Fatal(err)
		}
	}
	observe(event.HeadSHA, "open")
	acceptance := func(actor string) domain.AgentTaskAcceptance {
		t.Helper()
		d, err := s.GetAgentTask(ctx, actor, slug, task.ID)
		if err != nil || d.Acceptance == nil {
			t.Fatalf("delivery read: %v", err)
		}
		return *d.Acceptance
	}
	a := acceptance("owner")
	if a.State != "reviewing" {
		t.Fatalf("missing CI inferred as acceptance: %s", a.State)
	}
	exec(`INSERT INTO review_provider_check_observations(run_id,head_sha,state,checks,observed_at) VALUES($1,$2,'observed','[{"name":"CI / tests","state":"success","origin":"independent"}]',now()) ON CONFLICT(run_id) DO UPDATE SET state='observed',checks=EXCLUDED.checks,observed_at=now()`, runID, event.HeadSHA)
	lease := func() {
		t.Helper()
		exec(`UPDATE agent_task_acceptances SET poll_after=now()-interval '1 second' WHERE task_id=$1`, task.ID)
		var err error
		target, err = s.ClaimAgentWorkflow(ctx, "fixture-monitor")
		if err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE review_provider_check_observations SET state='queued',error_code='github_commit_status_read_forbidden' WHERE run_id=$1`, runID)
	lease()
	observe(event.HeadSHA, "open")
	a = acceptance("owner")
	if a.State != "needs_attention" || a.CanDecide || !strings.Contains(a.Reason, "Commit statuses read permission") {
		t.Fatalf("missing provider permission inferred acceptance or hid recovery: %+v", a)
	}
	exec(`UPDATE review_provider_check_observations SET state='observed',error_code='' WHERE run_id=$1`, runID)
	lease()
	observe(event.HeadSHA, "open")
	a = acceptance("owner")
	if a.State != "awaiting_acceptance" || !a.CanDecide || acceptance("reviewer").CanDecide {
		t.Fatalf("acceptance permissions/state: %+v", a)
	}
	decision := domain.AgentTaskAcceptanceInput{Revision: a.Revision, HeadSHA: a.HeadSHA, Decision: "accepted", Evidence: []string{"Retry test passed", "Failure visible in regression fixture"}, Reason: "Every criterion verified at this commit"}
	if _, err = s.DecideAgentTaskAcceptance(ctx, "reviewer", slug, task.ID, decision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer accepted: %v", err)
	}
	stale := decision
	stale.Revision--
	if _, err = s.DecideAgentTaskAcceptance(ctx, "owner", slug, task.ID, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale decision accepted: %v", err)
	}
	missing := decision
	missing.Evidence = missing.Evidence[:1]
	if _, err = s.DecideAgentTaskAcceptance(ctx, "owner", slug, task.ID, missing); !errors.Is(err, ErrConflict) {
		t.Fatalf("partial criteria accepted: %v", err)
	}
	exec(`UPDATE provider_installations SET active=false WHERE id=$1`, installation)
	if _, err = s.DecideAgentTaskAcceptance(ctx, "owner", slug, task.ID, decision); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked installation accepted: %v", err)
	}
	exec(`UPDATE provider_installations SET active=true WHERE id=$1`, installation)
	accepted, err := s.DecideAgentTaskAcceptance(ctx, "owner", slug, task.ID, decision)
	if err != nil || accepted.State != "accepted" || len(accepted.Evidence) != 2 {
		t.Fatalf("acceptance: %+v %v", accepted, err)
	}
	if origin == "campaign" {
		d, e := s.GetAgentCampaign(ctx, "owner", slug, campaignID)
		if e != nil || !d.Summary.Closed || d.Summary.Counts["accepted"] != 1 {
			t.Fatalf("campaign accepted summary: %+v %v", d.Summary, e)
		}
	}
	// Temporary observation loss suspends readiness but preserves the decision.
	lease()
	observe("", "unavailable")
	if a = acceptance("owner"); a.State != "needs_attention" || a.Decision != "accepted" {
		t.Fatalf("decision lost during outage: %+v", a)
	}
	lease()
	observe(event.HeadSHA, "open")
	if a = acceptance("owner"); a.State != "accepted" {
		t.Fatalf("acceptance did not recover: %+v", a)
	}
	exec(`UPDATE review_provider_check_observations SET state='running' WHERE run_id=$1`, runID)
	lease()
	observe(event.HeadSHA, "open")
	if a = acceptance("owner"); a.State != "reviewing" || a.Decision != "accepted" {
		t.Fatalf("CI refresh erased decision: %+v", a)
	}
	exec(`UPDATE review_provider_check_observations SET state='observed' WHERE run_id=$1`, runID)
	lease()
	observe(event.HeadSHA, "open")
	if a = acceptance("owner"); a.State != "accepted" || a.DecisionReason != decision.Reason {
		t.Fatalf("CI recovery lost decision evidence: %+v", a)
	}
	// Acceptance must still be observable after the usual 24-hour CI window.
	exec(`UPDATE review_runs SET created_at=now()-interval '2 days' WHERE id=$1`, runID)
	exec(`UPDATE review_provider_check_observations SET available_at=now()-interval '100 years' WHERE run_id=$1`, runID)
	probe, probeErr := s.ClaimProviderCheckProbe(ctx, "fixture-checks", time.Minute)
	if probeErr != nil || probe.RunID != runID {
		t.Fatalf("workflow CI stopped after 24 hours: %+v %v", probe, probeErr)
	}
	now := time.Now()
	if err = s.CompleteProviderCheckProbe(ctx, *probe, domain.ProviderCheckObservation{Provider: task.Provider, Repository: task.Repository, HeadSHA: event.HeadSHA, State: "observed", ObservedAt: &now, Checks: []domain.ProviderCheck{{Name: "CI / tests", State: "success", Origin: "independent"}}}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	lease()
	observe(event.HeadSHA, "open")
	if a = acceptance("owner"); a.State != "accepted" {
		t.Fatalf("accepted state lost: %+v", a)
	}
	exec(`UPDATE review_provider_check_observations SET state='failed' WHERE run_id=$1`, runID)
	lease()
	observe(event.HeadSHA, "open")
	a = acceptance("owner")
	if _, err = s.RetryAgentTaskChecks(ctx, "reviewer", slug, task.ID, a.Revision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer retried checks: %v", err)
	}
	if _, err = s.RetryAgentTaskChecks(ctx, "owner", slug, task.ID, a.Revision-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale check retry: %v", err)
	}
	if _, err = s.RetryAgentTaskChecks(ctx, "owner", slug, task.ID, a.Revision); err != nil {
		t.Fatal(err)
	}
	var checkState string
	if err = s.pool.QueryRow(ctx, `SELECT state FROM review_provider_check_observations WHERE run_id=$1`, runID).Scan(&checkState); err != nil || checkState != "queued" {
		t.Fatalf("check retry did not queue: %s %v", checkState, err)
	}
	a = acceptance("owner")
	if _, err = s.RetryAgentTaskChecks(ctx, "owner", slug, task.ID, a.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("active check retry duplicated: %v", err)
	}
	exec(`UPDATE review_provider_check_observations SET state='observed',observed_at=now() WHERE run_id=$1`, runID)

	lease()
	observe(strings.Repeat("c", 40), "open")
	if a = acceptance("owner"); a.State != "superseded" {
		t.Fatalf("changed commit inherited acceptance: %+v", a)
	}
	// Shared branch budget covers future plans/feedback, rather than resetting.
	exec(`UPDATE agent_task_attempts SET attempt=3 WHERE id=$1`, attempt.ID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = checkAgentWorkflowBudgetTx(ctx, tx, target.Task); !errors.Is(err, ErrInvalidAgentTaskPlan) {
		t.Fatalf("branch budget reset: %v", err)
	}
	budgetDetail, detailErr := s.GetAgentTask(ctx, "owner", slug, task.ID)
	if detailErr != nil || budgetDetail.PlanPermissions.CanApprovePlan || budgetDetail.PlanPermissions.CreateBlockReason != "execution_budget_exhausted" {
		t.Fatalf("exhausted budget was not visible: %+v %v", budgetDetail.PlanPermissions, detailErr)
	}

}
