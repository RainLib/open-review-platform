package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNoGitLabAuthorAdmission = errors.New("no GitLab author admission is due")

type GitLabAuthorAdmissionTarget struct {
	DeliveryID         uuid.UUID
	DeliveryExternalID string
	ReceivedAt         time.Time
	Payload            []byte
	ExpectedAuthorID   string
	Attempt            int
	WorkerID           string
	Job                domain.ReviewJob
}

// ClaimGitLabAuthorAdmission leases a verified webhook's unresolved author
// without passing any provider token through the public API or database.
func (s *PostgresStore) ClaimGitLabAuthorAdmission(ctx context.Context, workerID string, lease time.Duration) (*GitLabAuthorAdmissionTarget, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || lease <= 0 {
		return nil, fmt.Errorf("GitLab author worker identity and lease are required")
	}
	var target GitLabAuthorAdmissionTarget
	err := s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT admission.delivery_id
			FROM gitlab_author_admissions admission
			WHERE (admission.state='queued' AND admission.available_at<=now())
			   OR (admission.state='running' AND admission.locked_until<now())
			ORDER BY admission.available_at,admission.delivery_id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE gitlab_author_admissions admission SET
			state='running',attempt=admission.attempt+1,worker_id=$1,
			locked_until=now()+$2::interval,updated_at=now()
		FROM candidate,webhook_deliveries delivery,provider_installations installation
		WHERE admission.delivery_id=candidate.delivery_id
		  AND delivery.id=admission.delivery_id
		  AND installation.id=admission.installation_id
		RETURNING admission.delivery_id,delivery.delivery_id,delivery.received_at,
		          delivery.payload,admission.author_id,admission.attempt,admission.worker_id,
		          installation.tenant_id,installation.id,installation.external_id,
		          installation.credential_ref,installation.api_base_url,
		          admission.repository,admission.review_number,admission.head_sha`, workerID, lease.String()).Scan(
		&target.DeliveryID, &target.DeliveryExternalID, &target.ReceivedAt,
		&target.Payload, &target.ExpectedAuthorID, &target.Attempt, &target.WorkerID,
		&target.Job.TenantID, &target.Job.InstallationID, &target.Job.InstallationExternalID,
		&target.Job.CredentialRef, &target.Job.APIBaseURL,
		&target.Job.Repository, &target.Job.ReviewNumber, &target.Job.HeadSHA)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoGitLabAuthorAdmission
	}
	if err != nil {
		return nil, fmt.Errorf("claim GitLab author admission: %w", err)
	}
	target.Job.Provider = domain.ProviderGitLab
	return &target, nil
}

func (s *PostgresStore) CompleteGitLabAuthorAdmission(ctx context.Context, target GitLabAuthorAdmissionTarget, event domain.InboundEvent) error {
	if !validGitLabAuthorTarget(target) || event.Provider != domain.ProviderGitLab ||
		event.DeliveryID != target.DeliveryExternalID || event.Repository != target.Job.Repository ||
		event.ReviewNumber != target.Job.ReviewNumber || !strings.EqualFold(event.HeadSHA, target.Job.HeadSHA) ||
		event.AuthorExternalID != target.ExpectedAuthorID || strings.TrimSpace(event.Author) == "" ||
		!bytes.Equal(event.Payload, target.Payload) {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockGitLabAuthorAdmission(ctx, tx, target); err != nil {
		return err
	}
	installation, err := resolveInboundInstallation(ctx, tx, event.Provider, event.APIBaseURL, event.InstallationExternalID, event.Repository)
	if errors.Is(err, ErrUnknownInstallation) {
		return finishGitLabAuthorAdmission(ctx, tx, target, "skipped", "installation_unavailable")
	}
	if err != nil {
		return err
	}
	if installation.ID != target.Job.InstallationID || installation.TenantID != target.Job.TenantID || installation.CredentialRef != target.Job.CredentialRef {
		return finishGitLabAuthorAdmission(ctx, tx, target, "skipped", "installation_changed")
	}
	if err := lockReviewAdmissionIdentity(ctx, tx, installation.ID, event.Repository, event.ReviewNumber); err != nil {
		return err
	}
	// A newer already-admitted head must not be superseded by a delayed author
	// lookup for an older webhook, even if the provider changed between the
	// read-only lookup and this transaction.
	var newer bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM review_requests request
			JOIN review_runs run ON run.id=request.current_run_id
			WHERE request.tenant_id=$1 AND request.installation_id=$2
			  AND request.repository=$3 AND request.review_number=$4
			  AND run.created_at>$5 AND lower(run.head_sha)<>lower($6)
		)`, installation.TenantID, installation.ID, event.Repository, event.ReviewNumber,
		target.ReceivedAt, event.HeadSHA).Scan(&newer); err != nil {
		return fmt.Errorf("check newer review before GitLab author admission: %w", err)
	}
	if newer {
		return finishGitLabAuthorAdmission(ctx, tx, target, "skipped", "newer_review_head")
	}
	job, _, err := admitVerifiedReview(ctx, tx, installation, target.DeliveryID, event)
	if err != nil {
		return err
	}
	state := "admitted"
	if job.ID == uuid.Nil {
		state = "skipped"
	}
	return finishGitLabAuthorAdmission(ctx, tx, target, state, "")
}

func (s *PostgresStore) SkipGitLabAuthorAdmission(ctx context.Context, target GitLabAuthorAdmissionTarget, reason string) error {
	if !validGitLabAuthorTarget(target) || len(reason) > 80 || strings.TrimSpace(reason) == "" {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockGitLabAuthorAdmission(ctx, tx, target); err != nil {
		return err
	}
	return finishGitLabAuthorAdmission(ctx, tx, target, "skipped", reason)
}

func (s *PostgresStore) RetryGitLabAuthorAdmission(ctx context.Context, target GitLabAuthorAdmissionTarget, retryAt time.Time, reason string) error {
	if !validGitLabAuthorTarget(target) || retryAt.IsZero() || len(reason) > 80 || strings.TrimSpace(reason) == "" {
		return ErrConflict
	}
	state := "queued"
	if target.Attempt >= 5 {
		state = "failed"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin GitLab author admission retry: %w", err)
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE gitlab_author_admissions SET state=$5,error_code=$6,
			available_at=$7,worker_id=NULL,locked_until=NULL,updated_at=now()
		WHERE delivery_id=$1 AND state='running' AND worker_id=$2
		  AND attempt=$3 AND locked_until>=now() AND author_id=$4`,
		target.DeliveryID, target.WorkerID, target.Attempt, target.ExpectedAuthorID,
		state, reason, retryAt.UTC())
	if err != nil {
		return fmt.Errorf("retry GitLab author admission: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	if state == "failed" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
			VALUES ($1,'provider:gitlab','review.gitlab_author_admission_failed',$2,
			        jsonb_build_object('reason',$3::text,'repository',$4::text,'review_number',$5::integer,'attempt',$6::integer))`,
			target.Job.TenantID, target.Job.Repository+"#"+fmt.Sprint(target.Job.ReviewNumber),
			reason, target.Job.Repository, target.Job.ReviewNumber, target.Attempt); err != nil {
			return fmt.Errorf("audit failed GitLab author admission: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit GitLab author admission retry: %w", err)
	}
	return nil
}

func validGitLabAuthorTarget(target GitLabAuthorAdmissionTarget) bool {
	return target.DeliveryID != uuid.Nil && target.WorkerID != "" && target.Attempt > 0 && target.ExpectedAuthorID != ""
}

func lockGitLabAuthorAdmission(ctx context.Context, tx pgx.Tx, target GitLabAuthorAdmissionTarget) error {
	var valid bool
	if err := tx.QueryRow(ctx, `
		SELECT state='running' AND worker_id=$2 AND attempt=$3
		       AND locked_until>=now() AND author_id=$4
		FROM gitlab_author_admissions WHERE delivery_id=$1 FOR UPDATE`,
		target.DeliveryID, target.WorkerID, target.Attempt, target.ExpectedAuthorID).Scan(&valid); err != nil {
		return fmt.Errorf("lock GitLab author admission: %w", err)
	}
	if !valid {
		return ErrJobClaimLost
	}
	return nil
}

func finishGitLabAuthorAdmission(ctx context.Context, tx pgx.Tx, target GitLabAuthorAdmissionTarget, state, reason string) error {
	command, err := tx.Exec(ctx, `
		UPDATE gitlab_author_admissions SET state=$2,error_code=$3,
			worker_id=NULL,locked_until=NULL,updated_at=now()
		WHERE delivery_id=$1`, target.DeliveryID, state, reason)
	if err != nil {
		return fmt.Errorf("finish GitLab author admission: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,'provider:gitlab','review.gitlab_author_admission_finished',$2,
		        jsonb_build_object('state',$3::text,'reason',$4::text,'repository',$5::text,'review_number',$6::integer))`,
		target.Job.TenantID, target.Job.Repository+"#"+fmt.Sprint(target.Job.ReviewNumber),
		state, reason, target.Job.Repository, target.Job.ReviewNumber); err != nil {
		return fmt.Errorf("audit GitLab author admission: %w", err)
	}
	return tx.Commit(ctx)
}
