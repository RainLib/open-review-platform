package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestInstallationWebhookReceiptsAreScopedAndPayloadFree(t *testing.T) {
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

	tenantID, installationID, otherInstallationID := uuid.New(), uuid.New(), uuid.New()
	deliveryID, jobID, requestID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	issueDeliveryID, issueJobID := uuid.New(), uuid.New()
	otherDeliveryID, otherJobID := uuid.New(), uuid.New()
	tenantSlug := "installation-webhooks-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Installation webhook receipts')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app')`, installationID, tenantID, "delivery-target-"+installationID.String())
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES ($1,$2,'gitlab',$3,'RainLib/*','https://gitlab.com/api/v4','gitlab-token')`, otherInstallationID, tenantID, "delivery-other-"+otherInstallationID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload,received_at) VALUES ($1,'github',$2,'pull_request','{"signature":"never expose"}'::jsonb,now()-interval '1 minute')`, deliveryID, "delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',3,'main','base','feature/webhooks','head','succeeded')`, jobID, tenantID, installationID, deliveryID)
	batch.Queue(`INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',3)`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id,request_id,legacy_job_id,state,trigger_kind,review_mode,head_sha,base_sha) VALUES ($1,$2,$3,'completed','pull_request','deep','head','base')`, runID, requestID, jobID)
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'github',$2,'issues','{"body":"never expose"}'::jsonb)`, issueDeliveryID, "delivery-"+issueDeliveryID.String())
	batch.Queue(`INSERT INTO provider_issue_analysis_jobs (id,tenant_id,installation_id,last_delivery_id,provider,api_base_url,repository,issue_number,action,title,state,stable_marker,model_route,model_route_sha256,prompt_config,prompt_config_sha256) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform',9,'edited','User issue','completed',$5,'{}'::jsonb,'model-sha','{}'::jsonb,'prompt-sha')`, issueJobID, tenantID, installationID, issueDeliveryID, "issue-marker-"+issueJobID.String())
	batch.Queue(`INSERT INTO provider_issue_analysis_receipts (delivery_id,tenant_id,installation_id,job_id,revision,repository,issue_number,action) VALUES ($1,$2,$3,$4,2,'RainLib/open-review-platform',9,'edited')`, issueDeliveryID, tenantID, installationID, issueJobID)
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'gitlab',$2,'merge_request','{}'::jsonb)`, otherDeliveryID, "delivery-"+otherDeliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state) VALUES ($1,$2,$3,$4,'gitlab','https://gitlab.com/api/v4','RainLib/other','https://gitlab.com/RainLib/other.git',4,'main','base','feature/other','head','queued')`, otherJobID, tenantID, otherInstallationID, otherDeliveryID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed installation webhook receipts: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	receipts, err := postgres.ListInstallationWebhookReceipts(ctx, "owner", tenantSlug, installationID, 10)
	if err != nil || len(receipts) != 2 {
		t.Fatalf("receipts=%#v error=%v", receipts, err)
	}
	issueReceipt, reviewReceipt := receipts[0], receipts[1]
	if issueReceipt.ID != issueDeliveryID || issueReceipt.EventName != "issues" || issueReceipt.ResourceKind != "issue" || issueReceipt.Action != "edited" || issueReceipt.Revision != 2 || issueReceipt.JobID != issueJobID || issueReceipt.ReviewNumber != 9 || issueReceipt.JobState != domain.JobSucceeded || issueReceipt.RunID != nil || issueReceipt.TriggerKind != "provider_issue" {
		t.Fatalf("unexpected issue receipt=%#v", issueReceipt)
	}
	if reviewReceipt.ID != deliveryID || reviewReceipt.EventName != "pull_request" || reviewReceipt.ResourceKind != "pull_request" || reviewReceipt.JobID != jobID || reviewReceipt.Repository != "RainLib/open-review-platform" || reviewReceipt.ReviewNumber != 3 || reviewReceipt.JobState != domain.JobSucceeded || reviewReceipt.RunID == nil || *reviewReceipt.RunID != runID || reviewReceipt.RunState == nil || *reviewReceipt.RunState != domain.RunCompleted || reviewReceipt.TriggerKind != "pull_request" {
		t.Fatalf("unexpected review receipt=%#v", reviewReceipt)
	}
	if _, err := postgres.ListInstallationWebhookReceipts(ctx, "outsider", tenantSlug, installationID, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant receipt list error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.ListInstallationWebhookReceipts(ctx, "owner", tenantSlug, uuid.New(), 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown installation receipt list error=%v, want ErrNotFound", err)
	}
}
