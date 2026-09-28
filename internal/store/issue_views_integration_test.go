package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func issueViewsTestStore(t *testing.T) (*PostgresStore, context.Context, uuid.UUID, string) {
	t.Helper()
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	s, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	id := uuid.New()
	slug := "issue-views-" + id.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Issue views test')`, id, slug)
	batch.Queue(`INSERT INTO memberships(tenant_id,subject,role) VALUES($1,'owner','owner'),($1,'admin','admin'),($1,'Alice','viewer'),($1,'alice','reviewer')`, id)
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		for _, statement := range []string{
			`DELETE FROM review_finding_rule_attributions WHERE rule_version_id IN (SELECT version.id FROM rule_versions version JOIN rule_sets rules ON rules.id=version.rule_set_id WHERE rules.tenant_id=$1)`,
			`DELETE FROM rule_versions WHERE rule_set_id IN (SELECT id FROM rule_sets WHERE tenant_id=$1)`,
			`DELETE FROM tenants WHERE id=$1`,
		} {
			if _, err := s.pool.Exec(cleanup, statement, id); err != nil {
				t.Errorf("clean up Issue views fixture: %v", err)
				return
			}
		}
	})
	return s, ctx, id, slug
}

func issueViewInput(name, visibility string) domain.IssueSavedViewInput {
	return domain.IssueSavedViewInput{Name: name, Visibility: visibility, Definition: domain.IssueViewDefinition{View: "all", Filters: &domain.IssueFilterExpression{Condition: "and", Items: []domain.IssueFilterExpression{}}}}
}

func TestIssueSavedViewsIsolateVisibilityAndRevisionedPermissions(t *testing.T) {
	s, ctx, tenant, slug := issueViewsTestStore(t)
	personal, err := s.CreateIssueView(ctx, "Alice", slug, issueViewInput("My issues", "personal"))
	if err != nil || personal.Revision != 1 || !personal.CanManage || personal.OwnerSubject != "Alice" {
		t.Fatalf("personal=%#v err=%v", personal, err)
	}
	raw, _ := json.Marshal(personal.Definition)
	if string(raw) != `{"view":"all","filters":{"condition":"and","items":[]}}` {
		t.Fatalf("wire contract=%s", raw)
	}
	if _, err := s.CreateIssueView(ctx, "Alice", slug, issueViewInput("MY ISSUES", "personal")); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name=%v", err)
	}
	other, err := s.CreateIssueView(ctx, "alice", slug, issueViewInput("My issues", "personal"))
	if err != nil {
		t.Fatal(err)
	}
	shared, err := s.CreateIssueView(ctx, "owner", slug, issueViewInput("Team critical", "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIssueView(ctx, "Alice", slug, issueViewInput("Forbidden", "workspace")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer shared create=%v", err)
	}
	page, err := s.ListIssueViews(ctx, "Alice", slug)
	if err != nil || page.CanCreateWorkspace || len(page.Views) != 2 {
		t.Fatalf("viewer list=%#v err=%v", page, err)
	}
	for _, view := range page.Views {
		if view.ID == other.ID || view.CanManage != (view.ID == personal.ID) {
			t.Fatalf("private/capability leak: %#v", view)
		}
	}
	adminPage, err := s.ListIssueViews(ctx, "admin", slug)
	if err != nil || !adminPage.CanCreateWorkspace || len(adminPage.Views) != 1 || !adminPage.Views[0].CanManage {
		t.Fatalf("admin list=%#v err=%v", adminPage, err)
	}
	update := issueViewInput("Renamed", "personal")
	update.Revision = personal.Revision
	if _, err := s.UpdateIssueView(ctx, "owner", slug, personal.ID, update); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner saw another personal view: %v", err)
	}
	if err := s.DeleteIssueView(ctx, "alice", slug, personal.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private delete: %v", err)
	}
	updated, err := s.UpdateIssueView(ctx, "Alice", slug, personal.ID, update)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("update=%#v err=%v", updated, err)
	}
	if _, err := s.UpdateIssueView(ctx, "Alice", slug, personal.ID, update); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update=%v", err)
	}
	if err := s.DeleteIssueView(ctx, "Alice", slug, personal.ID, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale delete=%v", err)
	}
	sharedUpdate := issueViewInput("Team high", "workspace")
	sharedUpdate.Revision = shared.Revision
	if _, err := s.UpdateIssueView(ctx, "Alice", slug, shared.ID, sharedUpdate); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer shared update=%v", err)
	}
	if err := s.DeleteIssueView(ctx, "Alice", slug, shared.ID, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer shared delete=%v", err)
	}
	if _, err := s.UpdateIssueView(ctx, "admin", slug, shared.ID, sharedUpdate); err != nil {
		t.Fatal(err)
	}
	secondTenant := uuid.New()
	secondSlug := "other-" + secondTenant.String()[:8]
	if _, err := s.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Other')`, secondTenant, secondSlug); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, secondTenant) })
	if _, err := s.pool.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role) VALUES($1,'Alice','owner')`, secondTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateIssueView(ctx, "Alice", secondSlug, personal.ID, update); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant update=%v", err)
	}
	if err := s.DeleteIssueView(ctx, "Alice", secondSlug, personal.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant delete=%v", err)
	}
	if _, err := s.ListIssueViews(ctx, "outsider", slug); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider list=%v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE memberships SET active=false, deactivated_at=now(), deactivated_by='test' WHERE tenant_id=$1 AND subject='alice'`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListIssueViews(ctx, "alice", slug); !errors.Is(err, ErrForbidden) {
		t.Fatalf("inactive list=%v", err)
	}
	if _, err := s.CreateIssueView(ctx, "alice", slug, issueViewInput("Inactive", "personal")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("inactive create=%v", err)
	}
	if err := s.DeleteIssueView(ctx, "Alice", slug, personal.ID, 2); err != nil {
		t.Fatal(err)
	}
	var auditCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND target=$2 AND action IN ('issue_view.created','issue_view.updated','issue_view.deleted')`, tenant, personal.ID.String()).Scan(&auditCount); err != nil || auditCount != 3 {
		t.Fatalf("audit=%d err=%v", auditCount, err)
	}
}

func TestIssueSavedViewsEnforceScopeCapsWithoutTruncatingDirectory(t *testing.T) {
	s, ctx, tenant, slug := issueViewsTestStore(t)
	definition, _ := json.Marshal(issueViewInput("", "personal").Definition)
	if _, err := s.pool.Exec(ctx, `INSERT INTO issue_saved_views(tenant_id,owner_subject,visibility,name,definition) SELECT $1,'owner',scope,'View '||n,$2::jsonb FROM generate_series(1,100) n CROSS JOIN (VALUES('personal'),('workspace')) kinds(scope)`, tenant, definition); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListIssueViews(ctx, "owner", slug)
	if err != nil || len(page.Views) != 200 {
		t.Fatalf("directory was truncated: %d err=%v", len(page.Views), err)
	}
	for _, scope := range []string{"personal", "workspace"} {
		if _, err := s.CreateIssueView(ctx, "owner", slug, issueViewInput("Overflow", scope)); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s cap=%v", scope, err)
		}
	}
	if _, err := s.CreateIssueView(ctx, "Alice", slug, issueViewInput("My independent cap", "personal")); err != nil {
		t.Fatal(err)
	}
	for _, view := range page.Views {
		if view.Visibility == "personal" {
			input := issueViewInput("Rename under cap", "personal")
			input.Revision = view.Revision
			if _, err := s.UpdateIssueView(ctx, "owner", slug, view.ID, input); err != nil {
				t.Fatalf("rename at cap=%v", err)
			}
			break
		}
	}
}

func TestIssueGroupedFiltersPreserveTenantGroupingAndAnchoredPagination(t *testing.T) {
	s, ctx, tenant, slug := issueViewsTestStore(t)
	anchor := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	type fixture struct {
		status, severity, provider, host, path, assignee string
		seen                                             time.Time
	}
	fixtures := []fixture{
		{"open", "high", "github", "https://api.github.com", "src/literal%_name.go", "Alice", anchor.Add(-time.Hour)},
		{"resolved", "critical", "gitlab", "https://git.example/api/v4", "src/ordinary.go", "alice", anchor.Add(-2 * time.Hour)},
		{"open", "low", "gitlab", "https://other.example/api/v4", "src/ordinary.go", "", anchor.Add(-3 * time.Hour)},
		{"resolved", "medium", "github", "https://api.github.com", "src/ordinary.go", "", anchor.Add(-8 * 24 * time.Hour)},
	}
	for i, item := range fixtures {
		if _, err := s.pool.Exec(ctx, `INSERT INTO review_issues(tenant_id,provider,api_base_url,repository,fingerprint,path,severity,category,body_preview,status,assignee_subject,first_seen_at,last_seen_at) VALUES($1,$2,$3,'RainLib/project',$4,$5,$6,'security','SQL injection concern',$7,$8,$9,$9)`, tenant, item.provider, item.host, fmt.Sprintf("filter-%d", i), item.path, item.severity, item.status, item.assignee, item.seen); err != nil {
			t.Fatal(err)
		}
	}
	other := uuid.New()
	otherSlug := "other-filter-" + other.String()[:8]
	if _, err := s.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Other filter')`, other, otherSlug); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, other) })
	if _, err := s.pool.Exec(ctx, `INSERT INTO review_issues(tenant_id,provider,api_base_url,repository,fingerprint,path,severity,category,body_preview,status,first_seen_at,last_seen_at) VALUES($1,'github','https://api.github.com','RainLib/project','foreign','src/private.go','critical','security','private','open',$2,$2)`, other, anchor); err != nil {
		t.Fatal(err)
	}
	expression := mustIssueExpression(t, `{"condition":"or","items":[{"field":"status","operator":"is","value":"open"},{"field":"severity","operator":"is","value":"critical"}]}`)
	filter := domain.IssueFilter{Filters: expression, FilterTime: &anchor, Limit: 1, View: "all"}
	first, err := s.ListIssues(ctx, "Alice", slug, filter)
	if err != nil || first.TotalCount != 3 || len(first.Issues) != 1 || first.NextCursor == "" || !first.FilterTime.Equal(anchor) {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	filter.Cursor = first.NextCursor
	second, err := s.ListIssues(ctx, "Alice", slug, filter)
	if err != nil || second.TotalCount != 3 || len(second.Issues) != 1 || second.Issues[0].ID == first.Issues[0].ID || second.PreviousCursor == "" {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	back := filter
	back.Cursor = second.PreviousCursor
	back.CursorDirection = domain.IssueCursorBefore
	previous, err := s.ListIssues(ctx, "Alice", slug, back)
	if err != nil || len(previous.Issues) != 1 || previous.Issues[0].ID != first.Issues[0].ID {
		t.Fatalf("back=%#v err=%v", previous, err)
	}
	for _, change := range []func(*domain.IssueFilter){func(f *domain.IssueFilter) { later := anchor.Add(time.Second); f.FilterTime = &later }, func(f *domain.IssueFilter) { f.FilterTime = nil }, func(f *domain.IssueFilter) { f.Filters = mustIssueExpression(t, `{"condition":"and","items":[]}`) }, func(f *domain.IssueFilter) { f.View = "open" }} {
		changed := filter
		change(&changed)
		if _, err := s.ListIssues(ctx, "Alice", slug, changed); !errors.Is(err, ErrInvalidIssueFilter) {
			t.Fatalf("changed filter accepted: %v", err)
		}
	}
	if _, err := s.ListIssues(ctx, "alice", slug, filter); !errors.Is(err, ErrInvalidIssueFilter) {
		t.Fatalf("actor changed accepted: %v", err)
	}
	for _, test := range []struct {
		raw   string
		count int
	}{
		{`{"condition":"and","items":[{"field":"path","operator":"contains","value":"%_"}]}`, 1},
		{`{"condition":"and","items":[{"field":"path","operator":"not_contains","value":"%_"}]}`, 3},
		{`{"condition":"and","items":[{"field":"assignee","operator":"is","value":"me"}]}`, 1},
		{`{"condition":"and","items":[{"field":"assignee","operator":"is","value":"unassigned"}]}`, 2},
		{`{"condition":"and","items":[{"field":"age","operator":"within","value":"7d"}]}`, 3},
		{`{"condition":"and","items":[{"field":"age","operator":"not_within","value":"7d"}]}`, 1},
		{`{"condition":"and","items":[{"field":"provider","operator":"is","value":"gitlab"},{"condition":"or","items":[{"field":"api_base_url","operator":"is","value":"https://git.example/api/v4"},{"field":"severity","operator":"is","value":"critical"}]}]}`, 1},
		{`{"condition":"and","items":[{"field":"query","operator":"contains","value":"SQL injection"}]}`, 4},
		{`{"condition":"and","items":[{"field":"repository","operator":"is","value":"x' OR true --"}]}`, 0},
	} {
		page, err := s.ListIssues(ctx, "Alice", slug, domain.IssueFilter{Filters: mustIssueExpression(t, test.raw), FilterTime: &anchor, Limit: 10})
		if err != nil || page.TotalCount != test.count || len(page.Issues) != test.count {
			t.Fatalf("filter=%s count=%d issues=%d err=%v", test.raw, page.TotalCount, len(page.Issues), err)
		}
	}
	fresh, err := s.ListIssues(ctx, "Alice", slug, domain.IssueFilter{Filters: expression, Limit: 1})
	if err != nil || fresh.FilterTime.IsZero() {
		t.Fatalf("automatic filter anchor=%#v err=%v", fresh, err)
	}
	if _, err := s.ListIssues(ctx, "Alice", slug, domain.IssueFilter{Filters: &domain.IssueFilterExpression{Condition: "or"}, Limit: 10}); !errors.Is(err, ErrInvalidIssueFilter) {
		t.Fatalf("invalid empty OR=%v", err)
	}
}

func TestIssueRuleFilterUsesRetainedFindingAttribution(t *testing.T) {
	s, ctx, tenant, slug := issueViewsTestStore(t)
	installation, ruleSet, version := uuid.New(), uuid.New(), uuid.New()
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES($1,$2,'github',$3,'RainLib/*','https://api.github.com','secret://issues')`, installation, tenant, installation.String())
	batch.Queue(`INSERT INTO rule_sets(id,tenant_id,name,created_by) VALUES($1,$2,'Rule filtering','owner')`, ruleSet, tenant)
	batch.Queue(`INSERT INTO rule_versions(id,rule_set_id,version,state,rules,content_sha256,created_by) VALUES($1,$2,1,'published','[]','filter-test','owner')`, version, ruleSet)
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	job := seedIssueJob(t, ctx, s, tenant, installation, uuid.New(), 1, "rule-filter-head")
	if err := s.SaveFindings(ctx, job, []domain.Finding{{Path: "src/rule.go", StartLine: 1, EndLine: 1, Severity: "high", Category: "security", Body: "Attribution fixture"}, {Path: "src/unattributed.go", StartLine: 1, EndLine: 1, Severity: "high", Category: "security", Body: "No attribution fixture"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO review_finding_rule_attributions(finding_id,rule_key,rule_version_id) SELECT id,'security.sql', $2 FROM review_findings WHERE job_id=$1 AND path='src/rule.go'`, job, version); err != nil {
		t.Fatal(err)
	}
	// An old occurrence still supplies retained provenance after it stops being active.
	if _, err := s.pool.Exec(ctx, `UPDATE review_issue_occurrences SET active=false WHERE finding_id IN (SELECT id FROM review_findings WHERE job_id=$1)`, job); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ operator, value, path string }{{"is", "security.sql", "src/rule.go"}, {"contains", "security.", "src/rule.go"}, {"is_not", "security.sql", "src/unattributed.go"}, {"not_contains", "security.", "src/unattributed.go"}} {
		expression := &domain.IssueFilterExpression{Condition: "and", Items: []domain.IssueFilterExpression{{Field: "rule", Operator: test.operator, Value: test.value}}}
		page, err := s.ListIssues(ctx, "owner", slug, domain.IssueFilter{Filters: expression, Limit: 10})
		if err != nil || page.TotalCount != 1 || len(page.Issues) != 1 || page.Issues[0].Path != test.path {
			t.Fatalf("rule %s: page=%#v err=%v", test.operator, page, err)
		}
	}
}

func mustIssueExpression(t *testing.T, raw string) *domain.IssueFilterExpression {
	t.Helper()
	expression, err := domain.ParseIssueFilterExpression(raw)
	if err != nil {
		t.Fatal(err)
	}
	return expression
}
