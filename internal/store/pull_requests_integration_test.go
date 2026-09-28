package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPullRequestIndexUsesCurrentRunsFiltersCountsAndKeysetPagination(t *testing.T) {
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

	tenantID, installationID := uuid.New(), uuid.New()
	tenantSlug := "pull-request-page-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Pull request pagination')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref) VALUES ($1, $2, 'github', $3, 'RainLib/*', 'https://api.github.com', 'secret://pull-requests')`, installationID, tenantID, "pull-request-installation-"+tenantID.String())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed pull request tenant: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	base := time.Date(2026, time.September, 19, 10, 0, 0, 0, time.UTC)
	type fixture struct {
		requestID uuid.UUID
		runID     uuid.UUID
		state     domain.RunState
		createdAt time.Time
		number    int
		failure   string
		title     string
		author    string
	}
	fixtures := []fixture{
		{requestID: uuid.New(), runID: uuid.New(), state: domain.RunCompleted, createdAt: base.Add(5 * time.Minute), number: 42, title: "Protect redirect destinations", author: "ada"},
		{requestID: uuid.New(), runID: uuid.New(), state: domain.RunPreparing, createdAt: base.Add(4 * time.Minute), number: 41, title: "Simplify admission policy", author: "lin"},
		{requestID: uuid.New(), runID: uuid.New(), state: domain.RunNeedsAttention, createdAt: base.Add(3 * time.Minute), number: 40, failure: "model provider timeout", title: "Add durable retries", author: "grace"},
		{requestID: uuid.New(), runID: uuid.New(), state: domain.RunCancelled, createdAt: base.Add(2 * time.Minute), number: 39, title: "Clean stale worker state", author: "ada"},
	}
	for _, item := range fixtures {
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO review_requests (id, tenant_id, installation_id, provider, api_base_url, repository, review_number, title, author)
			VALUES ($1, $2, $3, 'github', 'https://api.github.com', 'RainLib/open-review-platform', $4, $5, $6)`, item.requestID, tenantID, installationID, item.number, item.title, item.author); err != nil {
			t.Fatalf("seed request %d: %v", item.number, err)
		}
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO review_runs (id, request_id, state, trigger_kind, review_mode, head_sha, base_sha, failure_message, created_at)
			VALUES ($1, $2, $3, 'pull_request', 'configured', $4, 'base', NULLIF($5, ''), $6)`, item.runID, item.requestID, item.state, "head-"+item.runID.String()[:8], item.failure, item.createdAt); err != nil {
			t.Fatalf("seed run %d: %v", item.number, err)
		}
		if _, err := postgres.pool.Exec(ctx, `UPDATE review_requests SET current_run_id=$1 WHERE id=$2`, item.runID, item.requestID); err != nil {
			t.Fatalf("set current run %d: %v", item.number, err)
		}
	}
	// A historical run for #42 must not create a second pull-request row.
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO review_runs (id, request_id, state, trigger_kind, review_mode, head_sha, base_sha, created_at)
		VALUES ($1, $2, 'failed', 'pull_request', 'configured', 'old-head', 'base', $3)`, uuid.New(), fixtures[0].requestID, base.Add(time.Minute)); err != nil {
		t.Fatalf("seed historical run: %v", err)
	}

	filter := domain.PullRequestFilter{View: domain.PullRequestAll, Limit: 2}
	first, err := postgres.ListPullRequests(ctx, "owner", tenantSlug, filter)
	if err != nil || len(first.Runs) != 2 || first.Runs[0].ID != fixtures[0].runID || first.Runs[1].ID != fixtures[1].runID || first.NextCursor == "" || first.PreviousCursor != "" {
		t.Fatalf("first pull request page=%#v err=%v", first, err)
	}
	if first.Counts != (domain.PullRequestCounts{Active: 1, Attention: 1, Completed: 2, All: 4}) {
		t.Fatalf("pull request counts=%#v", first.Counts)
	}
	if first.Runs[0].Title != fixtures[0].title || first.Runs[0].Author != fixtures[0].author {
		t.Fatalf("pull request metadata=%#v", first.Runs[0])
	}
	byMetadata, err := postgres.ListPullRequests(ctx, "owner", tenantSlug, domain.PullRequestFilter{View: domain.PullRequestAll, Query: "admission policy", Limit: 25})
	if err != nil || len(byMetadata.Runs) != 1 || byMetadata.Runs[0].ID != fixtures[1].runID {
		t.Fatalf("metadata search=%#v err=%v", byMetadata, err)
	}

	after := filter
	after.Cursor, after.CursorDirection = first.NextCursor, domain.WorkQueueCursorAfter
	second, err := postgres.ListPullRequests(ctx, "owner", tenantSlug, after)
	if err != nil || len(second.Runs) != 2 || second.Runs[0].ID != fixtures[2].runID || second.Runs[1].ID != fixtures[3].runID || second.PreviousCursor == "" {
		t.Fatalf("second pull request page=%#v err=%v", second, err)
	}

	before := filter
	before.Cursor, before.CursorDirection = second.PreviousCursor, domain.WorkQueueCursorBefore
	back, err := postgres.ListPullRequests(ctx, "owner", tenantSlug, before)
	if err != nil || len(back.Runs) != 2 || back.Runs[0].ID != fixtures[0].runID || back.Runs[1].ID != fixtures[1].runID || back.NextCursor == "" {
		t.Fatalf("previous pull request page=%#v err=%v", back, err)
	}

	attention, err := postgres.ListPullRequests(ctx, "owner", tenantSlug, domain.PullRequestFilter{View: domain.PullRequestAttention, Query: "timeout", Limit: 25})
	if err != nil || len(attention.Runs) != 1 || attention.Runs[0].ID != fixtures[2].runID {
		t.Fatalf("attention pull request page=%#v err=%v", attention, err)
	}

	wrongScope := after
	wrongScope.View = domain.PullRequestCompleted
	if _, err := postgres.ListPullRequests(ctx, "owner", tenantSlug, wrongScope); !errors.Is(err, ErrInvalidPullRequestFilter) {
		t.Fatalf("scope-mismatched pull request cursor error=%v, want ErrInvalidPullRequestFilter", err)
	}
}
