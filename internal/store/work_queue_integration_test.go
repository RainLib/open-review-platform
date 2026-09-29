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

func TestWorkQueueUsesServerFiltersCountsAndBidirectionalKeysetPagination(t *testing.T) {
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
	tenantSlug := "work-queue-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Work queue pagination')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref) VALUES ($1, $2, 'github', $3, 'RainLib/*', 'https://api.github.com', 'secret://work-queue')`, installationID, tenantID, "work-queue-installation-"+tenantID.String())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed work queue tenant: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	type fixture struct {
		id         uuid.UUID
		state      domain.RunState
		createdAt  time.Time
		failureMsg string
		number     int
	}
	base := time.Date(2026, time.September, 19, 9, 0, 0, 0, time.UTC)
	fixtures := []fixture{
		{id: uuid.New(), state: domain.RunAcknowledged, createdAt: base.Add(5 * time.Minute), number: 5},
		{id: uuid.New(), state: domain.RunPreparing, createdAt: base.Add(4 * time.Minute), number: 4},
		{id: uuid.New(), state: domain.RunNeedsAttention, createdAt: base.Add(3 * time.Minute), failureMsg: "provider status write requires a human decision", number: 3},
		{id: uuid.New(), state: domain.RunFailed, createdAt: base.Add(2 * time.Minute), failureMsg: "model provider timeout", number: 2},
		{id: uuid.New(), state: domain.RunCompleted, createdAt: base.Add(time.Minute), number: 1},
	}
	for _, item := range fixtures {
		requestID := uuid.New()
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO review_requests (id, tenant_id, installation_id, provider, api_base_url, repository, review_number)
			VALUES ($1, $2, $3, 'github', 'https://api.github.com', 'RainLib/open-review-platform', $4)`, requestID, tenantID, installationID, item.number); err != nil {
			t.Fatalf("seed request %d: %v", item.number, err)
		}
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO review_runs (id, request_id, state, trigger_kind, review_mode, head_sha, base_sha, failure_message, created_at)
			VALUES ($1, $2, $3, 'pull_request', 'configured', $4, 'base', NULLIF($5, ''), $6)`, item.id, requestID, item.state, "head-"+item.id.String()[:8], item.failureMsg, item.createdAt); err != nil {
			t.Fatalf("seed run %d: %v", item.number, err)
		}
		if item.state == domain.RunFailed || item.state == domain.RunNeedsAttention {
			if _, err := postgres.pool.Exec(ctx, `
				INSERT INTO review_run_interventions (tenant_id, run_id, state, reason)
				VALUES ($1, $2, 'open', $3)`, tenantID, item.id, "fixture requires explicit human attention"); err != nil {
				t.Fatalf("seed intervention %d: %v", item.number, err)
			}
		}
	}

	filter := domain.WorkQueueFilter{View: domain.WorkQueueRunning, Limit: 1}
	first, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, filter)
	if err != nil || len(first.Runs) != 1 || first.Runs[0].ID != fixtures[0].id || first.NextCursor == "" || first.PreviousCursor != "" {
		t.Fatalf("first work queue page=%#v err=%v", first, err)
	}
	if first.Counts != (domain.WorkQueueCounts{Running: 2, NeedsAttention: 2}) {
		t.Fatalf("work queue counts=%#v", first.Counts)
	}

	after := filter
	after.Cursor, after.CursorDirection = first.NextCursor, domain.WorkQueueCursorAfter
	second, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, after)
	if err != nil || len(second.Runs) != 1 || second.Runs[0].ID != fixtures[1].id || second.PreviousCursor == "" {
		t.Fatalf("second work queue page=%#v err=%v", second, err)
	}

	before := filter
	before.Cursor, before.CursorDirection = second.PreviousCursor, domain.WorkQueueCursorBefore
	back, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, before)
	if err != nil || len(back.Runs) != 1 || back.Runs[0].ID != fixtures[0].id || back.NextCursor == "" {
		t.Fatalf("previous work queue page=%#v err=%v", back, err)
	}

	attention, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, domain.WorkQueueFilter{View: domain.WorkQueueNeedsAttention, Query: "timeout", Limit: 25})
	if err != nil || len(attention.Runs) != 1 || attention.Runs[0].ID != fixtures[3].id {
		t.Fatalf("filtered attention page=%#v err=%v", attention, err)
	}

	wrongScope := after
	wrongScope.Query = "different"
	if _, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, wrongScope); !errors.Is(err, ErrInvalidWorkQueueFilter) {
		t.Fatalf("scope-mismatched work queue cursor error=%v, want ErrInvalidWorkQueueFilter", err)
	}

	// Disconnecting a provider must move only its live queue projection to
	// attention. The admitted run and its immutable execution evidence remain
	// intact, and restoring eligibility does not create a second run.
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET verification_state='failed' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	blocked, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, domain.WorkQueueFilter{View: domain.WorkQueueNeedsAttention, Limit: 25})
	if err != nil || blocked.Counts != (domain.WorkQueueCounts{Running: 0, NeedsAttention: 4}) || len(blocked.Runs) != 4 {
		t.Fatalf("verification-blocked queue=%#v err=%v", blocked, err)
	}
	for _, run := range blocked.Runs[:2] {
		if run.QueueBlock == nil || run.QueueBlock.Reason != "verification_required" || run.QueueBlock.InstallationID != installationID || run.QueueBlock.VerificationState != domain.InstallationVerificationFailed {
			t.Fatalf("verification-blocked run=%#v", run)
		}
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET active=FALSE, verification_state='verified' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	inactive, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, domain.WorkQueueFilter{View: domain.WorkQueueNeedsAttention, Limit: 25})
	if err != nil || len(inactive.Runs) != 4 || inactive.Runs[0].QueueBlock == nil || inactive.Runs[0].QueueBlock.Reason != "installation_inactive" {
		t.Fatalf("inactive-installation queue=%#v err=%v", inactive, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET active=TRUE WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	restored, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, domain.WorkQueueFilter{View: domain.WorkQueueRunning, Limit: 25})
	if err != nil || restored.Counts != (domain.WorkQueueCounts{Running: 2, NeedsAttention: 2}) || len(restored.Runs) != 2 || restored.Runs[0].QueueBlock != nil {
		t.Fatalf("restored queue=%#v err=%v", restored, err)
	}

	// A comment run is different from an admitted worker job: once its visible
	// reply has exhausted broker delivery, verification alone cannot release it.
	var requestID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT request_id FROM review_runs WHERE id=$1`, fixtures[0].id).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	interactionID, responseID := uuid.New(), uuid.New()
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM inbox_messages WHERE message_id=$1`, responseID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE id=$1`, responseID)
	}()
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_runs SET trigger_kind='comment' WHERE id=$1`, fixtures[0].id); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_interactions(id,tenant_id,request_id,provider,provider_delivery_id,actor_external_id,command,normalized_input,result,result_run_id) VALUES($1,$2,$3,'github',$4,'reviewer','review','@openreview review','accepted',$5)`, interactionID, tenantID, requestID, "queue-"+interactionID.String(), fixtures[0].id); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO outbox_messages(id,aggregate_type,aggregate_id,topic,dedupe_key,payload,published_at) VALUES($1,'review_interaction',$2,'review.interaction.response',$3,jsonb_build_object('release_run_id',$4::text),now())`, responseID, interactionID, "queue:"+interactionID.String(), fixtures[0].id.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO inbox_messages(consumer,message_id,state,attempt) VALUES('interaction-responder-v1',$1,'released',5)`, responseID); err != nil {
		t.Fatal(err)
	}
	stillDelivering, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, domain.WorkQueueFilter{View: domain.WorkQueueRunning, Limit: 25})
	if err != nil || stillDelivering.Counts != (domain.WorkQueueCounts{Running: 2, NeedsAttention: 2}) {
		t.Fatalf("broker retry still active=%#v err=%v", stillDelivering, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE inbox_messages SET attempt=6 WHERE message_id=$1`, responseID); err != nil {
		t.Fatal(err)
	}
	exhausted, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, domain.WorkQueueFilter{View: domain.WorkQueueNeedsAttention, Limit: 25})
	if err != nil || exhausted.Counts != (domain.WorkQueueCounts{Running: 1, NeedsAttention: 3}) || len(exhausted.Runs) != 3 || exhausted.Runs[0].ID != fixtures[0].id || exhausted.Runs[0].QueueBlock == nil || exhausted.Runs[0].QueueBlock.Reason != "acknowledgement_exhausted" {
		t.Fatalf("exhausted reply queue=%#v err=%v", exhausted, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE inbox_messages SET state='completed' WHERE message_id=$1`, responseID); err != nil {
		t.Fatal(err)
	}
	released, err := postgres.ListWorkQueue(ctx, "owner", tenantSlug, domain.WorkQueueFilter{View: domain.WorkQueueRunning, Limit: 25})
	if err != nil || released.Counts != (domain.WorkQueueCounts{Running: 2, NeedsAttention: 2}) || released.Runs[0].QueueBlock != nil {
		t.Fatalf("completed reply queue=%#v err=%v", released, err)
	}
}
