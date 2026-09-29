package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestGetInstallationReadsExactTenantScopedRowBeyondOverviewLimit(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("exact installation read requires an isolated migrated PostgreSQL database")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()
	tenantID, otherTenantID := uuid.New(), uuid.New()
	tenantSlug := "installation-exact-" + tenantID.String()[:8]
	otherSlug := "installation-other-" + otherTenantID.String()[:8]
	oldID, recentID, otherID := uuid.New(), uuid.New(), uuid.New()
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Exact installation'),($3,$4,'Other tenant')`,
		tenantID, tenantSlug, otherTenantID, otherSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'owner','owner',TRUE)`, tenantID); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id, tenant               uuid.UUID
		external, scope, created string
	}{
		{oldID, tenantID, "old-" + oldID.String(), "RainLib/old", "2026-01-01T00:00:00Z"},
		{recentID, tenantID, "new-" + recentID.String(), "RainLib/new", "2026-02-01T00:00:00Z"},
		{otherID, otherTenantID, "other-" + otherID.String(), "Other/private", "2026-03-01T00:00:00Z"},
	} {
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state,created_at)
			VALUES($1,$2,'github',$3,$4,'https://api.github.com','test-secret','verified',$5)`,
			item.id, item.tenant, item.external, item.scope, item.created); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := postgres.ListInstallations(ctx, "owner", tenantSlug, 1)
	if err != nil || len(listed) != 1 || listed[0].ID != recentID {
		t.Fatalf("bounded overview=%#v error=%v", listed, err)
	}
	exact, err := postgres.GetInstallation(ctx, "owner", tenantSlug, oldID)
	if err != nil || exact.ID != oldID || exact.RepositoryScope != "RainLib/old" || !exact.Active || exact.VerificationState != "verified" {
		t.Fatalf("exact older installation=%#v error=%v", exact, err)
	}
	if _, err := postgres.GetInstallation(ctx, "owner", tenantSlug, otherID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant installation read error=%v, want not found", err)
	}
	if _, err := postgres.GetInstallation(ctx, "outsider", tenantSlug, oldID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-member installation read error=%v, want forbidden", err)
	}
}
