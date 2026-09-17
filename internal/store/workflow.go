package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/rules"
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

	ruleSnapshotID, err := resolveRuleSnapshot(ctx, tx, installation.TenantID, event.Repository, event.BaseRef)
	if err != nil {
		return fmt.Errorf("resolve rule snapshot: %w", err)
	}
	var runID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO review_runs (request_id, legacy_job_id, state, trigger_kind, head_sha, base_sha, rule_snapshot_id)
		VALUES ($1, $2, 'acknowledged', 'pull_request', $3, $4, $5)
		RETURNING id`, requestID, job.ID, event.HeadSHA, event.BaseSHA, ruleSnapshotID).Scan(&runID)
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
		if err := queueTerminalInteractionResponses(ctx, tx, stale.id, stale.revision, domain.RunSuperseded); err != nil {
			return err
		}
	}
	return nil
}

func insertOutbox(ctx context.Context, tx pgx.Tx, runID uuid.UUID, topic, dedupeKey string, payload map[string]any) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (aggregate_type, aggregate_id, topic, dedupe_key, payload)
		VALUES ('review_run', $1, $2, $3, $4::jsonb)
		ON CONFLICT (dedupe_key) DO NOTHING`, runID, topic, dedupeKey, jsonPayload(payload))
	if err != nil {
		return fmt.Errorf("insert %s outbox message: %w", topic, err)
	}
	return nil
}

// resolveRuleSnapshot selects only published, active bindings in the trusted
// control-plane database. It runs inside admission's transaction so the run
// references exactly the snapshot that was compiled for it, even if an admin
// changes bindings immediately afterwards.
func resolveRuleSnapshot(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, repository, targetBranch string) (uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT v.id, b.precedence, v.rules, b.target_branch_glob, b.path_include_glob, b.path_exclude_glob
		FROM rule_bindings b
		JOIN rule_versions v ON v.id = b.rule_version_id
		JOIN rule_sets rs ON rs.id = v.rule_set_id
		WHERE b.tenant_id = $1
		  AND b.state = 'active'
		  AND v.state = 'published'
		  AND rs.tenant_id = $1
		  AND (b.starts_at IS NULL OR b.starts_at <= now())
		  AND (b.ends_at IS NULL OR b.ends_at > now())
		  AND (b.scope_kind = 'tenant' OR (b.scope_kind = 'repository' AND b.scope_ref = $2))
		ORDER BY b.precedence ASC, v.id ASC`, tenantID, repository)
	if err != nil {
		return uuid.Nil, fmt.Errorf("select active rule bindings: %w", err)
	}
	defer rows.Close()
	sources := make([]rules.Source, 0)
	seenVersion := make(map[string]int)
	for rows.Next() {
		var versionID uuid.UUID
		var precedence int
		var rawRules []byte
		var branchGlob string
		var includeGlob string
		var excludeGlob string
		if err := rows.Scan(&versionID, &precedence, &rawRules, &branchGlob, &includeGlob, &excludeGlob); err != nil {
			return uuid.Nil, fmt.Errorf("scan active rule binding: %w", err)
		}
		if !matchesRuleTargetBranch(branchGlob, targetBranch) {
			continue
		}
		versionKey := versionID.String()
		if previous, exists := seenVersion[versionKey]; exists {
			if previous != precedence {
				return uuid.Nil, fmt.Errorf("published rule version %s is bound at conflicting precedences", versionKey)
			}
			// Multiple active bindings can intentionally reuse one published
			// version at the same precedence. Preserve a single source for merge
			// semantics while unioning their native OCR file filters.
			for index := range sources {
				if sources[index].VersionID != versionKey {
					continue
				}
				if includeGlob != "" {
					sources[index].Include = append(sources[index].Include, includeGlob)
				}
				if excludeGlob != "" {
					sources[index].Exclude = append(sources[index].Exclude, excludeGlob)
				}
				break
			}
			continue
		}
		var versionRules []rules.Rule
		if err := json.Unmarshal(rawRules, &versionRules); err != nil {
			return uuid.Nil, fmt.Errorf("decode published rule version %s: %w", versionKey, err)
		}
		seenVersion[versionKey] = precedence
		source := rules.Source{VersionID: versionKey, Precedence: precedence, Rules: versionRules}
		if includeGlob != "" {
			source.Include = []string{includeGlob}
		}
		if excludeGlob != "" {
			source.Exclude = []string{excludeGlob}
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, fmt.Errorf("iterate active rule bindings: %w", err)
	}
	compiled, err := rules.Compile(sources)
	if err != nil {
		return uuid.Nil, err
	}
	var snapshotID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_snapshots (tenant_id, sha256, compiler_version, engine, canonical_payload)
		VALUES ($1, $2, 'rules-v2', 'ocr', $3::jsonb)
		ON CONFLICT (tenant_id, sha256) DO UPDATE SET sha256 = EXCLUDED.sha256
		RETURNING id`, tenantID, compiled.SHA256, string(compiled.Canonical)).Scan(&snapshotID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store rule snapshot: %w", err)
	}
	for _, source := range sources {
		versionID, err := uuid.Parse(source.VersionID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("parse compiled rule version id: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO rule_snapshot_sources (snapshot_id, rule_version_id, precedence)
			VALUES ($1, $2, $3)
			ON CONFLICT (snapshot_id, rule_version_id) DO NOTHING`, snapshotID, versionID, source.Precedence); err != nil {
			return uuid.Nil, fmt.Errorf("store rule snapshot source: %w", err)
		}
	}
	return snapshotID, nil
}

func matchesRuleTargetBranch(pattern, branch string) bool {
	if pattern == "" {
		return true
	}
	matched, err := path.Match(pattern, branch)
	return err == nil && matched
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
	selectedRunID, err := resolveInteractionRunTarget(ctx, tx, requestID, currentRunID, input.Command, input.Target)
	if err != nil {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "review run id is invalid or does not belong to this pull request")
	}
	if input.Command == "help" || input.Command == "status" {
		var body string
		if input.Command == "help" {
			body = "Available commands: `@openreview review [--mode=standard|deep|security]`, `@openreview status [run-id]`, `@openreview cancel [run-id]`, and `@openreview retry [run-id]`."
		} else if selectedRunID == nil {
			body = "There is no review run for this pull request yet."
		} else {
			var state domain.RunState
			if err := tx.QueryRow(ctx, `SELECT state FROM review_runs WHERE id = $1`, *selectedRunID).Scan(&state); err != nil {
				return domain.InteractionOutcome{}, fmt.Errorf("load review status: %w", err)
			}
			body = fmt.Sprintf("Review run `%s` is currently **%s**.", selectedRunID.String(), state)
		}
		if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, selectedRunID, false, body); err != nil {
			return domain.InteractionOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.InteractionOutcome{}, fmt.Errorf("commit read-only interaction: %w", err)
		}
		return domain.InteractionOutcome{Accepted: true, RunID: selectedRunID, Reason: "command accepted"}, nil
	}
	if selectedRunID == nil {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "no review run is available for this pull request")
	}
	current, err := scanReviewRun(tx.QueryRow(ctx, `
		SELECT id, request_id, legacy_job_id, revision, state, trigger_kind, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, rule_snapshot_id, created_at, started_at, finished_at
		FROM review_runs WHERE id = $1 AND request_id = $2 FOR UPDATE`, *selectedRunID, requestID))
	if err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("load current review run: %w", err)
	}

	if input.Command == "cancel" {
		if current.State.Terminal() || current.CancelRequestedAt != nil {
			return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "run cannot be cancelled")
		}
		if err := cancelRun(ctx, tx, &current, "user", "", map[string]any{"interaction_id": interactionID.String()}); err != nil {
			return domain.InteractionOutcome{}, err
		}
		if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, &current.ID, false, fmt.Sprintf("Review run `%s` has been cancelled.", current.ID)); err != nil {
			return domain.InteractionOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.InteractionOutcome{}, fmt.Errorf("commit cancel interaction: %w", err)
		}
		return domain.InteractionOutcome{Accepted: true, RunID: &current.ID, Reason: "cancelled"}, nil
	}

	if input.Command == "review" && !current.State.Terminal() {
		if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, &current.ID, false, fmt.Sprintf("A review is already running as `%s`; use `@openreview status` for progress.", current.ID)); err != nil {
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
	if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, &run.ID, true, fmt.Sprintf("Review run `%s` is acknowledged and queued. I will publish the result when it completes.", run.ID)); err != nil {
		return domain.InteractionOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("commit review interaction: %w", err)
	}
	return domain.InteractionOutcome{Accepted: true, RunID: &run.ID, Reason: "review acknowledged"}, nil
}

func commandAllowed(role, command string) bool {
	if command == "help" || command == "status" || command == "invalid" {
		return role == "owner" || role == "admin" || role == "rule_admin" || role == "reviewer" || role == "viewer"
	}
	return role == "owner" || role == "admin" || role == "rule_admin" || role == "reviewer"
}

// resolveInteractionRunTarget accepts an explicit run id only for commands
// that operate on task state. The lookup is scoped to the already-locked
// request, so an otherwise valid UUID from another tenant or pull request is
// indistinguishable from a missing run to the caller.
func resolveInteractionRunTarget(ctx context.Context, tx pgx.Tx, requestID uuid.UUID, currentRunID *uuid.UUID, command, target string) (*uuid.UUID, error) {
	if command != "status" && command != "cancel" && command != "retry" {
		return currentRunID, nil
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return currentRunID, nil
	}
	runID, err := uuid.Parse(target)
	if err != nil {
		return nil, err
	}
	var resolved uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM review_runs WHERE id = $1 AND request_id = $2`, runID, requestID).Scan(&resolved)
	if err != nil {
		return nil, err
	}
	return &resolved, nil
}

func acceptInteraction(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, runID *uuid.UUID, releaseRun bool, body string) error {
	if _, err := tx.Exec(ctx, `UPDATE review_interactions SET result = 'accepted', result_run_id = $2 WHERE id = $1`, interactionID, runID); err != nil {
		return fmt.Errorf("accept interaction: %w", err)
	}
	var releaseRunID *uuid.UUID
	if releaseRun {
		releaseRunID = runID
	}
	return queueInteractionResponse(ctx, tx, installation, event, interactionID, body, interactionReaction(installation.Provider, true), releaseRunID)
}

func rejectInteraction(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, reason string) (domain.InteractionOutcome, error) {
	if _, err := tx.Exec(ctx, `UPDATE review_interactions SET result = 'rejected' WHERE id = $1`, interactionID); err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("reject interaction: %w", err)
	}
	if err := queueInteractionResponse(ctx, tx, installation, event, interactionID, "Unable to run that command: "+reason, interactionReaction(installation.Provider, false), nil); err != nil {
		return domain.InteractionOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("commit rejected interaction: %w", err)
	}
	return domain.InteractionOutcome{Reason: reason}, nil
}

func interactionReaction(provider domain.Provider, accepted bool) domain.InteractionReaction {
	// GitHub guarantees a single reaction of a given content from the same app
	// installation identity. GitLab command replies remain notes until its
	// award-emoji adapter is introduced, rather than pretending an equivalent
	// API exists for every self-managed version.
	if provider != domain.ProviderGitHub {
		return domain.InteractionReactionNone
	}
	if accepted {
		return domain.InteractionReactionEyes
	}
	return domain.InteractionReactionConfused
}

// cancelRun commits the terminal state before acknowledging the user. A
// worker already inside a checkout cannot be force-killed from a transaction,
// but its subsequent stage transition sees this immutable state and cannot
// publish findings. The legacy job is also terminal so no polling worker can
// claim it again.
func cancelRun(ctx context.Context, tx pgx.Tx, run *domain.ReviewRun, actorKind, actorSubject string, payload map[string]any) error {
	run.Revision++
	if _, err := tx.Exec(ctx, `UPDATE review_runs SET state = 'cancelled', cancel_requested_at = now(), revision = $2, finished_at = now() WHERE id = $1`, run.ID, run.Revision); err != nil {
		return fmt.Errorf("cancel review run: %w", err)
	}
	if run.LegacyJobID != nil {
		if _, err := tx.Exec(ctx, `UPDATE review_jobs SET state = 'cancelled', locked_by = NULL, locked_until = NULL, finished_at = now() WHERE id = $1 AND state IN ('queued', 'running')`, *run.LegacyJobID); err != nil {
			return fmt.Errorf("cancel review job: %w", err)
		}
	}
	if err := appendRunEvent(ctx, tx, run.ID, run.Revision, "run.cancelled", actorKind, actorSubject, payload); err != nil {
		return err
	}
	if err := insertOutbox(ctx, tx, run.ID, "review.run.cancelled", "run:"+run.ID.String()+fmt.Sprintf(":%d:cancelled", run.Revision), map[string]any{"run_id": run.ID.String(), "revision": run.Revision}); err != nil {
		return err
	}
	if err := queueTerminalInteractionResponses(ctx, tx, run.ID, run.Revision, domain.RunCancelled); err != nil {
		return err
	}
	now := time.Now().UTC()
	run.State, run.CancelRequestedAt, run.FinishedAt = domain.RunCancelled, &now, &now
	return nil
}

func queueInteractionResponse(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, body string, reaction domain.InteractionReaction, releaseRunID *uuid.UUID) error {
	payload := map[string]any{
		"provider":                 installation.Provider,
		"api_base_url":             installation.APIBaseURL,
		"installation_external_id": installation.ExternalID,
		"credential_ref":           installation.CredentialRef,
		"repository":               event.Repository,
		"review_number":            event.ReviewNumber,
		"comment_external_id":      event.CommentExternalID,
		"reaction":                 reaction,
		"body":                     body,
		"marker":                   "open-review-platform:interaction:" + interactionID.String(),
	}
	if releaseRunID != nil {
		payload["release_run_id"] = releaseRunID.String()
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

// queueTerminalInteractionResponses updates the original marker-keyed
// acknowledgement for command-triggered work. It intentionally reuses the
// interaction responder and its inbox dedupe: the broker can retry the status
// publication without ever creating a second visible comment.
func queueTerminalInteractionResponses(ctx context.Context, tx pgx.Tx, runID uuid.UUID, revision int, state domain.RunState) error {
	if !state.Terminal() {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT i.id, j.provider, j.api_base_url, installation.external_id, installation.credential_ref, j.repository, j.review_number
		FROM review_interactions i
		JOIN review_runs r ON r.id = i.result_run_id
		JOIN review_jobs j ON j.id = r.legacy_job_id
		JOIN provider_installations installation ON installation.id = j.installation_id
		WHERE i.result_run_id = $1
		  AND i.result = 'accepted'
		  AND i.command IN ('review', 'retry')
		ORDER BY i.created_at ASC`, runID)
	if err != nil {
		return fmt.Errorf("select terminal interaction responses: %w", err)
	}
	type terminalInteraction struct {
		id                     uuid.UUID
		provider               domain.Provider
		apiBaseURL             string
		installationExternalID string
		credentialRef          string
		repository             string
		reviewNumber           int
	}
	interactions := make([]terminalInteraction, 0)
	for rows.Next() {
		var interaction terminalInteraction
		if err := rows.Scan(&interaction.id, &interaction.provider, &interaction.apiBaseURL, &interaction.installationExternalID, &interaction.credentialRef, &interaction.repository, &interaction.reviewNumber); err != nil {
			rows.Close()
			return fmt.Errorf("scan terminal interaction response: %w", err)
		}
		interactions = append(interactions, interaction)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate terminal interaction responses: %w", err)
	}
	rows.Close()
	for _, interaction := range interactions {
		payload := map[string]any{
			"provider":                 interaction.provider,
			"api_base_url":             interaction.apiBaseURL,
			"installation_external_id": interaction.installationExternalID,
			"credential_ref":           interaction.credentialRef,
			"repository":               interaction.repository,
			"review_number":            interaction.reviewNumber,
			"reaction":                 domain.InteractionReactionNone,
			"body":                     terminalInteractionBody(runID, state),
			"marker":                   "open-review-platform:interaction:" + interaction.id.String(),
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO outbox_messages (aggregate_type, aggregate_id, topic, dedupe_key, payload)
			VALUES ('review_interaction', $1, 'review.interaction.response', $2, $3::jsonb)
			ON CONFLICT (dedupe_key) DO NOTHING`, interaction.id, fmt.Sprintf("interaction:%s:status:%d", interaction.id, revision), jsonPayload(payload))
		if err != nil {
			return fmt.Errorf("queue terminal interaction response: %w", err)
		}
	}
	return nil
}

func terminalInteractionBody(runID uuid.UUID, state domain.RunState) string {
	switch state {
	case domain.RunCompleted:
		return fmt.Sprintf("Review run `%s` has completed. Findings, if any, were published to this pull request.", runID)
	case domain.RunCancelled:
		return fmt.Sprintf("Review run `%s` was cancelled before publication.", runID)
	case domain.RunSuperseded:
		return fmt.Sprintf("Review run `%s` was superseded by a newer pull request revision and will not publish findings.", runID)
	case domain.RunNeedsAttention:
		return fmt.Sprintf("Review run `%s` needs attention before its findings can be published. See the task detail for the safe error summary.", runID)
	default:
		return fmt.Sprintf("Review run `%s` could not be completed after retrying. See the task detail for the safe error summary.", runID)
	}
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

	ruleSnapshotID, err := resolveRuleSnapshot(ctx, tx, installation.TenantID, job.Repository, job.BaseRef)
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("resolve comment rule snapshot: %w", err)
	}
	run, err := scanReviewRun(tx.QueryRow(ctx, `
		INSERT INTO review_runs (request_id, legacy_job_id, state, trigger_kind, head_sha, base_sha, rule_snapshot_id)
		VALUES ($1, $2, 'acknowledged', $3, $4, $5, $6)
		RETURNING id, request_id, legacy_job_id, revision, state, trigger_kind, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, rule_snapshot_id, created_at, started_at, finished_at`, requestID, job.ID, triggerKind, current.HeadSHA, current.BaseSHA, ruleSnapshotID))
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
	return run, nil
}

// ReleaseAcknowledgedRun is the response-to-execution barrier for a command
// trigger. The interaction responder calls it only after GitHub has accepted
// the marker-keyed acknowledgement (and its source-comment reaction). A
// retry is safe because the durable outbox key is unique.
func (s *PostgresStore) ReleaseAcknowledgedRun(ctx context.Context, runID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin interaction run release: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state domain.RunState
	var revision int
	err = tx.QueryRow(ctx, `SELECT state, revision FROM review_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&state, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load acknowledged run for release: %w", err)
	}
	if state != domain.RunAcknowledged {
		return tx.Commit(ctx)
	}
	if err := insertOutbox(ctx, tx, runID, "review.run.acknowledged", "run:"+runID.String()+fmt.Sprintf(":%d:acknowledged", revision), map[string]any{"run_id": runID.String(), "revision": revision}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit interaction run release: %w", err)
	}
	return nil
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
	if err := row.Scan(&run.ID, &run.RequestID, &run.LegacyJobID, &run.Revision, &run.State, &run.TriggerKind, &run.HeadSHA, &run.BaseSHA, &run.CancelRequestedAt, &run.SupersededBy, &failureCode, &failureMessage, &run.RuleSnapshotID, &run.CreatedAt, &run.StartedAt, &run.FinishedAt); err != nil {
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
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at,
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
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at,
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
	if role != "owner" && role != "admin" && role != "rule_admin" && role != "reviewer" {
		return domain.ReviewRun{}, ErrForbidden
	}
	run, err := scanReviewRun(tx.QueryRow(ctx, `
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at
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
	if err := cancelRun(ctx, tx, &run, "user", actor, map[string]any{"source": "task_api"}); err != nil {
		return domain.ReviewRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("commit cancellation request: %w", err)
	}
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
		SELECT id, request_id, legacy_job_id, revision, state, trigger_kind, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, rule_snapshot_id, created_at, started_at, finished_at
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
	// Queue delivery is at-least-once and an acknowledgement can arrive after
	// the user cancelled (or a newer head superseded) the run. Terminal runs
	// are immutable, so the stale stage signal is safely consumed as a no-op
	// instead of being retried into the dead-letter queue.
	if run.State.Terminal() {
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
	if next.Terminal() {
		if err := queueTerminalInteractionResponses(ctx, tx, run.ID, run.Revision, next); err != nil {
			return domain.ReviewRun{}, err
		}
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
	if err := row.Scan(&summary.ID, &summary.RequestID, &summary.LegacyJobID, &summary.Revision, &summary.State, &summary.TriggerKind, &summary.HeadSHA, &summary.BaseSHA, &summary.CancelRequestedAt, &summary.SupersededBy, &failureCode, &failureMessage, &summary.RuleSnapshotID, &summary.CreatedAt, &summary.StartedAt, &summary.FinishedAt, &summary.Provider, &summary.Repository, &summary.ReviewNumber); err != nil {
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
