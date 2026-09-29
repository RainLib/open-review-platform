package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestWithoutRepositoryModelConcurrency(t *testing.T) {
	content, err := withoutRepositoryModelConcurrency(json.RawMessage(`{"enabled":false,"provider":"deployment","protocol":"deployment","base_url":"","model":"","credential_ref":"","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":9}`))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	if _, found := value["max_concurrent_runs"]; found {
		t.Fatalf("repository model route retained tenant concurrency: %s", content)
	}
	if _, err := domain.DecodeModelRoute(content); err != nil {
		t.Fatalf("stripped repository model route must remain valid: %v", err)
	}
}

func TestReviewConfigurationVersionsInheritanceAndRestore(t *testing.T) {
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
	tenantSlug := "review-config-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Review Config')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner'), ($1, 'viewer', 'viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	defaultView, err := postgres.GetReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigGeneral, domain.ReviewConfigScope{Kind: domain.ReviewConfigTenantScope})
	if err != nil || defaultView.OriginScopeKind != "default" || defaultView.Revision != 0 {
		t.Fatalf("default view=%#v error=%v", defaultView, err)
	}
	tenantContent := json.RawMessage(`{"automatic_review":true,"review_language":"en"}`)
	tenantView, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 0, Content: tenantContent})
	if err != nil || tenantView.Revision != 1 || tenantView.ContentSHA256 == "" {
		t.Fatalf("tenant save=%#v error=%v", tenantView, err)
	}

	repository := "RainLib/open-review-platform"
	inherited, err := postgres.GetReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigGeneral, domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository})
	if err != nil || !inherited.Inherited || inherited.OriginScopeKind != "tenant" || inherited.RequestedScopeRef != repository {
		t.Fatalf("inherited view=%#v error=%v", inherited, err)
	}
	override, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository, ExpectedRevision: 0, Content: json.RawMessage(`{"automatic_review":false,"review_language":"zh-CN"}`)})
	if err != nil || override.Revision != 1 || override.Inherited {
		t.Fatalf("override=%#v error=%v", override, err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository, ExpectedRevision: 0, Content: tenantContent}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale save error=%v, want ErrRevisionConflict", err)
	}
	override, err = postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository, ExpectedRevision: 1, Content: json.RawMessage(`{"automatic_review":true,"review_language":"zh-CN"}`)})
	if err != nil || override.Revision != 2 {
		t.Fatalf("second override=%#v error=%v", override, err)
	}
	history, err := postgres.ListReviewConfigVersions(ctx, "owner", tenantSlug, domain.ReviewConfigGeneral, domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository}, 10)
	if err != nil || history.OriginScopeKind != "repository" || len(history.Versions) != 2 || history.Versions[0].Revision != 2 || history.Versions[0].ContentSHA256 == "" {
		t.Fatalf("history=%#v error=%v", history, err)
	}
	restored, err := postgres.RestoreInheritedReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository, ExpectedRevision: 2})
	if err != nil || !restored.Inherited || restored.OriginScopeKind != "tenant" || restored.Revision != 1 {
		t.Fatalf("restored=%#v error=%v", restored, err)
	}
	inheritedHistory, err := postgres.ListReviewConfigVersions(ctx, "owner", tenantSlug, domain.ReviewConfigGeneral, domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository}, 10)
	if err != nil || !inheritedHistory.Inherited || inheritedHistory.OriginScopeKind != "tenant" || len(inheritedHistory.Versions) != 1 || inheritedHistory.Versions[0].Revision != 1 {
		t.Fatalf("inherited history=%#v error=%v", inheritedHistory, err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "viewer", tenantSlug, domain.ReviewConfigInput{Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 1, Content: tenantContent}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer save error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.GetReviewConfig(ctx, "outsider", tenantSlug, domain.ReviewConfigGeneral, domain.ReviewConfigScope{Kind: domain.ReviewConfigTenantScope}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider read error=%v, want ErrForbidden", err)
	}
	issueTriage, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigIssueTriage, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: 0, Content: domain.DefaultReviewConfig(domain.ReviewConfigIssueTriage),
	})
	if err != nil || issueTriage.Revision != 1 || issueTriage.ContentSHA256 == "" {
		t.Fatalf("issue triage save=%#v error=%v", issueTriage, err)
	}
}

func TestIssueTriageRepositoryOverridesAreIsolatedByProviderIdentity(t *testing.T) {
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
	tenantSlug := "issue-triage-scope-" + tenantID.String()[:8]
	repository := "RainLib/open-review-platform"
	githubBaseURL := "https://api.github.com"
	gitlabBaseURL := "https://gitlab.example.test/api/v4"
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Issue Triage Scope')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	for _, installation := range []struct {
		provider domain.Provider
		apiBase  string
	}{
		{provider: domain.ProviderGitHub, apiBase: githubBaseURL},
		{provider: domain.ProviderGitLab, apiBase: gitlabBaseURL},
	} {
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref, verification_state)
			VALUES ($1, $2, $3, $4, 'RainLib/*', $5, 'secret://issue-triage-scope', 'verified')`,
			uuid.New(), tenantID, installation.provider, "issue-triage-"+string(installation.provider)+"-"+tenantID.String(), installation.apiBase); err != nil {
			t.Fatal(err)
		}
	}

	githubScope := domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: domain.ProviderGitHub, APIBaseURL: githubBaseURL}
	gitlabScope := domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: domain.ProviderGitLab, APIBaseURL: gitlabBaseURL}
	githubView, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigIssueTriage, ScopeKind: githubScope.Kind, ScopeRef: githubScope.Ref,
		ScopeProvider: githubScope.Provider, ScopeAPIBaseURL: githubScope.APIBaseURL, ExpectedRevision: 0,
		Content: json.RawMessage(`{"preset":"engineering","custom_guidance":"Use GitHub issue labels as evidence."}`),
	})
	if err != nil || githubView.OriginProvider != domain.ProviderGitHub || !strings.Contains(string(githubView.Content), "GitHub issue labels") {
		t.Fatalf("save GitHub issue triage=%#v error=%v", githubView, err)
	}
	gitlabView, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigIssueTriage, ScopeKind: gitlabScope.Kind, ScopeRef: gitlabScope.Ref,
		ScopeProvider: gitlabScope.Provider, ScopeAPIBaseURL: gitlabScope.APIBaseURL, ExpectedRevision: 0,
		Content: json.RawMessage(`{"preset":"engineering","custom_guidance":"Use self-managed GitLab labels as evidence."}`),
	})
	if err != nil || gitlabView.OriginProvider != domain.ProviderGitLab || !strings.Contains(string(gitlabView.Content), "self-managed GitLab labels") {
		t.Fatalf("save GitLab issue triage=%#v error=%v", gitlabView, err)
	}

	for _, expected := range []struct {
		scope    domain.ReviewConfigScope
		needle   string
		provider domain.Provider
		apiBase  string
	}{
		{scope: githubScope, needle: "GitHub issue labels", provider: domain.ProviderGitHub, apiBase: githubBaseURL},
		{scope: gitlabScope, needle: "self-managed GitLab labels", provider: domain.ProviderGitLab, apiBase: gitlabBaseURL},
	} {
		view, err := postgres.GetReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigIssueTriage, expected.scope)
		if err != nil || view.OriginProvider != expected.provider || view.OriginAPIBaseURL != expected.apiBase || !strings.Contains(string(view.Content), expected.needle) {
			t.Fatalf("get scope=%#v view=%#v error=%v", expected.scope, view, err)
		}
		tx, err := postgres.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := resolveEffectiveReviewConfiguration(ctx, tx, tenantID, expected.scope, domain.ReviewConfigIssueTriage)
		_ = tx.Rollback(ctx)
		if err != nil || resolved.OriginProvider != expected.provider || resolved.OriginAPIBaseURL != expected.apiBase || !strings.Contains(string(resolved.Content), expected.needle) {
			t.Fatalf("resolve scope=%#v resolved=%#v error=%v", expected.scope, resolved, err)
		}
	}

	_, err = postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigIssueTriage, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository,
		ScopeProvider: domain.ProviderGitLab, ScopeAPIBaseURL: "https://untrusted-gitlab.example.test/api/v4", ExpectedRevision: 0,
		Content: json.RawMessage(`{"preset":"engineering"}`),
	})
	if !errors.Is(err, ErrInvalidReviewConfig) {
		t.Fatalf("uninstalled provider scope save error=%v, want ErrInvalidReviewConfig", err)
	}
}

func TestRuleBindingsAndExceptionsAreIsolatedByProviderIdentity(t *testing.T) {
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
	tenantSlug := "rule-provider-scope-" + tenantID.String()[:8]
	repository := "RainLib/open-review-platform"
	githubScope := domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com"}
	gitlabScope := domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab.example.test/api/v4"}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Rule Provider Scope')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner'), ($1, 'rule-admin', 'rule_admin')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	for _, scope := range []domain.ReviewConfigScope{githubScope, gitlabScope} {
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref, verification_state)
			VALUES ($1, $2, $3, $4, 'RainLib/*', $5, 'secret://rule-provider-scope', 'verified')`,
			uuid.New(), tenantID, scope.Provider, "rule-scope-"+string(scope.Provider)+"-"+tenantID.String(), scope.APIBaseURL); err != nil {
			t.Fatal(err)
		}
	}

	githubSetID, githubVersionID := uuid.New(), uuid.New()
	gitlabSetID, gitlabVersionID := uuid.New(), uuid.New()
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO rule_sets (id, tenant_id, name, created_by) VALUES ($1, $2, 'GitHub scoped rules', 'owner')`, githubSetID, tenantID)
	batch.Queue(`INSERT INTO rule_sets (id, tenant_id, name, created_by) VALUES ($1, $2, 'GitLab scoped rules', 'owner')`, gitlabSetID, tenantID)
	batch.Queue(`INSERT INTO rule_versions (id, rule_set_id, version, state, rules, content_sha256, created_by, published_at) VALUES ($1, $2, 1, 'published', '[{"key":"security.github-only","enforcement":"mandatory","merge_behavior":"replace","severity":"high","content":{"instruction":"GitHub-only policy"}}]'::jsonb, $3, 'owner', now())`, githubVersionID, githubSetID, strings.Repeat("a", 64))
	batch.Queue(`INSERT INTO rule_versions (id, rule_set_id, version, state, rules, content_sha256, created_by, published_at) VALUES ($1, $2, 1, 'published', '[{"key":"security.gitlab-only","enforcement":"mandatory","merge_behavior":"replace","severity":"high","content":{"instruction":"GitLab-only policy"}}]'::jsonb, $3, 'owner', now())`, gitlabVersionID, gitlabSetID, strings.Repeat("b", 64))
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}

	for _, expected := range []struct {
		scope     domain.ReviewConfigScope
		versionID uuid.UUID
	}{
		{scope: githubScope, versionID: githubVersionID},
		{scope: gitlabScope, versionID: gitlabVersionID},
	} {
		binding, err := postgres.CreateRuleBinding(ctx, "owner", tenantSlug, domain.RuleBindingInput{
			RuleVersionID: expected.versionID, ScopeKind: string(expected.scope.Kind), ScopeRef: expected.scope.Ref,
			ScopeProvider: expected.scope.Provider, ScopeAPIBaseURL: expected.scope.APIBaseURL,
			Precedence: 100, TargetBranchGlob: "main", State: "active",
		})
		if err != nil || binding.ScopeProvider != expected.scope.Provider || binding.ScopeAPIBaseURL != expected.scope.APIBaseURL {
			t.Fatalf("create provider binding=%#v error=%v", binding, err)
		}
	}
	if _, err := postgres.CreateRuleBinding(ctx, "owner", tenantSlug, domain.RuleBindingInput{
		RuleVersionID: githubVersionID, ScopeKind: "repository", ScopeRef: repository, Precedence: 1, State: "active",
	}); !errors.Is(err, ErrInvalidRuleBinding) {
		t.Fatalf("legacy raw rule binding write error=%v, want ErrInvalidRuleBinding", err)
	}
	if _, err := postgres.CreateRuleBinding(ctx, "owner", tenantSlug, domain.RuleBindingInput{
		RuleVersionID: githubVersionID, ScopeKind: "repository", ScopeRef: repository, ScopeProvider: domain.ProviderGitLab,
		ScopeAPIBaseURL: "https://untrusted-gitlab.example.test/api/v4", Precedence: 1, State: "active",
	}); !errors.Is(err, ErrInvalidRuleBinding) {
		t.Fatalf("uninstalled rule binding write error=%v, want ErrInvalidRuleBinding", err)
	}

	exception, err := postgres.CreateRuleException(ctx, "owner", tenantSlug, domain.RuleExceptionInput{
		RuleVersionID: githubVersionID, RuleKey: "security.github-only", ScopeKind: "repository", ScopeRef: repository,
		ScopeProvider: githubScope.Provider, ScopeAPIBaseURL: githubScope.APIBaseURL,
		Reason: "Compensating control covers this exact GitHub scope", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create provider exception: %v", err)
	}
	if _, err := postgres.DecideRuleException(ctx, "rule-admin", tenantSlug, exception.ID, domain.RuleExceptionDecisionInput{Decision: "approved"}); err != nil {
		t.Fatalf("approve provider exception: %v", err)
	}

	for _, expected := range []struct {
		scope     domain.ReviewConfigScope
		versionID uuid.UUID
	}{
		{scope: githubScope, versionID: githubVersionID},
		{scope: gitlabScope, versionID: gitlabVersionID},
	} {
		tx, err := postgres.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		snapshotID, err := resolveRuleSnapshot(ctx, tx, tenantID, expected.scope, "main", nil)
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("resolve provider snapshot scope=%#v: %v", expected.scope, err)
		}
		var sourceIDs []uuid.UUID
		rows, err := tx.Query(ctx, `SELECT rule_version_id FROM rule_snapshot_sources WHERE snapshot_id=$1 ORDER BY rule_version_id`, snapshotID)
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		for rows.Next() {
			var sourceID uuid.UUID
			if err := rows.Scan(&sourceID); err != nil {
				rows.Close()
				_ = tx.Rollback(ctx)
				t.Fatal(err)
			}
			sourceIDs = append(sourceIDs, sourceID)
		}
		rows.Close()
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if len(sourceIDs) != 1 || sourceIDs[0] != expected.versionID {
			t.Fatalf("snapshot source IDs for %s = %v, want only %s", expected.scope.Provider, sourceIDs, expected.versionID)
		}
	}

	for _, expected := range []struct {
		scope     domain.ReviewConfigScope
		wantCount int
	}{
		{scope: githubScope, wantCount: 1},
		{scope: gitlabScope, wantCount: 0},
	} {
		applied, _, err := resolveApplicableRuleExceptions(ctx, postgres.pool, tenantID, expected.scope, "main", map[string]int{githubVersionID.String(): 100})
		if err != nil || len(applied) != expected.wantCount {
			t.Fatalf("provider exception resolution scope=%#v count=%d error=%v, want=%d", expected.scope, len(applied), err, expected.wantCount)
		}
	}
}

func TestReviewConfigurationSnapshotIsImmutableAtAdmission(t *testing.T) {
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
	tenantID, installationID, deliveryID, requestID, runID, jobID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tenantSlug := "review-config-snapshot-" + tenantID.String()[:8]
	repository := "RainLib/open-review-platform"
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Review Config Snapshot')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref) VALUES ($1, $2, 'github', $3, 'RainLib/*', 'https://api.github.com', 'secret://snapshot')`, installationID, tenantID, "snapshot-installation-"+tenantID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id, provider, delivery_id, event_name, payload) VALUES ($1, 'github', $2, 'pull_request', '{}'::jsonb)`, deliveryID, "snapshot-delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id, tenant_id, installation_id, delivery_id, provider, api_base_url, repository, clone_url, review_number, base_ref, base_sha, head_ref, head_sha, state) VALUES ($1, $2, $3, $4, 'github', 'https://api.github.com', $5, 'https://github.com/RainLib/open-review-platform.git', 8, 'main', 'snapshot-base', 'feature/snapshot', 'snapshot-head', 'succeeded')`, jobID, tenantID, installationID, deliveryID, repository)
	batch.Queue(`INSERT INTO review_requests (id, tenant_id, installation_id, provider, api_base_url, repository, review_number) VALUES ($1, $2, $3, 'github', 'https://api.github.com', $4, 8)`, requestID, tenantID, installationID, repository)
	batch.Queue(`INSERT INTO review_runs (id, request_id, legacy_job_id, state, trigger_kind, head_sha, base_sha) VALUES ($1, $2, $3, 'acknowledged', 'pull_request', 'snapshot-head', 'snapshot-base')`, runID, requestID, jobID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	tenantGeneral, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: 0, Content: json.RawMessage(`{"automatic_review":true,"review_language":"en"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	repositoryPrompts, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigPrompts, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository,
		ExpectedRevision: 0, Content: json.RawMessage(`{"system_instruction":"Review risk first.","allow_repository_instructions":false}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	tenantMessages, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigMessages, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: 0, Content: json.RawMessage(`{"started":"Acknowledged {{repository}}.","progress":"Reviewing {{head_sha}}.","success":"Passed.","recommendation":"Recommendations attached.","blocked":"Blocked.","failed":"Failed.","superseded":"Superseded."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	tenantSummary, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigSummary, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: 0, Content: json.RawMessage(`{"sections":["scope","verification"],"max_characters":6000,"include_change_contract":false,"include_verification_evidence":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	tenantCategories, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigCategories, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: 0, Content: json.RawMessage(`{"security":{"enabled":true,"minimum_severity":"high"},"performance":{"enabled":false,"minimum_severity":"medium"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	tenantFilters, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigFilters, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: 0, Content: json.RawMessage(`{"include_paths":["internal/**"],"exclude_paths":["**/fixtures/**"],"skip_generated":true,"skip_vendor":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshotReviewConfigurations(ctx, tx, runID, tenantID, domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com"}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: tenantGeneral.Revision, Content: json.RawMessage(`{"automatic_review":false,"review_language":"zh-CN"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigPrompts, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository,
		ExpectedRevision: repositoryPrompts.Revision, Content: json.RawMessage(`{"system_instruction":"Updated after admission.","max_prompt_tokens":6400,"allow_repository_instructions":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigMessages, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: tenantMessages.Revision, Content: json.RawMessage(`{"started":"Updated after admission.","progress":"Updated after admission.","success":"Updated after admission.","recommendation":"Updated after admission.","blocked":"Updated after admission.","failed":"Updated after admission.","superseded":"Updated after admission."}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigSummary, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: tenantSummary.Revision, Content: json.RawMessage(`{"sections":["provenance"],"max_characters":6000,"include_change_contract":true,"include_verification_evidence":false}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigCategories, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: tenantCategories.Revision, Content: json.RawMessage(`{"security":{"enabled":false,"minimum_severity":"critical"}}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigFilters, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: tenantFilters.Revision, Content: json.RawMessage(`{"include_paths":["apps/**"],"exclude_paths":[],"skip_generated":false,"skip_vendor":false}`),
	}); err != nil {
		t.Fatal(err)
	}
	evidence, err := postgres.GetReviewEvidence(ctx, "owner", tenantSlug, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.ConfigurationSnapshot) != len(domain.ReviewConfigSections()) {
		t.Fatalf("configuration snapshot count=%d, want %d", len(evidence.ConfigurationSnapshot), len(domain.ReviewConfigSections()))
	}
	for _, snapshot := range evidence.ConfigurationSnapshot {
		switch snapshot.Section {
		case domain.ReviewConfigGeneral:
			if snapshot.OriginScopeKind != "tenant" || snapshot.OriginRevision != tenantGeneral.Revision || snapshot.ContentSHA256 != tenantGeneral.ContentSHA256 {
				t.Fatalf("general snapshot=%#v, want original tenant revision", snapshot)
			}
		case domain.ReviewConfigPrompts:
			if snapshot.OriginScopeKind != "repository" || snapshot.OriginScopeRef != repository || snapshot.ContentSHA256 != repositoryPrompts.ContentSHA256 {
				t.Fatalf("prompt snapshot=%#v, want repository override", snapshot)
			}
		}
	}
	messages, err := postgres.ReviewConfigSnapshotForJob(ctx, jobID, domain.ReviewConfigMessages)
	if err != nil {
		t.Fatal(err)
	}
	if messages.Section != domain.ReviewConfigMessages || len(messages.Content) == 0 {
		t.Fatalf("message snapshot=%#v, want immutable messages content", messages)
	}
	if messages.ContentSHA256 != tenantMessages.ContentSHA256 || !strings.Contains(string(messages.Content), "Acknowledged") || strings.Contains(string(messages.Content), "Updated after admission") {
		t.Fatalf("messages snapshot=%s, want original immutable revision %s", messages.Content, tenantMessages.ContentSHA256)
	}
	prompts, err := postgres.ReviewConfigSnapshotForJob(ctx, jobID, domain.ReviewConfigPrompts)
	if err != nil {
		t.Fatal(err)
	}
	if prompts.ContentSHA256 != repositoryPrompts.ContentSHA256 || !strings.Contains(string(prompts.Content), "Review risk first.") || strings.Contains(string(prompts.Content), "Updated after admission") {
		t.Fatalf("prompts snapshot=%s, want original immutable revision %s", prompts.Content, repositoryPrompts.ContentSHA256)
	}
	summary, err := postgres.ReviewConfigSnapshotForJob(ctx, jobID, domain.ReviewConfigSummary)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ContentSHA256 != tenantSummary.ContentSHA256 || !strings.Contains(string(summary.Content), `"scope"`) || strings.Contains(string(summary.Content), `"provenance"`) {
		t.Fatalf("summary snapshot=%s, want original immutable revision %s", summary.Content, tenantSummary.ContentSHA256)
	}
	categories, err := postgres.ReviewConfigSnapshotForJob(ctx, jobID, domain.ReviewConfigCategories)
	if err != nil {
		t.Fatal(err)
	}
	if categories.ContentSHA256 != tenantCategories.ContentSHA256 || !strings.Contains(string(categories.Content), `"performance"`) || strings.Contains(string(categories.Content), `"enabled":true`) {
		t.Fatalf("categories snapshot=%s, want original immutable revision %s", categories.Content, tenantCategories.ContentSHA256)
	}
	filters, err := postgres.ReviewConfigSnapshotForJob(ctx, jobID, domain.ReviewConfigFilters)
	if err != nil {
		t.Fatal(err)
	}
	if filters.ContentSHA256 != tenantFilters.ContentSHA256 || !strings.Contains(string(filters.Content), `"internal/**"`) || strings.Contains(string(filters.Content), `"apps/**"`) {
		t.Fatalf("filters snapshot=%s, want original immutable revision %s", filters.Content, tenantFilters.ContentSHA256)
	}
}

func TestWebhookAdmissionPolicyUsesEffectiveReviewConfiguration(t *testing.T) {
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
	tenantSlug := "review-admission-" + tenantID.String()[:8]
	repository := "RainLib/open-review-platform"
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Review Admission')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	installationID := uuid.New()
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, automatic_reviews, minimum_severity, api_base_url, credential_ref, verification_state)
		VALUES ($1, $2, 'github', 'admission-installation', 'RainLib/*', TRUE, 'medium', 'https://api.github.com', 'secret://admission', 'legacy')`, installationID, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	general, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: 0,
		Content: json.RawMessage(`{"automatic_review":true,"review_drafts":false,"rereview_on_push":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	installation := domain.Installation{TenantID: tenantID, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", AutomaticReviews: true}
	baseEvent := domain.InboundEvent{Provider: domain.ProviderGitHub, Repository: repository, BaseRef: "main", Action: "opened", Author: "maintainer"}
	skip := func(event domain.InboundEvent) string {
		tx, err := postgres.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		reason, err := reviewAdmissionSkipReason(ctx, tx, installation, event)
		if err != nil {
			t.Fatal(err)
		}
		return reason
	}
	installation.AuthorScope, installation.AuthorExternalID = "mine", "42"
	if got := skip(domain.InboundEvent{Provider: baseEvent.Provider, Repository: baseEvent.Repository, BaseRef: baseEvent.BaseRef, Action: baseEvent.Action, Author: baseEvent.Author, AuthorExternalID: "17"}); got != "pull request author does not match this connection's OAuth-bound author scope" {
		t.Fatalf("different PR author admission skip=%q", got)
	}
	if got := skip(domain.InboundEvent{Provider: baseEvent.Provider, Repository: baseEvent.Repository, BaseRef: baseEvent.BaseRef, Action: baseEvent.Action, Author: baseEvent.Author}); got != "pull request author does not match this connection's OAuth-bound author scope" {
		t.Fatalf("missing PR author admission skip=%q", got)
	}
	if got := skip(domain.InboundEvent{Provider: baseEvent.Provider, Repository: baseEvent.Repository, BaseRef: baseEvent.BaseRef, Action: baseEvent.Action, Author: baseEvent.Author, TriggerKind: "comment"}); got != "" {
		t.Fatalf("explicit authorized command should bypass automatic author scope: %q", got)
	}
	installation.AuthorScope, installation.AuthorExternalID = "all", ""

	if got := skip(domain.InboundEvent{Provider: baseEvent.Provider, Repository: baseEvent.Repository, BaseRef: baseEvent.BaseRef, Action: baseEvent.Action, Author: baseEvent.Author, IsDraft: true}); got != "draft pull requests are excluded by workspace policy" {
		t.Fatalf("draft admission skip=%q", got)
	}
	skippedJob, duplicate, err := postgres.Enqueue(ctx, domain.InboundEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", DeliveryID: "admission-policy-skip-" + tenantID.String(), EventName: "pull_request",
		InstallationExternalID: "admission-installation", Repository: repository, CloneURL: "https://github.com/RainLib/open-review-platform.git", ReviewNumber: 7,
		BaseRef: "main", BaseSHA: "base", HeadRef: "feature", HeadSHA: "head", Payload: json.RawMessage(`{}`), ReceivedAt: time.Now().UTC(), Action: "opened", IsDraft: true, Author: "maintainer",
	})
	if err != nil || duplicate || skippedJob.ID != uuid.Nil || skippedJob.State != domain.JobCancelled || skippedJob.ErrorMessage == "" {
		t.Fatalf("webhook policy skip=%#v duplicate=%t error=%v", skippedJob, duplicate, err)
	}
	var jobCount, auditCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM review_jobs WHERE tenant_id = $1`, tenantID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id = $1 AND action = 'review.admission_skipped'`, tenantID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 0 || auditCount != 1 {
		t.Fatalf("policy skip must not create a job and must write one audit event, jobs=%d audit=%d", jobCount, auditCount)
	}
	general, err = postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: general.Revision,
		Content: json.RawMessage(`{"automatic_review":true,"review_drafts":true,"rereview_on_push":false}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := skip(domain.InboundEvent{Provider: baseEvent.Provider, Repository: baseEvent.Repository, BaseRef: baseEvent.BaseRef, Action: "synchronize", Author: baseEvent.Author}); got != "push re-reviews are disabled by workspace policy" {
		t.Fatalf("push admission skip=%q", got)
	}
	general, err = postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigTenantScope, ExpectedRevision: general.Revision,
		Content: json.RawMessage(`{"automatic_review":true,"review_drafts":true,"rereview_on_push":true,"default_review_mode":"security"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigFilters, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository, ExpectedRevision: 0,
		Content: json.RawMessage(`{"exclude_authors":["dependabot[bot]"],"required_labels":["review-ready"],"target_branches":["main"]}`),
	}); err != nil {
		t.Fatal(err)
	}
	if got := skip(domain.InboundEvent{Provider: baseEvent.Provider, Repository: baseEvent.Repository, BaseRef: baseEvent.BaseRef, Action: baseEvent.Action, Author: "dependabot[bot]", Labels: []string{"review-ready"}}); got == "" {
		t.Fatal("excluded author was admitted")
	}
	if got := skip(domain.InboundEvent{Provider: baseEvent.Provider, Repository: baseEvent.Repository, BaseRef: baseEvent.BaseRef, Action: baseEvent.Action, Author: baseEvent.Author}); got == "" {
		t.Fatal("missing required label was admitted")
	}
	if got := skip(domain.InboundEvent{Provider: baseEvent.Provider, Repository: baseEvent.Repository, BaseRef: "develop", Action: baseEvent.Action, Author: baseEvent.Author, Labels: []string{"review-ready"}}); got == "" {
		t.Fatal("non-matching target branch was admitted")
	}
	if got := skip(domain.InboundEvent{Provider: baseEvent.Provider, Repository: baseEvent.Repository, BaseRef: baseEvent.BaseRef, Action: baseEvent.Action, Author: baseEvent.Author, Labels: []string{"review-ready"}}); got != "" {
		t.Fatalf("matching metadata should admit review, got skip=%q", got)
	}
	admitted, duplicate, err := postgres.Enqueue(ctx, domain.InboundEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", DeliveryID: "admission-metadata-first-" + tenantID.String(), EventName: "pull_request",
		InstallationExternalID: "admission-installation", Repository: repository, CloneURL: "https://github.com/RainLib/open-review-platform.git", ReviewNumber: 8,
		BaseRef: "main", BaseSHA: "base", HeadRef: "feature", HeadSHA: "head-one", Payload: json.RawMessage(`{}`), ReceivedAt: time.Now().UTC(), Action: "opened", Title: "Harden webhook admission", Author: "maintainer", Labels: []string{"review-ready"},
	})
	if err != nil || duplicate || admitted.ID == uuid.Nil {
		t.Fatalf("admit review metadata job=%#v duplicate=%t error=%v", admitted, duplicate, err)
	}
	var admittedMode domain.ReviewMode
	if err := postgres.pool.QueryRow(ctx, `SELECT review_mode FROM review_runs WHERE legacy_job_id=$1`, admitted.ID).Scan(&admittedMode); err != nil {
		t.Fatal(err)
	}
	if admittedMode != domain.ReviewModeSecurity {
		t.Fatalf("automatic run mode=%q, want repository default %q", admittedMode, domain.ReviewModeSecurity)
	}
	general, err = postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository, ExpectedRevision: 0,
		Content: json.RawMessage(`{"trigger_mode":"manual"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := skip(baseEvent); got != "automatic reviews require an explicit comment command or CLI request" {
		t.Fatalf("manual trigger skip=%q", got)
	}
	general, err = postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository, ExpectedRevision: general.Revision,
		Content: json.RawMessage(`{"trigger_mode":"off"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled, duplicate, err := postgres.Enqueue(ctx, domain.InboundEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", DeliveryID: "admission-policy-off-" + tenantID.String(), EventName: "pull_request",
		InstallationExternalID: "admission-installation", Repository: repository, CloneURL: "https://github.com/RainLib/open-review-platform.git", ReviewNumber: 9,
		BaseRef: "main", BaseSHA: "base", HeadRef: "feature", HeadSHA: "head-off", Payload: json.RawMessage(`{}`), ReceivedAt: time.Now().UTC(), Action: "opened", Author: "maintainer", Labels: []string{"review-ready"},
	})
	if err != nil || duplicate || disabled.ID != uuid.Nil || disabled.State != domain.JobCancelled || disabled.ErrorMessage != "reviews are disabled by repository policy" {
		t.Fatalf("off trigger admission=%#v duplicate=%t error=%v", disabled, duplicate, err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigRepositoryScope, ScopeRef: repository, ExpectedRevision: general.Revision,
		Content: json.RawMessage(`{"trigger_mode":"automatic","default_review_mode":"security"}`),
	}); err != nil {
		t.Fatal(err)
	}
	updated, duplicate, err := postgres.Enqueue(ctx, domain.InboundEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", DeliveryID: "admission-metadata-update-" + tenantID.String(), EventName: "pull_request",
		InstallationExternalID: "admission-installation", Repository: repository, CloneURL: "https://github.com/RainLib/open-review-platform.git", ReviewNumber: 8,
		BaseRef: "main", BaseSHA: "base", HeadRef: "feature", HeadSHA: "head-two", Payload: json.RawMessage(`{}`), ReceivedAt: time.Now().UTC(), Action: "synchronize", Title: "Harden webhook admission safely", Author: "reviewer", Labels: []string{"review-ready"},
	})
	if err != nil || duplicate || updated.ID == uuid.Nil {
		t.Fatalf("update review metadata job=%#v duplicate=%t error=%v", updated, duplicate, err)
	}
	var title, author string
	if err := postgres.pool.QueryRow(ctx, `SELECT title,author FROM review_requests WHERE tenant_id=$1 AND review_number=8`, tenantID).Scan(&title, &author); err != nil {
		t.Fatal(err)
	}
	if title != "Harden webhook admission safely" || author != "reviewer" {
		t.Fatalf("request metadata title=%q author=%q", title, author)
	}
}
