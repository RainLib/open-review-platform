package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOperationalMetricsAggregateWorkflowSignalsWithoutDimensions(t *testing.T) {
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
	queuedDeliveryID, runningDeliveryID := uuid.New(), uuid.New()
	queuedJobID, runningJobID := uuid.New(), uuid.New()
	requestID, runID := uuid.New(), uuid.New()
	outboxID, receiptID := uuid.New(), uuid.New()
	destinationID, notificationID := uuid.New(), uuid.New()
	workerID := "operational-metrics-" + uuid.NewString()
	activeWorkerID := "operational-metrics-active-" + uuid.NewString()
	now := time.Now().UTC()
	tx, err := postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	seed := func(name, query string, args ...any) {
		t.Helper()
		if _, execErr := tx.Exec(ctx, query, args...); execErr != nil {
			t.Fatalf("seed %s: %v", name, execErr)
		}
	}
	seed("tenant", `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Operational metrics')`, tenantID, "operational-metrics-"+tenantID.String()[:8])
	seed("installation", `INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'acme/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "operational-metrics-"+installationID.String())
	seed("webhook deliveries", `INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload,received_at) VALUES ($1,'github',$2,'pull_request','{}'::jsonb,$3),($4,'github',$5,'pull_request','{}'::jsonb,$3)`, queuedDeliveryID, "operational-queued-"+queuedDeliveryID.String(), now, runningDeliveryID, "operational-running-"+runningDeliveryID.String())
	seed("queued job", `INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state,available_at,created_at) VALUES ($1,$2,$3,$4,'github','https://api.github.com','acme/service','https://github.com/acme/service.git',1,'main','base','feature','head','queued',$5,$6)`, queuedJobID, tenantID, installationID, queuedDeliveryID, now, now.Add(-time.Minute))
	seed("running job", `INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state,available_at,locked_by,locked_until,created_at,started_at) VALUES ($1,$2,$3,$4,'github','https://api.github.com','acme/service','https://github.com/acme/service.git',2,'main','base','feature','head','running',$5,$6,$7,$8,$8)`, runningJobID, tenantID, installationID, runningDeliveryID, now, workerID, now.Add(-time.Minute), now.Add(-2*time.Minute))
	seed("review request", `INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number,created_at,updated_at) VALUES ($1,$2,$3,'github','https://api.github.com','acme/service',3,$4,$4)`, requestID, tenantID, installationID, now.Add(-time.Minute))
	seed("failed review run", `INSERT INTO review_runs (id,request_id,state,trigger_kind,review_mode,head_sha,base_sha,created_at,finished_at,failure_code,failure_message) VALUES ($1,$2,'failed','manual','standard','head','base',$3,$4,'model_unavailable','bounded failure')`, runID, requestID, now.Add(-time.Minute), now.Add(-30*time.Second))
	seed("publication receipt", `INSERT INTO publication_receipts (id,run_id,provider,receipt_kind,stable_marker,payload_hash,last_error,created_at,updated_at) VALUES ($1,$2,'github','status',$3,$4,'provider unavailable',$5,$5)`, receiptID, runID, "operational-metrics:"+receiptID.String(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", now.Add(-20*time.Second))
	seed("acknowledgement outbox", `INSERT INTO outbox_messages (id,aggregate_type,aggregate_id,topic,dedupe_key,payload,available_at,created_at) VALUES ($1,'review_run',$2,'review.run.acknowledged',$3,'{}'::jsonb,$4,$5)`, outboxID, runID, "operational-metrics:"+outboxID.String(), now.Add(-10*time.Second), now.Add(-10*time.Second))
	seed("stale worker heartbeat", `INSERT INTO worker_heartbeats (worker_id,kind,version,capacity,busy,started_at,heartbeat_at,expires_at) VALUES ($1,'notifier','test',1,0,$2,$3,$4)`, workerID, now.Add(-time.Hour), now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	seed("active worker heartbeat", `INSERT INTO worker_heartbeats (worker_id,kind,version,capacity,busy,started_at,heartbeat_at,expires_at) VALUES ($1,'review-runner','test',1,0,$2,$3,$4)`, activeWorkerID, now.Add(-time.Hour), now, now.Add(time.Minute))
	seed("notification destination", `INSERT INTO notification_destinations (id,tenant_id,name,provider,secret_ref,enabled) VALUES ($1,$2,'Operational alerts','webhook','env:OPERATIONAL_TEST',TRUE)`, destinationID, tenantID)
	seed("pending notification", `INSERT INTO notification_deliveries (id,event_id,run_id,destination_id,state,created_at,updated_at) VALUES ($1,$2,$3,$4,'pending',$5,$5)`, notificationID, "operational-metrics:"+notificationID.String(), runID, destinationID, now.Add(-30*time.Second))
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE id=$1`, outboxID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM worker_heartbeats WHERE worker_id=ANY($1::text[])`, []string{workerID, activeWorkerID})
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM webhook_deliveries WHERE id=ANY($1::uuid[])`, []uuid.UUID{queuedDeliveryID, runningDeliveryID})
	}()

	snapshot, err := postgres.GetOperationalMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.QueuedReviewJobs < 1 || snapshot.OldestQueuedReviewJobSeconds < 50 ||
		snapshot.UnpublishedOutboxMessages < 1 || snapshot.OldestUnpublishedOutboxSeconds < 5 ||
		snapshot.AcknowledgementSLABreaches < 1 || snapshot.ExpiredReviewLeases < 1 ||
		snapshot.StaleWorkerHeartbeats < 1 || snapshot.PublicationFailuresLastHour < 1 ||
		snapshot.FailedRunsLastHour < 1 || snapshot.TerminalRunsLastHour < 1 ||
		snapshot.PendingNotifications < 1 || snapshot.OldestPendingNotificationSeconds < 20 {
		t.Fatalf("operational snapshot omitted seeded signals: %#v", snapshot)
	}
}
