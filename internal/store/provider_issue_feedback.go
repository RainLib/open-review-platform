package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ClaimProviderIssueFeedbackPoll leases one completed GitHub Issue analysis.
// A bounded 30-day window prevents old comments from becoming permanent
// background traffic while edits naturally reopen the window.
func (s *PostgresStore) ClaimProviderIssueFeedbackPoll(ctx context.Context, workerID string, lease time.Duration) (*domain.ProviderIssueFeedbackPollTarget, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, fmt.Errorf("provider Issue feedback poll worker id is required")
	}
	if lease <= 0 {
		lease = time.Minute
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO provider_issue_feedback_polls (job_id)
		SELECT job.id
		FROM provider_issue_analysis_jobs job
		JOIN provider_installations installation ON installation.id=job.installation_id
		WHERE job.provider='github' AND job.state='completed'
		  AND installation.active=TRUE AND installation.verification_state IN ('legacy','verified')
		  AND COALESCE((job.issue_triage_config->>'reaction_feedback')::boolean,FALSE)=TRUE
		  AND job.completed_at >= now()-interval '30 days'
		ON CONFLICT (job_id) DO NOTHING`); err != nil {
		return nil, fmt.Errorf("seed provider Issue feedback polls: %w", err)
	}
	var target domain.ProviderIssueFeedbackPollTarget
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT poll.job_id
			FROM provider_issue_feedback_polls poll
			JOIN provider_issue_analysis_jobs job ON job.id=poll.job_id
			JOIN provider_installations installation ON installation.id=job.installation_id
			WHERE job.provider='github' AND job.state='completed'
			  AND job.completed_at >= now()-interval '30 days'
			  AND installation.active=TRUE AND installation.verification_state IN ('legacy','verified')
			  AND COALESCE((job.issue_triage_config->>'reaction_feedback')::boolean,FALSE)=TRUE
			  AND ((poll.state IN ('queued','completed') AND poll.available_at <= now())
			       OR (poll.state='running' AND poll.locked_until < now()))
			ORDER BY poll.available_at,poll.job_id
			FOR UPDATE OF poll SKIP LOCKED
			LIMIT 1
		)
		UPDATE provider_issue_feedback_polls poll SET
			state='running',attempt=poll.attempt+1,worker_id=$1,
			locked_until=now()+$2::interval,updated_at=now()
		FROM candidate,provider_issue_analysis_jobs job,provider_installations installation
		WHERE poll.job_id=candidate.job_id AND job.id=candidate.job_id
		  AND installation.id=job.installation_id
		RETURNING job.id,job.tenant_id,job.installation_id,installation.external_id,
		          installation.credential_ref,job.provider,job.api_base_url,job.repository,
		          job.issue_number,job.revision,job.stable_marker,poll.worker_id,poll.attempt`,
		workerID, lease.String()).Scan(
		&target.Job.ID, &target.Job.TenantID, &target.Job.InstallationID,
		&target.Job.InstallationExternalID, &target.Job.CredentialRef, &target.Job.Provider,
		&target.Job.APIBaseURL, &target.Job.Repository, &target.Job.IssueNumber,
		&target.Job.Revision, &target.Job.StableMarker, &target.WorkerID, &target.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoProviderIssueFeedbackPoll
	}
	if err != nil {
		return nil, fmt.Errorf("claim provider Issue feedback poll: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &target, nil
}

type activeProviderIssueFeedback struct {
	ActorExternalID string
	Kind            string
}

// CompleteProviderIssueFeedbackPoll reconciles one complete provider snapshot.
// A missing previously active reaction is a retraction. Audit rows are emitted
// only for a semantic change, so recurring no-op polls remain quiet.
func (s *PostgresStore) CompleteProviderIssueFeedbackPoll(ctx context.Context, target domain.ProviderIssueFeedbackPollTarget, reactions []domain.ProviderIssueFeedbackReaction, observedAt, nextPollAt time.Time) error {
	if target.Job.ID == uuid.Nil || strings.TrimSpace(target.WorkerID) == "" || observedAt.IsZero() || !nextPollAt.After(observedAt) {
		return fmt.Errorf("provider Issue feedback poll result is invalid")
	}
	current := make(map[string]domain.ProviderIssueFeedbackReaction, len(reactions))
	for _, reaction := range reactions {
		if !reaction.Valid() {
			return fmt.Errorf("provider Issue feedback reaction is invalid")
		}
		if _, duplicate := current[reaction.ExternalID]; duplicate {
			return fmt.Errorf("provider Issue feedback reaction snapshot contains duplicate id")
		}
		current[reaction.ExternalID] = reaction
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var held bool
	err = tx.QueryRow(ctx, `
		SELECT TRUE FROM provider_issue_feedback_polls
		WHERE job_id=$1 AND state='running' AND worker_id=$2 AND locked_until >= now()
		FOR UPDATE`, target.Job.ID, target.WorkerID).Scan(&held)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrJobClaimLost
	}
	if err != nil {
		return fmt.Errorf("lock provider Issue feedback poll: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT reaction_external_id,actor_external_id,kind
		FROM provider_issue_analysis_feedback
		WHERE job_id=$1 AND provider='github' AND retracted_at IS NULL
		FOR UPDATE`, target.Job.ID)
	if err != nil {
		return fmt.Errorf("load active provider Issue feedback: %w", err)
	}
	existing := make(map[string]activeProviderIssueFeedback)
	for rows.Next() {
		var externalID string
		var feedback activeProviderIssueFeedback
		if err := rows.Scan(&externalID, &feedback.ActorExternalID, &feedback.Kind); err != nil {
			rows.Close()
			return fmt.Errorf("scan active provider Issue feedback: %w", err)
		}
		existing[externalID] = feedback
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate active provider Issue feedback: %w", err)
	}
	rows.Close()
	for externalID, reaction := range current {
		previous, unchanged := existing[externalID]
		unchanged = unchanged && previous.ActorExternalID == reaction.ActorExternalID && previous.Kind == reaction.Kind
		if unchanged {
			delete(existing, externalID)
			continue
		}
		command, err := tx.Exec(ctx, `
			INSERT INTO provider_issue_analysis_feedback (
				tenant_id,job_id,provider,delivery_id,reaction_external_id,actor_external_id,kind
			) VALUES ($1,$2,'github',$3,$4,$5,$6)
			ON CONFLICT (provider,reaction_external_id) DO UPDATE SET
				delivery_id=EXCLUDED.delivery_id,actor_external_id=EXCLUDED.actor_external_id,
				kind=EXCLUDED.kind,retracted_at=NULL
			WHERE provider_issue_analysis_feedback.job_id=EXCLUDED.job_id`,
			target.Job.TenantID, target.Job.ID, "poll:"+target.Job.ID.String()+":"+externalID,
			externalID, reaction.ActorExternalID, reaction.Kind)
		if err != nil {
			return fmt.Errorf("upsert polled provider Issue feedback: %w", err)
		}
		if command.RowsAffected() != 1 {
			return fmt.Errorf("provider Issue feedback reaction belongs to another analysis")
		}
		if err := auditProviderIssueFeedback(ctx, tx, target.Job.TenantID, target.Job.ID, reaction.ActorExternalID, "created", reaction.Kind); err != nil {
			return err
		}
		delete(existing, externalID)
	}
	for externalID, feedback := range existing {
		command, err := tx.Exec(ctx, `
			UPDATE provider_issue_analysis_feedback SET retracted_at=$3
			WHERE job_id=$1 AND provider='github' AND reaction_external_id=$2 AND retracted_at IS NULL`,
			target.Job.ID, externalID, observedAt.UTC())
		if err != nil {
			return fmt.Errorf("retract missing provider Issue feedback: %w", err)
		}
		if command.RowsAffected() == 1 {
			if err := auditProviderIssueFeedback(ctx, tx, target.Job.TenantID, target.Job.ID, feedback.ActorExternalID, "deleted", feedback.Kind); err != nil {
				return err
			}
		}
	}
	command, err := tx.Exec(ctx, `
		UPDATE provider_issue_feedback_polls SET
			state='completed',observed_at=$3,reaction_count=$4,last_error='',
			available_at=$5,worker_id=NULL,locked_until=NULL,updated_at=now()
		WHERE job_id=$1 AND state='running' AND worker_id=$2`,
		target.Job.ID, target.WorkerID, observedAt.UTC(), len(current), nextPollAt.UTC())
	if err != nil {
		return fmt.Errorf("complete provider Issue feedback poll: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit provider Issue feedback poll: %w", err)
	}
	return nil
}

func (s *PostgresStore) FailProviderIssueFeedbackPoll(ctx context.Context, target domain.ProviderIssueFeedbackPollTarget, message string, retryAt time.Time) error {
	message = normalizeReviewRequestMetadata(message, 1000)
	if target.Job.ID == uuid.Nil || strings.TrimSpace(target.WorkerID) == "" || retryAt.IsZero() {
		return fmt.Errorf("provider Issue feedback poll failure is invalid")
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE provider_issue_feedback_polls SET
			state='completed',last_error=$3,available_at=$4,
			worker_id=NULL,locked_until=NULL,updated_at=now()
		WHERE job_id=$1 AND state='running' AND worker_id=$2`,
		target.Job.ID, target.WorkerID, message, retryAt.UTC())
	if err != nil {
		return fmt.Errorf("fail provider Issue feedback poll: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	return nil
}

func auditProviderIssueFeedback(ctx context.Context, tx pgx.Tx, tenantID, jobID uuid.UUID, actorExternalID, action, kind string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,$3,$4,jsonb_build_object('kind',$5::text,'provider','github','source','poll'))`,
		tenantID, "provider:"+actorExternalID, "provider_issue.feedback_"+action, jobID.String(), kind)
	if err != nil {
		return fmt.Errorf("audit polled provider Issue feedback: %w", err)
	}
	return nil
}
