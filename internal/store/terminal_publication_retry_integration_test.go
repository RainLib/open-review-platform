package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestTerminalPublicationRetryUsesDurableAvailableAtAndAttemptBudget(t *testing.T) {
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

	message := domain.OutboxMessage{
		ID: uuid.New(), AggregateID: uuid.New(), Topic: "review.run.failed",
		Payload: map[string]any{"run_id": uuid.NewString(), "revision": float64(7)},
	}
	dedupeKey := "terminal-publication-retry:" + message.ID.String()
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE dedupe_key=$1`, dedupeKey)
	}()

	before := time.Now().UTC()
	scheduled, err := postgres.ScheduleTerminalPublicationRetry(ctx, message, 9*time.Second)
	if err != nil || !scheduled {
		t.Fatalf("schedule terminal publication retry: scheduled=%t err=%v", scheduled, err)
	}
	var topic, retryOf string
	var attempt int
	var availableAt time.Time
	if err := postgres.pool.QueryRow(ctx, `
		SELECT topic,payload->>'terminal_publication_retry_of',
		       (payload->>'terminal_publication_attempt')::integer,available_at
		FROM outbox_messages WHERE dedupe_key=$1`, dedupeKey).
		Scan(&topic, &retryOf, &attempt, &availableAt); err != nil {
		t.Fatal(err)
	}
	if topic != message.Topic || retryOf != message.ID.String() || attempt != 2 || availableAt.Before(before.Add(8*time.Second)) {
		t.Fatalf("retry topic=%s retry_of=%s attempt=%d available_at=%s before=%s", topic, retryOf, attempt, availableAt, before)
	}
	// Lost acknowledgements may replay the scheduling transaction. The dedupe
	// key must retain one successor and still let the source message complete.
	if scheduled, err = postgres.ScheduleTerminalPublicationRetry(ctx, message, 9*time.Second); err != nil || !scheduled {
		t.Fatalf("idempotent schedule: scheduled=%t err=%v", scheduled, err)
	}
	var count int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE dedupe_key=$1`, dedupeKey).Scan(&count); err != nil || count != 1 {
		t.Fatalf("successor count=%d err=%v", count, err)
	}

	exhausted := message
	exhausted.ID = uuid.New()
	exhausted.Payload = map[string]any{"run_id": uuid.NewString(), "terminal_publication_attempt": float64(5)}
	if scheduled, err = postgres.ScheduleTerminalPublicationRetry(ctx, exhausted, 0); err != nil || scheduled {
		t.Fatalf("exhausted retry: scheduled=%t err=%v", scheduled, err)
	}
}
