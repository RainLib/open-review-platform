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

func (s *PostgresStore) UpsertProviderIdentity(ctx context.Context, actor, tenantSlug string, input domain.ProviderIdentity) (domain.ProviderIdentity, error) {
	if !input.Provider.Valid() || input.ExternalID == "" || input.Subject == "" {
		return domain.ProviderIdentity{}, fmt.Errorf("provider identity is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderIdentity{}, fmt.Errorf("begin provider identity update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT t.id FROM tenants t JOIN memberships m ON m.tenant_id = t.id
		WHERE t.slug = $1 AND m.subject = $2 AND m.role IN ('owner', 'admin')`, tenantSlug, actor).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderIdentity{}, ErrForbidden
	}
	if err != nil {
		return domain.ProviderIdentity{}, fmt.Errorf("authorize provider identity update: %w", err)
	}
	input.TenantID = tenantID
	err = tx.QueryRow(ctx, `
		INSERT INTO provider_actor_mappings (tenant_id, provider, external_id, subject)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, provider, external_id) DO UPDATE
		SET subject = EXCLUDED.subject, updated_at = now()
		RETURNING tenant_id, provider, external_id, subject`, input.TenantID, input.Provider, input.ExternalID, input.Subject).
		Scan(&input.TenantID, &input.Provider, &input.ExternalID, &input.Subject)
	if isUniqueViolation(err) {
		return domain.ProviderIdentity{}, ErrConflict
	}
	if err != nil {
		return domain.ProviderIdentity{}, fmt.Errorf("upsert provider identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target) VALUES ($1, $2, 'provider_identity.upserted', $3)`, tenantID, actor, string(input.Provider)+":"+input.ExternalID); err != nil {
		return domain.ProviderIdentity{}, fmt.Errorf("audit provider identity update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ProviderIdentity{}, fmt.Errorf("commit provider identity update: %w", err)
	}
	return input, nil
}

func (s *PostgresStore) ProcessInteraction(ctx context.Context, input domain.InteractionCommand) (domain.InteractionOutcome, error) {
	if input.Event.Provider != domain.ProviderGitHub && input.Event.Provider != domain.ProviderGitLab {
		return domain.InteractionOutcome{}, fmt.Errorf("unsupported interaction provider %q", input.Event.Provider)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("begin interaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var installation domain.Installation
	err = tx.QueryRow(ctx, `
		SELECT id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref, active
		FROM provider_installations
		WHERE provider = $1 AND api_base_url = $2 AND external_id = $3 AND active = TRUE`, input.Event.Provider, input.Event.APIBaseURL, input.Event.InstallationExternalID).
		Scan(&installation.ID, &installation.TenantID, &installation.Provider, &installation.ExternalID, &installation.RepositoryScope, &installation.APIBaseURL, &installation.CredentialRef, &installation.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InteractionOutcome{}, ErrUnknownInstallation
	}
	if err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("resolve interaction installation: %w", err)
	}

	var requestID uuid.UUID
	var currentRunID *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, current_run_id FROM review_requests
		WHERE tenant_id = $1 AND installation_id = $2 AND repository = $3 AND review_number = $4
		FOR UPDATE`, installation.TenantID, installation.ID, input.Event.Repository, input.Event.ReviewNumber).Scan(&requestID, &currentRunID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InteractionOutcome{}, ErrUnknownInstallation
	}
	if err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("resolve interaction request: %w", err)
	}

	var interactionID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO review_interactions (tenant_id, request_id, provider, provider_delivery_id, actor_external_id, command, normalized_input, result)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'ignored')
		ON CONFLICT (provider, provider_delivery_id) DO NOTHING
		RETURNING id`, installation.TenantID, requestID, input.Event.Provider, input.Event.DeliveryID, input.Event.ActorExternalID, input.Command, input.Normalized).Scan(&interactionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InteractionOutcome{Duplicate: true}, nil
	}
	if err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("record interaction: %w", err)
	}

	var role string
	err = tx.QueryRow(ctx, `
		SELECT m.role
		FROM provider_actor_mappings p
		JOIN memberships m ON m.tenant_id = p.tenant_id AND m.subject = p.subject
		WHERE p.tenant_id = $1 AND p.provider = $2 AND p.external_id = $3`, installation.TenantID, input.Event.Provider, input.Event.ActorExternalID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "actor is not mapped to an organization member")
	}
	if err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("authorize interaction actor: %w", err)
	}
	if !commandAllowed(role, input.Command) {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "actor role is not allowed to run this command")
	}

	if input.Command == "invalid" {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "invalid command; use @openreview help")
	}
	if input.Command == "help" || input.Command == "status" {
		var body string
		if input.Command == "help" {
			body = "Available commands: `@openreview review [standard|deep|security]`, `@openreview status`, `@openreview cancel`, and `@openreview retry`."
		} else if currentRunID == nil {
			body = "There is no review run for this pull request yet."
		} else {
			var state domain.RunState
			if err := tx.QueryRow(ctx, `SELECT state FROM review_runs WHERE id = $1`, *currentRunID).Scan(&state); err != nil {
				return domain.InteractionOutcome{}, fmt.Errorf("load review status: %w", err)
			}
			body = fmt.Sprintf("Review run `%s` is currently **%s**.", currentRunID.String(), state)
		}
		if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, currentRunID, body); err != nil {
			return domain.InteractionOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.InteractionOutcome{}, fmt.Errorf("commit read-only interaction: %w", err)
		}
		return domain.InteractionOutcome{Accepted: true, RunID: currentRunID, Reason: "command accepted"}, nil
	}
	if currentRunID == nil {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "no review run is available for this pull request")
	}
	current, err := scanReviewRun(tx.QueryRow(ctx, `
		SELECT id, request_id, legacy_job_id, revision, state, trigger_kind, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, created_at, started_at, finished_at
		FROM review_runs WHERE id = $1 FOR UPDATE`, *currentRunID))
	if err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("load current review run: %w", err)
	}

	if input.Command == "cancel" {
		if current.State.Terminal() || current.CancelRequestedAt != nil {
			return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "run cannot be cancelled")
		}
		current.Revision++
		if _, err := tx.Exec(ctx, `UPDATE review_runs SET cancel_requested_at = now(), revision = $2 WHERE id = $1`, current.ID, current.Revision); err != nil {
			return domain.InteractionOutcome{}, fmt.Errorf("request run cancellation: %w", err)
		}
		if err := appendRunEvent(ctx, tx, current.ID, current.Revision, "run.cancel_requested", "user", "", map[string]any{"interaction_id": interactionID.String()}); err != nil {
			return domain.InteractionOutcome{}, err
		}
		if err := insertOutbox(ctx, tx, current.ID, "review.run.cancel-requested", "run:"+current.ID.String()+fmt.Sprintf(":%d:cancel-requested", current.Revision), map[string]any{"run_id": current.ID.String(), "revision": current.Revision}); err != nil {
			return domain.InteractionOutcome{}, err
		}
		if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, &current.ID, fmt.Sprintf("Cancellation for review run `%s` has been requested.", current.ID)); err != nil {
			return domain.InteractionOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.InteractionOutcome{}, fmt.Errorf("commit cancel interaction: %w", err)
		}
		return domain.InteractionOutcome{Accepted: true, RunID: &current.ID, Reason: "cancellation requested"}, nil
	}

	if input.Command == "review" && !current.State.Terminal() {
		if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, &current.ID, fmt.Sprintf("A review is already running as `%s`; use `@openreview status` for progress.", current.ID)); err != nil {
			return domain.InteractionOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.InteractionOutcome{}, fmt.Errorf("commit active review interaction: %w", err)
		}
		return domain.InteractionOutcome{Accepted: true, RunID: &current.ID, Reason: "review is already active"}, nil
	}
	if input.Command == "retry" && !current.State.Terminal() {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "retry is only available after a terminal run")
	}
	if input.Command != "review" && input.Command != "retry" {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "unsupported command")
	}

	run, err := createCommentRun(ctx, tx, requestID, current, installation, input.Event, input.Command, interactionID)
	if err != nil {
		return domain.InteractionOutcome{}, err
	}
	if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, &run.ID, fmt.Sprintf("Review run `%s` is acknowledged and queued. I will publish the result when it completes.", run.ID)); err != nil {
		return domain.InteractionOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("commit review interaction: %w", err)
	}
	return domain.InteractionOutcome{Accepted: true, RunID: &run.ID, Reason: "review acknowledged"}, nil
}

func commandAllowed(role, command string) bool {
	if command == "help" || command == "status" || command == "invalid" {
		return role == "owner" || role == "admin" || role == "reviewer" || role == "viewer"
	}
	return role == "owner" || role == "admin" || role == "reviewer"
}

func acceptInteraction(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, runID *uuid.UUID, body string) error {
	if _, err := tx.Exec(ctx, `UPDATE review_interactions SET result = 'accepted', result_run_id = $2 WHERE id = $1`, interactionID, runID); err != nil {
		return fmt.Errorf("accept interaction: %w", err)
	}
	return queueInteractionResponse(ctx, tx, installation, event, interactionID, body)
}

func rejectInteraction(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, reason string) (domain.InteractionOutcome, error) {
	if _, err := tx.Exec(ctx, `UPDATE review_interactions SET result = 'rejected' WHERE id = $1`, interactionID); err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("reject interaction: %w", err)
	}
	if err := queueInteractionResponse(ctx, tx, installation, event, interactionID, "Unable to run that command: "+reason); err != nil {
		return domain.InteractionOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("commit rejected interaction: %w", err)
	}
	return domain.InteractionOutcome{Reason: reason}, nil
}

func queueInteractionResponse(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, body string) error {
	payload := map[string]any{
		"provider":                 installation.Provider,
		"api_base_url":             installation.APIBaseURL,
		"installation_external_id": installation.ExternalID,
		"credential_ref":           installation.CredentialRef,
		"repository":               event.Repository,
		"review_number":            event.ReviewNumber,
		"body":                     body,
		"marker":                   "open-review-platform:interaction:" + interactionID.String(),
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (aggregate_type, aggregate_id, topic, dedupe_key, payload)
		VALUES ('review_interaction', $1, 'review.interaction.response', $2, $3::jsonb)`,
		interactionID, "interaction:"+interactionID.String()+":response", jsonPayload(payload))
	if err != nil {
		return fmt.Errorf("queue interaction response: %w", err)
	}
	return nil
}

// createCommentRun deliberately creates a new queueable job.  A command-triggered
// review must not merely create an audit run: without its own review_jobs row the
// runner would never claim it.  The last run's job supplies the immutable clone
// and ref metadata, while the issue_comment delivery makes the retry auditable.
func createCommentRun(ctx context.Context, tx pgx.Tx, requestID uuid.UUID, current domain.ReviewRun, installation domain.Installation, event domain.CommentEvent, triggerKind string, interactionID uuid.UUID) (domain.ReviewRun, error) {
	if current.LegacyJobID == nil {
		return domain.ReviewRun{}, fmt.Errorf("current review run has no execution job")
	}
	var deliveryID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (provider, delivery_id, event_name, payload)
		VALUES ($1, $2, 'issue_comment', $3::jsonb)
		ON CONFLICT (provider, delivery_id) DO UPDATE
		SET payload = webhook_deliveries.payload
		RETURNING id`, event.Provider, event.DeliveryID, jsonPayload(map[string]any{
		"interaction_id": interactionID.String(), "comment_id": event.CommentExternalID,
	})).Scan(&deliveryID)
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("record comment delivery: %w", err)
	}

	job, err := scanJob(tx.QueryRow(ctx, `
		INSERT INTO review_jobs (
			tenant_id, installation_id, delivery_id, provider, api_base_url, repository, clone_url,
			review_number, base_ref, base_sha, head_ref, head_sha, state
		)
		SELECT tenant_id, installation_id, $1, provider, api_base_url, repository, clone_url,
		       review_number, base_ref, $2, head_ref, $3, 'queued'
		FROM review_jobs
		WHERE id = $4
		RETURNING id, tenant_id, installation_id, $5::text, $6::text, delivery_id, provider, api_base_url, repository, clone_url,
		          review_number, base_ref, base_sha, head_ref, head_sha, state, attempts,
		          locked_by, locked_until, error_message, created_at, started_at, finished_at`,
		deliveryID, current.BaseSHA, current.HeadSHA, *current.LegacyJobID, installation.ExternalID, installation.CredentialRef))
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("queue comment review job: %w", err)
	}

	run, err := scanReviewRun(tx.QueryRow(ctx, `
		INSERT INTO review_runs (request_id, legacy_job_id, state, trigger_kind, head_sha, base_sha)
		VALUES ($1, $2, 'acknowledged', $3, $4, $5)
		RETURNING id, request_id, legacy_job_id, revision, state, trigger_kind, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, created_at, started_at, finished_at`, requestID, job.ID, triggerKind, current.HeadSHA, current.BaseSHA))
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("create comment review run: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE review_requests SET current_run_id = $2, updated_at = now() WHERE id = $1`, requestID, run.ID); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("set comment review current run: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO review_run_stages (run_id, stage, state) SELECT $1, stage, 'pending' FROM unnest(ARRAY['ack', 'admit', 'prepare', 'analyze', 'normalize', 'publish']) AS stage`, run.ID); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("initialize comment run stages: %w", err)
	}
	if err := appendRunEvent(ctx, tx, run.ID, run.Revision, "run.acknowledged", "user", "", map[string]any{"interaction_id": interactionID.String(), "trigger": triggerKind}); err != nil {
		return domain.ReviewRun{}, err
	}
	if err := insertOutbox(ctx, tx, run.ID, "review.run.acknowledged", "run:"+run.ID.String()+":1:acknowledged", map[string]any{"run_id": run.ID.String(), "revision": run.Revision}); err != nil {
		return domain.ReviewRun{}, err
	}
	return run, nil
}

func appendRunEvent(ctx context.Context, tx pgx.Tx, runID uuid.UUID, revision int, eventType, actorKind, actorSubject string, payload map[string]any) error {
	if _, err := tx.Exec(ctx, `INSERT INTO review_run_events (run_id, revision, event_type, actor_kind, actor_subject, payload) VALUES ($1, $2, $3, $4, $5, $6::jsonb)`, runID, revision, eventType, actorKind, actorSubject, jsonPayload(payload)); err != nil {
		return fmt.Errorf("append %s event: %w", eventType, err)
	}
	return nil
}

func scanReviewRun(row rowScanner) (domain.ReviewRun, error) {
	var run domain.ReviewRun
	var failureCode, failureMessage *string
	if err := row.Scan(&run.ID, &run.RequestID, &run.LegacyJobID, &run.Revision, &run.State, &run.TriggerKind, &run.HeadSHA, &run.BaseSHA, &run.CancelRequestedAt, &run.SupersededBy, &failureCode, &failureMessage, &run.CreatedAt, &run.StartedAt, &run.FinishedAt); err != nil {
		return domain.ReviewRun{}, err
	}
	if failureCode != nil {
		run.FailureCode = *failureCode
	}
	if failureMessage != nil {
		run.FailureMessage = *failureMessage
	}
	return run, nil
}

func (s *PostgresStore) ListReviewRuns(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.ReviewRunSummary, error) {
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.created_at, r.started_at, r.finished_at,
		       request.provider, request.repository, request.review_number
		FROM review_runs r
		JOIN review_requests request ON request.id = r.request_id
		WHERE request.tenant_id = $1
		ORDER BY r.created_at DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list review runs: %w", err)
	}
	defer rows.Close()
	runs := make([]domain.ReviewRunSummary, 0)
	for rows.Next() {
		run, err := scanReviewRunSummary(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review runs: %w", err)
	}
	return runs, nil
}

func (s *PostgresStore) GetReviewRun(ctx context.Context, actor, tenantSlug string, runID uuid.UUID) (domain.ReviewRunSummary, error) {
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewRunSummary{}, err
	}
	run, err := scanReviewRunSummary(s.pool.QueryRow(ctx, `
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.created_at, r.started_at, r.finished_at,
		       request.provider, request.repository, request.review_number
		FROM review_runs r
		JOIN review_requests request ON request.id = r.request_id
		WHERE request.tenant_id = $1 AND r.id = $2`, tenantID, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewRunSummary{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewRunSummary{}, fmt.Errorf("get review run: %w", err)
	}
	return run, nil
}

func (s *PostgresStore) ListRunEvents(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, afterRevision int) ([]domain.RunEvent, error) {
	if afterRevision < 0 {
		return nil, fmt.Errorf("after revision cannot be negative")
	}
	if _, err := s.GetReviewRun(ctx, actor, tenantSlug, runID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, revision, event_type, actor_kind, actor_subject, payload, created_at
		FROM review_run_events WHERE run_id = $1 AND revision > $2 ORDER BY revision`, runID, afterRevision)
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.RunEvent, 0)
	for rows.Next() {
		var event domain.RunEvent
		var payload []byte
		if err := rows.Scan(&event.ID, &event.RunID, &event.Revision, &event.EventType, &event.ActorKind, &event.ActorSubject, &payload, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan run event: %w", err)
		}
		if err := json.Unmarshal(payload, &event.Payload); err != nil {
			return nil, fmt.Errorf("decode run event payload: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run events: %w", err)
	}
	return events, nil
}

func (s *PostgresStore) RequestRunCancellation(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, expectedRevision int) (domain.ReviewRun, error) {
	if expectedRevision < 1 {
		return domain.ReviewRun{}, ErrRevisionConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("begin cancellation request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewRun{}, err
	}
	if role != "owner" && role != "admin" && role != "reviewer" {
		return domain.ReviewRun{}, ErrForbidden
	}
	run, err := scanReviewRun(tx.QueryRow(ctx, `
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.created_at, r.started_at, r.finished_at
		FROM review_runs r JOIN review_requests request ON request.id = r.request_id
		WHERE r.id = $1 AND request.tenant_id = $2 FOR UPDATE`, runID, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewRun{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("load review run for cancellation: %w", err)
	}
	if run.Revision != expectedRevision {
		return domain.ReviewRun{}, ErrRevisionConflict
	}
	if run.State.Terminal() || run.CancelRequestedAt != nil {
		return domain.ReviewRun{}, ErrConflict
	}
	run.Revision++
	if _, err := tx.Exec(ctx, `UPDATE review_runs SET cancel_requested_at = now(), revision = $2 WHERE id = $1`, runID, run.Revision); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("update cancellation request: %w", err)
	}
	if err := appendRunEvent(ctx, tx, runID, run.Revision, "run.cancel_requested", "user", actor, map[string]any{"source": "task_api"}); err != nil {
		return domain.ReviewRun{}, err
	}
	if err := insertOutbox(ctx, tx, runID, "review.run.cancel-requested", "run:"+runID.String()+fmt.Sprintf(":%d:cancel-requested", run.Revision), map[string]any{"run_id": runID.String(), "revision": run.Revision}); err != nil {
		return domain.ReviewRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("commit cancellation request: %w", err)
	}
	now := time.Now().UTC()
	run.CancelRequestedAt = &now
	return run, nil
}

// AdvanceLegacyRun lets the existing polling worker publish durable stage
// transitions while the RabbitMQ execution workers are introduced gradually.
func (s *PostgresStore) AdvanceRun(ctx context.Context, runID uuid.UUID, next domain.RunState) (domain.ReviewRun, error) {
	var jobID *uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT legacy_job_id FROM review_runs WHERE id = $1`, runID).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewRun{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("load review run for transition: %w", err)
	}
	if jobID == nil {
		return domain.ReviewRun{}, ErrNotFound
	}
	return s.AdvanceLegacyRun(ctx, *jobID, next)
}

func (s *PostgresStore) AdvanceLegacyRun(ctx context.Context, jobID uuid.UUID, next domain.RunState) (domain.ReviewRun, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("begin legacy run transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := scanReviewRun(tx.QueryRow(ctx, `
		SELECT id, request_id, legacy_job_id, revision, state, trigger_kind, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, created_at, started_at, finished_at
		FROM review_runs WHERE legacy_job_id = $1 FOR UPDATE`, jobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewRun{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("load legacy review run: %w", err)
	}
	if run.CancelRequestedAt != nil && !run.State.Terminal() {
		next = domain.RunCancelled
	}
	if run.State == next {
		return run, tx.Commit(ctx)
	}
	// A retried legacy job resumes from the furthest durable stage it reached.
	// Replaying the worker's initial admitted/preparing calls must therefore be
	// a no-op rather than an invalid backwards transition.
	if runStateRank(run.State) > runStateRank(next) && !next.Terminal() {
		return run, tx.Commit(ctx)
	}
	if err := domain.ValidateRunTransition(run.State, next); err != nil {
		return domain.ReviewRun{}, err
	}
	run.Revision++
	var started, finished string
	if next == domain.RunPreparing {
		started = ", started_at = COALESCE(started_at, now())"
	}
	if next.Terminal() {
		finished = ", finished_at = now()"
	}
	query := `UPDATE review_runs SET state = $2, revision = $3` + started + finished + ` WHERE id = $1`
	if _, err := tx.Exec(ctx, query, run.ID, next, run.Revision); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("advance legacy review run: %w", err)
	}
	if err := appendRunEvent(ctx, tx, run.ID, run.Revision, "run."+string(next), "worker", "legacy-runner", map[string]any{"legacy_job_id": jobID.String()}); err != nil {
		return domain.ReviewRun{}, err
	}
	if err := insertOutbox(ctx, tx, run.ID, "review.run."+string(next), "run:"+run.ID.String()+fmt.Sprintf(":%d:%s", run.Revision, next), map[string]any{"run_id": run.ID.String(), "revision": run.Revision}); err != nil {
		return domain.ReviewRun{}, err
	}
	run.State = next
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("commit legacy run transition: %w", err)
	}
	return run, nil
}

func runStateRank(state domain.RunState) int {
	switch state {
	case domain.RunAcknowledged:
		return 1
	case domain.RunAdmitted:
		return 2
	case domain.RunPreparing:
		return 3
	case domain.RunAnalyzing:
		return 4
	case domain.RunNormalizing:
		return 5
	case domain.RunPublishing:
		return 6
	default:
		return 0
	}
}

func (s *PostgresStore) authorizedTenant(ctx context.Context, actor, tenantSlug string) (uuid.UUID, string, error) {
	var tenantID uuid.UUID
	var role string
	err := s.pool.QueryRow(ctx, `
		SELECT t.id, m.role FROM tenants t JOIN memberships m ON m.tenant_id = t.id
		WHERE t.slug = $1 AND m.subject = $2`, tenantSlug, actor).Scan(&tenantID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", ErrForbidden
	}
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("authorize tenant: %w", err)
	}
	return tenantID, role, nil
}

func authorizedTenantTx(ctx context.Context, tx pgx.Tx, actor, tenantSlug string) (uuid.UUID, string, error) {
	var tenantID uuid.UUID
	var role string
	err := tx.QueryRow(ctx, `
		SELECT t.id, m.role FROM tenants t JOIN memberships m ON m.tenant_id = t.id
		WHERE t.slug = $1 AND m.subject = $2`, tenantSlug, actor).Scan(&tenantID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", ErrForbidden
	}
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("authorize tenant: %w", err)
	}
	return tenantID, role, nil
}

func scanReviewRunSummary(row rowScanner) (domain.ReviewRunSummary, error) {
	var summary domain.ReviewRunSummary
	var failureCode, failureMessage *string
	if err := row.Scan(&summary.ID, &summary.RequestID, &summary.LegacyJobID, &summary.Revision, &summary.State, &summary.TriggerKind, &summary.HeadSHA, &summary.BaseSHA, &summary.CancelRequestedAt, &summary.SupersededBy, &failureCode, &failureMessage, &summary.CreatedAt, &summary.StartedAt, &summary.FinishedAt, &summary.Provider, &summary.Repository, &summary.ReviewNumber); err != nil {
		return domain.ReviewRunSummary{}, err
	}
	if failureCode != nil {
		summary.FailureCode = *failureCode
	}
	if failureMessage != nil {
		summary.FailureMessage = *failureMessage
	}
	return summary, nil
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
