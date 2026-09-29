package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestIssueAggregationTracksResolutionRegressionAndPullRequests(t *testing.T) {
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
	installationID := uuid.New()
	tenantSlug := "issue-integration-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Issue Integration')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner'),($1, 'rule-admin', 'rule_admin'),($1, 'reviewer', 'reviewer'),($1, 'viewer', 'viewer')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref) VALUES ($1, $2, 'github', $3, 'RainLib/*', 'https://api.github.com', 'secret://issues')`, installationID, tenantID, "issue-installation-"+tenantID.String())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed issue tenant: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	finding := domain.Finding{Path: "internal/api/server.go", StartLine: 40, EndLine: 42, Severity: "high", Category: "security", Body: "Untrusted redirect target", Suggestion: "Validate the target."}
	requestOne := uuid.New()
	firstJob := seedIssueJob(t, ctx, postgres, tenantID, installationID, requestOne, 3, "head-one")
	if err := postgres.SaveFindings(ctx, firstJob, []domain.Finding{finding}); err != nil {
		t.Fatalf("save first findings: %v", err)
	}
	issuePage, err := postgres.ListIssues(ctx, "owner", tenantSlug, domain.IssueFilter{Status: domain.IssueOpen, Limit: 20})
	if err != nil || len(issuePage.Issues) != 1 {
		t.Fatalf("open issues=%#v error=%v", issuePage, err)
	}
	issueID := issuePage.Issues[0].ID
	assertIssueCounts(t, issuePage.Issues[0], domain.IssueOpen, 1, 1, 1)

	secondJob := seedIssueJob(t, ctx, postgres, tenantID, installationID, requestOne, 3, "head-two")
	if err := postgres.SaveFindings(ctx, secondJob, nil); err != nil {
		t.Fatalf("save resolved head: %v", err)
	}
	resolved, err := postgres.GetIssue(ctx, "owner", tenantSlug, issueID)
	if err != nil {
		t.Fatal(err)
	}
	assertIssueCounts(t, resolved.IssueSummary, domain.IssueResolved, 1, 0, 1)
	if resolved.ResolvedAt == nil {
		t.Fatal("resolved issue has no resolved_at")
	}

	thirdJob := seedIssueJob(t, ctx, postgres, tenantID, installationID, requestOne, 3, "head-three")
	if err := postgres.SaveFindings(ctx, thirdJob, []domain.Finding{finding}); err != nil {
		t.Fatalf("save regressed head: %v", err)
	}
	regressed, err := postgres.GetIssue(ctx, "owner", tenantSlug, issueID)
	if err != nil {
		t.Fatal(err)
	}
	assertIssueCounts(t, regressed.IssueSummary, domain.IssueRegressed, 2, 1, 1)
	if regressed.ResolvedAt != nil {
		t.Fatal("regressed issue still has resolved_at")
	}

	requestTwo := uuid.New()
	fourthJob := seedIssueJob(t, ctx, postgres, tenantID, installationID, requestTwo, 4, "head-four")
	if err := postgres.SaveFindings(ctx, fourthJob, []domain.Finding{finding}); err != nil {
		t.Fatalf("save second pull request occurrence: %v", err)
	}
	detail, err := postgres.GetIssue(ctx, "owner", tenantSlug, issueID)
	if err != nil {
		t.Fatal(err)
	}
	assertIssueCounts(t, detail.IssueSummary, domain.IssueRegressed, 3, 2, 2)
	if len(detail.Occurrences) != 3 {
		t.Fatalf("occurrence count=%d, want 3", len(detail.Occurrences))
	}
	active := 0
	for _, occurrence := range detail.Occurrences {
		if occurrence.Active {
			active++
		}
	}
	if active != 2 {
		t.Fatalf("active occurrences=%d, want 2", active)
	}
	assigned, err := postgres.MutateIssue(ctx, "owner", tenantSlug, issueID, domain.IssueActionInput{
		Action: "assign", AssigneeSubject: "reviewer", ExpectedRevision: detail.Revision,
	})
	if err != nil || assigned.AssigneeSubject != "reviewer" || assigned.Revision != detail.Revision+1 || len(assigned.Events) != 1 {
		t.Fatalf("assigned issue=%#v error=%v", assigned, err)
	}
	assignedIssues, err := postgres.ListIssues(ctx, "reviewer", tenantSlug, domain.IssueFilter{AssigneeSubject: "reviewer", Limit: 20})
	if err != nil || len(assignedIssues.Issues) != 1 || assignedIssues.Issues[0].ID != issueID {
		t.Fatalf("assigned issue filter=%#v error=%v", assignedIssues, err)
	}
	suppressed, err := postgres.MutateIssue(ctx, "reviewer", tenantSlug, issueID, domain.IssueActionInput{
		Action: "false_positive", Reason: "generated test fixture", ExpectedRevision: assigned.Revision,
	})
	if err != nil || suppressed.Status != domain.IssueSuppressed || suppressed.DispositionKind != "false_positive" || suppressed.DispositionReason != "generated test fixture" || len(suppressed.Events) != 2 {
		t.Fatalf("suppressed issue=%#v error=%v", suppressed, err)
	}
	if _, err := postgres.MutateIssue(ctx, "owner", tenantSlug, issueID, domain.IssueActionInput{Action: "resolve", ExpectedRevision: assigned.Revision}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale issue mutation error=%v, want ErrRevisionConflict", err)
	}
	reopened, err := postgres.MutateIssue(ctx, "owner", tenantSlug, issueID, domain.IssueActionInput{
		Action: "suppression_cleared", ExpectedRevision: suppressed.Revision,
	})
	if err != nil || reopened.Status != domain.IssueOpen || reopened.DispositionKind != "" || len(reopened.Events) != 3 {
		t.Fatalf("reopened issue=%#v error=%v", reopened, err)
	}
	ruleSetID, ruleVersionID := uuid.New(), uuid.New()
	ruleBatch := &pgx.Batch{}
	ruleBatch.Queue(`INSERT INTO rule_sets (id,tenant_id,name,created_by) VALUES ($1,$2,'Issue exception rules','owner')`, ruleSetID, tenantID)
	ruleBatch.Queue(`INSERT INTO rule_versions (id,rule_set_id,version,state,rules,content_sha256,created_by,published_at)
		VALUES ($1,$2,1,'published','[{"key":"security.redirect","enforcement":"mandatory","merge_behavior":"replace","severity":"high","content":{"instruction":"Validate redirect targets"}}]'::jsonb,$3,'owner',now())`,
		ruleVersionID, ruleSetID, "issue-exception-"+tenantID.String())
	if err := postgres.pool.SendBatch(ctx, ruleBatch).Close(); err != nil {
		t.Fatalf("seed issue exception rule: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_finding_rule_attributions (finding_id,rule_key,rule_version_id) VALUES ($1,'security.redirect',$2)`, detail.Occurrences[0].FindingID, ruleVersionID); err != nil {
		t.Fatalf("seed issue rule attribution: %v", err)
	}
	attributed, err := postgres.GetIssue(ctx, "viewer", tenantSlug, issueID)
	if err != nil || len(attributed.Occurrences) == 0 || len(attributed.Occurrences[0].RuleAttributions) != 1 {
		t.Fatalf("issue rule attribution missing: detail=%#v error=%v", attributed, err)
	}
	ruleAttribution := attributed.Occurrences[0].RuleAttributions[0]
	if ruleAttribution.RuleKey != "security.redirect" || ruleAttribution.RuleVersionID != ruleVersionID || ruleAttribution.RuleSetID != ruleSetID || ruleAttribution.RuleSetName != "Issue exception rules" || ruleAttribution.Version != 1 {
		t.Fatalf("issue attribution did not preserve immutable rule identity: %#v", ruleAttribution)
	}
	if len(attributed.Occurrences) > 1 && len(attributed.Occurrences[1].RuleAttributions) != 0 {
		t.Fatalf("rule attribution leaked to another occurrence: %#v", attributed.Occurrences[1].RuleAttributions)
	}
	exception, err := postgres.CreateRuleException(ctx, "owner", tenantSlug, domain.RuleExceptionInput{
		RuleVersionID: ruleVersionID, RuleKey: "security.redirect", ScopeKind: "repository",
		ScopeRef: "RainLib/open-review-platform", ScopeProvider: domain.ProviderGitHub, ScopeAPIBaseURL: "https://api.github.com", Reason: "Temporary redirect migration with compensating monitoring",
		ExpiresAt: time.Now().Add(time.Hour), SourceIssueID: &issueID, SourceIssueRevision: reopened.Revision,
	})
	if err != nil || exception.SourceIssueID == nil || *exception.SourceIssueID != issueID || exception.SourceIssueRevision == nil || *exception.SourceIssueRevision != reopened.Revision {
		t.Fatalf("linked exception=%#v error=%v", exception, err)
	}
	approved, err := postgres.DecideRuleException(ctx, "rule-admin", tenantSlug, exception.ID, domain.RuleExceptionDecisionInput{Decision: "approved", Comment: "Compensating monitoring accepted"})
	if err != nil || approved.EffectiveState != "approved" {
		t.Fatalf("approved exception=%#v error=%v", approved, err)
	}
	exceptionSuppressed, err := postgres.GetIssue(ctx, "owner", tenantSlug, issueID)
	if err != nil || exceptionSuppressed.Status != domain.IssueSuppressed || exceptionSuppressed.DispositionKind != "exception" || exceptionSuppressed.ExceptionRequest == nil || exceptionSuppressed.ExceptionRequest.ID != exception.ID || exceptionSuppressed.Events[0].Action != "exception_approved" {
		t.Fatalf("exception-suppressed issue=%#v error=%v", exceptionSuppressed, err)
	}
	if _, err := postgres.MutateIssue(ctx, "owner", tenantSlug, issueID, domain.IssueActionInput{Action: "suppression_cleared", ExpectedRevision: exceptionSuppressed.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("manual exception suppression clear error=%v, want ErrConflict", err)
	}
	if _, err := postgres.RevokeRuleException(ctx, "rule-admin", tenantSlug, exception.ID); err != nil {
		t.Fatalf("revoke linked exception: %v", err)
	}
	restored, err := postgres.GetIssue(ctx, "owner", tenantSlug, issueID)
	if err != nil || restored.Status != domain.IssueOpen || restored.DispositionKind != "" || restored.ExceptionRequest == nil || restored.ExceptionRequest.EffectiveState != "revoked" || restored.Events[0].Action != "exception_revoked" {
		t.Fatalf("restored issue=%#v error=%v", restored, err)
	}
	expiring, err := postgres.CreateRuleException(ctx, "owner", tenantSlug, domain.RuleExceptionInput{
		RuleVersionID: ruleVersionID, RuleKey: "security.redirect", ScopeKind: "repository",
		ScopeRef: "RainLib/open-review-platform", ScopeProvider: domain.ProviderGitHub, ScopeAPIBaseURL: "https://api.github.com", Reason: "Short lived migration acceptance",
		ExpiresAt: time.Now().Add(time.Hour), SourceIssueID: &issueID, SourceIssueRevision: restored.Revision,
	})
	if err != nil {
		t.Fatalf("create expiring exception: %v", err)
	}
	if _, err := postgres.DecideRuleException(ctx, "rule-admin", tenantSlug, expiring.ID, domain.RuleExceptionDecisionInput{Decision: "approved"}); err != nil {
		t.Fatalf("approve expiring exception: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE rule_exceptions SET expires_at = now() - interval '1 millisecond' WHERE id = $1`, expiring.ID); err != nil {
		t.Fatalf("expire linked exception fixture: %v", err)
	}
	// The background reconciler must restore this Issue even if no user opens
	// the inbox/detail page after expiry. A later read remains idempotent.
	reconciled, err := postgres.ReconcileExpiredRuleExceptions(ctx, 10)
	if err != nil || reconciled != 1 {
		t.Fatalf("background expiry reconciliation=%d err=%v, want one restored issue", reconciled, err)
	}
	if reconciled, err := postgres.ReconcileExpiredRuleExceptions(ctx, 10); err != nil || reconciled != 0 {
		t.Fatalf("replayed background expiry reconciliation=%d err=%v, want zero", reconciled, err)
	}
	expired, err := postgres.GetIssue(ctx, "owner", tenantSlug, issueID)
	if err != nil || expired.Status != domain.IssueOpen || expired.DispositionKind != "" || expired.ExceptionRequest == nil || expired.ExceptionRequest.EffectiveState != "expired" || expired.Events[0].Action != "exception_expired" {
		t.Fatalf("expired exception issue=%#v error=%v", expired, err)
	}
	viewerDetail, err := postgres.GetIssue(ctx, "viewer", tenantSlug, issueID)
	if err != nil || viewerDetail.CanManage {
		t.Fatalf("viewer detail=%#v error=%v", viewerDetail, err)
	}
	if _, err := postgres.GetIssue(ctx, "outsider", tenantSlug, issueID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant issue lookup error=%v, want ErrForbidden", err)
	}
}

func seedIssueJob(t *testing.T, ctx context.Context, postgres *PostgresStore, tenantID, installationID, requestID uuid.UUID, reviewNumber int, headSHA string) uuid.UUID {
	t.Helper()
	deliveryID := uuid.New()
	jobID := uuid.New()
	runID := uuid.New()
	repository := "RainLib/open-review-platform"
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO webhook_deliveries (id, provider, delivery_id, event_name, payload) VALUES ($1, 'github', $2, 'pull_request', '{}'::jsonb)`, deliveryID, "issue-delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id, tenant_id, installation_id, delivery_id, provider, api_base_url, repository, clone_url, review_number, base_ref, base_sha, head_ref, head_sha, state) VALUES ($1, $2, $3, $4, 'github', 'https://api.github.com', $5, 'https://github.com/RainLib/open-review-platform.git', $6, 'main', 'base', 'feature', $7, 'succeeded')`, jobID, tenantID, installationID, deliveryID, repository, reviewNumber, headSHA)
	batch.Queue(`INSERT INTO review_requests (id, tenant_id, installation_id, provider, api_base_url, repository, review_number) VALUES ($1, $2, $3, 'github', 'https://api.github.com', $4, $5) ON CONFLICT (tenant_id, installation_id, repository, review_number) DO NOTHING`, requestID, tenantID, installationID, repository, reviewNumber)
	batch.Queue(`INSERT INTO review_runs (id, request_id, legacy_job_id, state, trigger_kind, head_sha, base_sha, finished_at) VALUES ($1, $2, $3, 'completed', 'pull_request', $4, 'base', now())`, runID, requestID, jobID, headSHA)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed issue job %s: %v", headSHA, err)
	}
	return jobID
}

func assertIssueCounts(t *testing.T, issue domain.IssueSummary, status domain.IssueStatus, total, active, pullRequests int) {
	t.Helper()
	if issue.Status != status || issue.OccurrenceCount != total || issue.ActiveOccurrenceCount != active || issue.PullRequestCount != pullRequests {
		t.Fatalf("issue=%s total=%d active=%d prs=%d, want %s/%d/%d/%d", issue.Status, issue.OccurrenceCount, issue.ActiveOccurrenceCount, issue.PullRequestCount, status, total, active, pullRequests)
	}
	if issue.Repository != "RainLib/open-review-platform" {
		t.Fatalf("repository=%q", issue.Repository)
	}
}

func TestIssueFilterRejectsInvalidValues(t *testing.T) {
	for _, filter := range []domain.IssueFilter{
		{Status: "unknown", Limit: 20},
		{Severity: "urgent", Limit: 20},
		{AssigneeSubject: strings.Repeat("a", 257), Limit: 20},
		{Limit: 0},
		{Limit: 101},
	} {
		if filter.Valid() {
			t.Fatalf("filter should be invalid: %s", fmt.Sprintf("%#v", filter))
		}
	}
}

func TestIssueListUsesFilterBoundBidirectionalKeysetPagination(t *testing.T) {
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
	tenantSlug := "issue-page-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Issue pagination')`, tenantID, tenantSlug); err != nil {
		t.Fatalf("seed page tenant: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID); err != nil {
		t.Fatalf("seed page membership: %v", err)
	}

	type fixture struct {
		id         uuid.UUID
		status     domain.IssueStatus
		severity   string
		seenAt     time.Time
		assignee   string
		active     int
		repository string
		category   string
	}
	base := time.Date(2026, time.September, 19, 5, 0, 0, 0, time.UTC)
	fixtures := []fixture{
		{id: uuid.New(), status: domain.IssueRegressed, severity: "critical", seenAt: base.Add(5 * time.Minute), active: 1},
		{id: uuid.New(), status: domain.IssueRegressed, severity: "high", seenAt: base.Add(4 * time.Minute), active: 1},
		{id: uuid.New(), status: domain.IssueOpen, severity: "critical", seenAt: base.Add(3 * time.Minute), assignee: "owner", active: 1},
		{id: uuid.New(), status: domain.IssueOpen, severity: "medium", seenAt: base.Add(2 * time.Minute)},
		{id: uuid.New(), status: domain.IssueSuppressed, severity: "medium", seenAt: base.Add(time.Minute), assignee: "owner", repository: "RainLib/archived-service", category: "privacy"},
	}
	for index, item := range fixtures {
		repository := item.repository
		if repository == "" {
			repository = "RainLib/open-review-platform"
		}
		category := item.category
		if category == "" {
			category = "security"
		}
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO review_issues (
				id, tenant_id, provider, api_base_url, repository, fingerprint, path, severity, category,
				body_preview, status, assignee_subject, active_occurrence_count, first_seen_at, last_seen_at, updated_at
			) VALUES ($1, $2, 'github', 'https://api.github.com', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12, $12)`,
			item.id, tenantID, repository, fmt.Sprintf("pagination-%d", index), fmt.Sprintf("internal/page/%d.go", index), item.severity, category,
			fmt.Sprintf("redirect finding %d", index), item.status, item.assignee, item.active, item.seenAt); err != nil {
			t.Fatalf("seed page issue %d: %v", index, err)
		}
	}

	filter := domain.IssueFilter{Limit: 2}
	first, err := postgres.ListIssues(ctx, "owner", tenantSlug, filter)
	if err != nil || len(first.Issues) != 2 || first.Issues[0].ID != fixtures[0].id || first.Issues[1].ID != fixtures[1].id || first.NextCursor == "" || first.PreviousCursor != "" {
		t.Fatalf("first page=%#v error=%v", first, err)
	}
	if first.Counts != (domain.IssueInboxCounts{Open: 2, Regressed: 2, Critical: 2, Assigned: 1, Suppressed: 1}) {
		t.Fatalf("first page counts=%#v", first.Counts)
	}
	if !slices.Equal(first.Facets.Repositories, []string{"RainLib/archived-service", "RainLib/open-review-platform"}) || !slices.Equal(first.Facets.Categories, []string{"privacy", "security"}) {
		t.Fatalf("tenant-wide issue facets=%#v", first.Facets)
	}
	assigned, err := postgres.ListIssues(ctx, "owner", tenantSlug, domain.IssueFilter{View: "assigned", Limit: 10})
	if err != nil || len(assigned.Issues) != 1 || assigned.Issues[0].ID != fixtures[2].id {
		t.Fatalf("active assigned issues=%#v error=%v", assigned, err)
	}
	inactive := fixtures[4].id
	assigned, err = postgres.ListIssues(ctx, "owner", tenantSlug, domain.IssueFilter{View: "assigned", SelectedIssueID: &inactive, Limit: 10})
	if err != nil || assigned.SelectedInView == nil || *assigned.SelectedInView {
		t.Fatalf("inactive selected issue in assigned view=%v error=%v", assigned.SelectedInView, err)
	}
	selected := fixtures[2].id
	for _, test := range []struct {
		view string
		want bool
	}{
		{view: "all", want: true},
		{view: "open", want: true},
		{view: "critical", want: true},
		{view: "regressed", want: false},
		{view: "suppressed", want: false},
	} {
		page, err := postgres.ListIssues(ctx, "owner", tenantSlug, domain.IssueFilter{View: test.view, SelectedIssueID: &selected, Limit: 2})
		if err != nil || page.SelectedInView == nil || *page.SelectedInView != test.want {
			t.Fatalf("selection in %s view=%v, want %v, error=%v", test.view, page.SelectedInView, test.want, err)
		}
	}
	filtered, err := postgres.ListIssues(ctx, "owner", tenantSlug, domain.IssueFilter{View: "open", Repository: "RainLib/other", SelectedIssueID: &selected, Limit: 2})
	if err != nil || filtered.SelectedInView == nil || *filtered.SelectedInView {
		t.Fatalf("selection in filtered view=%v, want false, error=%v", filtered.SelectedInView, err)
	}

	afterFilter := filter
	afterFilter.Cursor = first.NextCursor
	afterFilter.CursorDirection = domain.IssueCursorAfter
	second, err := postgres.ListIssues(ctx, "owner", tenantSlug, afterFilter)
	if err != nil || len(second.Issues) != 2 || second.Issues[0].ID != fixtures[2].id || second.Issues[1].ID != fixtures[3].id || second.PreviousCursor == "" || second.NextCursor == "" {
		t.Fatalf("second page=%#v error=%v", second, err)
	}

	beforeFilter := filter
	beforeFilter.Cursor = second.PreviousCursor
	beforeFilter.CursorDirection = domain.IssueCursorBefore
	back, err := postgres.ListIssues(ctx, "owner", tenantSlug, beforeFilter)
	if err != nil || len(back.Issues) != 2 || back.Issues[0].ID != fixtures[0].id || back.Issues[1].ID != fixtures[1].id {
		t.Fatalf("previous page=%#v error=%v", back, err)
	}

	activeCritical, err := postgres.ListIssues(ctx, "owner", tenantSlug, domain.IssueFilter{Severity: "critical", ActiveOnly: true, Limit: 10})
	if err != nil || len(activeCritical.Issues) != 2 || activeCritical.Issues[0].ID != fixtures[0].id || activeCritical.Issues[1].ID != fixtures[2].id {
		t.Fatalf("active critical page=%#v error=%v", activeCritical, err)
	}
	active, err := postgres.ListIssues(ctx, "owner", tenantSlug, domain.IssueFilter{ActiveOnly: true, Limit: 10})
	if err != nil || len(active.Issues) != 3 {
		t.Fatalf("active page=%#v error=%v", active, err)
	}
	search, err := postgres.ListIssues(ctx, "owner", tenantSlug, domain.IssueFilter{Query: "page/3", Limit: 10})
	if err != nil || len(search.Issues) != 1 || search.Issues[0].ID != fixtures[3].id {
		t.Fatalf("search page=%#v error=%v", search, err)
	}

	wrongScope := afterFilter
	wrongScope.Repository = "RainLib/other"
	if _, err := postgres.ListIssues(ctx, "owner", tenantSlug, wrongScope); !errors.Is(err, ErrInvalidIssueFilter) {
		t.Fatalf("scope-mismatched cursor error=%v, want ErrInvalidIssueFilter", err)
	}
}
