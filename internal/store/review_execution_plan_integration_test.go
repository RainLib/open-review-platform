package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestReviewExecutionPlanIsImmutableAndReloadsPersistedFindings(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	tenantID, installationID, deliveryID := uuid.New(), uuid.New(), uuid.New()
	jobID, requestID, runID := uuid.New(), uuid.New(), uuid.New()
	ruleSetID, ruleVersionID, snapshotID := uuid.New(), uuid.New(), uuid.New()
	tenantSlug := "execution-plan-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Execution plan')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO rule_sets (id,tenant_id,name,created_by) VALUES ($1,$2,'Execution plan policy','owner')`, ruleSetID, tenantID)
	batch.Queue(`INSERT INTO rule_versions (id,rule_set_id,version,state,rules,content_sha256,created_by,published_at) VALUES ($1,$2,1,'published','[]'::jsonb,'execution-plan-policy','owner',now())`, ruleVersionID, ruleSetID)
	batch.Queue(`INSERT INTO rule_snapshots (id,tenant_id,sha256,compiler_version,engine,canonical_payload) VALUES ($1,$2,'execution-plan-snapshot','rules-v2','ocr',jsonb_build_object('rules',jsonb_build_array(jsonb_build_object('key','security.input-validation','source_version',$3::text))))`, snapshotID, tenantID, ruleVersionID)
	batch.Queue(`INSERT INTO rule_snapshot_sources (snapshot_id,rule_version_id,precedence) VALUES ($1,$2,10)`, snapshotID, ruleVersionID)
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "execution-plan-installation-"+installationID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'github',$2,'pull_request','{}'::jsonb)`, deliveryID, "execution-plan-delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',3,'main','base','feature/plan','head','running')`, jobID, tenantID, installationID, deliveryID)
	batch.Queue(`INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',3)`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id,request_id,legacy_job_id,rule_snapshot_id,state,trigger_kind,review_mode,head_sha,base_sha) VALUES ($1,$2,$3,$4,'publishing','pull_request','security','head','base')`, runID, requestID, jobID, snapshotID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed execution plan: %v", err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM webhook_deliveries WHERE id = $1`, deliveryID)
	}()

	input := domain.ReviewExecutionPlan{
		Mode: "critical", SelectedPaths: []string{"internal/api/server.go", "apps/web/page.tsx"}, DeferredFiles: 1,
		StaticImpactSignals: []string{"public boundary or asynchronous workflow", "persistent state or financial side effect"},
		FileScopes: []domain.ReviewFileScope{
			{Path: "internal/api/server.go", Selected: true, Score: 70, Reasons: []string{"public boundary or asynchronous workflow"}, ChangeType: "modified", Additions: 12, Deletions: 3, StatsKnown: true},
			{Path: "apps/web/page.tsx", Selected: true, Score: 45, Reasons: []string{"changed application code"}},
			{Path: "docs/plan.md", Selected: false, Score: 10, Reasons: []string{"documentation-only change"}},
		},
	}
	stored, err := postgres.SaveReviewExecutionPlan(ctx, jobID, input)
	if err != nil || stored.Mode != "critical" || len(stored.SelectedPaths) != 2 || stored.DeferredFiles != 1 || len(stored.StaticImpactSignals) != 2 || len(stored.FileScopes) != 3 {
		t.Fatalf("stored plan=%#v err=%v", stored, err)
	}
	loaded, err := postgres.ReviewExecutionPlanForJob(ctx, jobID)
	if err != nil || loaded.Mode != input.Mode || len(loaded.SelectedPaths) != 2 || loaded.SelectedPaths[0] != input.SelectedPaths[0] || len(loaded.StaticImpactSignals) != 2 || loaded.StaticImpactSignals[1] != input.StaticImpactSignals[1] || len(loaded.FileScopes) != 3 || loaded.FileScopes[0].ChangeType != "modified" || loaded.FileScopes[0].Additions != 12 || loaded.FileScopes[0].Deletions != 3 || !loaded.FileScopes[0].StatsKnown || loaded.FileScopes[2].Path != "docs/plan.md" || loaded.FileScopes[2].Selected || loaded.FileScopes[2].Reasons[0] != "documentation-only change" {
		t.Fatalf("loaded plan=%#v err=%v", loaded, err)
	}
	changed := input
	changed.FileScopes = append([]domain.ReviewFileScope(nil), input.FileScopes...)
	changed.FileScopes[2].Score = 11
	if _, err := postgres.SaveReviewExecutionPlan(ctx, jobID, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed plan error=%v, want ErrConflict", err)
	}
	changed = input
	changed.FileScopes = append([]domain.ReviewFileScope(nil), input.FileScopes...)
	changed.FileScopes[0].Additions++
	if _, err := postgres.SaveReviewExecutionPlan(ctx, jobID, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed source-free diff metadata error=%v, want ErrConflict", err)
	}

	findings := []domain.Finding{{Path: "internal/api/server.go", StartLine: 40, EndLine: 41, Severity: "high", Category: "security", Body: "Validate the caller boundary.", Suggestion: "Reject malformed requests.", CodeExcerpt: "if unsafe {\n  redirect()\n}", CodeExcerptStartLine: 39, ProposedPatch: "--- a/internal/api/server.go\n+++ b/internal/api/server.go\n@@ -40,1 +40,1 @@\n-unsafe\n+safe\n", RuleReferences: []domain.FindingRuleReference{{RuleKey: "security.input-validation", SourceVersion: ruleVersionID.String()}, {RuleKey: "security.guessed", SourceVersion: uuid.NewString()}}}}
	if err := postgres.SaveFindings(ctx, jobID, findings); err != nil {
		t.Fatalf("save findings: %v", err)
	}
	persisted, err := postgres.FindingsForJob(ctx, jobID)
	if err != nil || len(persisted) != 1 || persisted[0].Path != findings[0].Path || persisted[0].Suggestion != findings[0].Suggestion || persisted[0].CodeExcerpt != findings[0].CodeExcerpt || persisted[0].CodeExcerptStartLine != 39 || persisted[0].ProposedPatch != findings[0].ProposedPatch || len(persisted[0].RuleReferences) != 1 || persisted[0].RuleReferences[0].RuleKey != "security.input-validation" || persisted[0].RuleReferences[0].SourceVersion != ruleVersionID.String() {
		t.Fatalf("persisted findings=%#v err=%v", persisted, err)
	}
}
