package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestReviewSchedulesRemainOutsideTheQueueUntilDueThenAdmitOrBlock(t *testing.T) {
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
	deliveryID, sourceJobID, requestID, sourceRunID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tenantSlug := "review-schedule-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Review schedules')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'viewer','viewer')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "schedule-installation-"+installationID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'github',$2,'pull_request','{}'::jsonb)`, deliveryID, "schedule-delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state,finished_at) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',3,'main','base-sha','feature/scheduled','head-sha','succeeded',now())`, sourceJobID, tenantID, installationID, deliveryID)
	batch.Queue(`INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number,title,author) VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',3,'Durable scheduled admission','reviewer')`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id,request_id,legacy_job_id,state,trigger_kind,review_mode,head_sha,base_sha,finished_at) VALUES ($1,$2,$3,'completed','pull_request','security','head-sha','base-sha',now())`, sourceRunID, requestID, sourceJobID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed scheduled source: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	future := time.Now().UTC().Add(2 * time.Minute)
	created, err := postgres.CreateReviewSchedule(ctx, "owner", tenantSlug, sourceRunID, domain.ReviewScheduleInput{ScheduledFor: future})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if created.State != domain.ReviewScheduleScheduled || created.SourceRunID != sourceRunID || created.ReviewMode != domain.ReviewModeSecurity || created.HeadSHA != "head-sha" {
		t.Fatalf("created schedule=%#v", created)
	}
	var jobsBeforeDue, reservedBeforeDue int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_jobs WHERE tenant_id=$1`, tenantID).Scan(&jobsBeforeDue); err != nil || jobsBeforeDue != 1 {
		t.Fatalf("jobs before due=%d err=%v", jobsBeforeDue, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM usage_reservations WHERE tenant_id=$1`, tenantID).Scan(&reservedBeforeDue); err != nil || reservedBeforeDue != 0 {
		t.Fatalf("usage before due=%d err=%v", reservedBeforeDue, err)
	}
	listed, err := postgres.ListReviewSchedules(ctx, "owner", tenantSlug, 50)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed schedules=%#v err=%v", listed, err)
	}
	if _, err := postgres.CreateReviewSchedule(ctx, "viewer", tenantSlug, sourceRunID, domain.ReviewScheduleInput{ScheduledFor: future}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create schedule error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.CancelReviewSchedule(ctx, "owner", tenantSlug, created.ID, created.Revision+1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale cancellation error=%v, want ErrRevisionConflict", err)
	}
	cancelled, err := postgres.CancelReviewSchedule(ctx, "owner", tenantSlug, created.ID, created.Revision)
	if err != nil || cancelled.State != domain.ReviewScheduleCancelled || cancelled.CancelledBy != "owner" {
		t.Fatalf("cancelled schedule=%#v err=%v", cancelled, err)
	}

	admission, err := postgres.CreateReviewSchedule(ctx, "owner", tenantSlug, sourceRunID, domain.ReviewScheduleInput{ScheduledFor: future})
	if err != nil {
		t.Fatalf("create admission schedule: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_schedules SET scheduled_for=now()-interval '1 second' WHERE id=$1`, admission.ID); err != nil {
		t.Fatalf("make admission schedule due: %v", err)
	}
	admitted, claimed, err := postgres.AdmitDueReviewSchedule(ctx, "schedule-test-worker")
	if err != nil || !claimed || admitted.ID != admission.ID || admitted.State != domain.ReviewScheduleAdmitted || admitted.AdmittedRunID == nil {
		t.Fatalf("admitted schedule=%#v claimed=%t err=%v", admitted, claimed, err)
	}
	if *admitted.AdmittedRunID == sourceRunID {
		t.Fatalf("admitted run reused source run=%s", sourceRunID)
	}
	var admittedTrigger string
	var admittedHead string
	if err := postgres.pool.QueryRow(ctx, `SELECT trigger_kind, head_sha FROM review_runs WHERE id=$1`, *admitted.AdmittedRunID).Scan(&admittedTrigger, &admittedHead); err != nil || admittedTrigger != "scheduled" || admittedHead != "head-sha" {
		t.Fatalf("admitted run trigger=%q head=%q err=%v", admittedTrigger, admittedHead, err)
	}
	var snapshots, outbox, reservations, scheduleAudit int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_configuration_snapshots WHERE run_id=$1`, *admitted.AdmittedRunID).Scan(&snapshots); err != nil || snapshots != len(domain.ReviewConfigSections()) {
		t.Fatalf("admitted configuration snapshots=%d err=%v", snapshots, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.run.acknowledged'`, *admitted.AdmittedRunID).Scan(&outbox); err != nil || outbox != 1 {
		t.Fatalf("admitted outbox=%d err=%v", outbox, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM usage_reservations WHERE run_id=$1`, *admitted.AdmittedRunID).Scan(&reservations); err != nil || reservations != 1 {
		t.Fatalf("admitted usage=%d err=%v", reservations, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='review_schedule.admitted' AND target=$2`, tenantID, admission.ID.String()).Scan(&scheduleAudit); err != nil || scheduleAudit != 1 {
		t.Fatalf("admission audit=%d err=%v", scheduleAudit, err)
	}

	coalescedSchedule, err := postgres.CreateReviewSchedule(ctx, "owner", tenantSlug, sourceRunID, domain.ReviewScheduleInput{ScheduledFor: future})
	if err != nil {
		t.Fatalf("create coalescing schedule: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_schedules SET scheduled_for=now()-interval '1 second' WHERE id=$1`, coalescedSchedule.ID); err != nil {
		t.Fatalf("make coalescing schedule due: %v", err)
	}
	coalesced, claimed, err := postgres.AdmitDueReviewSchedule(ctx, "schedule-test-worker")
	if err != nil || !claimed || coalesced.State != domain.ReviewScheduleCoalesced || coalesced.AdmittedRunID == nil || *coalesced.AdmittedRunID != *admitted.AdmittedRunID {
		t.Fatalf("coalesced schedule=%#v claimed=%t err=%v", coalesced, claimed, err)
	}

	setupBlockedSchedule, err := postgres.CreateReviewSchedule(ctx, "owner", tenantSlug, sourceRunID, domain.ReviewScheduleInput{ScheduledFor: future})
	if err != nil {
		t.Fatalf("create setup-blocked schedule: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO workspace_setup_checkpoints (tenant_id,current_step,revision,updated_by) VALUES ($1,'review_scope',1,'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_schedules SET scheduled_for=now()-interval '1 second' WHERE id=$1`, setupBlockedSchedule.ID); err != nil {
		t.Fatalf("make setup-blocked schedule due: %v", err)
	}
	setupBlocked, claimed, err := postgres.AdmitDueReviewSchedule(ctx, "schedule-test-worker")
	if err != nil || !claimed || setupBlocked.State != domain.ReviewScheduleBlocked || !strings.Contains(setupBlocked.BlockedReason, "workspace setup is incomplete") || setupBlocked.AdmittedRunID != nil {
		t.Fatalf("setup-blocked schedule=%#v claimed=%t err=%v", setupBlocked, claimed, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE workspace_setup_checkpoints SET current_step='complete',revision=2,completed_at=now() WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatal(err)
	}

	blockedSchedule, err := postgres.CreateReviewSchedule(ctx, "owner", tenantSlug, sourceRunID, domain.ReviewScheduleInput{ScheduledFor: future})
	if err != nil {
		t.Fatalf("create blocked schedule: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET active=FALSE WHERE id=$1`, installationID); err != nil {
		t.Fatalf("deactivate schedule connection: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_schedules SET scheduled_for=now()-interval '1 second' WHERE id=$1`, blockedSchedule.ID); err != nil {
		t.Fatalf("make blocked schedule due: %v", err)
	}
	blocked, claimed, err := postgres.AdmitDueReviewSchedule(ctx, "schedule-test-worker")
	if err != nil || !claimed || blocked.State != domain.ReviewScheduleBlocked || blocked.BlockedReason == "" || blocked.AdmittedRunID != nil {
		t.Fatalf("blocked schedule=%#v claimed=%t err=%v", blocked, claimed, err)
	}
	var finalJobs int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_jobs WHERE tenant_id=$1`, tenantID).Scan(&finalJobs); err != nil || finalJobs != 3 {
		t.Fatalf("jobs after admitted/coalesced/blocked schedules=%d err=%v", finalJobs, err)
	}
}
