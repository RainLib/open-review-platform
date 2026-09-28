package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestReviewEvidenceRetainsRunFindingsStagesReceiptsAndEvents(t *testing.T) {
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

	tenantID := uuid.New()
	installationID := uuid.New()
	deliveryID := uuid.New()
	jobID := uuid.New()
	requestID := uuid.New()
	runID := uuid.New()
	findingID := uuid.New()
	ruleSetID := uuid.New()
	ruleVersionID := uuid.New()
	snapshotID := uuid.New()
	tenantSlug := "review-evidence-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Review Evidence')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID)
	batch.Queue(`INSERT INTO rule_sets (id, tenant_id, name, created_by) VALUES ($1, $2, 'Evidence policy', 'owner')`, ruleSetID, tenantID)
	batch.Queue(`INSERT INTO rule_versions (id, rule_set_id, version, state, rules, content_sha256, created_by, published_at) VALUES ($1, $2, 3, 'published', '[]'::jsonb, 'evidence-policy', 'owner', now())`, ruleVersionID, ruleSetID)
	batch.Queue(`INSERT INTO rule_snapshots (id, tenant_id, sha256, compiler_version, engine, canonical_payload) VALUES ($1, $2, 'review-evidence-snapshot', 'rules-v2', 'ocr', jsonb_build_object('rules', jsonb_build_array(jsonb_build_object('key', 'security.redirects', 'source_version', $3::text))))`, snapshotID, tenantID, ruleVersionID)
	batch.Queue(`INSERT INTO rule_snapshot_sources (snapshot_id, rule_version_id, precedence) VALUES ($1, $2, 10)`, snapshotID, ruleVersionID)
	batch.Queue(`INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref) VALUES ($1, $2, 'github', $3, 'RainLib/*', 'https://api.github.com', 'secret://review-evidence')`, installationID, tenantID, "review-evidence-installation-"+tenantID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id, provider, delivery_id, event_name, payload) VALUES ($1, 'github', $2, 'pull_request', '{}'::jsonb)`, deliveryID, "review-evidence-delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id, tenant_id, installation_id, delivery_id, provider, api_base_url, repository, clone_url, review_number, base_ref, base_sha, head_ref, head_sha, state, attempts) VALUES ($1, $2, $3, $4, 'github', 'https://api.github.com', 'RainLib/open-review-platform', 'https://github.com/RainLib/open-review-platform.git', 3, 'main', 'base-sha', 'feature/review-evidence', 'head-sha', 'succeeded', 2)`, jobID, tenantID, installationID, deliveryID)
	batch.Queue(`INSERT INTO review_requests (id, tenant_id, installation_id, provider, api_base_url, repository, review_number) VALUES ($1, $2, $3, 'github', 'https://api.github.com', 'RainLib/open-review-platform', 3)`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id, request_id, legacy_job_id, rule_snapshot_id, revision, state, trigger_kind, review_mode, head_sha, base_sha, started_at, finished_at) VALUES ($1, $2, $3, $4, 3, 'completed', 'pull_request', 'deep', 'head-sha', 'base-sha', now() - interval '5 seconds', now())`, runID, requestID, jobID, snapshotID)
	batch.Queue(`UPDATE review_requests SET current_run_id = $1 WHERE id = $2`, runID, requestID)
	batch.Queue(`INSERT INTO review_configuration_snapshots (run_id, section, origin_scope_kind, origin_revision, content, content_sha256) VALUES ($1, 'general', 'tenant', 7, '{"merge_gate_enabled":true,"minimum_blocking_severity":"high"}'::jsonb, $2)`, runID, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	batch.Queue(`INSERT INTO review_execution_plans (run_id, mode, selected_paths, deferred_files, static_impact_signals, file_scopes) VALUES ($1, 'critical', '["internal/api/server.go", "internal/runner/runner.go"]'::jsonb, 1, '["public boundary or asynchronous workflow"]'::jsonb, '[{"path":"internal/api/server.go","selected":true,"score":70,"reasons":["public boundary or asynchronous workflow"]},{"path":"internal/runner/runner.go","selected":true,"score":45,"reasons":["changed application code"]},{"path":"docs/plan.md","selected":false,"score":10,"reasons":["documentation-only change"]}]'::jsonb)`, runID)
	batch.Queue(`INSERT INTO review_run_stages (run_id, stage, state, attempt, started_at, finished_at, details) VALUES ($1, 'analyze', 'succeeded', 1, now() - interval '4 seconds', now() - interval '2 seconds', '{"selected_paths":1}'::jsonb)`, runID)
	batch.Queue(`INSERT INTO review_findings (id, job_id, path, start_line, end_line, severity, category, body, suggestion, code_excerpt, code_excerpt_start_line, proposed_patch, fingerprint, provider_marker) VALUES ($1, $2, 'internal/api/server.go', 40, 42, 'high', 'security', 'Untrusted redirect target', 'Validate the target.', 'if unsafe { redirect() }', 40, '--- a/internal/api/server.go\n+++ b/internal/api/server.go\n@@ -40,1 +40,1 @@\n-if unsafe { redirect() }\n+if safe { redirect() }\n', 'fingerprint-review-evidence', $3)`, findingID, jobID, "marker-review-evidence-"+jobID.String())
	batch.Queue(`INSERT INTO review_finding_rule_attributions (finding_id, rule_key, rule_version_id) VALUES ($1, 'security.redirects', $2)`, findingID, ruleVersionID)
	batch.Queue(`INSERT INTO finding_feedback (tenant_id, finding_id, provider, delivery_id, reaction_external_id, actor_external_id, kind) VALUES ($1, $2, 'console', 'console', $3, 'owner', 'resolved')`, tenantID, findingID, "console:"+findingID.String()+":owner")
	batch.Queue(`INSERT INTO review_run_events (run_id, revision, event_type, actor_kind, payload) VALUES ($1, 1, 'review.completed', 'worker', '{"worker":"integration"}'::jsonb)`, runID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed review evidence: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()
	if err := postgres.RecordPublicationReceipts(ctx, jobID, []domain.PublicationReceipt{{
		ReceiptKind:  "status",
		StableMarker: "status-review-evidence",
		PayloadHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		LastError:    "Provider analysis status could not be updated. The review continues and will attempt a final status.",
	}}); err != nil {
		t.Fatalf("record initial publication receipt: %v", err)
	}
	if err := postgres.RecordPublicationReceipts(ctx, jobID, []domain.PublicationReceipt{{
		ReceiptKind:  "status",
		StableMarker: "status-review-evidence",
		ExternalID:   "check-42",
		PayloadHash:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Published:    true,
	}}); err != nil {
		t.Fatalf("record completed publication receipt: %v", err)
	}
	decision, err := postgres.SaveReviewMergeGateDecision(ctx, jobID, domain.ReviewMergeGateDecisionInput{
		Enabled:           true,
		Threshold:         "high",
		Conclusion:        "failure",
		BlockingFindings:  1,
		FindingCount:      1,
		EvaluationVersion: "v1",
	})
	if err != nil {
		t.Fatalf("save merge gate decision: %v", err)
	}
	if decision.ConfigurationContentSHA256 != "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" || decision.OriginScopeKind != "tenant" || decision.OriginRevision != 7 {
		t.Fatalf("unexpected merge gate provenance: %#v", decision)
	}
	if _, err := postgres.SaveReviewMergeGateDecision(ctx, jobID, domain.ReviewMergeGateDecisionInput{
		Enabled: true, Threshold: "medium", Conclusion: "failure", BlockingFindings: 1, FindingCount: 1, EvaluationVersion: "v1",
	}); err == nil {
		t.Fatal("expected immutable merge gate decision to reject a rewrite")
	}

	evidence, err := postgres.GetReviewEvidence(ctx, "owner", tenantSlug, runID)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Run.ID != runID || evidence.Run.HeadSHA != "head-sha" || len(evidence.RelatedRuns) != 1 {
		t.Fatalf("unexpected run evidence: %#v related=%d", evidence.Run, len(evidence.RelatedRuns))
	}
	if evidence.ExecutionAttempts != 2 {
		t.Fatalf("execution attempts=%d, want 2", evidence.ExecutionAttempts)
	}
	if len(evidence.Findings) != 1 || evidence.Findings[0].ID != findingID || evidence.Findings[0].Disposition != "resolved" || evidence.Findings[0].CodeExcerptStartLine != 40 || evidence.Findings[0].CodeExcerpt != "if unsafe { redirect() }" || evidence.Findings[0].ProposedPatch == "" || len(evidence.Findings[0].RuleAttributions) != 1 || evidence.Findings[0].RuleAttributions[0].RuleVersionID != ruleVersionID || evidence.Findings[0].RuleAttributions[0].RuleSetID != ruleSetID || evidence.Findings[0].RuleAttributions[0].Version != 3 {
		t.Fatalf("unexpected findings: %#v", evidence.Findings)
	}
	if len(evidence.Stages) != 1 || evidence.Stages[0].Stage != "analyze" || evidence.Stages[0].Details["selected_paths"] != float64(1) {
		t.Fatalf("unexpected stages: %#v", evidence.Stages)
	}
	if evidence.ExecutionPlan == nil || evidence.ExecutionPlan.Mode != "critical" || evidence.ExecutionPlan.DeferredFiles != 1 || len(evidence.ExecutionPlan.SelectedPaths) != 2 || len(evidence.ExecutionPlan.StaticImpactSignals) != 1 || evidence.ExecutionPlan.StaticImpactSignals[0] != "public boundary or asynchronous workflow" || len(evidence.ExecutionPlan.FileScopes) != 3 || evidence.ExecutionPlan.FileScopes[2].Path != "docs/plan.md" || evidence.ExecutionPlan.FileScopes[2].Selected {
		t.Fatalf("unexpected execution plan: %#v", evidence.ExecutionPlan)
	}
	if evidence.MergeGate == nil || evidence.MergeGate.Conclusion != "failure" || evidence.MergeGate.Threshold != "high" || evidence.MergeGate.BlockingFindings != 1 || evidence.MergeGate.ConfigurationContentSHA256 != decision.ConfigurationContentSHA256 {
		t.Fatalf("unexpected merge gate evidence: %#v", evidence.MergeGate)
	}
	if len(evidence.Receipts) != 1 || evidence.Receipts[0].ExternalID != "check-42" || evidence.Receipts[0].PayloadHash != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || evidence.Receipts[0].PublishedAt == nil || evidence.Receipts[0].LastError != "" {
		t.Fatalf("unexpected receipts: %#v", evidence.Receipts)
	}
	if len(evidence.Events) != 1 || evidence.Events[0].EventType != "review.completed" {
		t.Fatalf("unexpected events: %#v", evidence.Events)
	}
	if _, err := postgres.GetReviewEvidence(ctx, "outsider", tenantSlug, runID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant review evidence error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_jobs SET state='running', attempts=1, locked_by='receipt-worker', available_at=now() WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	beforeRetry := time.Now()
	if err := postgres.FailWithRetryAfter(ctx, jobID, "receipt-worker", "provider returned HTTP 429", 9*time.Second); err != nil {
		t.Fatalf("record provider retry-after: %v", err)
	}
	var state string
	var availableAt time.Time
	if err := postgres.pool.QueryRow(ctx, `SELECT state, available_at FROM review_jobs WHERE id=$1`, jobID).Scan(&state, &availableAt); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || availableAt.Before(beforeRetry.Add(8*time.Second)) {
		t.Fatalf("provider retry-after was not persisted: state=%s available_at=%s", state, availableAt)
	}
}

func TestAdvanceLegacyRunPersistsDurableStageProgress(t *testing.T) {
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
	tenantSlug := "stage-progress-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Stage Progress')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref) VALUES ($1, $2, 'github', $3, 'RainLib/*', 'https://api.github.com', 'secret://stage-progress')`, installationID, tenantID, "stage-progress-installation-"+tenantID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id, provider, delivery_id, event_name, payload) VALUES ($1, 'github', $2, 'pull_request', '{}'::jsonb)`, deliveryID, "stage-progress-delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id, tenant_id, installation_id, delivery_id, provider, api_base_url, repository, clone_url, review_number, base_ref, base_sha, head_ref, head_sha, state) VALUES ($1, $2, $3, $4, 'github', 'https://api.github.com', 'RainLib/open-review-platform', 'https://github.com/RainLib/open-review-platform.git', 4, 'main', 'base', 'feature/stages', 'head', 'queued')`, jobID, tenantID, installationID, deliveryID)
	batch.Queue(`INSERT INTO review_requests (id, tenant_id, installation_id, provider, api_base_url, repository, review_number) VALUES ($1, $2, $3, 'github', 'https://api.github.com', 'RainLib/open-review-platform', 4)`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id, request_id, legacy_job_id, state, trigger_kind, head_sha, base_sha) VALUES ($1, $2, $3, 'acknowledged', 'pull_request', 'head', 'base')`, runID, requestID, jobID)
	batch.Queue(`INSERT INTO review_run_stages (run_id, stage, state) SELECT $1, stage, 'pending' FROM unnest(ARRAY['ack', 'admit', 'prepare', 'analyze', 'normalize', 'publish']) AS stage`, runID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed stage progress: %v", err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE aggregate_id = $1`, runID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	}()

	for _, state := range []domain.RunState{domain.RunAdmitted, domain.RunPreparing, domain.RunAnalyzing, domain.RunNormalizing, domain.RunPublishing, domain.RunCompleted} {
		if _, err := postgres.AdvanceLegacyRun(ctx, jobID, state); err != nil {
			t.Fatalf("advance to %s: %v", state, err)
		}
	}
	evidence, err := postgres.GetReviewEvidence(ctx, "owner", tenantSlug, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Stages) != 6 {
		t.Fatalf("stage count=%d, want 6", len(evidence.Stages))
	}
	for _, stage := range evidence.Stages {
		if stage.State != "succeeded" || stage.StartedAt == nil || stage.FinishedAt == nil {
			t.Fatalf("stage %s evidence=%#v", stage.Stage, stage)
		}
	}
}
