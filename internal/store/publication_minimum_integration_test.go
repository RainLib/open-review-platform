package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestReviewPublicationMinimumIsFrozenAtAdmission(t *testing.T) {
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
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Publication Floor')`, tenantID, "publication-floor-"+tenantID.String()[:8])
	batch.Queue(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,minimum_severity) VALUES($1,$2,'github',$3,'RainLib/*','https://api.github.com','secret://publication','high')`, installationID, tenantID, "publication-"+tenantID.String())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	createRun := func(number int) uuid.UUID {
		t.Helper()
		deliveryID, requestID, jobID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		batch := &pgx.Batch{}
		batch.Queue(`INSERT INTO webhook_deliveries(id,provider,delivery_id,event_name,payload) VALUES($1,'github',$2,'pull_request','{}'::jsonb)`, deliveryID, "publication-"+deliveryID.String())
		batch.Queue(`INSERT INTO review_jobs(id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state) VALUES($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',$5,'main','base','feature','head','queued')`, jobID, tenantID, installationID, deliveryID, number)
		batch.Queue(`INSERT INTO review_requests(id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',$4)`, requestID, tenantID, installationID, number)
		batch.Queue(`INSERT INTO review_runs(id,request_id,legacy_job_id,state,trigger_kind,head_sha,base_sha) VALUES($1,$2,$3,'acknowledged','pull_request','head','base')`, runID, requestID, jobID)
		if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
			t.Fatal(err)
		}
		return jobID
	}

	firstJob := createRun(101)
	if minimum, err := postgres.PublicationMinimumForJob(ctx, firstJob); err != nil || minimum != "high" {
		t.Fatalf("initial minimum=%q error=%v, want high", minimum, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET minimum_severity='low' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	if minimum, err := postgres.PublicationMinimumForJob(ctx, firstJob); err != nil || minimum != "high" {
		t.Fatalf("old run changed with installation: minimum=%q error=%v", minimum, err)
	}
	secondJob := createRun(102)
	if minimum, err := postgres.PublicationMinimumForJob(ctx, secondJob); err != nil || minimum != "low" {
		t.Fatalf("new run minimum=%q error=%v, want low", minimum, err)
	}
}
