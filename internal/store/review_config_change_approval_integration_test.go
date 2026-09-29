package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestModelRouteChangeRequiresIndependentApproval(t *testing.T) {
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
	tenantSlug := "model-route-approval-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Model Route Approval')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'admin','admin'),($1,'rule-admin','rule_admin')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	initial := json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"model-a","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_TEST","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":2}`)
	first, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{Section: domain.ReviewConfigModels, ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 0, Content: initial})
	if err != nil || first.Revision != 1 {
		t.Fatalf("create initial model route=%#v error=%v", first, err)
	}
	updated := json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"model-b","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_TEST","effort":"medium","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":2}`)
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{Section: domain.ReviewConfigModels, ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 1, Content: updated}); !errors.Is(err, ErrReviewConfigApprovalRequired) {
		t.Fatalf("direct model update error=%v, want ErrReviewConfigApprovalRequired", err)
	}
	proposal, err := postgres.RequestReviewConfigChange(ctx, "owner", tenantSlug, domain.ReviewConfigChangeRequestInput{Section: domain.ReviewConfigModels, ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 1, Content: updated, Reason: "Move production review to the vetted replacement model"})
	if err != nil || proposal.State != domain.ReviewConfigChangePending {
		t.Fatalf("request proposal=%#v error=%v", proposal, err)
	}
	if _, err := postgres.DecideReviewConfigChange(ctx, "owner", tenantSlug, proposal.ID, domain.ReviewConfigChangeDecisionInput{Decision: "approved"}); !errors.Is(err, ErrSeparationOfDuties) {
		t.Fatalf("self approval error=%v, want ErrSeparationOfDuties", err)
	}
	approved, err := postgres.DecideReviewConfigChange(ctx, "admin", tenantSlug, proposal.ID, domain.ReviewConfigChangeDecisionInput{Decision: "approved", Comment: "Provider route and budget reviewed."})
	if err != nil || approved.State != domain.ReviewConfigChangeApproved || approved.AppliedRevision != 2 {
		t.Fatalf("approve proposal=%#v error=%v", approved, err)
	}
	active, err := postgres.GetReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigModels, domain.ReviewConfigScope{Kind: domain.ReviewConfigTenantScope})
	var activeContent map[string]any
	if decodeErr := json.Unmarshal(active.Content, &activeContent); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if err != nil || active.Revision != 2 || activeContent["model"] != "model-b" {
		t.Fatalf("active model route=%#v error=%v", active, err)
	}
	requests, err := postgres.ListReviewConfigChangeRequests(ctx, "owner", tenantSlug, 10)
	if err != nil || len(requests) != 1 || requests[0].State != domain.ReviewConfigChangeApproved || requests[0].ApprovalCount != 1 {
		t.Fatalf("approval evidence=%#v error=%v", requests, err)
	}

	// A repository inheritance restore is also a high-impact model-route
	// change. Its browser body must be ignored: the durable proposal records
	// the locked override that will actually be disabled.
	repositoryScope := "RainLib/open-review-platform"
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations (tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,'github',$2,$3,'https://api.github.com','github-app','verified')`, tenantID, "model-route-"+tenantID.String(), repositoryScope); err != nil {
		t.Fatal(err)
	}
	override := json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"repository-model","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_TEST","effort":"medium","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":64}`)
	repository, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{Section: domain.ReviewConfigModels, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repositoryScope, ScopeProvider: domain.ProviderGitHub, ScopeAPIBaseURL: "https://api.github.com", ExpectedRevision: 0, Content: override})
	if err != nil || repository.Revision != 1 {
		t.Fatalf("create repository model override=%#v error=%v", repository, err)
	}
	if _, err := postgres.RestoreInheritedReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{Section: domain.ReviewConfigModels, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repositoryScope, ScopeProvider: domain.ProviderGitHub, ScopeAPIBaseURL: "https://api.github.com", ExpectedRevision: 1}); !errors.Is(err, ErrReviewConfigApprovalRequired) {
		t.Fatalf("direct repository model restore error=%v, want ErrReviewConfigApprovalRequired", err)
	}
	untrustedBody := json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"must-not-be-recorded","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_TEST","effort":"high","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":2}`)
	restore, err := postgres.RequestReviewConfigChange(ctx, "owner", tenantSlug, domain.ReviewConfigChangeRequestInput{Section: domain.ReviewConfigModels, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repositoryScope, ScopeProvider: domain.ProviderGitHub, ScopeAPIBaseURL: "https://api.github.com", ExpectedRevision: 1, Content: untrustedBody, Reason: "Return this repository to the workspace model route", Operation: domain.ReviewConfigChangeRestoreInheritance})
	if err != nil || restore.ProposedContentSHA256 != repository.ContentSHA256 || restore.BaseContentSHA256 != repository.ContentSHA256 {
		t.Fatalf("restore proposal=%#v repository=%#v error=%v", restore, repository, err)
	}
	if strings.Contains(string(restore.ProposedContent), "must-not-be-recorded") {
		t.Fatalf("restore proposal retained untrusted browser content: %s", restore.ProposedContent)
	}
	restored, err := postgres.DecideReviewConfigChange(ctx, "admin", tenantSlug, restore.ID, domain.ReviewConfigChangeDecisionInput{Decision: "approved"})
	if err != nil || restored.State != domain.ReviewConfigChangeApproved || restored.AppliedRevision != 1 {
		t.Fatalf("approve restore=%#v error=%v", restored, err)
	}
	afterRestore, err := postgres.GetReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigModels, domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repositoryScope, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com"})
	if err != nil || !afterRestore.Inherited || afterRestore.Revision != 2 {
		t.Fatalf("restored inheritance=%#v error=%v", afterRestore, err)
	}
}
