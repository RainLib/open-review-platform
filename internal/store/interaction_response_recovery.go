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

const (
	// RabbitMQ's quorum delivery limit is five redeliveries. The first
	// delivery plus those redeliveries leave six released inbox claims before
	// the message is dead-lettered; never compete with an active broker retry.
	interactionResponseExhaustedAttempts = 6
	maxManualInteractionResponseRetries  = 3
)

// interactionResponseRecoveryAvailable is a read-only Console hint. The
// mutation repeats every check while holding the run lock; this snapshot can
// never authorize a provider write by itself.
func (s *PostgresStore) interactionResponseRecoveryAvailable(ctx context.Context, tenantID, runID uuid.UUID) (bool, error) {
	var available bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM review_interactions interaction
			JOIN review_runs run ON run.id=interaction.result_run_id
			JOIN review_requests request ON request.id=run.request_id
			JOIN provider_installations installation ON installation.id=request.installation_id
			JOIN LATERAL (
				SELECT response.id,response.payload,response.published_at
				FROM outbox_messages response
				WHERE response.aggregate_id=interaction.id
				  AND response.topic='review.interaction.response'
				  AND response.payload->>'release_run_id'=run.id::text
				ORDER BY response.created_at DESC,response.id DESC LIMIT 1
			) latest ON true
			JOIN inbox_messages inbox ON inbox.message_id=latest.id
			  AND inbox.consumer='interaction-responder-v1'
			WHERE run.id=$1 AND request.tenant_id=$2
			  AND interaction.tenant_id=$2 AND interaction.result='accepted'
			  AND interaction.command IN ('review','retry')
			  AND interaction.id=(
			    SELECT selected.id FROM review_interactions selected
			    WHERE selected.result_run_id=run.id AND selected.tenant_id=$2
			      AND selected.result='accepted' AND selected.command IN ('review','retry')
			    ORDER BY selected.created_at DESC,selected.id DESC LIMIT 1)
			  AND run.state='acknowledged' AND run.trigger_kind IN ('comment','retry')
			  AND installation.active AND installation.verification_state IN ('legacy','verified')
			  AND btrim(installation.credential_ref)<>''
			  AND latest.published_at IS NOT NULL
			  AND inbox.state='released' AND inbox.attempt >= $3
			  AND (btrim(COALESCE(latest.payload->>'body',''))<>''
			       OR (latest.payload->>'reaction_only'='true'
			           AND COALESCE(latest.payload->>'body','')=''
			           AND latest.payload->>'reaction'='eyes'))
			  AND btrim(COALESCE(latest.payload->>'comment_external_id',''))<>''
			  AND latest.payload->>'marker'='open-review-platform:interaction:' || interaction.id::text
			  AND latest.payload->>'provider'=installation.provider
			  AND latest.payload->>'api_base_url'=installation.api_base_url
			  AND latest.payload->>'repository'=request.repository
			  AND latest.payload->>'review_number'=request.review_number::text
			  AND COALESCE(latest.payload->>'reaction','') IN ('','eyes','confused')
			  AND (SELECT count(*) FROM outbox_messages retry
			       WHERE retry.aggregate_id=interaction.id
			         AND retry.dedupe_key LIKE 'interaction:' || interaction.id::text || ':ack-retry:%') < $4
		)`, runID, tenantID, interactionResponseExhaustedAttempts, maxManualInteractionResponseRetries).Scan(&available)
	if err != nil {
		return false, fmt.Errorf("inspect interaction response recovery: %w", err)
	}
	return available, nil
}

// RetryInteractionResponse republishes the original source reaction (or a
// legacy marker-keyed reply) only after its latest durable responder attempt
// exhausted broker delivery. The run remains acknowledged until the provider
// accepts it; this path cannot create another review run.
func (s *PostgresStore) RetryInteractionResponse(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, input domain.RunRetryInput) (domain.InteractionResponseRetryResult, error) {
	input, valid := domain.NormalizeRunRetryInput(input)
	if !valid || runID == uuid.Nil {
		return domain.InteractionResponseRetryResult{}, ErrInvalidRunRetry
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.InteractionResponseRetryResult{}, fmt.Errorf("begin interaction response retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.InteractionResponseRetryResult{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.InteractionResponseRetryResult{}, ErrForbidden
	}
	setupComplete, err := workspaceSetupAllowsReview(ctx, tx, tenantID)
	if err != nil {
		return domain.InteractionResponseRetryResult{}, err
	}
	if !setupComplete {
		return domain.InteractionResponseRetryResult{}, ErrWorkspaceSetupIncomplete
	}

	var state domain.RunState
	var revision, reviewNumber int
	var repository string
	var installation domain.Installation
	err = tx.QueryRow(ctx, `
		SELECT run.state,run.revision,request.repository,request.review_number,
		       installation.id,installation.tenant_id,installation.provider,
		       installation.api_base_url,installation.external_id,
		       installation.credential_ref,installation.active,
		       installation.verification_state
		FROM review_runs run
		JOIN review_requests request ON request.id=run.request_id
		JOIN provider_installations installation ON installation.id=request.installation_id
		WHERE run.id=$1 AND request.tenant_id=$2
		FOR UPDATE OF run`, runID, tenantID).Scan(
		&state, &revision, &repository, &reviewNumber,
		&installation.ID, &installation.TenantID, &installation.Provider,
		&installation.APIBaseURL, &installation.ExternalID,
		&installation.CredentialRef, &installation.Active,
		&installation.VerificationState)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InteractionResponseRetryResult{}, ErrNotFound
	}
	if err != nil {
		return domain.InteractionResponseRetryResult{}, fmt.Errorf("load acknowledged review: %w", err)
	}
	var interactionID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM review_interactions
		WHERE result_run_id=$1 AND tenant_id=$2 AND result='accepted'
		  AND command IN ('review','retry')
		ORDER BY created_at DESC,id DESC LIMIT 1`, runID, tenantID).Scan(&interactionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InteractionResponseRetryResult{}, ErrConflict
	}
	if err != nil {
		return domain.InteractionResponseRetryResult{}, fmt.Errorf("load review interaction: %w", err)
	}
	keyPrefix := "interaction:" + interactionID.String() + ":ack-retry:"
	key := keyPrefix + input.IdempotencyKey
	var replayAttempt int
	err = tx.QueryRow(ctx, `
		SELECT COALESCE((payload->>'recovery_attempt')::int,0)
		FROM outbox_messages WHERE aggregate_id=$1 AND dedupe_key=$2`, interactionID, key).Scan(&replayAttempt)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.InteractionResponseRetryResult{}, fmt.Errorf("commit replayed interaction response retry: %w", err)
		}
		return domain.InteractionResponseRetryResult{RunID: runID, Attempt: replayAttempt, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.InteractionResponseRetryResult{}, fmt.Errorf("find interaction response retry: %w", err)
	}
	if revision != input.ExpectedRevision {
		return domain.InteractionResponseRetryResult{}, ErrRevisionConflict
	}
	if state != domain.RunAcknowledged || !installation.Active || !installation.VerificationState.EligibleForReview() || strings.TrimSpace(installation.CredentialRef) == "" {
		return domain.InteractionResponseRetryResult{}, ErrConflict
	}
	var manualAttempts int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_messages WHERE aggregate_id=$1 AND dedupe_key LIKE $2`, interactionID, keyPrefix+"%").Scan(&manualAttempts); err != nil {
		return domain.InteractionResponseRetryResult{}, fmt.Errorf("count interaction response retries: %w", err)
	}
	if manualAttempts >= maxManualInteractionResponseRetries {
		return domain.InteractionResponseRetryResult{}, ErrConflict
	}

	var sourceOutboxID uuid.UUID
	var body, commentExternalID, reaction, marker, sourceProvider, sourceAPIBaseURL, sourceRepository string
	var reactionOnly bool
	var sourceReviewNumber int
	var publishedAt *time.Time
	var inboxState *string
	var inboxAttempts *int
	err = tx.QueryRow(ctx, `
		SELECT outbox.id,COALESCE(outbox.payload->>'body',''),
		       COALESCE(outbox.payload->>'reaction_only','false')='true',
		       COALESCE(outbox.payload->>'comment_external_id',''),
		       COALESCE(outbox.payload->>'reaction',''),
		       COALESCE(outbox.payload->>'marker',''),
		       COALESCE(outbox.payload->>'provider',''),
		       COALESCE(outbox.payload->>'api_base_url',''),
		       COALESCE(outbox.payload->>'repository',''),
		       CASE WHEN outbox.payload->>'review_number' ~ '^[0-9]{1,9}$'
		         THEN (outbox.payload->>'review_number')::int ELSE 0 END,
		       outbox.published_at,inbox.state,inbox.attempt
		FROM outbox_messages outbox
		LEFT JOIN inbox_messages inbox
		  ON inbox.message_id=outbox.id AND inbox.consumer='interaction-responder-v1'
		WHERE outbox.aggregate_id=$1 AND outbox.topic='review.interaction.response'
		  AND outbox.payload->>'release_run_id'=$2
		ORDER BY outbox.created_at DESC,outbox.id DESC LIMIT 1`, interactionID, runID.String()).Scan(
		&sourceOutboxID, &body, &reactionOnly, &commentExternalID, &reaction, &marker,
		&sourceProvider, &sourceAPIBaseURL, &sourceRepository, &sourceReviewNumber,
		&publishedAt, &inboxState, &inboxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InteractionResponseRetryResult{}, ErrConflict
	}
	if err != nil {
		return domain.InteractionResponseRetryResult{}, fmt.Errorf("load interaction response delivery: %w", err)
	}
	if publishedAt == nil || inboxState == nil || *inboxState != "released" || inboxAttempts == nil || *inboxAttempts < interactionResponseExhaustedAttempts {
		return domain.InteractionResponseRetryResult{}, ErrInteractionResponseNotExhausted
	}
	if ((!reactionOnly && strings.TrimSpace(body) == "") || (reactionOnly && (body != "" || reaction != string(domain.InteractionReactionEyes)))) || strings.TrimSpace(commentExternalID) == "" || marker != interactionResponseMarker(interactionID) || sourceProvider != string(installation.Provider) || sourceAPIBaseURL != installation.APIBaseURL || sourceRepository != repository || sourceReviewNumber != reviewNumber || !domain.InteractionReaction(reaction).Valid() {
		return domain.InteractionResponseRetryResult{}, ErrConflict
	}
	event := domain.CommentEvent{Repository: repository, ReviewNumber: reviewNumber, CommentExternalID: commentExternalID}
	if err := queueInteractionResponseWithKeyMode(ctx, tx, installation, event, interactionID, body, domain.InteractionReaction(reaction), &runID, key, reactionOnly); err != nil {
		return domain.InteractionResponseRetryResult{}, err
	}
	attempt := manualAttempts + 1
	if _, err := tx.Exec(ctx, `UPDATE outbox_messages SET payload=payload || jsonb_build_object('recovery_attempt',$2::int) WHERE aggregate_id=$1 AND dedupe_key=$3`, interactionID, attempt, key); err != nil {
		return domain.InteractionResponseRetryResult{}, fmt.Errorf("record interaction response attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata)
		VALUES($1,$2,'review.interaction_response_retry',$3,
		       jsonb_build_object('interaction_id',$4::text,'source_outbox_id',$5::text,'attempt',$6::int))`,
		tenantID, actor, runID.String(), interactionID.String(), sourceOutboxID.String(), attempt); err != nil {
		return domain.InteractionResponseRetryResult{}, fmt.Errorf("audit interaction response retry: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InteractionResponseRetryResult{}, fmt.Errorf("commit interaction response retry: %w", err)
	}
	return domain.InteractionResponseRetryResult{RunID: runID, Attempt: attempt}, nil
}
