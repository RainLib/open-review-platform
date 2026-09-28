package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestWorkspaceInvitationLifecycleProtectsMembershipBoundary(t *testing.T) {
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
	tenantSlug := "workspace-invitation-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Workspace invitation integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'admin','admin'),($1,'viewer','viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	if _, err := postgres.CreateWorkspaceInvitation(ctx, "owner", tenantSlug, domain.WorkspaceInvitationInput{Subject: "invalid-owner", Role: "owner", ExpiresInHours: 24}); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("owner invitation error=%v, want ErrInvalidInvitation", err)
	}
	creation, err := postgres.CreateWorkspaceInvitation(ctx, "owner", tenantSlug, domain.WorkspaceInvitationInput{Subject: "invited", Role: "reviewer", ExpiresInHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	if len(creation.Token) != 64 || creation.Invitation.Status != "pending" || creation.Invitation.Subject != "invited" {
		t.Fatalf("unexpected creation=%#v", creation)
	}
	var storedHash string
	if err := postgres.pool.QueryRow(ctx, `SELECT token_sha256 FROM workspace_invitations WHERE id=$1`, creation.Invitation.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == creation.Token || len(storedHash) != 64 {
		t.Fatalf("invitation token was not stored as SHA-256: %q", storedHash)
	}
	if _, err := postgres.ListWorkspaceInvitations(ctx, "viewer", tenantSlug, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer list error=%v, want ErrForbidden", err)
	}
	items, err := postgres.ListWorkspaceInvitations(ctx, "admin", tenantSlug, 10)
	if err != nil || len(items) != 1 || items[0].ID != creation.Invitation.ID {
		t.Fatalf("admin list=%#v error=%v", items, err)
	}
	if _, err := postgres.AcceptWorkspaceInvitation(ctx, "wrong-subject", creation.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong subject acceptance error=%v, want ErrNotFound", err)
	}
	membership, err := postgres.AcceptWorkspaceInvitation(ctx, "invited", creation.Token)
	if err != nil || membership.TenantID != tenantID || membership.Role != "reviewer" {
		t.Fatalf("accept membership=%#v error=%v", membership, err)
	}
	if _, err := postgres.AcceptWorkspaceInvitation(ctx, "invited", creation.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay acceptance error=%v, want ErrNotFound", err)
	}

	revocable, err := postgres.CreateWorkspaceInvitation(ctx, "owner", tenantSlug, domain.WorkspaceInvitationInput{Subject: "revoked-subject", Role: "viewer", ExpiresInHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := postgres.RevokeWorkspaceInvitation(ctx, "owner", tenantSlug, revocable.Invitation.ID)
	if err != nil || revoked.Status != "revoked" || revoked.RevokedBy != "owner" {
		t.Fatalf("revoke=%#v error=%v", revoked, err)
	}
	if _, err := postgres.AcceptWorkspaceInvitation(ctx, "revoked-subject", revocable.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token acceptance error=%v, want ErrNotFound", err)
	}

	var auditCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND action LIKE 'workspace_invitation.%'`, tenantID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 4 {
		t.Fatalf("invitation audit count=%d, want 4", auditCount)
	}
}

func TestMembershipDeactivationImmediatelyRemovesTenantAccess(t *testing.T) {
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
	tenantSlug := "membership-activation-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Membership activation integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner-one','owner'),($1,'owner-two','owner'),($1,'admin','admin')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	if _, err := postgres.UpsertMembership(ctx, "owner-one", tenantSlug, "owner-one", "admin"); !errors.Is(err, ErrConflict) {
		t.Fatalf("self owner role downgrade error=%v, want ErrConflict", err)
	}
	deactivated, err := postgres.SetMembershipActive(ctx, "owner-one", tenantSlug, "admin", false)
	if err != nil || deactivated.Active || deactivated.DeactivatedAt == nil || deactivated.DeactivatedBy != "owner-one" {
		t.Fatalf("deactivate=%#v error=%v", deactivated, err)
	}
	if tenants, err := postgres.ListTenants(ctx, "admin", 10); err != nil || len(tenants) != 0 {
		t.Fatalf("deactivated subject tenants=%#v error=%v", tenants, err)
	}
	if _, err := postgres.UpsertMembership(ctx, "admin", tenantSlug, "viewer", "viewer"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("deactivated admin mutation error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.SetMembershipActive(ctx, "owner-one", tenantSlug, "owner-one", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("self deactivation error=%v, want ErrConflict", err)
	}
	reactivated, err := postgres.SetMembershipActive(ctx, "owner-one", tenantSlug, "admin", true)
	if err != nil || !reactivated.Active || reactivated.DeactivatedAt != nil || reactivated.DeactivatedBy != "" {
		t.Fatalf("reactivate=%#v error=%v", reactivated, err)
	}
	if tenants, err := postgres.ListTenants(ctx, "admin", 10); err != nil || len(tenants) != 1 || tenants[0].Slug != tenantSlug {
		t.Fatalf("reactivated subject tenants=%#v error=%v", tenants, err)
	}
	var auditCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND action IN ('membership.deactivated','membership.reactivated')`, tenantID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("activation audit count=%d, want 2", auditCount)
	}
}
