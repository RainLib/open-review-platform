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

func TestIssueFormatTemplateCatalogIsTenantScopedVersionedAndAudited(t *testing.T) {
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
	tenantSlug := "issue-format-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Issue format integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup := context.Background()
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'viewer','viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}

	security := domain.IssueFormatTemplateInput{
		Name:        "Security boundary",
		Description: "Reusable evidence contract",
		Content: json.RawMessage(`{
			"preset":"security","language":"inherit",
			"required_issue_sections":["outcome","evidence","security_impact","risk"],
			"response_sections":["assessment","risk","next_steps","provenance"],
			"collapse_secondary":true,"link_file_references":true,
			"reaction_feedback":true,"max_items_per_section":8,
			"custom_guidance":"Require CWE evidence."
		}`),
	}
	created, err := postgres.CreateIssueFormatTemplate(ctx, "owner", tenantSlug, security)
	if err != nil || created.Revision != 1 || len(created.ContentSHA256) != 64 {
		t.Fatalf("created template=%#v error=%v", created, err)
	}
	if _, err := postgres.CreateIssueFormatTemplate(ctx, "owner", tenantSlug, security); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name error=%v, want ErrConflict", err)
	}
	if _, err := postgres.CreateIssueFormatTemplate(ctx, "viewer", tenantSlug, domain.IssueFormatTemplateInput{Name: "Viewer format", Content: security.Content}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create error=%v, want ErrForbidden", err)
	}
	listed, err := postgres.ListIssueFormatTemplates(ctx, "viewer", tenantSlug)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID || listed[0].ContentSHA256 != created.ContentSHA256 {
		t.Fatalf("viewer list=%#v error=%v", listed, err)
	}

	security.Name = "Security and privacy"
	security.Description = "Version two"
	security.ExpectedRevision = 1
	updated, err := postgres.UpdateIssueFormatTemplate(ctx, "owner", tenantSlug, created.ID, security)
	if err != nil || updated.Revision != 2 || updated.Name != security.Name {
		t.Fatalf("updated template=%#v error=%v", updated, err)
	}
	if _, err := postgres.UpdateIssueFormatTemplate(ctx, "owner", tenantSlug, created.ID, security); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update error=%v, want ErrRevisionConflict", err)
	}
	if err := postgres.ArchiveIssueFormatTemplate(ctx, "viewer", tenantSlug, created.ID, 2); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer archive error=%v, want ErrForbidden", err)
	}
	if err := postgres.ArchiveIssueFormatTemplate(ctx, "owner", tenantSlug, created.ID, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale archive error=%v, want ErrRevisionConflict", err)
	}
	if err := postgres.ArchiveIssueFormatTemplate(ctx, "owner", tenantSlug, created.ID, 2); err != nil {
		t.Fatalf("archive template: %v", err)
	}
	listed, err = postgres.ListIssueFormatTemplates(ctx, "owner", tenantSlug)
	if err != nil || len(listed) != 0 {
		t.Fatalf("archived template remains visible: %#v error=%v", listed, err)
	}

	var versionCount, auditCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_format_template_versions WHERE template_id=$1`, created.ID).Scan(&versionCount); err != nil || versionCount != 2 {
		t.Fatalf("version count=%d error=%v", versionCount, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND target=$2 AND action LIKE 'issue_format_template.%'`, tenantID, created.ID.String()).Scan(&auditCount); err != nil || auditCount != 3 {
		t.Fatalf("audit count=%d error=%v", auditCount, err)
	}
}
