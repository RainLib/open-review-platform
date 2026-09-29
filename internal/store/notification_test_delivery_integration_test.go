package store

import (
	"context"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestNotificationTestCreatesAStableAsyncReceipt(t *testing.T) {
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
	tenantSlug := "notification-test-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Notification Test')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE aggregate_type = 'notification_destination' AND aggregate_id IN (SELECT id FROM notification_destinations WHERE tenant_id = $1)`, tenantID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	}()

	destination, err := postgres.CreateNotificationDestination(ctx, "owner", tenantSlug, domain.NotificationDestinationInput{
		Name: "Platform alerts", Provider: domain.NotificationSlack, SecretRef: "env:NOTIFICATION_TEST", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create notification destination: %v", err)
	}
	receipt, err := postgres.RequestNotificationTest(ctx, "owner", tenantSlug, destination.ID, domain.NotificationTestInput{ExpectedRevision: destination.Revision})
	if err != nil {
		t.Fatalf("request notification test: %v", err)
	}
	if receipt.State != "pending" || receipt.EventType != notificationTestTopic || receipt.RunID != nil || receipt.DestinationID != destination.ID {
		t.Fatalf("unexpected queued test receipt: %#v", receipt)
	}
	eventID, err := uuid.Parse(receipt.EventID)
	if err != nil {
		t.Fatal(err)
	}
	var topic, payloadDestination string
	var payloadRevision int
	if err := postgres.pool.QueryRow(ctx, `SELECT topic, payload->>'destination_id', (payload->>'destination_revision')::integer FROM outbox_messages WHERE id = $1`, eventID).Scan(&topic, &payloadDestination, &payloadRevision); err != nil {
		t.Fatal(err)
	}
	if topic != notificationTestTopic || payloadDestination != destination.ID.String() || payloadRevision != destination.Revision {
		t.Fatalf("unexpected test outbox payload: topic=%s destination=%s revision=%d", topic, payloadDestination, payloadRevision)
	}

	deliveries, err := postgres.PrepareNotificationDeliveries(ctx, domain.OutboxMessage{
		ID: eventID, AggregateID: destination.ID, Topic: topic,
		Payload: map[string]any{"destination_id": destination.ID.String(), "destination_revision": float64(destination.Revision)},
	})
	if err != nil || len(deliveries) != 1 || !deliveries[0].Event.Test || deliveries[0].Destination.SecretRef != "env:NOTIFICATION_TEST" {
		t.Fatalf("prepared test deliveries=%#v err=%v", deliveries, err)
	}
	if err := postgres.FinishNotificationDelivery(ctx, deliveries[0].ID, 200, nil); err != nil {
		t.Fatalf("finish notification test: %v", err)
	}
	history, err := postgres.ListNotificationDeliveries(ctx, "owner", tenantSlug, 10)
	if err != nil || len(history) != 1 || history[0].State != "delivered" || history[0].RunID != nil || history[0].ResponseCode == nil || *history[0].ResponseCode != 200 {
		t.Fatalf("unexpected notification test history=%#v err=%v", history, err)
	}

	stale, err := postgres.RequestNotificationTest(ctx, "owner", tenantSlug, destination.ID, domain.NotificationTestInput{ExpectedRevision: destination.Revision})
	if err != nil {
		t.Fatalf("request stale notification test: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE notification_destinations SET revision = revision + 1, updated_at = now() WHERE id = $1`, destination.ID); err != nil {
		t.Fatal(err)
	}
	staleEventID, err := uuid.Parse(stale.EventID)
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err = postgres.PrepareNotificationDeliveries(ctx, domain.OutboxMessage{
		ID: staleEventID, AggregateID: destination.ID, Topic: notificationTestTopic,
		Payload: map[string]any{"destination_id": destination.ID.String(), "destination_revision": float64(destination.Revision)},
	})
	if err != nil || len(deliveries) != 0 {
		t.Fatalf("stale test must not send: deliveries=%#v err=%v", deliveries, err)
	}
	var state, lastError string
	if err := postgres.pool.QueryRow(ctx, `SELECT state, COALESCE(last_error, '') FROM notification_deliveries WHERE id = $1`, stale.ID).Scan(&state, &lastError); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || lastError != "notification destination changed before test delivery" {
		t.Fatalf("stale test receipt state=%s error=%q", state, lastError)
	}
}
