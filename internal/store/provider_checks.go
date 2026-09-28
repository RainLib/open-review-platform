package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNoProviderCheckProbe = errors.New("no provider check observation is due")

// ClaimProviderCheckProbe seeds only completed, exact-SHA reviews and leases
// one observation. The provider read occurs outside this transaction.
func (s *PostgresStore) ClaimProviderCheckProbe(ctx context.Context, workerID string, lease time.Duration) (*domain.ProviderCheckTarget, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, fmt.Errorf("provider check worker id is required")
	}
	if lease <= 0 {
		lease = time.Minute
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_provider_check_observations (run_id,head_sha)
		SELECT run.id,lower(run.head_sha)
		FROM review_runs run
		JOIN review_jobs job ON job.id=run.legacy_job_id
		JOIN provider_installations installation ON installation.id=job.installation_id
		WHERE run.state='completed' AND run.created_at > now()-interval '30 days'
		  AND run.head_sha ~ '^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$'
		  AND lower(run.head_sha)=lower(job.head_sha)
		  AND installation.active=TRUE AND installation.verification_state='verified'
		ON CONFLICT (run_id) DO NOTHING`); err != nil {
		return nil, fmt.Errorf("seed provider check observations: %w", err)
	}
	var target domain.ProviderCheckTarget
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT observation.run_id
			FROM review_provider_check_observations observation
			JOIN review_runs run ON run.id=observation.run_id
			JOIN review_jobs job ON job.id=run.legacy_job_id
			JOIN provider_installations installation ON installation.id=job.installation_id
			WHERE run.state='completed' AND installation.active=TRUE
			  AND installation.verification_state='verified'
			  AND lower(run.head_sha)=observation.head_sha
			  AND lower(job.head_sha)=observation.head_sha
			  AND ((observation.state='queued' AND observation.available_at<=now())
			       OR (observation.state='observed' AND run.created_at>now()-interval '24 hours' AND observation.available_at<=now())
			       OR (observation.state='running' AND observation.locked_until<now()))
			ORDER BY observation.available_at,observation.run_id
			FOR UPDATE OF observation SKIP LOCKED LIMIT 1
		)
		UPDATE review_provider_check_observations observation SET
			state='running',attempt=observation.attempt+1,worker_id=$1,
			locked_until=now()+$2::interval,updated_at=now()
		FROM candidate,review_runs run,review_jobs job,provider_installations installation
		WHERE observation.run_id=candidate.run_id AND run.id=candidate.run_id
		  AND job.id=run.legacy_job_id AND installation.id=job.installation_id
		RETURNING observation.run_id,observation.attempt,observation.worker_id,
		          job.id,job.tenant_id,job.installation_id,installation.external_id,
		          installation.credential_ref,job.provider,job.api_base_url,
		          job.repository,observation.head_sha`, workerID, lease.String()).Scan(
		&target.RunID, &target.Attempt, &target.WorkerID,
		&target.Job.ID, &target.Job.TenantID, &target.Job.InstallationID,
		&target.Job.InstallationExternalID, &target.Job.CredentialRef,
		&target.Job.Provider, &target.Job.APIBaseURL, &target.Job.Repository,
		&target.Job.HeadSHA)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoProviderCheckProbe
	}
	if err != nil {
		return nil, fmt.Errorf("claim provider check observation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit provider check claim: %w", err)
	}
	return &target, nil
}

func (s *PostgresStore) CompleteProviderCheckProbe(ctx context.Context, target domain.ProviderCheckTarget, observation domain.ProviderCheckObservation, nextAt time.Time) error {
	if target.RunID == uuid.Nil || target.WorkerID == "" || target.Attempt < 1 ||
		observation.State != "observed" || observation.ObservedAt == nil ||
		!strings.EqualFold(observation.HeadSHA, target.Job.HeadSHA) ||
		observation.Provider != target.Job.Provider || observation.Repository != target.Job.Repository ||
		!nextAt.After(*observation.ObservedAt) || len(observation.Checks) > 200 {
		return fmt.Errorf("provider check observation is invalid")
	}
	checks, err := json.Marshal(observation.Checks)
	if err != nil {
		return fmt.Errorf("encode provider checks: %w", err)
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE review_provider_check_observations observation SET
			state='observed',checks=$5,truncated=$6,observed_at=$7,error_code='',
			attempt=0,available_at=$8,worker_id=NULL,locked_until=NULL,updated_at=now()
		WHERE observation.run_id=$1 AND observation.head_sha=$2
		  AND observation.state='running' AND observation.worker_id=$3
		  AND observation.attempt=$4 AND observation.locked_until>=now()
		  AND EXISTS (
		    SELECT 1 FROM review_runs run
		    JOIN review_jobs job ON job.id=run.legacy_job_id
		    JOIN provider_installations installation ON installation.id=job.installation_id
		    WHERE run.id=observation.run_id AND run.state='completed'
		      AND lower(run.head_sha)=observation.head_sha
		      AND lower(job.head_sha)=observation.head_sha
		      AND installation.active=TRUE AND installation.verification_state='verified'
		  )`, target.RunID, strings.ToLower(target.Job.HeadSHA), target.WorkerID,
		target.Attempt, checks, observation.Truncated, observation.ObservedAt.UTC(), nextAt.UTC())
	if err != nil {
		return fmt.Errorf("complete provider check observation: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	return nil
}

func (s *PostgresStore) FailProviderCheckProbe(ctx context.Context, target domain.ProviderCheckTarget, errorCode string, retryAt time.Time) error {
	if target.RunID == uuid.Nil || target.WorkerID == "" || target.Attempt < 1 || retryAt.IsZero() {
		return fmt.Errorf("provider check failure is invalid")
	}
	if len(errorCode) > 80 || strings.TrimSpace(errorCode) == "" {
		return fmt.Errorf("provider check failure code is invalid")
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE review_provider_check_observations SET
			state=CASE WHEN attempt<5 THEN 'queued' ELSE 'failed' END,
			error_code=$5,available_at=$6,worker_id=NULL,locked_until=NULL,updated_at=now()
		WHERE run_id=$1 AND head_sha=$2 AND state='running'
		  AND worker_id=$3 AND attempt=$4 AND locked_until>=now()`,
		target.RunID, strings.ToLower(target.Job.HeadSHA), target.WorkerID,
		target.Attempt, errorCode, retryAt.UTC())
	if err != nil {
		return fmt.Errorf("fail provider check observation: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	return nil
}

func (s *PostgresStore) loadProviderCheckObservation(ctx context.Context, run domain.ReviewRunSummary) (*domain.ProviderCheckObservation, error) {
	var observation domain.ProviderCheckObservation
	var checks []byte
	err := s.pool.QueryRow(ctx, `
		SELECT request.provider,request.repository,observation.head_sha,
		       observation.state,observation.observed_at,observation.checks,
		       observation.truncated,observation.error_code
		FROM review_provider_check_observations observation
		JOIN review_runs run ON run.id=observation.run_id
		JOIN review_requests request ON request.id=run.request_id
		WHERE observation.run_id=$1 AND lower(run.head_sha)=observation.head_sha`, run.ID).Scan(
		&observation.Provider, &observation.Repository, &observation.HeadSHA,
		&observation.State, &observation.ObservedAt, &checks,
		&observation.Truncated, &observation.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load provider check observation: %w", err)
	}
	if err := json.Unmarshal(checks, &observation.Checks); err != nil {
		return nil, fmt.Errorf("decode provider checks: %w", err)
	}
	observation.Stale = observation.ObservedAt == nil || time.Since(*observation.ObservedAt) > 15*time.Minute
	return &observation, nil
}
