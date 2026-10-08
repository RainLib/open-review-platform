package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/google/uuid"
)

func TestRuleImpactPreviewUsesExactVersionAndReplacesPriorPolicy(t *testing.T) {
	ctx := context.Background()
	postgres, err := Open(ctx, isolatedQueueDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(postgres.Close)
	tenantID, setID, publishedID, candidateID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	const slug, actor, repository = "preview-regression", "preview-owner", "tests/preview"
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Preview regression')`, tenantID, slug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role) VALUES($1,$2,'owner')`, tenantID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO rule_sets(id,tenant_id,name,created_by) VALUES($1,$2,'Preview policy',$3)`, setID, tenantID, actor); err != nil {
		t.Fatal(err)
	}
	oldRules := []rules.Rule{{Key: "preview.old", Enforcement: rules.Mandatory, MergeBehavior: rules.DenyOverride, Severity: "high", Content: json.RawMessage(`{"prompt":"Old policy"}`)}}
	newRules := []rules.Rule{{Key: "preview.new", Enforcement: rules.Advisory, MergeBehavior: rules.Replace, Severity: "medium", Content: json.RawMessage(`{"prompt":"Candidate policy"}`)}}
	for index, data := range []struct {
		id    uuid.UUID
		state string
		rules []rules.Rule
	}{{publishedID, "published", oldRules}, {candidateID, "draft", newRules}} {
		raw, err := json.Marshal(data.rules)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO rule_versions(id,rule_set_id,version,state,rules,content_sha256,created_by) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7)`, data.id, setID, index+1, data.state, string(raw), strings.Repeat(strings.ReplaceAll(data.id.String(), "-", ""), 2), actor); err != nil {
			t.Fatal(err)
		}
	}
	input := domain.RuleImpactPreviewInput{Repository: repository, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", TargetBranch: "main", Precedence: 100}
	wantCandidate, err := rules.Compile([]rules.Source{{VersionID: candidateID.String(), Precedence: 100, Rules: newRules}})
	if err != nil {
		t.Fatal(err)
	}
	// An unbound draft must compile with its durable UUID, including when
	// the exception resolver has no applicable exceptions to return.
	preview, err := postgres.PreviewRuleVersionImpact(ctx, actor, slug, setID, 2, input)
	if err != nil || !preview.Valid || preview.CandidateSHA256 != wantCandidate.SHA256 || preview.BaselineRuleCount != 0 {
		t.Fatalf("unbound draft preview=%#v error=%v", preview, err)
	}
	if preview.AddedRuleKeys == nil || preview.ChangedRuleKeys == nil || preview.RemovedRuleKeys == nil {
		t.Fatalf("preview must return arrays, including empty differences: %#v", preview)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO rule_bindings(tenant_id,rule_version_id,scope_kind,scope_ref,scope_provider,scope_api_base_url,precedence,target_branch_glob,state,created_by) VALUES($1,$2,'repository',$3,'github','https://api.github.com',100,'main','active',$4)`, tenantID, publishedID, repository, actor); err != nil {
		t.Fatal(err)
	}
	preview, err = postgres.PreviewRuleVersionImpact(ctx, actor, slug, setID, 2, input)
	if err != nil || !preview.Valid || preview.CandidateSHA256 != wantCandidate.SHA256 || preview.BaselineRuleCount != 1 || preview.CandidateRuleCount != 1 || !reflect.DeepEqual(preview.AddedRuleKeys, []string{"preview.new"}) || !reflect.DeepEqual(preview.RemovedRuleKeys, []string{"preview.old"}) {
		t.Fatalf("replacement preview=%#v error=%v", preview, err)
	}
	// Re-previewing the already active version must not duplicate its
	// source identity or change the effective snapshot.
	preview, err = postgres.PreviewRuleVersionImpact(ctx, actor, slug, setID, 1, input)
	if err != nil || !preview.Valid || preview.BaselineSHA256 != preview.CandidateSHA256 || len(preview.AddedRuleKeys)+len(preview.ChangedRuleKeys)+len(preview.RemovedRuleKeys) != 0 {
		t.Fatalf("active version preview=%#v error=%v", preview, err)
	}
	for _, table := range []string{"review_runs", "rule_snapshots", "rule_approval_requests", "rule_test_runs", "audit_events"} {
		var count int
		if err := postgres.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("read-only preview mutated %s: count=%d error=%v", table, count, err)
		}
	}
	// The same candidate must also produce an executable replay receipt,
	// with a durable outbox message and exact snapshot provenance.
	installationID, deliveryID, jobID, requestID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES($1,$2,'github',$3,$4,'https://api.github.com','test-only')`, []any{installationID, tenantID, installationID.String(), repository}},
		{`INSERT INTO webhook_deliveries(id,provider,delivery_id,event_name,payload) VALUES($1,'github',$2,'pull_request','{}')`, []any{deliveryID, deliveryID.String()}},
		{`INSERT INTO review_jobs(id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state) VALUES($1,$2,$3,$4,'github','https://api.github.com',$5,'https://github.com/tests/preview.git',1,'main',$6,'feature/preview',$7,'succeeded')`, []any{jobID, tenantID, installationID, deliveryID, repository, strings.Repeat("b", 40), strings.Repeat("c", 40)}},
		{`INSERT INTO review_requests(id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES($1,$2,$3,'github','https://api.github.com',$4,1)`, []any{requestID, tenantID, installationID, repository}},
		{`INSERT INTO review_runs(id,request_id,legacy_job_id,state,trigger_kind,head_sha,base_sha) VALUES($1,$2,$3,'completed','pull_request',$4,$5)`, []any{runID, requestID, jobID, strings.Repeat("c", 40), strings.Repeat("b", 40)}},
	}
	for _, statement := range statements {
		if _, err := postgres.pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	replay, err := postgres.CreateRuleTestRun(ctx, actor, slug, setID, 2, domain.RuleTestRunInput{SourceRunID: runID, Precedence: 100})
	if err != nil || replay.State != "queued" || replay.SnapshotSHA256 != wantCandidate.SHA256 || replay.RuleVersionID != candidateID || replay.SourceRunID != runID {
		t.Fatalf("create isolated replay=%#v error=%v", replay, err)
	}
	var queuedID string
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'test_run_id' FROM outbox_messages WHERE aggregate_id=$1 AND topic='rule.test.requested'`, replay.ID).Scan(&queuedID); err != nil || queuedID != replay.ID.String() {
		t.Fatalf("replay outbox identity=%s error=%v", queuedID, err)
	}
	var sourceID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT rule_version_id FROM rule_snapshot_sources WHERE snapshot_id=$1`, replay.SnapshotID).Scan(&sourceID); err != nil || sourceID != candidateID {
		t.Fatalf("replay retained wrong version: %s error=%v", sourceID, err)
	}
}
