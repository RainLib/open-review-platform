package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidReviewSchedule = errors.New("review schedule is invalid")

// ReviewScheduleStore keeps scheduled admission separate from Store's webhook
// contract. A schedule is a durable future intent, not an inbound provider
// delivery and not a queue entry a runner can execute prematurely.
type ReviewScheduleStore interface {
	CreateReviewSchedule(context.Context, string, string, uuid.UUID, domain.ReviewScheduleInput) (domain.ReviewSchedule, error)
	ListReviewSchedules(context.Context, string, string, int) ([]domain.ReviewSchedule, error)
	CancelReviewSchedule(context.Context, string, string, uuid.UUID, int) (domain.ReviewSchedule, error)
	AdmitDueReviewSchedule(context.Context, string) (domain.ReviewSchedule, bool, error)
}

var _ ReviewScheduleStore = (*PostgresStore)(nil)

const reviewScheduleColumns = `
	id, tenant_id, source_run_id, source_job_id, installation_id, request_id,
	admitted_run_id, revision, state, provider, api_base_url,
	repository, review_number, title, author, review_mode, base_sha, head_sha,
	scheduled_for, requested_by, blocked_reason, cancelled_by, cancelled_at,
	created_at, updated_at`

func (s *PostgresStore) CreateReviewSchedule(ctx context.Context, actor, tenantSlug string, sourceRunID uuid.UUID, input domain.ReviewScheduleInput) (domain.ReviewSchedule, error) {
	input, valid := domain.NormalizeReviewScheduleInput(input, time.Now().UTC())
	if !valid || sourceRunID == uuid.Nil {
		return domain.ReviewSchedule{}, ErrInvalidReviewSchedule
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("begin create review schedule: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewSchedule{}, err
	}
	if role != "owner" && role != "admin" && role != "rule_admin" && role != "reviewer" {
		return domain.ReviewSchedule{}, ErrForbidden
	}

	var source struct {
		RunID          uuid.UUID
		JobID          *uuid.UUID
		InstallationID uuid.UUID
		RequestID      uuid.UUID
		Provider       domain.Provider
		APIBaseURL     string
		Repository     string
		ReviewNumber   int
		Title          string
		Author         string
		ReviewMode     domain.ReviewMode
		BaseSHA        string
		HeadSHA        string
		State          domain.RunState
		Active         bool
		Verification   domain.InstallationVerificationState
	}
	err = tx.QueryRow(ctx, `
		SELECT r.id, r.legacy_job_id, request.installation_id, request.id,
		       request.provider, request.api_base_url, request.repository, request.review_number,
		       request.title, request.author, r.review_mode, r.base_sha, r.head_sha, r.state,
		       installation.active, installation.verification_state
		FROM review_runs r
		JOIN review_requests request ON request.id = r.request_id
		JOIN provider_installations installation ON installation.id = request.installation_id
		WHERE r.id = $1 AND request.tenant_id = $2
		FOR UPDATE`, sourceRunID, tenantID).Scan(
		&source.RunID, &source.JobID, &source.InstallationID, &source.RequestID,
		&source.Provider, &source.APIBaseURL, &source.Repository, &source.ReviewNumber,
		&source.Title, &source.Author, &source.ReviewMode, &source.BaseSHA, &source.HeadSHA, &source.State,
		&source.Active, &source.Verification,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewSchedule{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("load review source for schedule: %w", err)
	}
	if !source.State.Terminal() || source.JobID == nil || !source.Active || !source.Verification.EligibleForReview() || !source.ReviewMode.Valid() {
		return domain.ReviewSchedule{}, ErrConflict
	}

	schedule, err := scanReviewSchedule(tx.QueryRow(ctx, `
		INSERT INTO review_schedules (
			tenant_id, source_run_id, source_job_id, installation_id, request_id,
			provider, api_base_url, repository, review_number, title, author,
			review_mode, base_sha, head_sha, state, scheduled_for, requested_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'scheduled',$15,$16)
		RETURNING `+reviewScheduleColumns,
		tenantID, source.RunID, *source.JobID, source.InstallationID, source.RequestID,
		source.Provider, source.APIBaseURL, source.Repository, source.ReviewNumber, source.Title, source.Author,
		source.ReviewMode, source.BaseSHA, source.HeadSHA, input.ScheduledFor, actor))
	if err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("create review schedule: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1,$2,'review_schedule.created',$3,jsonb_build_object(
			'source_run_id',$4::text,'repository',$5::text,'review_number',$6::integer,
			'head_sha',$7::text,'scheduled_for',$8::timestamptz))`,
		tenantID, actor, schedule.ID.String(), source.RunID.String(), source.Repository, source.ReviewNumber, source.HeadSHA, schedule.ScheduledFor); err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("audit review schedule creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("commit review schedule creation: %w", err)
	}
	return schedule, nil
}

func (s *PostgresStore) ListReviewSchedules(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.ReviewSchedule, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidReviewSchedule
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+reviewScheduleColumns+`
		FROM review_schedules
		WHERE tenant_id = $1
		ORDER BY CASE state WHEN 'scheduled' THEN 0 WHEN 'blocked' THEN 1 ELSE 2 END,
		         scheduled_for ASC, created_at DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list review schedules: %w", err)
	}
	defer rows.Close()
	schedules := make([]domain.ReviewSchedule, 0)
	for rows.Next() {
		schedule, err := scanReviewSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan review schedule: %w", err)
		}
		schedules = append(schedules, schedule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review schedules: %w", err)
	}
	return schedules, nil
}

func (s *PostgresStore) CancelReviewSchedule(ctx context.Context, actor, tenantSlug string, scheduleID uuid.UUID, expectedRevision int) (domain.ReviewSchedule, error) {
	if scheduleID == uuid.Nil || expectedRevision < 1 {
		return domain.ReviewSchedule{}, ErrInvalidReviewSchedule
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("begin cancel review schedule: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewSchedule{}, err
	}
	if role != "owner" && role != "admin" && role != "rule_admin" && role != "reviewer" {
		return domain.ReviewSchedule{}, ErrForbidden
	}
	schedule, err := scanReviewSchedule(tx.QueryRow(ctx, `
		SELECT `+reviewScheduleColumns+`
		FROM review_schedules WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, scheduleID, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewSchedule{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("load review schedule for cancellation: %w", err)
	}
	if schedule.Revision != expectedRevision {
		return domain.ReviewSchedule{}, ErrRevisionConflict
	}
	if !schedule.State.Cancellable() {
		return domain.ReviewSchedule{}, ErrConflict
	}
	schedule, err = scanReviewSchedule(tx.QueryRow(ctx, `
		UPDATE review_schedules
		SET state='cancelled', revision=revision+1, cancelled_by=$3,
		    cancelled_at=now(), updated_at=now()
		WHERE id=$1 AND tenant_id=$2
		RETURNING `+reviewScheduleColumns, scheduleID, tenantID, actor))
	if err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("cancel review schedule: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1,$2,'review_schedule.cancelled',$3,jsonb_build_object('source_run_id',$4::text,'revision',$5::integer))`,
		tenantID, actor, schedule.ID.String(), schedule.SourceRunID.String(), schedule.Revision); err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("audit review schedule cancellation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("commit review schedule cancellation: %w", err)
	}
	return schedule, nil
}

// AdmitDueReviewSchedule atomically converts at most one due schedule into a
// fresh review run. It reads the source job only for clone/ref metadata and
// deliberately resolves the rule/configuration snapshots at this due-time
// admission boundary. No provider call happens in this transaction.
func (s *PostgresStore) AdmitDueReviewSchedule(ctx context.Context, schedulerID string) (domain.ReviewSchedule, bool, error) {
	if schedulerID == "" {
		return domain.ReviewSchedule{}, false, ErrInvalidReviewSchedule
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewSchedule{}, false, fmt.Errorf("begin scheduled review admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	schedule, err := scanReviewSchedule(tx.QueryRow(ctx, `
		SELECT `+reviewScheduleColumns+`
		FROM review_schedules
		WHERE state='scheduled' AND scheduled_for <= now()
		ORDER BY scheduled_for, created_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewSchedule{}, false, nil
	}
	if err != nil {
		return domain.ReviewSchedule{}, false, fmt.Errorf("claim due review schedule: %w", err)
	}

	var source struct {
		CloneURL      string
		BaseRef       string
		HeadRef       string
		ExternalID    string
		CredentialRef string
		Active        bool
		Verification  domain.InstallationVerificationState
	}
	err = tx.QueryRow(ctx, `
		SELECT j.clone_url, j.base_ref, j.head_ref, installation.external_id,
		       installation.credential_ref, installation.active, installation.verification_state
		FROM review_jobs j
		JOIN provider_installations installation ON installation.id=j.installation_id
		WHERE j.id=$1 AND j.tenant_id=$2 AND j.installation_id=$3`,
		schedule.SourceJobID, schedule.TenantID, schedule.InstallationID).Scan(
		&source.CloneURL, &source.BaseRef, &source.HeadRef, &source.ExternalID,
		&source.CredentialRef, &source.Active, &source.Verification,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		blocked, blockErr := blockReviewSchedule(ctx, tx, schedule, "the source review or connection is no longer available")
		return blocked, true, blockErr
	}
	if err != nil {
		return domain.ReviewSchedule{}, false, fmt.Errorf("load scheduled review source: %w", err)
	}
	if !source.Active || !source.Verification.EligibleForReview() {
		blocked, blockErr := blockReviewSchedule(ctx, tx, schedule, "the provider connection is inactive or has not passed verification")
		return blocked, true, blockErr
	}
	setupComplete, err := workspaceSetupAllowsReview(ctx, tx, schedule.TenantID)
	if err != nil {
		return domain.ReviewSchedule{}, false, err
	}
	if !setupComplete {
		blocked, blockErr := blockReviewSchedule(ctx, tx, schedule, "workspace setup is incomplete; finish setup before scheduled admission")
		return blocked, true, blockErr
	}

	var deliveryID uuid.UUID
	deliveryToken := "scheduled-review:" + schedule.ID.String()
	if err := tx.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (provider, delivery_id, event_name, payload)
		VALUES ($1,$2,'scheduled_admission',$3::jsonb)
		RETURNING id`, schedule.Provider, deliveryToken, jsonPayload(map[string]any{
		"schedule_id": schedule.ID.String(), "source_run_id": schedule.SourceRunID.String(),
		"requested_by": schedule.RequestedBy,
	})).Scan(&deliveryID); err != nil {
		return domain.ReviewSchedule{}, false, fmt.Errorf("record scheduled admission delivery: %w", err)
	}

	var jobID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO review_jobs (
			tenant_id, installation_id, delivery_id, provider, api_base_url, repository, clone_url,
			review_number, base_ref, base_sha, head_ref, head_sha, state
		)
		SELECT tenant_id, installation_id, $1, provider, api_base_url, repository, $2,
		       review_number, $3, $4, $5, $6, 'queued'
		FROM review_jobs WHERE id=$7
		RETURNING id`, deliveryID, source.CloneURL, source.BaseRef, schedule.BaseSHA, source.HeadRef, schedule.HeadSHA, schedule.SourceJobID).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		blocked, blockErr := blockReviewSchedule(ctx, tx, schedule, "the source execution metadata is unavailable")
		return blocked, true, blockErr
	}
	if err != nil {
		return domain.ReviewSchedule{}, false, fmt.Errorf("queue scheduled review job: %w", err)
	}
	installation := domain.Installation{ID: schedule.InstallationID, TenantID: schedule.TenantID, Provider: schedule.Provider, ExternalID: source.ExternalID, APIBaseURL: schedule.APIBaseURL, CredentialRef: source.CredentialRef}
	event := domain.InboundEvent{
		Provider: schedule.Provider, DeliveryID: deliveryToken, EventName: "scheduled_admission",
		InstallationExternalID: source.ExternalID, APIBaseURL: schedule.APIBaseURL,
		Repository: schedule.Repository, CloneURL: source.CloneURL, ReviewNumber: schedule.ReviewNumber,
		BaseRef: source.BaseRef, BaseSHA: schedule.BaseSHA, HeadRef: source.HeadRef, HeadSHA: schedule.HeadSHA,
		TriggerKind: "scheduled", ReviewMode: schedule.ReviewMode, ActorKind: "worker", ActorSubject: schedulerID,
		Title: schedule.Title, Author: schedule.Author, ReceivedAt: time.Now().UTC(),
	}
	workflowErr := createWorkflowRun(ctx, tx, installation, domain.ReviewJob{ID: jobID}, event)
	var admittedRunID *uuid.UUID
	state := domain.ReviewScheduleAdmitted
	if errors.Is(workflowErr, errRunCoalesced) {
		state = domain.ReviewScheduleCoalesced
		var current uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT current_run_id FROM review_requests WHERE id=$1`, schedule.RequestID).Scan(&current); err != nil {
			return domain.ReviewSchedule{}, false, fmt.Errorf("load coalesced review run: %w", err)
		}
		admittedRunID = &current
	} else if workflowErr != nil {
		return domain.ReviewSchedule{}, false, fmt.Errorf("create scheduled review workflow: %w", workflowErr)
	} else {
		var current uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT current_run_id FROM review_requests WHERE id=$1`, schedule.RequestID).Scan(&current); err != nil {
			return domain.ReviewSchedule{}, false, fmt.Errorf("load admitted review run: %w", err)
		}
		admittedRunID = &current
	}
	schedule, err = scanReviewSchedule(tx.QueryRow(ctx, `
		UPDATE review_schedules
		SET state=$2, admitted_run_id=$3, revision=revision+1, updated_at=now()
		WHERE id=$1
		RETURNING `+reviewScheduleColumns, schedule.ID, state, admittedRunID))
	if err != nil {
		return domain.ReviewSchedule{}, false, fmt.Errorf("mark scheduled review admitted: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1,$2,$3,$4,jsonb_build_object(
			'source_run_id',$5::text,'admitted_run_id',$6::text,'state',$7::text))`,
		schedule.TenantID, schedulerID, "review_schedule."+string(state), schedule.ID.String(), schedule.SourceRunID.String(), admittedRunID.String(), state); err != nil {
		return domain.ReviewSchedule{}, false, fmt.Errorf("audit scheduled review admission: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewSchedule{}, false, fmt.Errorf("commit scheduled review admission: %w", err)
	}
	return schedule, true, nil
}

func blockReviewSchedule(ctx context.Context, tx pgx.Tx, schedule domain.ReviewSchedule, reason string) (domain.ReviewSchedule, error) {
	blocked, err := scanReviewSchedule(tx.QueryRow(ctx, `
		UPDATE review_schedules SET state='blocked', blocked_reason=$2, revision=revision+1, updated_at=now()
		WHERE id=$1
		RETURNING `+reviewScheduleColumns, schedule.ID, reason))
	if err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("block review schedule: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		SELECT tenant_id, 'scheduler', 'review_schedule.blocked', id,
		       jsonb_build_object('source_run_id',source_run_id::text,'reason',$2::text)
		FROM review_schedules WHERE id=$1`, schedule.ID, reason); err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("audit blocked review schedule: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewSchedule{}, fmt.Errorf("commit blocked review schedule: %w", err)
	}
	return blocked, nil
}

func scanReviewSchedule(row rowScanner) (domain.ReviewSchedule, error) {
	var schedule domain.ReviewSchedule
	var admittedRunID *uuid.UUID
	var blockedReason, cancelledBy *string
	if err := row.Scan(
		&schedule.ID, &schedule.TenantID, &schedule.SourceRunID, &schedule.SourceJobID, &schedule.InstallationID, &schedule.RequestID,
		&admittedRunID, &schedule.Revision, &schedule.State,
		&schedule.Provider, &schedule.APIBaseURL, &schedule.Repository, &schedule.ReviewNumber,
		&schedule.Title, &schedule.Author, &schedule.ReviewMode, &schedule.BaseSHA, &schedule.HeadSHA,
		&schedule.ScheduledFor, &schedule.RequestedBy, &blockedReason, &cancelledBy, &schedule.CancelledAt,
		&schedule.CreatedAt, &schedule.UpdatedAt,
	); err != nil {
		return domain.ReviewSchedule{}, err
	}
	schedule.AdmittedRunID = admittedRunID
	if blockedReason != nil {
		schedule.BlockedReason = *blockedReason
	}
	if cancelledBy != nil {
		schedule.CancelledBy = *cancelledBy
	}
	if !schedule.ValidState() || !schedule.Provider.Valid() || !schedule.ReviewMode.Valid() || schedule.Revision < 1 || schedule.ReviewNumber < 1 {
		return domain.ReviewSchedule{}, ErrInvalidReviewSchedule
	}
	return schedule, nil
}
