package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestAuthorScopedInstallationRequiresCurrentOAuthBinding(t *testing.T) {
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
	tenantSlug := "author-scope-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Author Scope Test')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'other','admin')`, tenantID); err != nil {
		t.Fatal(err)
	}
	automatic := true
	input := domain.InstallationInput{
		Provider: domain.ProviderGitHub, ExternalID: "author-scope-" + tenantID.String(),
		RepositoryScope: "RainLib/*", AutomaticReviews: &automatic,
		AuthorScope: "mine", AuthorExternalID: "42", MinimumSeverity: "medium",
		APIBaseURL: "https://api.github.com", CredentialRef: "github-app",
	}
	if _, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unbound author scope error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.UpsertProviderIdentity(ctx, "owner", tenantSlug, domain.ProviderIdentity{Provider: domain.ProviderGitHub, ExternalID: "42", Subject: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.CreateInstallation(ctx, "other", tenantSlug, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("another workspace admin reused the author scope: %v", err)
	}
	installation, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, input)
	if err != nil || installation.AuthorScope != "mine" || installation.AuthorExternalID != "42" {
		t.Fatalf("author-scoped installation=%#v error=%v", installation, err)
	}
	listed, err := postgres.ListInstallations(ctx, "owner", tenantSlug, 10)
	if err != nil || len(listed) != 1 || listed[0].AuthorScope != "mine" {
		t.Fatalf("listed author scope=%#v error=%v", listed, err)
	}
	replayed, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, input)
	if err != nil || replayed.ID != installation.ID {
		t.Fatalf("exact OAuth-scoped replay=%#v error=%v", replayed, err)
	}
	if _, err := postgres.UpsertProviderIdentity(ctx, "other", tenantSlug, domain.ProviderIdentity{Provider: domain.ProviderGitHub, ExternalID: "42", Subject: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("rebound identity replay error=%v, want ErrForbidden", err)
	}
}
