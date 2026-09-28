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

func TestTerminalRunsCreateClaimResolveAndRetryInterventions(t *testing.T) {
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
	tenantSlug := "review-intervention-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Review interventions')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'reviewer','reviewer'),($1,'viewer','viewer')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "intervention-installation-"+installationID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'github',$2,'pull_request','{}'::jsonb)`, deliveryID, "intervention-delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',17,'main','base-sha','feature/intervention','head-sha','running')`, jobID, tenantID, installationID, deliveryID)
	batch.Queue(`INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number,title,author) VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',17,'Publishing boundary','reviewer')`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id,request_id,legacy_job_id,state,trigger_kind,review_mode,head_sha,base_sha) VALUES ($1,$2,$3,'publishing','pull_request','security','head-sha','base-sha')`, runID, requestID, jobID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed review intervention source: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	terminal, err := postgres.AdvanceLegacyRun(ctx, jobID, domain.RunNeedsAttention)
	if err != nil || terminal.State != domain.RunNeedsAttention || terminal.Revision != 2 {
		t.Fatalf("terminal transition=%#v err=%v", terminal, err)
	}
	page, err := postgres.ListWorkQueue(ctx, "reviewer", tenantSlug, domain.WorkQueueFilter{View: domain.WorkQueueNeedsAttention, Limit: 25})
	if err != nil || page.Counts.NeedsAttention != 1 || len(page.Runs) != 1 || page.Runs[0].ID != runID || page.Runs[0].Intervention == nil || page.Runs[0].Intervention.State != domain.ReviewInterventionOpen {
		t.Fatalf("queue after terminal transition=%#v err=%v", page, err)
	}
	intervention := page.Runs[0].Intervention
	if _, err := postgres.ClaimReviewIntervention(ctx, "viewer", tenantSlug, runID, intervention.Revision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer claim error=%v, want ErrForbidden", err)
	}
	claimed, err := postgres.ClaimReviewIntervention(ctx, "reviewer", tenantSlug, runID, intervention.Revision)
	if err != nil || claimed.State != domain.ReviewInterventionClaimed || claimed.AssigneeSubject != "reviewer" || claimed.Revision != intervention.Revision+1 {
		t.Fatalf("claimed intervention=%#v err=%v", claimed, err)
	}
	if _, err := postgres.ResolveReviewIntervention(ctx, "viewer", tenantSlug, runID, domain.ReviewInterventionResolutionInput{ExpectedRevision: claimed.Revision, Reason: "A different operator cannot close this claim."}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer resolve error=%v, want ErrForbidden", err)
	}

	retry, err := postgres.RequestRunRetry(ctx, "reviewer", tenantSlug, runID, domain.RunRetryInput{ExpectedRevision: terminal.Revision, IdempotencyKey: "intervention-retry-0001"})
	if err != nil || retry.Run.ID == runID || retry.Run.State != domain.RunAcknowledged {
		t.Fatalf("retry=%#v err=%v", retry, err)
	}
	page, err = postgres.ListWorkQueue(ctx, "reviewer", tenantSlug, domain.WorkQueueFilter{View: domain.WorkQueueNeedsAttention, Limit: 25})
	if err != nil || page.Counts.NeedsAttention != 0 || len(page.Runs) != 0 {
		t.Fatalf("queue after retry=%#v err=%v", page, err)
	}
	items, err := postgres.ListReviewInterventions(ctx, "reviewer", tenantSlug, domain.ReviewInterventionFilter{RunID: &runID, ActiveOnly: false, Limit: 1})
	if err != nil || len(items) != 1 || items[0].State != domain.ReviewInterventionResolved || items[0].Resolution != "retry_requested" || items[0].ResolvedBy != "reviewer" {
		t.Fatalf("intervention after retry=%#v err=%v", items, err)
	}
	var sourceState domain.RunState
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM review_runs WHERE id=$1`, runID).Scan(&sourceState); err != nil || sourceState != domain.RunNeedsAttention {
		t.Fatalf("source state mutated=%q err=%v", sourceState, err)
	}

	secondDeliveryID, secondJobID, secondRequestID, secondRunID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload)
		VALUES ($1,'github',$2,'pull_request','{}'::jsonb)`, secondDeliveryID, "intervention-delivery-"+secondDeliveryID.String()); err != nil {
		t.Fatalf("seed second intervention delivery: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state)
		VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',18,'main','base-sha','feature/intervention-two','head-sha-two','running')`, secondJobID, tenantID, installationID, secondDeliveryID); err != nil {
		t.Fatalf("seed second intervention job: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number,title,author)
		VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',18,'Manual acknowledgement','reviewer')`, secondRequestID, tenantID, installationID); err != nil {
		t.Fatalf("seed second intervention request: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO review_runs (id,request_id,legacy_job_id,state,trigger_kind,review_mode,head_sha,base_sha)
		VALUES ($1,$2,$3,'publishing','manual','security','head-sha-two','base-sha')`, secondRunID, secondRequestID, secondJobID); err != nil {
		t.Fatalf("seed second intervention run: %v", err)
	}
	if _, err := postgres.AdvanceLegacyRun(ctx, secondJobID, domain.RunFailed); err != nil {
		t.Fatalf("fail second intervention run: %v", err)
	}
	secondItems, err := postgres.ListReviewInterventions(ctx, "owner", tenantSlug, domain.ReviewInterventionFilter{RunID: &secondRunID, ActiveOnly: true, Limit: 1})
	if err != nil || len(secondItems) != 1 || secondItems[0].State != domain.ReviewInterventionOpen {
		t.Fatalf("second intervention=%#v err=%v", secondItems, err)
	}
	resolved, err := postgres.ResolveReviewIntervention(ctx, "owner", tenantSlug, secondRunID, domain.ReviewInterventionResolutionInput{ExpectedRevision: secondItems[0].Revision, Reason: "Provider incident is tracked externally and requires no retry."})
	if err != nil || resolved.State != domain.ReviewInterventionResolved || resolved.Resolution != "acknowledged" || resolved.Reason == "" {
		t.Fatalf("manual intervention resolution=%#v err=%v", resolved, err)
	}
	secondItems, err = postgres.ListReviewInterventions(ctx, "owner", tenantSlug, domain.ReviewInterventionFilter{RunID: &secondRunID, ActiveOnly: true, Limit: 1})
	if err != nil || len(secondItems) != 0 {
		t.Fatalf("resolved intervention remained active=%#v err=%v", secondItems, err)
	}
}
