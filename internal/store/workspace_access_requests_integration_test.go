package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestWorkspaceAccessRequestLifecycle(t *testing.T) {
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
	slug := "access-request-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Workspace access integration')`, tenantID, slug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role) VALUES($1,'owner','owner'),($1,'admin','admin'),($1,'viewer','viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}

	// All ordinary outcomes are indistinguishable to the requester.
	for _, requestedSlug := range []string{"unknown-workspace", slug, slug} {
		if err := postgres.RequestWorkspaceAccess(ctx, "requester", requestedSlug, "Please add me"); err != nil {
			t.Fatalf("request %q: %v", requestedSlug, err)
		}
	}
	if err := postgres.RequestWorkspaceAccess(ctx, "viewer", slug, "already a member"); err != nil {
		t.Fatalf("existing member: %v", err)
	}
	if _, err := postgres.ListWorkspaceAccessRequests(ctx, "viewer", slug, 20); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer list error=%v, want forbidden", err)
	}
	if _, err := postgres.ListWorkspaceAccessRequests(ctx, "outsider", slug, 20); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list error=%v, want hidden", err)
	}
	items, err := postgres.ListWorkspaceAccessRequests(ctx, "admin", slug, 20)
	if err != nil || len(items) != 1 || items[0].Subject != "requester" || items[0].Note != "Please add me" {
		t.Fatalf("admin list=%#v error=%v", items, err)
	}
	request := items[0]
	if _, err := postgres.DecideWorkspaceAccessRequest(ctx, "admin", slug, request.ID, request.Revision, "approve"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin decision error=%v, want forbidden", err)
	}
	if _, err := postgres.DecideWorkspaceAccessRequest(ctx, "owner", slug, request.ID, request.Revision+1, "approve"); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale decision error=%v, want revision conflict", err)
	}
	approved, err := postgres.DecideWorkspaceAccessRequest(ctx, "owner", slug, request.ID, request.Revision, "approve")
	if err != nil || approved.Status != "approved" || approved.Revision != request.Revision+1 {
		t.Fatalf("approval=%#v error=%v", approved, err)
	}
	if _, err := postgres.DecideWorkspaceAccessRequest(ctx, "owner", slug, request.ID, request.Revision, "reject"); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("replayed decision error=%v, want revision conflict", err)
	}
	_, role, err := postgres.authorizedTenant(ctx, "requester", slug)
	if err != nil || role != "viewer" {
		t.Fatalf("approved requester role=%q error=%v", role, err)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := postgres.RequestWorkspaceAccess(ctx, "parallel-requester", slug, "parallel"); err != nil {
				t.Errorf("parallel request: %v", err)
			}
		}()
	}
	wg.Wait()
	items, err = postgres.ListWorkspaceAccessRequests(ctx, "owner", slug, 20)
	if err != nil || len(items) != 1 || items[0].Subject != "parallel-requester" {
		t.Fatalf("parallel list=%#v error=%v", items, err)
	}
	rejected, err := postgres.DecideWorkspaceAccessRequest(ctx, "owner", slug, items[0].ID, items[0].Revision, "reject")
	if err != nil || rejected.Status != "rejected" {
		t.Fatalf("reject=%#v error=%v", rejected, err)
	}
	if _, _, err := postgres.authorizedTenant(ctx, "parallel-requester", slug); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected requester access error=%v, want hidden", err)
	}
	if err := postgres.RequestWorkspaceAccess(ctx, "parallel-requester", slug, "retry too soon"); err != nil {
		t.Fatalf("recent retry: %v", err)
	}
	items, err = postgres.ListWorkspaceAccessRequests(ctx, "owner", slug, 20)
	if err != nil || len(items) != 0 {
		t.Fatalf("recent retry list=%#v error=%v", items, err)
	}
	var auditCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action LIKE 'workspace_access.%'`, tenantID).Scan(&auditCount); err != nil || auditCount != 4 {
		t.Fatalf("audit count=%d error=%v, want 4", auditCount, err)
	}
}
