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

func TestAuditHistoryPaginatesAndLoadsOnlyOwnTenantEvents(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	s, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tenant, other := uuid.New(), uuid.New()
	slug := "audit-page-" + tenant.String()[:8]
	if _, err := s.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES ($1,$2,'Audit page'),($3,$4,'Other audit page')`, tenant, slug, other, "other-"+slug); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := s.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id IN ($1,$2)`, tenant, other); err != nil {
			t.Errorf("clean up audit fixture: %v", err)
		}
	}()
	if _, err := s.pool.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'viewer','viewer'),($1,'reviewer','reviewer'),($2,'other-owner','owner')`, tenant, other); err != nil {
		t.Fatal(err)
	}
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	if ids[0].String() < ids[1].String() {
		ids[0], ids[1] = ids[1], ids[0]
	}
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	for index, id := range ids {
		owner := tenant
		if index == 3 {
			owner = other
		}
		at := base.Add(-time.Duration(index) * time.Minute)
		if index == 1 {
			at = base // Exercise the event ID tie-breaker at one timestamp.
		}
		target := "repo/" + id.String()
		if _, err := s.pool.Exec(ctx, `INSERT INTO audit_events(id,tenant_id,actor_subject,action,target,created_at) VALUES ($1,$2,'owner','review.completed',$3,$4)`, id, owner, target, at); err != nil {
			t.Fatal(err)
		}
	}
	from, until := base.Add(-time.Minute), base.Add(time.Minute)
	filtered, err := s.ListAuditEvents(ctx, "viewer", slug, domain.AuditFilter{Target: "REPO/" + ids[1].String(), From: &from, Until: &until, Limit: 2})
	if err != nil || len(filtered) != 1 || filtered[0].ID != ids[1] {
		t.Fatalf("target/date audit filter=%v err=%v", filtered, err)
	}
	outside, err := s.ListAuditEvents(ctx, "viewer", slug, domain.AuditFilter{From: &until, Limit: 2})
	if err != nil || len(outside) != 0 {
		t.Fatalf("date boundary audit filter=%v err=%v", outside, err)
	}
	if _, err := s.ListAuditEvents(ctx, "viewer", slug, domain.AuditFilter{From: &until, Until: &from, Limit: 2}); !errors.Is(err, ErrInvalidAuditFilter) {
		t.Fatalf("invalid audit date filter error=%v", err)
	}
	first, err := s.ListAuditEvents(ctx, "viewer", slug, domain.AuditFilter{Action: "review.", Limit: 2})
	if err != nil || len(first) != 3 || first[0].ID != ids[0] || first[1].ID != ids[1] || first[2].ID != ids[2] {
		t.Fatalf("first audit page=%v err=%v", first, err)
	}
	// A filter is a literal prefix, not a SQL LIKE pattern: '_' and '%' must
	// not expand one action into another.
	for _, prefix := range []string{"review_", "review%"} {
		wildcard, err := s.ListAuditEvents(ctx, "viewer", slug, domain.AuditFilter{Action: prefix, Limit: 2})
		if err != nil || len(wildcard) != 0 {
			t.Fatalf("wildcard-shaped literal filter %q page=%v err=%v", prefix, wildcard, err)
		}
	}
	second, err := s.ListAuditEvents(ctx, "viewer", slug, domain.AuditFilter{Limit: 2, Before: &ids[1]})
	if err != nil || len(second) != 1 || second[0].ID != ids[2] {
		t.Fatalf("second audit page=%v err=%v", second, err)
	}
	foreignCursor, err := s.ListAuditEvents(ctx, "viewer", slug, domain.AuditFilter{Limit: 2, Before: &ids[3]})
	if err != nil || len(foreignCursor) != 0 {
		t.Fatalf("foreign cursor page=%v err=%v", foreignCursor, err)
	}
	if event, err := s.GetAuditEvent(ctx, "viewer", slug, ids[2]); err != nil || event.ID != ids[2] {
		t.Fatalf("older audit detail=%v err=%v", event, err)
	}
	if _, err := s.GetAuditEvent(ctx, "viewer", slug, ids[3]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign audit detail error=%v", err)
	}
	if _, err := s.GetAuditEvent(ctx, "reviewer", slug, ids[0]); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized audit detail error=%v", err)
	}
}
