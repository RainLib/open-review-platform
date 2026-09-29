package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestModelProbeReceiptSnapshotsRouteAndCompletesUnderLease(t *testing.T) {
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
	tenantSlug := "model-probe-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Model Probe')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'viewer','viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	config, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigModels, ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 0,
		Content: json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"deepseek-v4-flash","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_TEST","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5}`),
	})
	if err != nil || config.Revision != 1 {
		t.Fatalf("save config=%#v error=%v", config, err)
	}
	if _, err := postgres.RequestModelProbe(ctx, "viewer", tenantSlug, domain.ModelProbeInput{ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 1, Acknowledged: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer model probe error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.RequestModelProbe(ctx, "owner", tenantSlug, domain.ModelProbeInput{ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 1}); !errors.Is(err, ErrInvalidReviewConfig) {
		t.Fatalf("unacknowledged model probe error=%v, want ErrInvalidReviewConfig", err)
	}
	receipt, err := postgres.RequestModelProbe(ctx, "owner", tenantSlug, domain.ModelProbeInput{ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 1, Acknowledged: true})
	if err != nil || receipt.State != domain.ModelProbeQueued || receipt.EndpointHost != "models.example" || receipt.Model != "deepseek-v4-flash" {
		t.Fatalf("request receipt=%#v error=%v", receipt, err)
	}
	if _, err := postgres.RequestModelProbe(ctx, "owner", tenantSlug, domain.ModelProbeInput{ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 1, Acknowledged: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate active probe error=%v, want ErrConflict", err)
	}
	target, err := postgres.ClaimModelProbe(ctx, "model-probe-test")
	if err != nil || target.ReceiptID != receipt.ID || target.Route.CredentialRef != "env://OPEN_REVIEW_MODEL_SECRET_TEST" {
		t.Fatalf("claim target=%#v error=%v", target, err)
	}
	if err := postgres.CompleteModelProbe(ctx, receipt.ID, "model-probe-test", domain.ModelProbeResult{ResponseSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LatencyMS: 37}); err != nil {
		t.Fatal(err)
	}
	receipts, err := postgres.ListModelProbes(ctx, "owner", tenantSlug, domain.ReviewConfigScope{Kind: domain.ReviewConfigTenantScope}, 10)
	if err != nil || len(receipts) != 1 || receipts[0].State != domain.ModelProbeSucceeded || receipts[0].ResponseSHA256 == "" || receipts[0].LatencyMS != 37 || receipts[0].ErrorMessage != "" {
		t.Fatalf("list receipts=%#v error=%v", receipts, err)
	}
}
