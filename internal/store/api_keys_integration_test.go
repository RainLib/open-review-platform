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

func TestAPIKeyLifecycleStoresOnlyHashAndEnforcesRestrictions(t *testing.T) {
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
	tenantSlug := "api-key-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'API Key Integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner'), ($1, 'viewer', 'viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	creation, err := postgres.CreateAPIKey(ctx, "owner", tenantSlug, domain.APIKeyInput{
		Name: "CLI automation", Scopes: []string{domain.APIKeyScopeReviewsCreate, domain.APIKeyScopeReviewsRead},
		Repositories: []string{"RainLib/open-review-platform"}, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if creation.Secret == "" || creation.APIKey.Prefix == creation.Secret || creation.APIKey.CallerSubject == "" {
		t.Fatalf("unexpected API key creation: %#v", creation)
	}
	var storedSecret string
	if err := postgres.pool.QueryRow(ctx, `SELECT encode(secret_hash, 'hex') FROM api_keys WHERE id = $1`, creation.APIKey.ID).Scan(&storedSecret); err != nil {
		t.Fatal(err)
	}
	if storedSecret == creation.Secret || len(storedSecret) != 64 {
		t.Fatalf("stored credential is not a SHA-256 hash: %q", storedSecret)
	}

	keys, err := postgres.ListAPIKeys(ctx, "owner", tenantSlug, 100)
	if err != nil || len(keys) != 1 || keys[0].Prefix != creation.APIKey.Prefix {
		t.Fatalf("list keys=%#v error=%v", keys, err)
	}
	if _, err := postgres.ListAPIKeys(ctx, "viewer", tenantSlug, 100); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer list error=%v, want ErrForbidden", err)
	}

	principal, err := postgres.AuthenticateAPIKey(ctx, creation.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if principal.TenantSlug != tenantSlug || !principal.HasScope(domain.APIKeyScopeReviewsCreate) || !principal.AllowsRepository("RainLib/open-review-platform") || principal.AllowsRepository("RainLib/private") {
		t.Fatalf("unexpected API key principal: %#v", principal)
	}

	revoked, err := postgres.RevokeAPIKey(ctx, "owner", tenantSlug, creation.APIKey.ID)
	if err != nil || revoked.RevokedAt == nil || revoked.RevokedBy != "owner" {
		t.Fatalf("revoked key=%#v error=%v", revoked, err)
	}
	if _, err := postgres.AuthenticateAPIKey(ctx, creation.Secret); !errors.Is(err, ErrInvalidAPIKeyCredential) {
		t.Fatalf("revoked key authenticate error=%v, want ErrInvalidAPIKeyCredential", err)
	}
}
