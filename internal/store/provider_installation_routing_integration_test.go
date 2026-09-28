package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestInboundInstallationRoutingRequiresSelectedRepositoryScope(t *testing.T) {
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
	tenantSlug := "provider-routing-" + tenantID.String()[:8]
	githubInstallationID, gitlabInstallationID := uuid.New(), uuid.New()
	githubExternalID := "github-install-" + githubInstallationID.String()
	gitlabScopeIdentity := "gitlab-scope-" + gitlabInstallationID.String()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Provider routing integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,automatic_reviews,minimum_severity,api_base_url,credential_ref,verification_state)
		VALUES
			($1,$2,'github',$3,'RainLib/allowed',TRUE,'medium','https://api.github.com','github-app','verified'),
			($4,$2,'gitlab',$5,'group/*',TRUE,'medium','https://gitlab.example.test/api/v4','gitlab-token','verified')`,
		githubInstallationID, tenantID, githubExternalID, gitlabInstallationID, gitlabScopeIdentity); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO workspace_setup_checkpoints (tenant_id,current_step,revision,updated_by,completed_at)
		VALUES ($1,'complete',1,'owner',$2)`, tenantID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	githubAllowed, _, err := postgres.Enqueue(ctx, inboundRoutingEvent(
		domain.ProviderGitHub, "https://api.github.com", githubExternalID,
		"RainLib/allowed", "https://github.com/RainLib/allowed.git", 1,
	))
	if err != nil || githubAllowed.InstallationID != githubInstallationID {
		t.Fatalf("GitHub selected repository admission job=%#v error=%v", githubAllowed, err)
	}
	if _, _, err := postgres.Enqueue(ctx, inboundRoutingEvent(
		domain.ProviderGitHub, "https://api.github.com", githubExternalID,
		"RainLib/not-selected", "https://github.com/RainLib/not-selected.git", 2,
	)); !errors.Is(err, ErrUnknownInstallation) {
		t.Fatalf("GitHub out-of-scope admission error=%v, want ErrUnknownInstallation", err)
	}

	// GitLab Note/MR hooks carry the project ID (here 987654), not the opaque
	// identity generated at OAuth setup. The selected group scope must route it
	// without accepting repositories outside that scope.
	gitlabAllowed, _, err := postgres.Enqueue(ctx, inboundRoutingEvent(
		domain.ProviderGitLab, "https://gitlab.example.test/api/v4", "987654",
		"group/repository", "https://gitlab.example.test/group/repository.git", 3,
	))
	if err != nil || gitlabAllowed.InstallationID != gitlabInstallationID {
		t.Fatalf("GitLab scope admission job=%#v error=%v", gitlabAllowed, err)
	}
	if _, _, err := postgres.Enqueue(ctx, inboundRoutingEvent(
		domain.ProviderGitLab, "https://gitlab.example.test/api/v4", "987654",
		"outside/repository", "https://gitlab.example.test/outside/repository.git", 4,
	)); !errors.Is(err, ErrUnknownInstallation) {
		t.Fatalf("GitLab out-of-scope admission error=%v, want ErrUnknownInstallation", err)
	}
}

func inboundRoutingEvent(provider domain.Provider, apiBaseURL, externalID, repository, cloneURL string, reviewNumber int) domain.InboundEvent {
	return domain.InboundEvent{
		Provider: provider, APIBaseURL: apiBaseURL,
		DeliveryID: "routing-" + uuid.NewString(), EventName: "pull_request",
		InstallationExternalID: externalID, Repository: repository, CloneURL: cloneURL,
		ReviewNumber: reviewNumber, BaseRef: "main", BaseSHA: "base-" + uuid.NewString(),
		HeadRef: "feature/routing", HeadSHA: "head-" + uuid.NewString(),
		Payload: []byte(`{}`), ReceivedAt: time.Now().UTC(), Action: "opened",
	}
}
