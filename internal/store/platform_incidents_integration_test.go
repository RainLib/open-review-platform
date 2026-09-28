package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestPlatformIncidentTimelineIsTenantScopedRevisionedAndAudited(t *testing.T) {
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
	tenantSlug := "incident-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Platform Incident Integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'viewer','viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	opened, err := postgres.CreatePlatformIncident(ctx, "owner", tenantSlug, domain.PlatformIncidentInput{
		Title: "Provider delivery latency", Scope: "provider", AffectedArea: "GitHub publication",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened.State != domain.PlatformIncidentActive || opened.Revision != 1 || opened.CreatedBy != "owner" || opened.ResolvedAt != nil {
		t.Fatalf("unexpected opened incident: %#v", opened)
	}
	if _, err := postgres.CreatePlatformIncident(ctx, "viewer", tenantSlug, domain.PlatformIncidentInput{
		Title: "Viewer cannot open", Scope: "workspace", AffectedArea: "console",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.ResolvePlatformIncident(ctx, "owner", tenantSlug, opened.ID, domain.PlatformIncidentResolutionInput{
		ExpectedRevision: 2, Resolution: "Publication receipt retry succeeded.",
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale resolution error=%v, want ErrRevisionConflict", err)
	}
	resolved, err := postgres.ResolvePlatformIncident(ctx, "owner", tenantSlug, opened.ID, domain.PlatformIncidentResolutionInput{
		ExpectedRevision: 1, Resolution: "Publication receipt retry succeeded.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.State != domain.PlatformIncidentResolved || resolved.Revision != 2 || resolved.ResolvedAt == nil || resolved.ResolvedBy != "owner" {
		t.Fatalf("unexpected resolved incident: %#v", resolved)
	}
	overview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil {
		t.Fatal(err)
	}
	if !overview.IncidentTrackingAvailable || len(overview.Incidents) != 1 || overview.Incidents[0].ID != opened.ID || overview.Incidents[0].State != domain.PlatformIncidentResolved {
		t.Fatalf("unexpected incident overview: %#v", overview.Incidents)
	}
	var auditCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND action IN ('platform_incident.opened','platform_incident.resolved')`, tenantID).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("incident audit count=%d err=%v, want 2", auditCount, err)
	}
}
