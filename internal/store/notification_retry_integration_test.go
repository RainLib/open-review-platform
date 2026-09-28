package store

import (
	"context"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestNotificationDeliveryRetryTargetsTheOriginalDestination(t *testing.T) {
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
	deliveryID, jobID, requestID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	eventID, destinationID, notificationID := uuid.New(), uuid.New(), uuid.New()
	tenantSlug := "notification-retry-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Notification retry')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "notification-retry-"+installationID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'github',$2,'pull_request','{}'::jsonb)`, deliveryID, "notification-retry-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state,finished_at) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',13,'main','base','feature/notification','head','failed',now())`, jobID, tenantID, installationID, deliveryID)
	batch.Queue(`INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',13)`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id,request_id,legacy_job_id,state,trigger_kind,review_mode,head_sha,base_sha,finished_at) VALUES ($1,$2,$3,'failed','pull_request','standard','head','base',now())`, runID, requestID, jobID)
	batch.Queue(`INSERT INTO notification_destinations (id,tenant_id,name,provider,secret_ref,enabled) VALUES ($1,$2,'Release alerts','slack','env:NOTIFICATION_RETRY',TRUE)`, destinationID, tenantID)
	batch.Queue(`INSERT INTO outbox_messages (id,aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES ($1,'review_run',$2,'review.run.failed',$3,'{}'::jsonb)`, eventID, runID, "notification-original-"+eventID.String())
	batch.Queue(`INSERT INTO notification_deliveries (id,event_id,run_id,destination_id,state,attempt,last_error) VALUES ($1,$2,$3,$4,'failed',1,'provider unavailable')`, notificationID, eventID.String(), runID, destinationID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed notification retry: %v", err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE aggregate_id = $1`, runID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	}()

	if err := postgres.RetryNotificationDelivery(ctx, "owner", tenantSlug, notificationID); err != nil {
		t.Fatalf("queue notification retry: %v", err)
	}
	var retryID uuid.UUID
	var topic string
	var destination string
	if err := postgres.pool.QueryRow(ctx, `SELECT id, topic, payload->>'target_destination_id' FROM outbox_messages WHERE aggregate_type = 'review_run' AND aggregate_id = $1 AND dedupe_key LIKE 'notification-retry:%'`, runID).Scan(&retryID, &topic, &destination); err != nil {
		t.Fatalf("load retry outbox: %v", err)
	}
	if topic != "review.run.failed" || destination != destinationID.String() {
		t.Fatalf("retry outbox topic=%q destination=%q", topic, destination)
	}

	deliveries, err := postgres.PrepareNotificationDeliveries(ctx, domain.OutboxMessage{
		ID: retryID, AggregateID: runID, Topic: topic,
		Payload: map[string]any{"target_destination_id": destinationID.String()},
	})
	if err != nil {
		t.Fatalf("prepare targeted retry: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Destination.ID != destinationID || deliveries[0].Event.RunID != runID {
		t.Fatalf("targeted retry deliveries=%#v", deliveries)
	}
}
