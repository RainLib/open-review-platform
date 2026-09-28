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

func TestManualRunRetryCreatesOneNewImmutableExecution(t *testing.T) {
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
	deliveryID, jobID, requestID, sourceRunID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tenantSlug := "manual-run-retry-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Manual run retry')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'viewer','viewer')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "retry-installation-"+installationID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'github',$2,'pull_request','{}'::jsonb)`, deliveryID, "retry-delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state,error_message,finished_at) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',3,'main','base-sha','feature/retry','head-sha','failed','model transport unavailable',now())`, jobID, tenantID, installationID, deliveryID)
	batch.Queue(`INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',3)`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id,request_id,legacy_job_id,revision,state,trigger_kind,review_mode,head_sha,base_sha,failure_code,failure_message,finished_at) VALUES ($1,$2,$3,4,'failed','pull_request','security','head-sha','base-sha','provider_unavailable','model transport unavailable',now())`, sourceRunID, requestID, jobID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed retry run: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	input := domain.RunRetryInput{ExpectedRevision: 4, IdempotencyKey: "console-retry-0001"}
	created, err := postgres.RequestRunRetry(ctx, "owner", tenantSlug, sourceRunID, input)
	if err != nil {
		t.Fatalf("request retry: %v", err)
	}
	if created.Replayed || created.Run.ID == sourceRunID || created.Run.State != domain.RunAcknowledged || created.Run.TriggerKind != "retry" || created.Run.ReviewMode != domain.ReviewModeSecurity || created.Run.HeadSHA != "head-sha" || created.Run.BaseSHA != "base-sha" || created.Run.LegacyJobID == nil {
		t.Fatalf("created retry=%#v", created)
	}

	var sourceState domain.RunState
	var sourceRevision int
	if err := postgres.pool.QueryRow(ctx, `SELECT state, revision FROM review_runs WHERE id=$1`, sourceRunID).Scan(&sourceState, &sourceRevision); err != nil || sourceState != domain.RunFailed || sourceRevision != 4 {
		t.Fatalf("source mutated: state=%q revision=%d err=%v", sourceState, sourceRevision, err)
	}
	var clonedJobState domain.JobState
	var clonedBase, clonedHead string
	if err := postgres.pool.QueryRow(ctx, `SELECT state, base_sha, head_sha FROM review_jobs WHERE id=$1`, *created.Run.LegacyJobID).Scan(&clonedJobState, &clonedBase, &clonedHead); err != nil || clonedJobState != domain.JobQueued || clonedBase != "base-sha" || clonedHead != "head-sha" {
		t.Fatalf("cloned job state=%q base=%q head=%q err=%v", clonedJobState, clonedBase, clonedHead, err)
	}
	var snapshots, stages, acknowledgements, outbox, audit int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_configuration_snapshots WHERE run_id=$1`, created.Run.ID).Scan(&snapshots); err != nil || snapshots != len(domain.ReviewConfigSections()) {
		t.Fatalf("configuration snapshots=%d error=%v", snapshots, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_run_stages WHERE run_id=$1`, created.Run.ID).Scan(&stages); err != nil || stages != 6 {
		t.Fatalf("stages=%d error=%v", stages, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_run_events WHERE run_id=$1 AND event_type='run.acknowledged'`, created.Run.ID).Scan(&acknowledgements); err != nil || acknowledgements != 1 {
		t.Fatalf("acknowledgements=%d error=%v", acknowledgements, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.run.acknowledged'`, created.Run.ID).Scan(&outbox); err != nil || outbox != 1 {
		t.Fatalf("outbox=%d error=%v", outbox, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='review_run.retry_requested' AND target=$2`, tenantID, created.Run.ID.String()).Scan(&audit); err != nil || audit != 1 {
		t.Fatalf("audit=%d error=%v", audit, err)
	}

	replayed, err := postgres.RequestRunRetry(ctx, "owner", tenantSlug, sourceRunID, input)
	if err != nil || !replayed.Replayed || replayed.Run.ID != created.Run.ID {
		t.Fatalf("replayed retry=%#v error=%v", replayed, err)
	}
	if _, err := postgres.RequestRunRetry(ctx, "viewer", tenantSlug, sourceRunID, domain.RunRetryInput{ExpectedRevision: 4, IdempotencyKey: "console-retry-0002"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer retry error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.RequestRunRetry(ctx, "owner", tenantSlug, sourceRunID, domain.RunRetryInput{ExpectedRevision: 3, IdempotencyKey: "console-retry-0003"}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale retry error=%v, want ErrRevisionConflict", err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO workspace_setup_checkpoints (tenant_id,current_step,revision,updated_by) VALUES ($1,'review_scope',1,'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.RequestRunRetry(ctx, "owner", tenantSlug, sourceRunID, domain.RunRetryInput{ExpectedRevision: 4, IdempotencyKey: "console-retry-0004"}); !errors.Is(err, ErrWorkspaceSetupIncomplete) {
		t.Fatalf("incomplete setup retry error=%v, want ErrWorkspaceSetupIncomplete", err)
	}
}
