package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestRuleCatalogInstallIsPinnedIdempotentAndNonExecutable(t *testing.T) {
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
	tenantSlug := "rule-catalog-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Rule catalog integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup := context.Background()
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM rule_versions WHERE rule_set_id IN (SELECT id FROM rule_sets WHERE tenant_id=$1)`, tenantID)
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM rule_sets WHERE tenant_id=$1`, tenantID)
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'viewer','viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}

	entries, err := postgres.ListRuleCatalog(ctx, "owner", tenantSlug)
	if err != nil || len(entries) == 0 || !entries[0].CanInstall {
		t.Fatalf("owner catalog=%#v error=%v", entries, err)
	}
	viewerEntries, err := postgres.ListRuleCatalog(ctx, "viewer", tenantSlug)
	if err != nil || len(viewerEntries) != len(entries) || viewerEntries[0].CanInstall {
		t.Fatalf("viewer catalog=%#v error=%v", viewerEntries, err)
	}
	entry := entries[0]
	input := domain.RuleCatalogInstallInput{Version: entry.Version, ContentSHA256: entry.ContentSHA256}
	created, err := postgres.InstallRuleCatalogEntry(ctx, "owner", tenantSlug, entry.ID, input)
	if err != nil || created.Replayed || created.RuleSet.Catalog == nil || created.RuleSet.Catalog.ContentSHA256 != entry.ContentSHA256 || created.Draft.State != "draft" {
		t.Fatalf("created catalog installation=%#v error=%v", created, err)
	}
	if created.RuleSet.LatestVersion == nil || created.RuleSet.LatestVersion.ID != created.Draft.ID {
		t.Fatalf("installation did not return its draft summary: %#v", created.RuleSet)
	}
	var bindingCount, auditCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM rule_bindings WHERE rule_version_id=$1`, created.Draft.ID).Scan(&bindingCount); err != nil || bindingCount != 0 {
		t.Fatalf("catalog install created a binding: count=%d error=%v", bindingCount, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND action='rule_catalog.installed' AND target=$2`, tenantID, created.RuleSet.ID.String()).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("catalog install audit count=%d error=%v", auditCount, err)
	}

	replayed, err := postgres.InstallRuleCatalogEntry(ctx, "owner", tenantSlug, entry.ID, input)
	if err != nil || !replayed.Replayed || replayed.RuleSet.ID != created.RuleSet.ID || replayed.Draft.ID != created.Draft.ID {
		t.Fatalf("catalog install replay=%#v error=%v", replayed, err)
	}
	if _, err := postgres.InstallRuleCatalogEntry(ctx, "owner", tenantSlug, entry.ID, domain.RuleCatalogInstallInput{Version: entry.Version, ContentSHA256: "stale"}); !errors.Is(err, ErrInvalidRuleCatalog) {
		t.Fatalf("stale catalog identity error=%v, want ErrInvalidRuleCatalog", err)
	}
	if _, err := postgres.InstallRuleCatalogEntry(ctx, "viewer", tenantSlug, entry.ID, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer catalog install error=%v, want ErrForbidden", err)
	}
}
