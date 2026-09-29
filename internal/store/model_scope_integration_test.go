package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

// One repository name exists on three providers/instances in a single workspace.
// A probe, pending approval, or restore must never operate on a sibling route.
func TestModelRoutesIsolateProviderInstanceAcrossProbesAndApprovals(t *testing.T) {
	if os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL") == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()
	tenantID := uuid.New()
	slug := "model-scope-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Model scope')`, tenantID, slug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role) VALUES($1,'owner','owner'),($1,'admin','admin')`, tenantID); err != nil {
		t.Fatal(err)
	}
	repository := "team/service"
	scopes := []domain.ReviewConfigScope{
		{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com"},
		{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab-a.example/api/v4"},
		{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab-b.example/api/v4"},
	}
	content := func(model string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":%q,"credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_TEST","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5}`, model))
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", slug, domain.ReviewConfigInput{Section: domain.ReviewConfigModels, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository, Content: content("unqualified")}); !errors.Is(err, ErrInvalidReviewConfig) {
		t.Fatalf("unqualified model save error=%v", err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", slug, domain.ReviewConfigInput{Section: domain.ReviewConfigModels, ScopeKind: domain.ReviewConfigTenantScope, Content: content("workspace-model")}); err != nil {
		t.Fatal(err)
	}
	proposals := make([]domain.ReviewConfigChangeRequest, 0, len(scopes))
	for index, scope := range scopes {
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations(tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,$3,$4,$5,'secret://fixture','verified')`, tenantID, scope.Provider, fmt.Sprintf("%s-%d", tenantID, index), "team/*", scope.APIBaseURL); err != nil {
			t.Fatal(err)
		}
		model := fmt.Sprintf("instance-%d", index)
		view, err := postgres.SaveReviewConfig(ctx, "owner", slug, domain.ReviewConfigInput{Section: domain.ReviewConfigModels, ScopeKind: scope.Kind, ScopeRef: scope.Ref, ScopeProvider: scope.Provider, ScopeAPIBaseURL: scope.APIBaseURL, Content: content(model)})
		if err != nil || view.Revision != 1 || view.OriginProvider != scope.Provider || view.OriginAPIBaseURL != scope.APIBaseURL {
			t.Fatalf("scope %d save=%#v err=%v", index, view, err)
		}
		probeInput := domain.ModelProbeInput{ScopeKind: scope.Kind, ScopeRef: scope.Ref, ScopeProvider: scope.Provider, ScopeAPIBaseURL: scope.APIBaseURL, ExpectedRevision: 1, Acknowledged: true}
		probe, err := postgres.RequestModelProbe(ctx, "owner", slug, probeInput)
		if err != nil || probe.Model != model || probe.ScopeProvider != scope.Provider || probe.ScopeAPIBaseURL != scope.APIBaseURL {
			t.Fatalf("scope %d probe=%#v err=%v", index, probe, err)
		}
		if _, err := postgres.RequestModelProbe(ctx, "owner", slug, probeInput); !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate scope %d probe err=%v", index, err)
		}
		proposal, err := postgres.RequestReviewConfigChange(ctx, "owner", slug, domain.ReviewConfigChangeRequestInput{Section: domain.ReviewConfigModels, ScopeKind: scope.Kind, ScopeRef: scope.Ref, ScopeProvider: scope.Provider, ScopeAPIBaseURL: scope.APIBaseURL, ExpectedRevision: 1, Content: content(model + "-next"), Reason: "Review this instance route independently"})
		if err != nil || proposal.ScopeProvider != scope.Provider || proposal.ScopeAPIBaseURL != scope.APIBaseURL {
			t.Fatalf("scope %d proposal=%#v err=%v", index, proposal, err)
		}
		proposals = append(proposals, proposal)
	}
	for index, scope := range scopes {
		probes, err := postgres.ListModelProbes(ctx, "owner", slug, scope, 20)
		if err != nil || len(probes) != 1 || probes[0].Model != fmt.Sprintf("instance-%d", index) {
			t.Fatalf("scope %d probes=%#v err=%v", index, probes, err)
		}
	}
	// Approving only GitLab A must not mutate GitHub or GitLab B, even though all
	// three active routes and proposals have revision 1 and the same path.
	if _, err := postgres.DecideReviewConfigChange(ctx, "admin", slug, proposals[1].ID, domain.ReviewConfigChangeDecisionInput{Decision: "approved"}); err != nil {
		t.Fatal(err)
	}
	for index, scope := range scopes {
		wantRevision := 1
		wantModel := fmt.Sprintf("instance-%d", index)
		if index == 1 {
			wantRevision, wantModel = 2, wantModel+"-next"
		}
		view, err := postgres.GetReviewConfig(ctx, "owner", slug, domain.ReviewConfigModels, scope)
		if err != nil || view.Revision != wantRevision {
			t.Fatalf("scope %d view=%#v err=%v", index, view, err)
		}
		route, err := domain.DecodeModelRoute(view.Content)
		if err != nil || route.Model != wantModel {
			t.Fatalf("scope %d model=%s err=%v", index, route.Model, err)
		}
		history, err := postgres.ListReviewConfigVersions(ctx, "owner", slug, domain.ReviewConfigModels, scope, 20)
		if err != nil || len(history.Versions) != wantRevision || history.OriginAPIBaseURL != scope.APIBaseURL {
			t.Fatalf("scope %d history=%#v err=%v", index, history, err)
		}
	}
	requests, err := postgres.ListReviewConfigChangeRequests(ctx, "admin", slug, 20)
	if err != nil || len(requests) != 3 {
		t.Fatalf("requests=%#v err=%v", requests, err)
	}
	for _, request := range requests {
		if request.ScopeProvider == "" || request.ScopeAPIBaseURL == "" {
			t.Fatalf("request lost provider identity: %#v", request)
		}
	}
	if _, err := postgres.DecideReviewConfigChange(ctx, "admin", slug, proposals[2].ID, domain.ReviewConfigChangeDecisionInput{Decision: "rejected"}); err != nil {
		t.Fatal(err)
	}
	b := scopes[2]
	restore, err := postgres.RequestReviewConfigChange(ctx, "owner", slug, domain.ReviewConfigChangeRequestInput{Section: domain.ReviewConfigModels, ScopeKind: b.Kind, ScopeRef: b.Ref, ScopeProvider: b.Provider, ScopeAPIBaseURL: b.APIBaseURL, ExpectedRevision: 1, Operation: domain.ReviewConfigChangeRestoreInheritance, Reason: "Restore this instance only"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.DecideReviewConfigChange(ctx, "admin", slug, restore.ID, domain.ReviewConfigChangeDecisionInput{Decision: "approved"}); err != nil {
		t.Fatal(err)
	}
	view, err := postgres.GetReviewConfig(ctx, "owner", slug, domain.ReviewConfigModels, b)
	if err != nil || !view.Inherited || view.OriginScopeKind != "tenant" {
		t.Fatalf("restore instance B=%#v err=%v", view, err)
	}
	view, err = postgres.GetReviewConfig(ctx, "owner", slug, domain.ReviewConfigModels, scopes[1])
	if err != nil || view.Inherited || view.Revision != 2 {
		t.Fatalf("restore B altered A=%#v err=%v", view, err)
	}
	if _, err := postgres.ListModelProbes(ctx, "outsider", slug, scopes[1], 20); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider probes err=%v", err)
	}
	// Legacy records remain an explicit compatibility source. A qualified page
	// must not treat the legacy version as its own revision during bootstrap.
	legacyID := uuid.New()
	canonical, hash, _ := domain.CanonicalReviewConfig(domain.ReviewConfigModels, content("legacy-model"))
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_configurations(id,tenant_id,section,scope_kind,scope_ref,active) VALUES($1,$2,'models','repository','team/legacy',TRUE)`, legacyID, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_configuration_versions(configuration_id,revision,content,content_sha256,created_by) VALUES($1,1,$2,$3,'owner')`, legacyID, canonical, hash); err != nil {
		t.Fatal(err)
	}
	legacyScope := scopes[1]
	legacyScope.Ref = "team/legacy"
	legacyView, err := postgres.GetReviewConfig(ctx, "owner", slug, domain.ReviewConfigModels, legacyScope)
	if err != nil || !legacyView.Inherited || legacyView.OriginProvider != "" || legacyView.RequestedProvider != domain.ProviderGitLab {
		t.Fatalf("legacy fallback=%#v err=%v", legacyView, err)
	}
}
