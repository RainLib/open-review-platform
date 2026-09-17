package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	outboxLease = 30 * time.Second
	inboxLease  = 10 * time.Minute
)

var _ WorkflowStore = (*PostgresStore)(nil)

// createWorkflowRun runs inside the delivery transaction. It means an accepted
// provider delivery can never leave a run without its first durable event and
// outbox notification, even when the broker is unavailable.
func createWorkflowRun(ctx context.Context, tx pgx.Tx, installation domain.Installation, job domain.ReviewJob, event domain.InboundEvent) error {
	var requestID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO review_requests (tenant_id, installation_id, provider, api_base_url, repository, review_number)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, installation_id, repository, review_number) DO UPDATE
		SET updated_at = now()
		RETURNING id`, installation.TenantID, installation.ID, event.Provider, event.APIBaseURL, event.Repository, event.ReviewNumber).Scan(&requestID)
	if err != nil {
		return fmt.Errorf("upsert review request: %w", err)
	}

	// A newer head replaces any active execution for the same provider review.
	// The old run is retained for audit, but it can no longer publish findings.
	rows, err := tx.Query(ctx, `
		UPDATE review_runs
		SET state = 'superseded', revision = revision + 1, finished_at = now()
		WHERE request_id = $1
		  AND state IN ('acknowledged', 'admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
		  AND head_sha <> $2
		RETURNING id, revision`, requestID, event.HeadSHA)
	if err != nil {
		return fmt.Errorf("supersede stale runs: %w", err)
	}
	staleRuns := make([]struct {
		id       uuid.UUID
		revision int
	}, 0)
	for rows.Next() {
		var stale struct {
			id       uuid.UUID
			revision int
		}
		if err := rows.Scan(&stale.id, &stale.revision); err != nil {
			rows.Close()
			return fmt.Errorf("scan superseded run: %w", err)
		}
		staleRuns = append(staleRuns, stale)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate superseded runs: %w", err)
	}
	rows.Close()

	var currentRunID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM review_runs
		WHERE request_id = $1
		  AND head_sha = $2
		  AND state IN ('acknowledged', 'admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
		LIMIT 1`, requestID, event.HeadSHA).Scan(&currentRunID)
	if err == nil {
		// GitHub may emit more than one accepted delivery for the same head. The
		// delivery ledger retains it, but we must not create a second active run.
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("find active run for head: %w", err)
	}

	var runID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO review_runs (request_id, legacy_job_id, state, trigger_kind, head_sha, base_sha)
		VALUES ($1, $2, 'acknowledged', 'pull_request', $3, $4)
		RETURNING id`, requestID, job.ID, event.HeadSHA, event.BaseSHA).Scan(&runID)
	if err != nil {
		return fmt.Errorf("create review run: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE review_requests SET current_run_id = $2, updated_at = now() WHERE id = $1`, requestID, runID); err != nil {
		return fmt.Errorf("set review request current run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_run_events (run_id, revision, event_type, actor_kind, payload)
		VALUES ($1, 1, 'run.acknowledged', 'provider', $2::jsonb)`, runID, jsonPayload(map[string]any{
		"provider": string(event.Provider), "delivery_id": event.DeliveryID, "review_number": event.ReviewNumber,
	})); err != nil {
		return fmt.Errorf("record run acknowledgement: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_run_stages (run_id, stage, state)
		SELECT $1, stage, 'pending'
		FROM unnest(ARRAY['ack', 'admit', 'prepare', 'analyze', 'normalize', 'publish']) AS stage`, runID); err != nil {
		return fmt.Errorf("initialize run stages: %w", err)
	}
	if err := insertOutbox(ctx, tx, runID, "review.run.acknowledged", "run:"+runID.String()+":1:acknowledged", map[string]any{
		"run_id": runID.String(), "revision": 1,
	}); err != nil {
		return err
	}
	for _, stale := range staleRuns {
		if _, err := tx.Exec(ctx, `
			INSERT INTO review_run_events (run_id, revision, event_type, actor_kind, payload)
			VALUES ($1, $2, 'run.superseded', 'provider', $3::jsonb)`, stale.id, stale.revision, jsonPayload(map[string]any{
			"replacement_run_id": runID.String(), "replacement_head_sha": event.HeadSHA,
		})); err != nil {
			return fmt.Errorf("record superseded run: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE review_runs SET superseded_by = $2 WHERE id = $1`, stale.id, runID); err != nil {
			return fmt.Errorf("link superseded run: %w", err)
		}
		if err := insertOutbox(ctx, tx, stale.id, "review.run.superseded", "run:"+stale.id.String()+fmt.Sprintf(":%d:superseded", stale.revision), map[string]any{
			"run_id": stale.id.String(), "revision": stale.revision, "replacement_run_id": runID.String(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func insertOutbox(ctx context.Context, tx pgx.Tx, runID uuid.UUID, topic, dedupeKey string, payload map[string]any) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (aggregate_type, aggregate_id, topic, dedupe_key, payload)
		VALUES ('review_run', $1, $2, $3, $4::jsonb)`, runID, topic, dedupeKey, jsonPayload(payload))
	if err != nil {
		return fmt.Errorf("insert %s outbox message: %w", topic, err)
	}
	return nil
}

func jsonPayload(value map[string]any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("static workflow payload must marshal: %v", err))
	}
	return string(encoded)
}

func (s *PostgresStore) ClaimOutbox(ctx context.Context, relayID string, limit int) ([]domain.OutboxMessage, error) {
	if relayID == "" || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("relay id and an outbox limit from 1 to 100 are required")
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT id
			FROM outbox_messages
			WHERE published_at IS NULL
			  AND available_at <= now()
			  AND (locked_until IS NULL OR locked_until < now())
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE outbox_messages AS outbox
		SET locked_by = $2,
			locked_until = now() + $3::interval,
			publish_attempts = outbox.publish_attempts + 1
		FROM candidates
		WHERE outbox.id = candidates.id
		RETURNING outbox.id, outbox.aggregate_id, outbox.topic, outbox.dedupe_key, outbox.payload, outbox.publish_attempts`, limit, relayID, outboxLease.String())
	if err != nil {
		return nil, fmt.Errorf("claim outbox messages: %w", err)
	}
	defer rows.Close()
	messages := make([]domain.OutboxMessage, 0)
	for rows.Next() {
		message, err := scanOutbox(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox messages: %w", err)
	}
	return messages, nil
}

func (s *PostgresStore) MarkOutboxPublished(ctx context.Context, messageID uuid.UUID, relayID string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET published_at = now(), locked_by = NULL, locked_until = NULL, last_error = NULL
		WHERE id = $1 AND locked_by = $2 AND published_at IS NULL`, messageID, relayID)
	if err != nil {
		return fmt.Errorf("mark outbox message published: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	return nil
}

func (s *PostgresStore) ReleaseOutbox(ctx context.Context, messageID uuid.UUID, relayID, reason string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET locked_by = NULL,
			locked_until = NULL,
			last_error = $3,
			available_at = now() + (LEAST(900, 5 * power(2, publish_attempts)) * interval '1 second')
		WHERE id = $1 AND locked_by = $2 AND published_at IS NULL`, messageID, relayID, reason)
	if err != nil {
		return fmt.Errorf("release outbox message: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	return nil
}

func (s *PostgresStore) ClaimInbox(ctx context.Context, consumer string, messageID uuid.UUID) (uuid.UUID, bool, error) {
	if consumer == "" || messageID == uuid.Nil {
		return uuid.Nil, false, fmt.Errorf("consumer and message id are required")
	}
	claimToken := uuid.New()
	var persistedToken uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO inbox_messages (consumer, message_id, state, claim_token, locked_until, attempt)
		VALUES ($1, $2, 'claimed', $3, now() + $4::interval, 1)
		ON CONFLICT (consumer, message_id) DO UPDATE
		SET state = 'claimed',
			claim_token = EXCLUDED.claim_token,
			locked_until = EXCLUDED.locked_until,
			attempt = inbox_messages.attempt + 1,
			updated_at = now(),
			last_error = NULL
		WHERE inbox_messages.state = 'released'
		   OR (inbox_messages.state = 'claimed' AND inbox_messages.locked_until < now())
		RETURNING claim_token`, consumer, messageID, claimToken, inboxLease.String()).Scan(&persistedToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("claim inbox message: %w", err)
	}
	return persistedToken, true, nil
}

func (s *PostgresStore) CompleteInbox(ctx context.Context, consumer string, messageID, claimToken uuid.UUID) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE inbox_messages
		SET state = 'completed', completed_at = now(), locked_until = NULL, updated_at = now(), last_error = NULL
		WHERE consumer = $1 AND message_id = $2 AND state = 'claimed' AND claim_token = $3`, consumer, messageID, claimToken)
	if err != nil {
		return fmt.Errorf("complete inbox message: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrInboxClaimLost
	}
	return nil
}

func (s *PostgresStore) ReleaseInbox(ctx context.Context, consumer string, messageID, claimToken uuid.UUID, reason string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE inbox_messages
		SET state = 'released', locked_until = NULL, updated_at = now(), last_error = $4
		WHERE consumer = $1 AND message_id = $2 AND state = 'claimed' AND claim_token = $3`, consumer, messageID, claimToken, reason)
	if err != nil {
		return fmt.Errorf("release inbox message: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrInboxClaimLost
	}
	return nil
}

type outboxScanner interface {
	Scan(...any) error
}

func scanOutbox(row outboxScanner) (domain.OutboxMessage, error) {
	var message domain.OutboxMessage
	var payload []byte
	if err := row.Scan(&message.ID, &message.AggregateID, &message.Topic, &message.DedupeKey, &payload, &message.Attempts); err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("scan outbox message: %w", err)
	}
	if err := json.Unmarshal(payload, &message.Payload); err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("decode outbox payload: %w", err)
	}
	return message, nil
}
