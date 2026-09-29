package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRuleRolloutsPairQualifiedBindingsAndFenceStateTransitions(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	if os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("rule rollout integration requires an isolated database")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(postgres.Close)

	tenantID, ruleSetID := uuid.New(), uuid.New()
	baselineVersion, candidateVersion := uuid.New(), uuid.New()
	baselineBinding, candidateBinding, mismatchedBinding := uuid.New(), uuid.New(), uuid.New()
	installationID, deliveryID, jobID, requestID, sourceRunID, baselineSnapshotID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tenantSlug, actor := "rule-rollout-"+tenantID.String()[:8], "rule-rollout-owner"
	t.Cleanup(func() {
		batch := &pgx.Batch{}
		batch.Queue(`DELETE FROM rule_rollout_run_selections WHERE rollout_id IN (SELECT id FROM rule_rollouts WHERE tenant_id=$1)`, tenantID)
		batch.Queue(`DELETE FROM rule_rollouts WHERE tenant_id=$1 AND mode='canary'`, tenantID)
		batch.Queue(`DELETE FROM rule_rollout_comparisons WHERE rollout_id IN (SELECT id FROM rule_rollouts WHERE tenant_id=$1)`, tenantID)
		batch.Queue(`DELETE FROM rule_test_runs WHERE tenant_id=$1`, tenantID)
		batch.Queue(`DELETE FROM rule_rollouts WHERE tenant_id=$1`, tenantID)
		batch.Queue(`DELETE FROM review_requests WHERE id=$1`, requestID)
		batch.Queue(`DELETE FROM review_requests WHERE tenant_id=$1 AND review_number=9`, tenantID)
		batch.Queue(`DELETE FROM review_jobs WHERE id=$1`, jobID)
		batch.Queue(`DELETE FROM webhook_deliveries WHERE id=$1`, deliveryID)
		batch.Queue(`DELETE FROM rule_snapshots WHERE tenant_id=$1`, tenantID)
		batch.Queue(`DELETE FROM rule_bindings WHERE tenant_id=$1`, tenantID)
		batch.Queue(`DELETE FROM rule_versions WHERE rule_set_id=$1`, ruleSetID)
		batch.Queue(`DELETE FROM rule_sets WHERE id=$1`, ruleSetID)
		batch.Queue(`DELETE FROM tenants WHERE id=$1`, tenantID)
		if err := postgres.pool.SendBatch(context.Background(), batch).Close(); err != nil {
			t.Errorf("clean rule rollout fixture: %v", err)
		}
	})
	baselineRules := []rules.Rule{{Key: "rollout.boundary", Enforcement: rules.Advisory, MergeBehavior: rules.Replace, Severity: "medium", Content: json.RawMessage(`{"instruction":"baseline"}`)}}
	candidateRules := []rules.Rule{{Key: "rollout.boundary", Enforcement: rules.Advisory, MergeBehavior: rules.Replace, Severity: "high", Content: json.RawMessage(`{"instruction":"candidate"}`)}}
	baselineRaw, err := json.Marshal(baselineRules)
	if err != nil {
		t.Fatal(err)
	}
	candidateRaw, err := json.Marshal(candidateRules)
	if err != nil {
		t.Fatal(err)
	}
	baselineCompiled, err := rules.Compile([]rules.Source{{VersionID: baselineVersion.String(), Precedence: 100, Rules: baselineRules, Include: []string{"services/**"}}})
	if err != nil {
		t.Fatal(err)
	}
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Rule rollout integration')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,$2,'owner',TRUE)`, tenantID, actor)
	batch.Queue(`INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,$2,'admin',TRUE)`, tenantID, "rule-rollout-approver")
	batch.Queue(`INSERT INTO rule_sets(id,tenant_id,name,created_by) VALUES($1,$2,'Rollout rules','test')`, ruleSetID, tenantID)
	batch.Queue(`INSERT INTO rule_versions(id,rule_set_id,version,state,rules,content_sha256,created_by) VALUES($1,$2,1,'published',$3::jsonb,'rollout-baseline','test')`, baselineVersion, ruleSetID, string(baselineRaw))
	batch.Queue(`INSERT INTO rule_versions(id,rule_set_id,version,state,rules,content_sha256,created_by) VALUES($1,$2,2,'published',$3::jsonb,'rollout-candidate','test')`, candidateVersion, ruleSetID, string(candidateRaw))
	for _, binding := range []struct {
		id, version, scope string
		state              string
	}{
		{baselineBinding.String(), baselineVersion.String(), "team/repository", "active"},
		{candidateBinding.String(), candidateVersion.String(), "team/repository", "shadow"},
		{mismatchedBinding.String(), candidateVersion.String(), "team/other", "shadow"},
	} {
		batch.Queue(`INSERT INTO rule_bindings(id,tenant_id,rule_version_id,scope_kind,scope_ref,scope_provider,scope_api_base_url,precedence,target_branch_glob,path_include_glob,path_exclude_glob,state,created_by) VALUES($1::uuid,$2,$3::uuid,'repository',$4,'gitlab','https://gitlab.example/api/v4',100,'main','services/**','','`+binding.state+`','test')`, binding.id, tenantID, binding.version, binding.scope)
	}
	batch.Queue(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES($1,$2,'gitlab',$3,'team/repository','https://gitlab.example/api/v4','test-only')`, installationID, tenantID, installationID.String())
	batch.Queue(`INSERT INTO webhook_deliveries(id,provider,delivery_id,event_name,payload) VALUES($1,'gitlab',$2,'merge_request','{}')`, deliveryID, deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs(id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state) VALUES($1,$2,$3,$4,'gitlab','https://gitlab.example/api/v4','team/repository','https://gitlab.example/team/repository.git',8,'main','base-sha','feature/shadow','head-sha','succeeded')`, jobID, tenantID, installationID, deliveryID)
	batch.Queue(`INSERT INTO review_requests(id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES($1,$2,$3,'gitlab','https://gitlab.example/api/v4','team/repository',8)`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO rule_snapshots(id,tenant_id,sha256,compiler_version,engine,canonical_payload) VALUES($1,$2,$3,'rules-v2','ocr',$4::jsonb)`, baselineSnapshotID, tenantID, baselineCompiled.SHA256, string(baselineCompiled.Canonical))
	batch.Queue(`INSERT INTO rule_snapshot_sources(snapshot_id,rule_version_id,precedence) VALUES($1,$2,100)`, baselineSnapshotID, baselineVersion)
	batch.Queue(`INSERT INTO review_runs(id,request_id,legacy_job_id,state,trigger_kind,head_sha,base_sha,rule_snapshot_id) VALUES($1,$2,$3,'completed','pull_request','head-sha','base-sha',$4)`, sourceRunID, requestID, jobID, baselineSnapshotID)
	batch.Queue(`INSERT INTO review_execution_plans(run_id,mode,selected_paths,deferred_files,static_impact_signals) VALUES($1,'focused','["services/main.go"]',0,'[]')`, sourceRunID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed rule rollout fixture: %v", err)
	}
	if _, err := postgres.CreateRuleBinding(ctx, actor, tenantSlug, domain.RuleBindingInput{
		RuleVersionID: candidateVersion, ScopeKind: "repository", ScopeRef: "team/repository",
		ScopeProvider: domain.ProviderGitLab, ScopeAPIBaseURL: "https://gitlab.example/api/v4",
		Precedence: 100, TargetBranchGlob: "main", PathIncludeGlob: "services/**", State: "active",
	}); !errors.Is(err, ErrRuleRolloutRequired) {
		t.Fatalf("direct active candidate creation error=%v, want governed rollout", err)
	}

	created, err := postgres.CreateRuleRollout(ctx, actor, tenantSlug, domain.RuleRolloutInput{BaselineBindingID: baselineBinding, CandidateBindingID: candidateBinding, Mode: "shadow"})
	if err != nil || created.State != "active" || created.Revision != 1 || created.Mode != "shadow" || created.CanaryBasisPoints != 0 {
		t.Fatalf("created rollout=%#v error=%v", created, err)
	}
	if _, err := postgres.UpdateRuleBinding(ctx, actor, tenantSlug, candidateBinding, domain.RuleBindingUpdateInput{State: "active"}); !errors.Is(err, ErrRuleRolloutRequired) {
		t.Fatalf("direct candidate activation error=%v, want governed rollout", err)
	}
	if err := postgres.QueueShadowRuleTests(ctx, jobID); err != nil {
		t.Fatalf("queue shadow replay: %v", err)
	}
	if err := postgres.QueueShadowRuleTests(ctx, jobID); err != nil {
		t.Fatalf("requeue shadow replay: %v", err)
	}
	var shadowTestID, shadowSnapshotID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id,snapshot_id FROM rule_test_runs WHERE rollout_id=$1 AND source_run_id=$2`, created.ID, sourceRunID).Scan(&shadowTestID, &shadowSnapshotID); err != nil {
		t.Fatalf("load queued shadow replay: %v", err)
	}
	if shadowSnapshotID == baselineSnapshotID {
		t.Fatal("shadow replay reused baseline snapshot")
	}
	var candidateSourceCount, outboxCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM rule_snapshot_sources WHERE snapshot_id=$1 AND rule_version_id=$2`, shadowSnapshotID, candidateVersion).Scan(&candidateSourceCount); err != nil || candidateSourceCount != 1 {
		t.Fatalf("candidate snapshot source count=%d error=%v", candidateSourceCount, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='rule.test.requested'`, shadowTestID).Scan(&outboxCount); err != nil || outboxCount != 1 {
		t.Fatalf("shadow outbox count=%d error=%v", outboxCount, err)
	}
	sharedFinding := domain.Finding{Path: "services/main.go", StartLine: 12, EndLine: 12, Severity: "high", Category: "security", Body: "shared boundary finding"}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_findings(job_id,path,start_line,end_line,severity,category,body,suggestion,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, jobID, sharedFinding.Path, sharedFinding.StartLine, sharedFinding.EndLine, sharedFinding.Severity, sharedFinding.Category, sharedFinding.Body, sharedFinding.Suggestion, findingFingerprint(sharedFinding)); err != nil {
		t.Fatalf("seed baseline finding: %v", err)
	}
	claimed, err := postgres.ClaimRuleTestRun(ctx, "shadow-rollout-test-worker", &shadowTestID)
	if err != nil {
		t.Fatalf("claim shadow replay: %v", err)
	}
	addedFinding := domain.Finding{Path: "services/new.go", StartLine: 3, EndLine: 3, Severity: "medium", Category: "maintainability", Body: "candidate-only finding"}
	if err := postgres.CompleteRuleTestRun(ctx, shadowTestID, "shadow-rollout-test-worker", claimed.Run.Attempts, domain.RuleTestCompletion{EngineVersion: "test", SelectedPathCount: 1, DurationMS: 1, Findings: []domain.Finding{sharedFinding, addedFinding}}); err != nil {
		t.Fatalf("complete shadow replay: %v", err)
	}
	var comparisonState string
	var baselineCount, candidateCount, addedCount, removedCount, matchedCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT state,baseline_finding_count,candidate_finding_count,added_finding_count,removed_finding_count,matched_finding_count FROM rule_rollout_comparisons WHERE candidate_test_run_id=$1`, shadowTestID).Scan(&comparisonState, &baselineCount, &candidateCount, &addedCount, &removedCount, &matchedCount); err != nil {
		t.Fatalf("load shadow comparison: %v", err)
	}
	if comparisonState != "completed" || baselineCount != 1 || candidateCount != 2 || addedCount != 1 || removedCount != 0 || matchedCount != 1 {
		t.Fatalf("comparison state=%s baseline=%d candidate=%d added=%d removed=%d matched=%d", comparisonState, baselineCount, candidateCount, addedCount, removedCount, matchedCount)
	}
	comparisons, err := postgres.ListRuleRolloutComparisons(ctx, actor, tenantSlug, created.ID, 25)
	if err != nil || len(comparisons) != 1 || comparisons[0].AddedFindingCount != 1 || comparisons[0].MatchedFindingCount != 1 {
		t.Fatalf("listed comparisons=%#v error=%v", comparisons, err)
	}
	if _, err := postgres.CreateRuleRollout(ctx, actor, tenantSlug, domain.RuleRolloutInput{BaselineBindingID: baselineBinding, CandidateBindingID: mismatchedBinding, Mode: "shadow"}); !errors.Is(err, ErrInvalidRuleBinding) {
		t.Fatalf("mismatched candidate error=%v, want invalid binding", err)
	}
	listed, err := postgres.ListRuleRollouts(ctx, actor, tenantSlug, 25)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed=%#v error=%v", listed, err)
	}
	if _, err := postgres.CreateRuleRollout(ctx, "rule-rollout-approver", tenantSlug, domain.RuleRolloutInput{BaselineBindingID: baselineBinding, CandidateBindingID: candidateBinding, Mode: "canary", CanaryBasisPoints: 10_000}); !errors.Is(err, ErrInvalidRuleRollout) {
		t.Fatalf("direct 100%% Canary error=%v, want staged rollout rejection", err)
	}
	canaryInput := domain.RuleRolloutInput{BaselineBindingID: baselineBinding, CandidateBindingID: candidateBinding, Mode: "canary", CanaryBasisPoints: 500}
	if _, err := postgres.pool.Exec(ctx, `UPDATE rule_rollout_comparisons SET state='failed',error_message='simulated latest replay failure' WHERE id=$1`, comparisons[0].ID); err != nil {
		t.Fatalf("mark latest comparison failed: %v", err)
	}
	if _, err := postgres.CreateRuleRollout(ctx, "rule-rollout-approver", tenantSlug, canaryInput); !errors.Is(err, ErrInvalidRuleRollout) {
		t.Fatalf("failed latest comparison error=%v, want invalid rollout", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE rule_rollout_comparisons SET state='completed',error_message='' WHERE id=$1`, comparisons[0].ID); err != nil {
		t.Fatalf("restore completed comparison: %v", err)
	}
	if _, err := postgres.CreateRuleRollout(ctx, actor, tenantSlug, canaryInput); !errors.Is(err, ErrInvalidRuleRollout) {
		t.Fatalf("self-approved canary error=%v, want independent approval", err)
	}
	canary, err := postgres.CreateRuleRollout(ctx, "rule-rollout-approver", tenantSlug, canaryInput)
	if err != nil || canary.ApprovedShadowComparisonID == nil || *canary.ApprovedShadowComparisonID != comparisons[0].ID {
		t.Fatalf("approved canary=%#v error=%v", canary, err)
	}
	if _, err := postgres.UpdateRuleRollout(ctx, "rule-rollout-approver", tenantSlug, canary.ID, domain.RuleRolloutUpdateInput{State: "active", CanaryBasisPoints: 2500, Revision: canary.Revision}); !errors.Is(err, ErrRuleRolloutEvidence) {
		t.Fatalf("unobserved Canary advance error=%v, want evidence gate", err)
	}
	if _, err := postgres.UpdateRuleBinding(ctx, actor, tenantSlug, baselineBinding, domain.RuleBindingUpdateInput{State: "shadow"}); !errors.Is(err, ErrActiveRuleRollout) {
		t.Fatalf("active Canary baseline demotion error=%v, want rollout fence", err)
	}
	if _, err := postgres.UpdateRuleBinding(ctx, actor, tenantSlug, candidateBinding, domain.RuleBindingUpdateInput{State: "disabled"}); !errors.Is(err, ErrActiveRuleRollout) {
		t.Fatalf("active Canary candidate disable error=%v, want rollout fence", err)
	}
	scope := domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: "team/repository", Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab.example/api/v4"}
	selectedReviewNumber := 0
	for reviewNumber := 1; reviewNumber <= 1000; reviewNumber++ {
		bucket, valid := domain.CohortBucket(tenantID, scope.Provider, scope.APIBaseURL, scope.Ref, reviewNumber, canary.CohortSalt)
		if valid && bucket < canary.CanaryBasisPoints {
			selectedReviewNumber = reviewNumber
			break
		}
	}
	if selectedReviewNumber == 0 {
		t.Fatal("could not find a selected Canary review")
	}
	canaryTx, err := postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var selections []ruleRolloutRunSelection
	selectedSnapshotID, err := resolveRuleSnapshotWithCanary(ctx, canaryTx, tenantID, scope, "main", nil, selectedReviewNumber, &selections)
	if err != nil {
		_ = canaryTx.Rollback(ctx)
		t.Fatalf("resolve canary admission: %v", err)
	}
	if len(selections) != 1 || !selections[0].selected || selections[0].selectedVersionID != candidateVersion {
		_ = canaryTx.Rollback(ctx)
		t.Fatalf("candidate admission selections=%#v", selections)
	}
	var selectedVersion uuid.UUID
	if err := canaryTx.QueryRow(ctx, `SELECT rule_version_id FROM rule_snapshot_sources WHERE snapshot_id=$1`, selectedSnapshotID).Scan(&selectedVersion); err != nil || selectedVersion != candidateVersion {
		_ = canaryTx.Rollback(ctx)
		t.Fatalf("snapshot version=%s error=%v", selectedVersion, err)
	}
	canaryRunID := uuid.New()
	if _, err := canaryTx.Exec(ctx, `INSERT INTO review_runs(id,request_id,state,trigger_kind,head_sha,base_sha,rule_snapshot_id) VALUES($1,$2,'completed','retry','canary-head','base-sha',$3)`, canaryRunID, requestID, selectedSnapshotID); err != nil {
		_ = canaryTx.Rollback(ctx)
		t.Fatalf("seed canary run: %v", err)
	}
	if err := recordRuleRolloutRunSelections(ctx, canaryTx, canaryRunID, selections); err != nil {
		_ = canaryTx.Rollback(ctx)
		t.Fatalf("record canary selection: %v", err)
	}
	var recordedCandidate bool
	if err := canaryTx.QueryRow(ctx, `SELECT selected_candidate FROM rule_rollout_run_selections WHERE run_id=$1 AND rollout_id=$2`, canaryRunID, canary.ID).Scan(&recordedCandidate); err != nil || !recordedCandidate {
		_ = canaryTx.Rollback(ctx)
		t.Fatalf("recorded candidate=%v error=%v", recordedCandidate, err)
	}
	if err := canaryTx.Rollback(ctx); err != nil {
		t.Fatalf("discard test-only canary run: %v", err)
	}
	pausedCanary, err := postgres.UpdateRuleRollout(ctx, "rule-rollout-approver", tenantSlug, canary.ID, domain.RuleRolloutUpdateInput{State: "paused", Revision: canary.Revision})
	if err != nil || pausedCanary.State != "paused" {
		t.Fatalf("pause canary=%#v error=%v", pausedCanary, err)
	}
	baselineTx, err := postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pausedSelections []ruleRolloutRunSelection
	pausedSnapshotID, err := resolveRuleSnapshotWithCanary(ctx, baselineTx, tenantID, scope, "main", nil, 8, &pausedSelections)
	if err != nil || pausedSnapshotID != baselineSnapshotID || len(pausedSelections) != 0 {
		_ = baselineTx.Rollback(ctx)
		t.Fatalf("paused canary snapshot=%s selections=%#v error=%v", pausedSnapshotID, pausedSelections, err)
	}
	if err := baselineTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.UpdateRuleRollout(ctx, "rule-rollout-approver", tenantSlug, canary.ID, domain.RuleRolloutUpdateInput{State: "rolled_back", Revision: pausedCanary.Revision}); err != nil {
		t.Fatalf("roll back canary: %v", err)
	}
	narrowCanary, err := postgres.CreateRuleRollout(ctx, "rule-rollout-approver", tenantSlug, domain.RuleRolloutInput{
		BaselineBindingID: baselineBinding, CandidateBindingID: candidateBinding,
		Mode: "canary", CanaryBasisPoints: 100, AutoRollbackFailedRuns: 2, AutoRollbackWindowMinutes: 60,
	})
	if err != nil {
		t.Fatalf("create narrow canary: %v", err)
	}
	outsideCohort := 0
	for reviewNumber := 1; reviewNumber <= 100; reviewNumber++ {
		bucket, valid := domain.CohortBucket(tenantID, scope.Provider, scope.APIBaseURL, scope.Ref, reviewNumber, narrowCanary.CohortSalt)
		if valid && bucket >= narrowCanary.CanaryBasisPoints {
			outsideCohort = reviewNumber
			break
		}
	}
	if outsideCohort == 0 {
		t.Fatal("could not find an out-of-cohort review")
	}
	narrowTx, err := postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var narrowSelections []ruleRolloutRunSelection
	narrowSnapshotID, err := resolveRuleSnapshotWithCanary(ctx, narrowTx, tenantID, scope, "main", nil, outsideCohort, &narrowSelections)
	if err != nil || narrowSnapshotID != baselineSnapshotID || len(narrowSelections) != 1 || narrowSelections[0].selected || narrowSelections[0].selectedVersionID != baselineVersion {
		_ = narrowTx.Rollback(ctx)
		t.Fatalf("out-of-cohort snapshot=%s selections=%#v error=%v", narrowSnapshotID, narrowSelections, err)
	}
	if err := narrowTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	secondRequestID := uuid.New()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_requests(id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES($1,$2,$3,'gitlab','https://gitlab.example/api/v4','team/repository',9)`, secondRequestID, tenantID, installationID); err != nil {
		t.Fatalf("seed second canary request: %v", err)
	}
	seedCanaryFailure := func(request uuid.UUID, head, failureCode string, selectedCandidate bool) {
		t.Helper()
		runID := uuid.New()
		snapshotID := baselineSnapshotID
		if selectedCandidate {
			snapshotID = comparisons[0].CandidateSnapshotID
		}
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_runs(id,request_id,state,trigger_kind,head_sha,base_sha,rule_snapshot_id,failure_code,finished_at) VALUES($1,$2,'failed','retry',$3,'base-sha',$4,$5,now())`, runID, request, head, snapshotID, failureCode); err != nil {
			t.Fatalf("seed failed canary run: %v", err)
		}
		selectedBinding, selectedVersion := baselineBinding, baselineVersion
		if selectedCandidate {
			selectedBinding, selectedVersion = candidateBinding, candidateVersion
		}
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO rule_rollout_run_selections(run_id,rollout_id,baseline_binding_id,candidate_binding_id,selected_binding_id,selected_rule_version_id,cohort_bucket,selected_candidate) VALUES($1,$2,$3,$4,$5,$6,0,$7)`, runID, narrowCanary.ID, baselineBinding, candidateBinding, selectedBinding, selectedVersion, selectedCandidate); err != nil {
			t.Fatalf("seed failed canary selection: %v", err)
		}
	}
	seedCanaryFailure(requestID, "quota-rejected", "quota_exceeded", true)
	seedCanaryFailure(requestID, "candidate-failure-1", "model_error", true)
	seedCanaryFailure(requestID, "candidate-failure-retry", "model_error", true)
	seedCanaryFailure(secondRequestID, "baseline-failure", "model_error", false)
	if rolledBack, err := postgres.ReconcileCanaryFailures(ctx, 100); err != nil || rolledBack != 0 {
		t.Fatalf("same PR, quota and baseline failure must not trigger rollback: count=%d error=%v", rolledBack, err)
	}
	seedCanaryFailure(secondRequestID, "candidate-failure-2", "model_error", true)
	if rolledBack, err := postgres.ReconcileCanaryFailures(ctx, 100); err != nil || rolledBack != 1 {
		t.Fatalf("two distinct candidate failures must roll back: count=%d error=%v", rolledBack, err)
	}
	if rolledBack, err := postgres.ReconcileCanaryFailures(ctx, 100); err != nil || rolledBack != 0 {
		t.Fatalf("rollback replay must be idempotent: count=%d error=%v", rolledBack, err)
	}
	var autoState, autoReason string
	if err := postgres.pool.QueryRow(ctx, `SELECT state,auto_rollback_reason FROM rule_rollouts WHERE id=$1`, narrowCanary.ID).Scan(&autoState, &autoReason); err != nil || autoState != "rolled_back" || autoReason == "" {
		t.Fatalf("auto rollback state=%q reason=%q error=%v", autoState, autoReason, err)
	}
	var autoAuditCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND target=$2 AND action='rule_rollout.auto_rolled_back'`, tenantID, narrowCanary.ID.String()).Scan(&autoAuditCount); err != nil || autoAuditCount != 1 {
		t.Fatalf("auto rollback audit count=%d error=%v", autoAuditCount, err)
	}
	baselineAfterRollbackTx, err := postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var afterRollbackSelections []ruleRolloutRunSelection
	baselineAfterRollback, err := resolveRuleSnapshotWithCanary(ctx, baselineAfterRollbackTx, tenantID, scope, "main", nil, 8, &afterRollbackSelections)
	if err != nil || baselineAfterRollback != baselineSnapshotID || len(afterRollbackSelections) != 0 {
		_ = baselineAfterRollbackTx.Rollback(ctx)
		t.Fatalf("auto rollback admission snapshot=%s selections=%#v error=%v", baselineAfterRollback, afterRollbackSelections, err)
	}
	if err := baselineAfterRollbackTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	paused, err := postgres.UpdateRuleRollout(ctx, actor, tenantSlug, created.ID, domain.RuleRolloutUpdateInput{State: "paused", Revision: created.Revision})
	if err != nil || paused.State != "paused" || paused.Revision != 2 {
		t.Fatalf("paused rollout=%#v error=%v", paused, err)
	}
	if _, err := postgres.UpdateRuleRollout(ctx, actor, tenantSlug, created.ID, domain.RuleRolloutUpdateInput{State: "active", Revision: created.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision error=%v, want conflict", err)
	}
	if _, err := postgres.UpdateRuleRollout(ctx, actor, tenantSlug, created.ID, domain.RuleRolloutUpdateInput{State: "promoted", Revision: paused.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Shadow cannot be directly promoted: error=%v, want conflict", err)
	}
	rolledBack, err := postgres.UpdateRuleRollout(ctx, actor, tenantSlug, created.ID, domain.RuleRolloutUpdateInput{State: "rolled_back", Revision: paused.Revision})
	if err != nil || rolledBack.State != "rolled_back" || rolledBack.Revision != 3 {
		t.Fatalf("rolled back rollout=%#v error=%v", rolledBack, err)
	}
	newShadow, err := postgres.CreateRuleRollout(ctx, actor, tenantSlug, domain.RuleRolloutInput{BaselineBindingID: baselineBinding, CandidateBindingID: candidateBinding, Mode: "shadow"})
	if err != nil {
		t.Fatalf("create promotion Shadow: %v", err)
	}
	if err := postgres.QueueShadowRuleTests(ctx, jobID); err != nil {
		t.Fatalf("queue promotion Shadow replay: %v", err)
	}
	var promotionTestID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM rule_test_runs WHERE rollout_id=$1 AND source_run_id=$2`, newShadow.ID, sourceRunID).Scan(&promotionTestID); err != nil {
		t.Fatalf("load promotion Shadow replay: %v", err)
	}
	promotionTest, err := postgres.ClaimRuleTestRun(ctx, "promotion-shadow-worker", &promotionTestID)
	if err != nil {
		t.Fatalf("claim promotion Shadow replay: %v", err)
	}
	if err := postgres.CompleteRuleTestRun(ctx, promotionTestID, "promotion-shadow-worker", promotionTest.Run.Attempts, domain.RuleTestCompletion{EngineVersion: "test", SelectedPathCount: 1, DurationMS: 1, Findings: []domain.Finding{sharedFinding}}); err != nil {
		t.Fatalf("complete promotion Shadow replay: %v", err)
	}
	promotable, err := postgres.CreateRuleRollout(ctx, "rule-rollout-approver", tenantSlug, domain.RuleRolloutInput{BaselineBindingID: baselineBinding, CandidateBindingID: candidateBinding, Mode: "canary", CanaryBasisPoints: 100, AutoRollbackWindowMinutes: 5})
	if err != nil {
		t.Fatalf("create promotion Canary: %v", err)
	}
	if _, err := postgres.UpdateRuleRollout(ctx, actor, tenantSlug, promotable.ID, domain.RuleRolloutUpdateInput{State: "promoted", Revision: promotable.Revision}); !errors.Is(err, ErrInvalidRuleRollout) {
		t.Fatalf("premature promotion error=%v, want invalid transition", err)
	}
	// Older deployments could create a Canary directly at 25% or 100%.
	// Neither row has the audit chain required by staged promotion.
	for _, legacyStage := range []int{2500, 10000} {
		if _, err := postgres.pool.Exec(ctx, `UPDATE rule_rollouts SET canary_basis_points=$2 WHERE id=$1`, promotable.ID, legacyStage); err != nil {
			t.Fatalf("simulate legacy Canary stage %d: %v", legacyStage, err)
		}
		state, next := "active", 10000
		if legacyStage == 10000 {
			state, next = "promoted", 0
		}
		if _, err := postgres.UpdateRuleRollout(ctx, actor, tenantSlug, promotable.ID, domain.RuleRolloutUpdateInput{State: state, CanaryBasisPoints: next, Revision: promotable.Revision}); !errors.Is(err, ErrInvalidRuleRollout) {
			t.Fatalf("legacy Canary stage %d bypass error=%v, want invalid rollout", legacyStage, err)
		}
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE rule_rollouts SET canary_basis_points=100 WHERE id=$1`, promotable.ID); err != nil {
		t.Fatalf("restore staged Canary fixture: %v", err)
	}
	for _, next := range []int{500, 2500, 10000, 0} {
		if _, err := postgres.pool.Exec(ctx, `UPDATE review_runs SET created_at=now()-interval '7 minutes',finished_at=now()-interval '7 minutes' WHERE id IN (SELECT run_id FROM rule_rollout_run_selections WHERE rollout_id=$1)`, promotable.ID); err != nil {
			t.Fatalf("retire prior-stage test evidence: %v", err)
		}
		if _, err := postgres.pool.Exec(ctx, `UPDATE rule_rollouts SET updated_at=now()-interval '6 minutes' WHERE id=$1`, promotable.ID); err != nil {
			t.Fatalf("age Canary observation: %v", err)
		}
		completedRunID := uuid.New()
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_runs(id,request_id,state,trigger_kind,head_sha,base_sha,rule_snapshot_id,finished_at) VALUES($1,$2,'completed','retry',$3,'base-sha',$4,now())`, completedRunID, requestID, "promotion-stage-"+completedRunID.String(), comparisons[0].CandidateSnapshotID); err != nil {
			t.Fatalf("seed completed candidate run: %v", err)
		}
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO rule_rollout_run_selections(run_id,rollout_id,baseline_binding_id,candidate_binding_id,selected_binding_id,selected_rule_version_id,cohort_bucket,selected_candidate) VALUES($1,$2,$3,$4,$4,$5,0,true)`, completedRunID, promotable.ID, baselineBinding, candidateBinding, candidateVersion); err != nil {
			t.Fatalf("record completed candidate selection: %v", err)
		}
		if next == 500 {
			if _, err := postgres.UpdateRuleRollout(ctx, "rule-rollout-approver", tenantSlug, promotable.ID, domain.RuleRolloutUpdateInput{State: "active", CanaryBasisPoints: next, Revision: promotable.Revision}); !errors.Is(err, ErrRuleRolloutEvidence) {
				t.Fatalf("candidate review without a merge-gate receipt advanced: %v", err)
			}
		}
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_merge_gate_decisions(run_id,enabled,threshold,conclusion,blocking_findings,finding_count,configuration_content_sha256,origin_scope_kind,origin_revision,evaluation_version) VALUES($1,false,'off','success',0,0,$2,'default',0,'integration-v1')`, completedRunID, strings.Repeat("a", 64)); err != nil {
			t.Fatalf("seed passing candidate merge gate: %v", err)
		}
		if next == 500 {
			failedRunID := uuid.New()
			if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_runs(id,request_id,state,trigger_kind,head_sha,base_sha,rule_snapshot_id,failure_code,finished_at) VALUES($1,$2,'failed','retry',$3,'base-sha',$4,'model_error',now())`, failedRunID, requestID, "failed-stage-"+failedRunID.String(), comparisons[0].CandidateSnapshotID); err != nil {
				t.Fatalf("seed failed candidate review: %v", err)
			}
			if _, err := postgres.pool.Exec(ctx, `INSERT INTO rule_rollout_run_selections(run_id,rollout_id,baseline_binding_id,candidate_binding_id,selected_binding_id,selected_rule_version_id,cohort_bucket,selected_candidate) VALUES($1,$2,$3,$4,$4,$5,0,true)`, failedRunID, promotable.ID, baselineBinding, candidateBinding, candidateVersion); err != nil {
				t.Fatalf("record failed candidate review: %v", err)
			}
			if _, err := postgres.UpdateRuleRollout(ctx, "rule-rollout-approver", tenantSlug, promotable.ID, domain.RuleRolloutUpdateInput{State: "active", CanaryBasisPoints: next, Revision: promotable.Revision}); !errors.Is(err, ErrRuleRolloutEvidence) {
				t.Fatalf("failed candidate review advanced: %v", err)
			}
			if _, err := postgres.pool.Exec(ctx, `UPDATE review_runs SET created_at=now()-interval '7 minutes',finished_at=now()-interval '7 minutes' WHERE id=$1`, failedRunID); err != nil {
				t.Fatalf("move failed test review outside this observation stage: %v", err)
			}
		}
		state := "active"
		decisionActor := "rule-rollout-approver"
		if next == 0 {
			state = "promoted"
			if _, err := postgres.UpdateRuleRollout(ctx, decisionActor, tenantSlug, promotable.ID, domain.RuleRolloutUpdateInput{State: state, Revision: promotable.Revision}); !errors.Is(err, ErrInvalidRuleRollout) {
				t.Fatalf("self-approved promotion error=%v, want independent owner", err)
			}
			decisionActor = actor
		}
		advanced, err := postgres.UpdateRuleRollout(ctx, decisionActor, tenantSlug, promotable.ID, domain.RuleRolloutUpdateInput{State: state, CanaryBasisPoints: next, Revision: promotable.Revision})
		if err != nil || advanced.Revision != promotable.Revision+1 || advanced.State != state {
			t.Fatalf("Canary next=%d result=%#v error=%v", next, advanced, err)
		}
		promotable = advanced
	}
	var baselineState, candidateState, shadowState string
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM rule_bindings WHERE id=$1`, baselineBinding).Scan(&baselineState); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM rule_bindings WHERE id=$1`, candidateBinding).Scan(&candidateState); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM rule_rollouts WHERE id=$1`, newShadow.ID).Scan(&shadowState); err != nil {
		t.Fatal(err)
	}
	if baselineState != "disabled" || candidateState != "active" || shadowState != "promoted" {
		t.Fatalf("promotion did not atomically switch bindings: baseline=%s candidate=%s shadow=%s", baselineState, candidateState, shadowState)
	}
	postPromotionTx, err := postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var postPromotionSelections []ruleRolloutRunSelection
	postPromotionSnapshot, err := resolveRuleSnapshotWithCanary(ctx, postPromotionTx, tenantID, scope, "main", nil, selectedReviewNumber, &postPromotionSelections)
	if err != nil || len(postPromotionSelections) != 0 || postPromotionSnapshot == baselineSnapshotID {
		_ = postPromotionTx.Rollback(ctx)
		t.Fatalf("future admission did not use promoted candidate: snapshot=%s selections=%#v error=%v", postPromotionSnapshot, postPromotionSelections, err)
	}
	var promotedVersionCount int
	if err := postPromotionTx.QueryRow(ctx, `SELECT COUNT(*) FROM rule_snapshot_sources WHERE snapshot_id=$1 AND rule_version_id=$2`, postPromotionSnapshot, candidateVersion).Scan(&promotedVersionCount); err != nil || promotedVersionCount != 1 {
		_ = postPromotionTx.Rollback(ctx)
		t.Fatalf("promoted snapshot candidate source count=%d error=%v", promotedVersionCount, err)
	}
	if err := postPromotionTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var retainedBaselineSnapshot uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT rule_snapshot_id FROM review_runs WHERE id=$1`, sourceRunID).Scan(&retainedBaselineSnapshot); err != nil || retainedBaselineSnapshot != baselineSnapshotID {
		t.Fatalf("historical run snapshot changed: snapshot=%s error=%v", retainedBaselineSnapshot, err)
	}
	var stageAuditCount, promotionAuditCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND target=$2 AND action='rule_rollout.stage_advanced'`, tenantID, promotable.ID.String()).Scan(&stageAuditCount); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND target=$2 AND action='rule_rollout.promoted'`, tenantID, promotable.ID.String()).Scan(&promotionAuditCount); err != nil {
		t.Fatal(err)
	}
	if stageAuditCount != 3 || promotionAuditCount != 1 {
		t.Fatalf("promotion audit missing: stages=%d promotion=%d", stageAuditCount, promotionAuditCount)
	}
}
