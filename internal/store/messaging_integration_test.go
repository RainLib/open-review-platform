package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The broker may deliver at least once. This test uses PostgreSQL to prove the
// durable half of that contract: relay ownership cannot be stolen, a failed
// publish becomes reclaimable, and a completed consumer receipt suppresses a
// duplicate delivery.
func TestOutboxAndInboxClaimsRecoverWithoutDuplicatingCompletedWork(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	if os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("outbox claim verification requires an isolated database")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	outboxID, aggregateID := uuid.New(), uuid.New()
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO outbox_messages (id, aggregate_type, aggregate_id, topic, dedupe_key, payload, created_at)
		VALUES ($1, 'integration', $2, 'review.interaction.response', $3, '{"source":"integration"}'::jsonb, now() - interval '1 day')`,
		outboxID, aggregateID, "integration:outbox:"+outboxID.String()); err != nil {
		t.Fatalf("seed outbox: %v", err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE id = $1`, outboxID)
	}()

	claimed, err := postgres.ClaimOutbox(ctx, "relay-a", 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != outboxID || claimed[0].Attempts != 1 {
		t.Fatalf("first outbox claim=%#v err=%v", claimed, err)
	}
	var lockedBy string
	if err := postgres.pool.QueryRow(ctx, `SELECT locked_by FROM outbox_messages WHERE id=$1`, outboxID).Scan(&lockedBy); err != nil || lockedBy != "relay-a" {
		t.Fatalf("another relay must not steal fixture lease: locked_by=%q err=%v", lockedBy, err)
	}
	if err := postgres.MarkOutboxPublished(ctx, outboxID, "relay-b"); !errors.Is(err, ErrJobClaimLost) {
		t.Fatalf("foreign relay mark error=%v, want ErrJobClaimLost", err)
	}
	if err := postgres.ReleaseOutbox(ctx, outboxID, "relay-a", "broker confirm timeout"); err != nil {
		t.Fatalf("release failed publish: %v", err)
	}
	var availableAt time.Time
	var releasedLock *string
	var attempts int
	var lastError *string
	if err := postgres.pool.QueryRow(ctx, `SELECT available_at, locked_by, publish_attempts, last_error FROM outbox_messages WHERE id=$1`, outboxID).Scan(&availableAt, &releasedLock, &attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if releasedLock != nil || attempts != 1 || lastError == nil || *lastError != "broker confirm timeout" || !availableAt.After(time.Now().Add(5*time.Second)) {
		t.Fatalf("released outbox state available=%s lock=%v attempts=%d error=%v", availableAt, releasedLock, attempts, lastError)
	}
	// Do not sleep through the backoff in a test. Moving this dedicated fixture
	// forward simulates the relay's next recovery scan.
	if _, err := postgres.pool.Exec(ctx, `UPDATE outbox_messages SET available_at = now() - interval '1 second' WHERE id=$1`, outboxID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := postgres.ClaimOutbox(ctx, "relay-b", 1)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != outboxID || reclaimed[0].Attempts != 2 {
		t.Fatalf("reclaimed outbox=%#v err=%v", reclaimed, err)
	}
	if err := postgres.MarkOutboxPublished(ctx, outboxID, "relay-b"); err != nil {
		t.Fatalf("mark confirmed publish: %v", err)
	}
	var publishedAt *time.Time
	if err := postgres.pool.QueryRow(ctx, `SELECT published_at FROM outbox_messages WHERE id=$1`, outboxID).Scan(&publishedAt); err != nil || publishedAt == nil {
		t.Fatalf("fixture was not durably published: published_at=%v err=%v", publishedAt, err)
	}

	consumer, messageID := "integration-consumer-"+uuid.NewString(), uuid.New()
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM inbox_messages WHERE consumer=$1 AND message_id=$2`, consumer, messageID)
	}()
	firstToken, firstClaimed, err := postgres.ClaimInbox(ctx, consumer, messageID)
	if err != nil || !firstClaimed || firstToken == uuid.Nil {
		t.Fatalf("first inbox claim token=%s claimed=%t err=%v", firstToken, firstClaimed, err)
	}
	if _, duplicateClaimed, err := postgres.ClaimInbox(ctx, consumer, messageID); err != nil || duplicateClaimed {
		t.Fatalf("active inbox duplicate claimed=%t err=%v", duplicateClaimed, err)
	}
	if err := postgres.CompleteInbox(ctx, consumer, messageID, uuid.New()); !errors.Is(err, ErrInboxClaimLost) {
		t.Fatalf("foreign inbox completion error=%v, want ErrInboxClaimLost", err)
	}
	if err := postgres.ReleaseInbox(ctx, consumer, messageID, firstToken, "provider temporary failure"); err != nil {
		t.Fatalf("release inbox retry: %v", err)
	}
	secondToken, reclaimedInbox, err := postgres.ClaimInbox(ctx, consumer, messageID)
	if err != nil || !reclaimedInbox || secondToken == uuid.Nil || secondToken == firstToken {
		t.Fatalf("reclaim inbox token=%s claimed=%t err=%v", secondToken, reclaimedInbox, err)
	}
	if err := postgres.CompleteInbox(ctx, consumer, messageID, secondToken); err != nil {
		t.Fatalf("complete inbox: %v", err)
	}
	if _, duplicateCompleted, err := postgres.ClaimInbox(ctx, consumer, messageID); err != nil || duplicateCompleted {
		t.Fatalf("completed inbox duplicate claimed=%t err=%v", duplicateCompleted, err)
	}
	var state string
	var inboxAttempts int
	if err := postgres.pool.QueryRow(ctx, `SELECT state, attempt FROM inbox_messages WHERE consumer=$1 AND message_id=$2`, consumer, messageID).Scan(&state, &inboxAttempts); err != nil {
		t.Fatal(err)
	}
	if state != "completed" || inboxAttempts != 2 {
		t.Fatalf("completed inbox state=%s attempts=%d", state, inboxAttempts)
	}
}
