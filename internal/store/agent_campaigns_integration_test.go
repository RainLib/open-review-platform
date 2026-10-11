package store

import (
	"context"
	"errors"
	"github.com/RainLib/open-review-platform/internal/agentplan"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func campaignFixture(t *testing.T) (*PostgresStore, context.Context, string, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, isolatedQueueDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	tenant, installation := uuid.New(), uuid.New()
	slug := "campaign-" + tenant.String()[:8]
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Campaign fixture')`, tenant, slug)
	exec(`INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'author','admin',true),($1,'approver','owner',true),($1,'viewer','viewer',true)`, tenant)
	exec(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,'team/*','https://api.github.com','fixture','verified')`, installation, tenant, installation.String())
	exec(`INSERT INTO provider_health_probes(installation_id,state,health_state,observed_at,receipt) VALUES($1,'completed','live',now(),' {"inventory_state":"synchronized"}') ON CONFLICT(installation_id) DO UPDATE SET observed_at=now(),receipt=EXCLUDED.receipt`, installation)
	exec(`INSERT INTO provider_repository_inventory(installation_id,external_id,name,last_seen_at) SELECT $1,i::text,'team/repo-'||i,(SELECT observed_at FROM provider_health_probes WHERE installation_id=$1) FROM generate_series(1,3) i`, installation)
	for _, repo := range []string{"team/repo-1", "team/repo-2", "team/repo-3"} {
		w := domain.AgentWorkflowPolicy{Enabled: true, RequireCriterionEvidence: true, MaxRepairCycles: 2, MaxTaskAttempts: 3, RequiredChecks: []string{"CI / docs"}}
		if _, err = s.SaveAgentTaskPolicy(ctx, "approver", slug, domain.AgentTaskPolicyInput{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: repo, Mode: "manual", DecisionBackend: "deterministic", Workflow: &w}); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx, slug, tenant, installation
}
func campaignInput(installation uuid.UUID) domain.AgentCampaignInput {
	return domain.AgentCampaignInput{IdempotencyKey: uuid.NewString(), Title: "Replace old documentation links", Mode: "replace", Requirements: "Observed behavior: the documentation contains old documentation links. Expected behavior: replace the old documentation links in the frozen README.md files, preserve unrelated content, and verify every changed link.", AcceptanceCriteria: []string{"Old documentation links are replaced"}, Paths: []string{"README.md"}, Search: "old", Replacement: "new", AllRepositories: true, InstallationIDs: []uuid.UUID{installation}, Concurrency: 1}
}
func finishCampaignFixtureScan(t *testing.T, s *PostgresStore, ctx context.Context, match bool) {
	t.Helper()
	c, target, err := s.ClaimAgentCampaignScan(ctx, "scan-fixture")
	if err != nil {
		t.Fatal(err)
	}
	receipt := domain.AgentCampaignScan{BaseRef: "main", BaseSHA: strings.Repeat("a", 40), Complete: true, FilesScanned: 1, Files: []domain.AgentCampaignFile{}}
	snapshot := domain.AgentTaskSourceSnapshot{}
	if match {
		receipt.Matches = 1
		receipt.Files = []domain.AgentCampaignFile{{Path: "README.md", SHA256: strings.Repeat("b", 64), Matches: 1, Bytes: 3}}
		target.Scan = receipt
		b := CampaignBinding(c, target)
		snapshot = domain.AgentTaskSourceSnapshot{BaseRef: receipt.BaseRef, BaseSHA: receipt.BaseSHA, Campaign: &b}
		plan, e := (agentplan.Planner{}).Generate(ctx, target.PlanningTask, snapshot)
		if e != nil {
			t.Fatal(e)
		}
		snapshot.GeneratedPlan = &plan
	}
	if err = s.FinishAgentCampaignScan(ctx, "scan-fixture", target, receipt, snapshot, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishAgentCampaignScan(ctx, "scan-fixture", target, receipt, snapshot, ""); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("duplicate scan overwrote the target: %v", err)
	}
}
func TestCampaignApprovalConcurrencyRecoveryAndCancellation(t *testing.T) {
	s, ctx, slug, tenant, installation := campaignFixture(t)
	input := campaignInput(installation)
	if _, err := s.CreateAgentCampaign(ctx, "viewer", slug, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer created campaign: %v", err)
	}
	c, err := s.CreateAgentCampaign(ctx, "author", slug, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateAgentCampaign(ctx, "author", slug, input)
	if err != nil || again.ID != c.ID {
		t.Fatalf("idempotency: %v", err)
	}
	changed := input
	changed.Search = "different"
	if _, err = s.CreateAgentCampaign(ctx, "author", slug, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed idempotent request: %v", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE agent_campaigns SET input='{}' WHERE id=$1`, c.ID); err == nil {
		t.Fatal("campaign request was mutable")
	}
	finishCampaignFixtureScan(t, s, ctx, true)
	finishCampaignFixtureScan(t, s, ctx, true)
	finishCampaignFixtureScan(t, s, ctx, false)
	d, err := s.GetAgentCampaign(ctx, "approver", slug, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Targets) != 3 || d.Summary.Closed || d.Summary.Counts["no_match"] != 1 {
		t.Fatalf("scan summary: %+v", d.Summary)
	}
	approval := domain.AgentCampaignApproval{Revision: c.Revision}
	requests := []domain.AgentTaskExecutionRequest{}
	for _, target := range d.Targets {
		if target.Detail == nil {
			continue
		}
		task := target.Detail.Task
		p := target.Detail.Plans[0]
		approval.Plans = append(approval.Plans, domain.AgentCampaignPlanApproval{TaskID: task.ID, PlanID: p.ID, Revision: p.Revision, SHA256: p.PlanSHA256})
		requests = append(requests, domain.AgentTaskExecutionRequest{TaskID: task.ID, TaskRevision: task.Revision + 1, PlanID: p.ID, PlanRevision: p.Revision, PlanSHA256: p.PlanSHA256})
		if task.OriginKind != "campaign" || task.OriginNumber != 0 {
			t.Fatal("campaign faked a provider Issue")
		}
		if _, err = s.pool.Exec(ctx, `UPDATE agent_campaign_targets SET scan='{}' WHERE id=$1`, target.ID); err == nil {
			t.Fatal("task source was mutable")
		}
	}
	if err = s.ApproveAgentCampaign(ctx, "author", slug, c.ID, approval); !errors.Is(err, ErrForbidden) {
		t.Fatalf("default self approval: %v", err)
	}
	bad := approval
	bad.Plans = append([]domain.AgentCampaignPlanApproval(nil), approval.Plans...)
	bad.Plans[len(bad.Plans)-1].SHA256 = strings.Repeat("0", 64)
	if err = s.ApproveAgentCampaign(ctx, "approver", slug, c.ID, bad); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed plan digest: %v", err)
	}
	first, _ := s.GetAgentTask(ctx, "approver", slug, requests[0].TaskID)
	if first.Task.State != "awaiting_approval" {
		t.Fatal("partial approval committed before failure")
	}
	if err = s.ApproveAgentCampaign(ctx, "approver", slug, c.ID, approval); err != nil {
		t.Fatal(err)
	}
	act := func(action string) {
		t.Helper()
		d, e := s.GetAgentCampaign(ctx, "approver", slug, c.ID)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.ActAgentCampaign(ctx, "approver", slug, c.ID, domain.AgentCampaignAction{Revision: d.Campaign.Revision, Action: action, Reason: "Operator fixture verifies lifecycle"}); e != nil {
			t.Fatal(e)
		}
	}
	act("pause")
	if _, err = s.ClaimAgentTaskAttempt(ctx, "executor", requests[0], time.Minute); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("paused work leased: %v", err)
	}
	act("resume")
	a, err := s.ClaimAgentTaskAttempt(ctx, "executor", requests[0], time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimAgentTaskAttempt(ctx, "competing", requests[1], time.Minute); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("concurrency exceeded: %v", err)
	}
	target, err := s.LoadAgentTaskAttemptTarget(ctx, a.ID, "executor")
	if err != nil || target.Campaign == nil || !target.Campaign.Valid() {
		t.Fatalf("signed campaign binding: %+v %v", target.Campaign, err)
	}
	if err = s.MarkAgentTaskAttemptNeedsAttention(ctx, a.ID, "executor", "fixture_verification_failed", "Verification fixture needs a new approved plan"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimAgentTaskAttempt(ctx, "executor", requests[1], time.Minute); err != nil {
		t.Fatal(err)
	}
	act("retry_failed")
	first, err = s.GetAgentTask(ctx, "approver", slug, requests[0].TaskID)
	if err != nil || first.Task.State != "awaiting_approval" || len(first.Plans) != 2 || first.ExecutionBudget.Used != 1 {
		t.Fatalf("retry approval or budget reset: %+v %v", first, err)
	}
	if err = s.ScheduleAgentCampaignExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	act("cancel")
	if _, _, err = s.RecordAgentTaskAdapterEvent(ctx, domain.AgentTaskAdapterEvent{AttemptID: a.ID, AdapterJobID: "cancelled-fixture", DeliveryID: "late", Kind: "failed", ErrorCode: "late"}, time.Minute); err == nil {
		t.Fatal("cancelled callback resurrected work")
	}
	d, err = s.GetAgentCampaign(ctx, "approver", slug, c.ID)
	if err != nil || d.Summary.Closed || d.Campaign.State != "cancelled" {
		t.Fatalf("cancel report: %+v %v", d.Summary, err)
	}
	if _, err = s.GetAgentCampaign(ctx, "outsider", slug, c.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross tenant access: %v", err)
	}
	var spent int
	if err = s.pool.QueryRow(ctx, `SELECT sum(a.attempt) FROM agent_task_attempts a JOIN agent_tasks t ON t.id=a.task_id WHERE t.tenant_id=$1`, tenant).Scan(&spent); err != nil || spent != 2 {
		t.Fatalf("pause/retry created extra attempts: %d %v", spent, err)
	}
}
func TestCampaignAllSelectionRejectsPartialInventoryAndRetainsBeyondFiveHundred(t *testing.T) {
	s, ctx, slug, _, installation := campaignFixture(t)
	if _, err := s.pool.Exec(ctx, `UPDATE provider_health_probes SET receipt='{"inventory_state":"partial"}' WHERE installation_id=$1`, installation); err != nil {
		t.Fatal(err)
	}
	input := campaignInput(installation)
	input.Mode = "scan"
	if _, err := s.CreateAgentCampaign(ctx, "author", slug, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("partial all selection allowed: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE provider_health_probes SET receipt='{"inventory_state":"synchronized"}' WHERE installation_id=$1`, installation); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO provider_repository_inventory(installation_id,external_id,name,last_seen_at) SELECT $1,i::text,'team/repo-'||i,(SELECT observed_at FROM provider_health_probes WHERE installation_id=$1) FROM generate_series(4,603) i`, installation); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateAgentCampaign(ctx, "author", slug, input)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.GetAgentCampaign(ctx, "author", slug, c.ID)
	if err != nil || len(d.Targets) != 603 {
		t.Fatalf("all repositories truncated: %d %v", len(d.Targets), err)
	}
}
