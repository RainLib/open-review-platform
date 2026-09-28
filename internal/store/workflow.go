package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
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

// errRunCoalesced tells Enqueue to commit the delivery ledger while returning
// duplicate=true. It is not a transaction failure: the newly allocated legacy
// job has been terminally cancelled because an identical active run already
// owns the same PR head.
var errRunCoalesced = errors.New("review run coalesced into active head")

// createWorkflowRun runs inside the delivery transaction. It means an accepted
// provider delivery can never leave a run without its first durable event and
// outbox notification, even when the broker is unavailable.
func createWorkflowRun(ctx context.Context, tx pgx.Tx, installation domain.Installation, job domain.ReviewJob, event domain.InboundEvent) error {
	// A deferred provider lookup and a newer webhook may race before either
	// request row exists. Serialize all run creation for this exact provider
	// review so the deferred path can inspect the latest committed head under
	// the same transaction-scoped lock.
	if err := lockReviewAdmissionIdentity(ctx, tx, installation.ID, event.Repository, event.ReviewNumber); err != nil {
		return err
	}
	triggerKind := strings.TrimSpace(event.TriggerKind)
	if triggerKind == "" {
		triggerKind = "pull_request"
	}
	actorKind := strings.TrimSpace(event.ActorKind)
	if actorKind == "" {
		actorKind = "provider"
	}
	configScope := reviewConfigScopeForInstallation(installation, event.Repository)
	reviewMode, err := resolveConfiguredReviewMode(ctx, tx, installation.TenantID, configScope, event.ReviewMode)
	if err != nil {
		return err
	}
	var requestID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO review_requests (tenant_id, installation_id, provider, api_base_url, repository, review_number, title, author)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant_id, installation_id, repository, review_number) DO UPDATE
		SET title = CASE WHEN EXCLUDED.title <> '' THEN EXCLUDED.title ELSE review_requests.title END,
		    author = CASE WHEN EXCLUDED.author <> '' THEN EXCLUDED.author ELSE review_requests.author END,
		    updated_at = now()
		RETURNING id`, installation.TenantID, installation.ID, event.Provider, event.APIBaseURL, event.Repository, event.ReviewNumber,
		normalizeReviewRequestMetadata(event.Title, 512), normalizeReviewRequestMetadata(event.Author, 256)).Scan(&requestID)
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
		// delivery ledger retains it, but we must not create a second active run
		// or leave its preallocated legacy job queued forever.
		if _, err := tx.Exec(ctx, `
			UPDATE review_jobs
			SET state = 'cancelled', finished_at = now(), error_message = $2
			WHERE id = $1`, job.ID, "coalesced into active review run "+currentRunID.String()); err != nil {
			return fmt.Errorf("cancel coalesced review job: %w", err)
		}
		return errRunCoalesced
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("find active run for head: %w", err)
	}

	var rolloutSelections []ruleRolloutRunSelection
	ruleSnapshotID, err := resolveRuleSnapshotWithCanary(ctx, tx, installation.TenantID, configScope, event.BaseRef, event.RuleSetID, event.ReviewNumber, &rolloutSelections)
	if err != nil {
		return fmt.Errorf("resolve rule snapshot: %w", err)
	}
	var runID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO review_runs (request_id, legacy_job_id, state, trigger_kind, review_mode, head_sha, base_sha, rule_snapshot_id)
		VALUES ($1, $2, 'acknowledged', $3, $4, $5, $6, $7)
		RETURNING id`, requestID, job.ID, triggerKind, reviewMode, event.HeadSHA, event.BaseSHA, ruleSnapshotID).Scan(&runID)
	if err != nil {
		return fmt.Errorf("create review run: %w", err)
	}
	if err := recordRuleRolloutRunSelections(ctx, tx, runID, rolloutSelections); err != nil {
		return err
	}
	if err := snapshotReviewConfigurations(ctx, tx, runID, installation.TenantID, configScope); err != nil {
		return fmt.Errorf("snapshot review configuration: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE review_requests SET current_run_id = $2, updated_at = now() WHERE id = $1`, requestID, runID); err != nil {
		return fmt.Errorf("set review request current run: %w", err)
	}
	if err := appendRunEvent(ctx, tx, runID, 1, "run.acknowledged", actorKind, event.ActorSubject, map[string]any{
		"provider": string(event.Provider), "delivery_id": event.DeliveryID, "review_number": event.ReviewNumber,
		"trigger": triggerKind, "review_mode": reviewMode,
	}); err != nil {
		return fmt.Errorf("record run acknowledgement: %w", err)
	}
	if triggerKind == "cli" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
			VALUES ($1, $2, 'cli_review.submitted', $3, jsonb_build_object(
				'repository', $4::text, 'review_number', $5::integer,
				'head_sha', $6::text, 'mode', $7::text
			))`, installation.TenantID, event.ActorSubject, runID.String(), event.Repository, event.ReviewNumber, event.HeadSHA, reviewMode); err != nil {
			return fmt.Errorf("audit CLI review admission: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_run_stages (run_id, stage, state)
		SELECT $1, stage, 'pending'
		FROM unnest(ARRAY['ack', 'admit', 'prepare', 'analyze', 'normalize', 'publish']) AS stage`, runID); err != nil {
		return fmt.Errorf("initialize run stages: %w", err)
	}
	if !event.DeferAcknowledgement {
		if err := insertOutbox(ctx, tx, runID, "review.run.acknowledged", "run:"+runID.String()+":1:acknowledged", map[string]any{
			"run_id": runID.String(), "revision": 1,
		}); err != nil {
			return err
		}
	}
	run := domain.ReviewRun{ID: runID, LegacyJobID: &job.ID, Revision: 1, State: domain.RunAcknowledged, TriggerKind: triggerKind, ReviewMode: reviewMode}
	if _, err := admitRunUsage(ctx, tx, installation.TenantID, event.Repository, &run); err != nil {
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
		if err := finalizeReviewUsage(ctx, tx, stale.id, false); err != nil {
			return err
		}
	}
	return nil
}

func lockReviewAdmissionIdentity(ctx context.Context, tx pgx.Tx, installationID uuid.UUID, repository string, reviewNumber int) error {
	key := "review-admission:" + installationID.String() + ":" + repository + ":" + strconv.Itoa(reviewNumber)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,0))`, key); err != nil {
		return fmt.Errorf("lock review admission identity: %w", err)
	}
	return nil
}

// resolveConfiguredReviewMode turns the command/API shorthand `configured`
// into the concrete default retained by the effective repository policy. The
// persisted run therefore remains reproducible if an administrator changes a
// default while it is waiting in the queue.
func resolveConfiguredReviewMode(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scope domain.ReviewConfigScope, requested domain.ReviewMode) (domain.ReviewMode, error) {
	if requested == "" {
		requested = domain.ReviewModeConfigured
	}
	if !requested.Valid() {
		return "", fmt.Errorf("review mode is invalid")
	}
	if requested != domain.ReviewModeConfigured {
		return requested, nil
	}
	generalSnapshot, err := resolveEffectiveReviewConfiguration(ctx, tx, tenantID, scope, domain.ReviewConfigGeneral)
	if err != nil {
		return "", fmt.Errorf("resolve configured review mode: %w", err)
	}
	general, err := domain.DecodeReviewGeneralConfig(generalSnapshot.Content)
	if err != nil {
		return "", fmt.Errorf("decode configured review mode: %w", err)
	}
	if !general.DefaultReviewMode.Selectable() {
		return "", fmt.Errorf("configured review mode is invalid")
	}
	return general.DefaultReviewMode, nil
}

func reviewTriggerModeForRepository(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scope domain.ReviewConfigScope) (domain.ReviewTriggerMode, error) {
	generalSnapshot, err := resolveEffectiveReviewConfiguration(ctx, tx, tenantID, scope, domain.ReviewConfigGeneral)
	if err != nil {
		return "", fmt.Errorf("resolve review trigger mode: %w", err)
	}
	general, err := domain.DecodeReviewGeneralConfig(generalSnapshot.Content)
	if err != nil {
		return "", fmt.Errorf("decode review trigger mode: %w", err)
	}
	return general.TriggerMode, nil
}

func normalizeReviewRequestMetadata(value string, maximumRunes int) string {
	value = strings.TrimSpace(value)
	if maximumRunes <= 0 || value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) > maximumRunes {
		return string(runes[:maximumRunes])
	}
	return value
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

// snapshotReviewConfigurations resolves repository override -> tenant override
// -> platform default inside the admission transaction. The runner and audit
// views can therefore prove the exact behavior of this run after later edits.
func snapshotReviewConfigurations(ctx context.Context, tx pgx.Tx, runID, tenantID uuid.UUID, scope domain.ReviewConfigScope) error {
	for _, section := range domain.ReviewConfigSections() {
		resolved, err := resolveEffectiveReviewConfiguration(ctx, tx, tenantID, scope, section)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO review_configuration_snapshots (
				 run_id, section, origin_scope_kind, origin_scope_ref, origin_scope_provider, origin_scope_api_base_url,
				 origin_revision, content, content_sha256
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9)
			ON CONFLICT (run_id, section) DO NOTHING`,
			runID, section, resolved.OriginScopeKind, resolved.OriginScopeRef, resolved.OriginProvider, resolved.OriginAPIBaseURL, resolved.OriginRevision, resolved.Content, resolved.ContentSHA256); err != nil {
			return fmt.Errorf("store %s review configuration snapshot: %w", section, err)
		}
	}
	return nil
}

type effectiveReviewConfiguration struct {
	OriginScopeKind  string
	OriginScopeRef   string
	OriginProvider   domain.Provider
	OriginAPIBaseURL string
	OriginRevision   int
	Content          json.RawMessage
	ContentSHA256    string
}

// resolveEffectiveReviewConfiguration is shared by admission and snapshotting,
// so the decision that lets a webhook create work is made from exactly the
// same canonical configuration that will later be retained with the run.
func resolveEffectiveReviewConfiguration(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scope domain.ReviewConfigScope, section domain.ReviewConfigSection) (effectiveReviewConfiguration, error) {
	if normalized, valid := scope.Normalize(); !valid || normalized.Kind != domain.ReviewConfigRepositoryScope || !normalized.QualifiedRepository() {
		return effectiveReviewConfiguration{}, fmt.Errorf("resolve %s review configuration: invalid provider-qualified repository scope", section)
	} else {
		scope = normalized
	}
	resolved := effectiveReviewConfiguration{
		OriginScopeKind: "default",
		Content:         domain.DefaultReviewConfig(section),
	}
	var stored []byte
	err := tx.QueryRow(ctx, `
			SELECT configuration.scope_kind, configuration.scope_ref, configuration.scope_provider, configuration.scope_api_base_url,
			       version.revision, version.content
			FROM review_configurations configuration
			JOIN LATERAL (
				SELECT revision, content
				FROM review_configuration_versions
				WHERE configuration_id = configuration.id
				ORDER BY revision DESC LIMIT 1
			) version ON TRUE
			WHERE configuration.tenant_id = $1 AND configuration.section = $2
			  AND configuration.active = TRUE
			  AND (
				(configuration.scope_kind = 'repository' AND configuration.scope_ref = $3
				 AND configuration.scope_provider = $4 AND configuration.scope_api_base_url = $5)
				OR (configuration.scope_kind = 'repository' AND configuration.scope_ref = $3
				 AND configuration.scope_provider = '' AND configuration.scope_api_base_url = '')
				OR (configuration.scope_kind = 'tenant' AND configuration.scope_ref = ''
				 AND configuration.scope_provider = '' AND configuration.scope_api_base_url = '')
			  )
			ORDER BY CASE
				WHEN configuration.scope_kind = 'repository' AND configuration.scope_provider = $4 AND configuration.scope_api_base_url = $5 THEN 1
				WHEN configuration.scope_kind = 'repository' THEN 2
				ELSE 3
			END
			LIMIT 1`, tenantID, section, scope.Ref, scope.Provider, scope.APIBaseURL).Scan(&resolved.OriginScopeKind, &resolved.OriginScopeRef, &resolved.OriginProvider, &resolved.OriginAPIBaseURL, &resolved.OriginRevision, &stored)
	if err == nil {
		resolved.Content = json.RawMessage(stored)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return effectiveReviewConfiguration{}, fmt.Errorf("resolve %s review configuration: %w", section, err)
	}
	canonical, hash, valid := domain.CanonicalReviewConfig(section, resolved.Content)
	if !valid {
		return effectiveReviewConfiguration{}, fmt.Errorf("resolved %s review configuration is invalid", section)
	}
	resolved.Content, resolved.ContentSHA256 = canonical, hash
	return resolved, nil
}

func reviewConfigScopeForInstallation(installation domain.Installation, repository string) domain.ReviewConfigScope {
	return domain.ReviewConfigScope{
		Kind: domain.ReviewConfigRepositoryScope, Ref: repository,
		Provider: installation.Provider, APIBaseURL: installation.APIBaseURL,
	}
}

// resolveRuleSnapshot selects only published, active bindings in the trusted
// control-plane database. It runs inside admission's transaction so the run
// references exactly the snapshot that was compiled for it, even if an admin
// changes bindings immediately afterwards.
func resolveRuleSnapshot(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scope domain.ReviewConfigScope, targetBranch string, selectedRuleSetID *uuid.UUID) (uuid.UUID, error) {
	return resolveRuleSnapshotWithCanary(ctx, tx, tenantID, scope, targetBranch, selectedRuleSetID, 0, nil)
}

func resolveRuleSnapshotWithCanary(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scope domain.ReviewConfigScope, targetBranch string, selectedRuleSetID *uuid.UUID, reviewNumber int, selections *[]ruleRolloutRunSelection) (uuid.UUID, error) {
	canaries, err := loadActiveCanaryRuleBindings(ctx, tx, tenantID, scope, selectedRuleSetID, reviewNumber)
	if err != nil {
		return uuid.Nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT b.id, v.id, b.precedence, v.rules, b.target_branch_glob, b.path_include_glob, b.path_exclude_glob
		FROM rule_bindings b
		JOIN rule_versions v ON v.id = b.rule_version_id
		JOIN rule_sets rs ON rs.id = v.rule_set_id
		WHERE b.tenant_id = $1
		  AND b.state = 'active'
		  AND v.state = 'published'
		  AND rs.tenant_id = $1
		  AND (b.starts_at IS NULL OR b.starts_at <= now())
		  AND (b.ends_at IS NULL OR b.ends_at > now())
		  AND (b.scope_kind = 'tenant' OR (
			b.scope_kind = 'repository' AND b.scope_ref = $2
			AND ((b.scope_provider = $3 AND b.scope_api_base_url = $4)
			     OR (b.scope_provider = '' AND b.scope_api_base_url = ''))
		  ))
		  AND ($5::uuid IS NULL OR rs.id = $5)
		ORDER BY b.precedence ASC, v.id ASC`, tenantID, scope.Ref, scope.Provider, scope.APIBaseURL, selectedRuleSetID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("select active rule bindings: %w", err)
	}
	defer rows.Close()
	sources := make([]rules.Source, 0)
	seenVersion := make(map[string]int)
	for rows.Next() {
		var bindingID uuid.UUID
		var versionID uuid.UUID
		var precedence int
		var rawRules []byte
		var branchGlob string
		var includeGlob string
		var excludeGlob string
		if err := rows.Scan(&bindingID, &versionID, &precedence, &rawRules, &branchGlob, &includeGlob, &excludeGlob); err != nil {
			return uuid.Nil, fmt.Errorf("scan active rule binding: %w", err)
		}
		if !matchesRuleTargetBranch(branchGlob, targetBranch) {
			continue
		}
		if canary, exists := canaries[bindingID]; exists {
			if versionID != canary.baselineVersionID {
				return uuid.Nil, ErrInvalidRuleRollout
			}
			selectedBindingID := bindingID
			if canary.selected {
				selectedBindingID = canary.candidateBindingID
				versionID = canary.candidateVersionID
				rawRules = canary.candidateRules
			}
			if selections != nil {
				*selections = append(*selections, ruleRolloutRunSelection{
					rolloutID: canary.rolloutID, baselineBindingID: bindingID,
					candidateBindingID: canary.candidateBindingID,
					selectedBindingID:  selectedBindingID, selectedVersionID: versionID,
					bucket: canary.bucket, selected: canary.selected,
				})
			}
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
	rows.Close()
	if selectedRuleSetID != nil && len(sources) == 0 {
		return uuid.Nil, ErrSelectedRuleSetUnavailable
	}
	compiled, err := rules.Compile(sources)
	if err != nil {
		return uuid.Nil, err
	}
	appliedExceptions, exceptionRecords, err := resolveApplicableRuleExceptions(ctx, tx, tenantID, scope, targetBranch, seenVersion)
	if err != nil {
		return uuid.Nil, err
	}
	compiled, err = rules.ApplyExceptions(compiled, appliedExceptions)
	if err != nil {
		return uuid.Nil, fmt.Errorf("apply approved rule exceptions: %w", err)
	}
	exceptionRecords = appliedExceptionRecords(compiled, exceptionRecords)
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
	for _, exception := range exceptionRecords {
		if _, err := tx.Exec(ctx, `
			INSERT INTO rule_snapshot_exceptions (snapshot_id, exception_id, rule_key, rule_version_id, expires_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (snapshot_id, exception_id) DO NOTHING`, snapshotID, exception.ID,
			exception.RuleKey, exception.RuleVersionID, exception.ExpiresAt); err != nil {
			return uuid.Nil, fmt.Errorf("store rule snapshot exception: %w", err)
		}
	}
	return snapshotID, nil
}

// ruleSetActiveForTarget is the no-write guard for an explicit comment
// selection. It mirrors the admission binding scope and target-branch rules,
// so an interaction can receive a durable rejection before a job is created.
func ruleSetActiveForTarget(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scope domain.ReviewConfigScope, targetBranch string, ruleSetID uuid.UUID) (bool, error) {
	rows, err := tx.Query(ctx, `
		SELECT b.target_branch_glob
		FROM rule_bindings b
		JOIN rule_versions v ON v.id = b.rule_version_id
		JOIN rule_sets rs ON rs.id = v.rule_set_id
		WHERE b.tenant_id = $1
		  AND b.state = 'active'
		  AND v.state = 'published'
		  AND rs.tenant_id = $1
		  AND rs.id = $5
		  AND (b.starts_at IS NULL OR b.starts_at <= now())
		  AND (b.ends_at IS NULL OR b.ends_at > now())
		  AND (b.scope_kind = 'tenant' OR (
			b.scope_kind = 'repository' AND b.scope_ref = $2
			AND ((b.scope_provider = $3 AND b.scope_api_base_url = $4)
			     OR (b.scope_provider = '' AND b.scope_api_base_url = ''))
		  ))`, tenantID, scope.Ref, scope.Provider, scope.APIBaseURL, ruleSetID)
	if err != nil {
		return false, fmt.Errorf("select selected rule set bindings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var targetBranchGlob string
		if err := rows.Scan(&targetBranchGlob); err != nil {
			return false, fmt.Errorf("scan selected rule set binding: %w", err)
		}
		if matchesRuleTargetBranch(targetBranchGlob, targetBranch) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate selected rule set bindings: %w", err)
	}
	return false, nil
}

func appliedExceptionRecords(compiled rules.Compiled, records []appliedRuleExceptionRecord) []appliedRuleExceptionRecord {
	if len(compiled.Snapshot.AppliedExceptions) == 0 {
		return nil
	}
	ids := make(map[uuid.UUID]struct{}, len(compiled.Snapshot.AppliedExceptions))
	for _, exception := range compiled.Snapshot.AppliedExceptions {
		if id, err := uuid.Parse(exception.ID); err == nil {
			ids[id] = struct{}{}
		}
	}
	filtered := make([]appliedRuleExceptionRecord, 0, len(ids))
	for _, record := range records {
		if _, ok := ids[record.ID]; ok {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

type appliedRuleExceptionRecord struct {
	ID            uuid.UUID
	RuleVersionID uuid.UUID
	RuleKey       string
	ScopeKind     string
	ScopeRef      string
	BranchGlob    string
	ExpiresAt     time.Time
}

type ruleExceptionQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func resolveApplicableRuleExceptions(ctx context.Context, queryer ruleExceptionQuerier, tenantID uuid.UUID, scope domain.ReviewConfigScope, targetBranch string, versions map[string]int) ([]rules.AppliedException, []appliedRuleExceptionRecord, error) {
	if len(versions) == 0 {
		return nil, nil, nil
	}
	versionIDs := make([]uuid.UUID, 0, len(versions))
	for raw := range versions {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("parse exception candidate version: %w", err)
		}
		versionIDs = append(versionIDs, id)
	}
	rows, err := queryer.Query(ctx, `
		SELECT id, rule_version_id, rule_key, scope_kind, scope_ref, target_branch_glob, expires_at
		FROM rule_exceptions
		WHERE tenant_id = $1
		  AND rule_version_id = ANY($2::uuid[])
		  AND state = 'approved'
		  AND expires_at > now()
		  AND (scope_kind = 'tenant' OR (
			scope_kind = 'repository' AND scope_ref = $3
			AND ((scope_provider = $4 AND scope_api_base_url = $5)
			     OR (scope_provider = '' AND scope_api_base_url = ''))
		  ))
		ORDER BY id`, tenantID, versionIDs, scope.Ref, scope.Provider, scope.APIBaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("select approved rule exceptions: %w", err)
	}
	defer rows.Close()
	applied := make([]rules.AppliedException, 0)
	records := make([]appliedRuleExceptionRecord, 0)
	for rows.Next() {
		var record appliedRuleExceptionRecord
		if err := rows.Scan(&record.ID, &record.RuleVersionID, &record.RuleKey, &record.ScopeKind, &record.ScopeRef, &record.BranchGlob, &record.ExpiresAt); err != nil {
			return nil, nil, fmt.Errorf("scan approved rule exception: %w", err)
		}
		if !matchesRuleTargetBranch(record.BranchGlob, targetBranch) {
			continue
		}
		records = append(records, record)
		applied = append(applied, rules.AppliedException{
			ID: record.ID.String(), RuleKey: record.RuleKey, SourceVersion: record.RuleVersionID.String(),
			ScopeKind: record.ScopeKind, ScopeRef: record.ScopeRef, TargetBranch: record.BranchGlob,
			ExpiresAt: record.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate approved rule exceptions: %w", err)
	}
	return applied, records, nil
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
		return domain.ProviderIdentity{}, ErrInvalidProviderIdentity
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderIdentity{}, fmt.Errorf("begin provider identity update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT t.id FROM tenants t JOIN memberships m ON m.tenant_id = t.id
		WHERE t.slug = $1 AND m.subject = $2 AND m.active = TRUE AND m.role IN ('owner', 'admin')`, tenantSlug, actor).Scan(&tenantID)
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

	installation, err := resolveInboundInstallation(ctx, tx, input.Event.Provider, input.Event.APIBaseURL, input.Event.InstallationExternalID, input.Event.Repository)
	if err != nil {
		return domain.InteractionOutcome{}, err
	}

	var role, actorSubject string
	err = tx.QueryRow(ctx, `
		SELECT m.role, m.subject
		FROM provider_actor_mappings p
		JOIN memberships m ON m.tenant_id = p.tenant_id AND m.subject = p.subject AND m.active = TRUE
		WHERE p.tenant_id = $1 AND p.provider = $2 AND p.external_id = $3`, installation.TenantID, input.Event.Provider, input.Event.ActorExternalID).Scan(&role, &actorSubject)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InteractionOutcome{}, ErrUnknownInstallation
	}
	if err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("authorize interaction actor: %w", err)
	}

	// A trusted member's command can be the first Open Review interaction for a
	// pull request: automatic review may be disabled, or setup may still be
	// incomplete. Keep the minimal provider-scoped request record so a rejected
	// command has a durable outbox response without inventing a job or run.
	requestID, currentRunID, err := ensureInteractionRequest(ctx, tx, installation, input.Event)
	if err != nil {
		return domain.InteractionOutcome{}, err
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
	if !commandAllowed(role, input.Command) {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "actor role is not allowed to run this command")
	}
	if input.Command == "review" || input.Command == "retry" {
		triggerMode, err := reviewTriggerModeForRepository(ctx, tx, installation.TenantID, reviewConfigScopeForInstallation(installation, input.Event.Repository))
		if err != nil {
			return domain.InteractionOutcome{}, err
		}
		if triggerMode == domain.ReviewTriggerOff {
			return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "reviews are disabled by repository policy")
		}
	}

	if input.Command == "invalid" {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "invalid command; use @openreview help")
	}
	var selectedRuleSetID *uuid.UUID
	if input.Command == "review" && strings.TrimSpace(input.RuleSetID) != "" {
		parsed, parseErr := uuid.Parse(input.RuleSetID)
		if parseErr != nil || parsed == uuid.Nil {
			return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "rule set id is invalid")
		}
		selectedRuleSetID = &parsed
	}
	if input.Command == "review" || input.Command == "retry" {
		setupComplete, err := workspaceSetupAllowsReview(ctx, tx, installation.TenantID)
		if err != nil {
			return domain.InteractionOutcome{}, err
		}
		if !setupComplete {
			return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "workspace setup is incomplete; complete setup in Open Review before requesting a review")
		}
	}
	selectedRunID, err := resolveInteractionRunTarget(ctx, tx, requestID, currentRunID, input.Command, input.Target)
	if err != nil {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "review run id is invalid or does not belong to this pull request")
	}
	if input.Command == "help" || input.Command == "status" || input.Command == "explain" {
		var body string
		if input.Command == "help" {
			body = "Available commands: `@openreview review [--force] [--mode=standard|deep|security] [--rule=<rule-set-id>]`, `@openreview status [run-id]`, `@openreview explain <finding-id>`, `@openreview cancel [run-id]`, and `@openreview retry [run-id]`. `--force` explicitly requests a new review after a terminal result; it never bypasses access, scope, or merge policy. A selected rule set must already be published and active for this repository and target branch."
			if link := tenantConsoleLink(ctx, tx, installation.TenantID, "/review-commands"); link != "" {
				body += "\n\n[Open the command guide in Open Review](" + link + ")"
			}
		} else if input.Command == "explain" {
			findingID, parseErr := uuid.Parse(strings.TrimSpace(input.Target))
			if parseErr != nil || findingID == uuid.Nil {
				return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "finding id is invalid or does not belong to this pull request")
			}
			var runID uuid.UUID
			var path, severity, category, findingBody, suggestion string
			err = tx.QueryRow(ctx, `
				SELECT run.id, finding.path, finding.severity, finding.category, finding.body, finding.suggestion
				FROM review_findings finding
				JOIN review_jobs job ON job.id = finding.job_id
				JOIN review_runs run ON run.legacy_job_id = job.id
				WHERE finding.id = $1 AND run.request_id = $2
				ORDER BY run.created_at DESC
				LIMIT 1`, findingID, requestID).Scan(&runID, &path, &severity, &category, &findingBody, &suggestion)
			if errors.Is(err, pgx.ErrNoRows) {
				return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "finding id is invalid or does not belong to this pull request")
			}
			if err != nil {
				return domain.InteractionOutcome{}, fmt.Errorf("load finding explanation: %w", err)
			}
			body = fmt.Sprintf("### Finding explanation\n\n`%s` — **%s · %s**\n\n%s\n\n**Suggested change:** %s", path, severity, category, normalizeReviewRequestMetadata(findingBody, 1800), normalizeReviewRequestMetadata(suggestion, 1200))
			if link := tenantConsoleLink(ctx, tx, installation.TenantID, "/reviews/"+runID.String()+"?tab=findings&finding="+findingID.String()+"#finding-"+findingID.String()); link != "" {
				body += "\n\n[Open this finding in Open Review](" + link + ")"
			}
		} else if selectedRunID == nil {
			body = "There is no review run for this pull request yet."
		} else {
			var state domain.RunState
			if err := tx.QueryRow(ctx, `SELECT state FROM review_runs WHERE id = $1`, *selectedRunID).Scan(&state); err != nil {
				return domain.InteractionOutcome{}, fmt.Errorf("load review status: %w", err)
			}
			body = fmt.Sprintf("Review run `%s` is currently **%s**.", selectedRunID.String(), state)
			if link := tenantConsoleLink(ctx, tx, installation.TenantID, "/reviews/"+selectedRunID.String()); link != "" {
				body += "\n\n[Open this review in Open Review](" + link + ")"
			}
		}
		if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, selectedRunID, false, body); err != nil {
			return domain.InteractionOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.InteractionOutcome{}, fmt.Errorf("commit read-only interaction: %w", err)
		}
		return domain.InteractionOutcome{Accepted: true, RunID: selectedRunID, Reason: "command accepted"}, nil
	}
	if input.Command == "review" && selectedRunID == nil {
		mode := domain.ReviewMode(input.Mode)
		if mode == "" {
			mode = domain.ReviewModeConfigured
		}
		if !mode.Valid() {
			return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "review mode is invalid")
		}
		if strings.TrimSpace(input.Event.CloneURL) == "" {
			return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "provider clone metadata is unavailable; retry after the pull request is synchronized")
		}
		mode, err = resolveConfiguredReviewMode(ctx, tx, installation.TenantID, reviewConfigScopeForInstallation(installation, input.Event.Repository), mode)
		if err != nil {
			return domain.InteractionOutcome{}, err
		}
		return deferProviderInteractionAdmission(ctx, tx, installation, input.Event, interactionID, actorSubject, mode, selectedRuleSetID)
	}
	if selectedRunID == nil {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "no review run is available for this pull request")
	}
	current, err := scanReviewRun(tx.QueryRow(ctx, `
		SELECT id, request_id, legacy_job_id, revision, state, trigger_kind, review_mode, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, rule_snapshot_id, created_at, started_at, finished_at
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
		if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, &current.ID, false, ""); err != nil {
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

	mode := current.ReviewMode
	if input.Command == "review" {
		mode = domain.ReviewMode(input.Mode)
		if mode == "" {
			mode = domain.ReviewModeConfigured
		}
	}
	if !mode.Valid() {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "review mode is invalid")
	}
	if input.Command == "review" {
		mode, err = resolveConfiguredReviewMode(ctx, tx, installation.TenantID, reviewConfigScopeForInstallation(installation, input.Event.Repository), mode)
		if err != nil {
			return domain.InteractionOutcome{}, err
		}
		// A fresh review always targets the provider's current immutable
		// revision. Reusing the previous run's SHAs races a branch update and
		// causes the runner's freshness guard to supersede the command before it
		// can publish. The admission worker performs this provider read after the
		// visible acknowledgement, keeping webhooks fast and credentials out of
		// the request transaction.
		return deferProviderInteractionAdmission(ctx, tx, installation, input.Event, interactionID, actorSubject, mode, selectedRuleSetID)
	}
	triggerKind, err := interactionTriggerKind(input.Command)
	if err != nil {
		return domain.InteractionOutcome{}, err
	}
	run, err := createCommentRun(ctx, tx, requestID, current, installation, input.Event, triggerKind, mode, selectedRuleSetID, interactionID)
	if errors.Is(err, ErrSelectedRuleSetUnavailable) {
		return rejectInteraction(ctx, tx, installation, input.Event, interactionID, "selected rule set is not active for this repository and target branch")
	}
	if err != nil {
		return domain.InteractionOutcome{}, err
	}
	if input.Command == "retry" {
		if err := resolveReviewInterventionForRetry(ctx, tx, installation.TenantID, current.ID, run.ID, actorSubject); err != nil {
			return domain.InteractionOutcome{}, err
		}
	}
	releaseRun := run.State == domain.RunAcknowledged
	if err := acceptInteraction(ctx, tx, installation, input.Event, interactionID, &run.ID, releaseRun, ""); err != nil {
		return domain.InteractionOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("commit review interaction: %w", err)
	}
	return domain.InteractionOutcome{Accepted: true, RunID: &run.ID, Reason: "review acknowledged"}, nil
}

func ensureInteractionRequest(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent) (uuid.UUID, *uuid.UUID, error) {
	var requestID uuid.UUID
	var currentRunID *uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO review_requests (
			tenant_id, installation_id, provider, api_base_url, repository, review_number
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, installation_id, repository, review_number) DO UPDATE
		SET updated_at = now()
		RETURNING id, current_run_id`,
		installation.TenantID, installation.ID, event.Provider, installation.APIBaseURL,
		event.Repository, event.ReviewNumber,
	).Scan(&requestID, &currentRunID)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("upsert interaction request: %w", err)
	}
	return requestID, currentRunID, nil
}

// deferProviderInteractionAdmission acknowledges a new @openreview review
// without making the webhook handler call a provider. A dedicated worker later
// reads the current PR/MR SHA and creates the run; the source reaction is
// reconfirmed before that run is released to the acknowledger.
func deferProviderInteractionAdmission(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, actorSubject string, mode domain.ReviewMode, ruleSetID *uuid.UUID) (domain.InteractionOutcome, error) {
	if _, err := tx.Exec(ctx, `UPDATE review_interactions SET result='accepted' WHERE id=$1`, interactionID); err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("accept initial interaction: %w", err)
	}
	admission := domain.InteractionAdmission{InteractionID: interactionID, Event: event, Mode: mode, RuleSetID: ruleSetID, ActorSubject: actorSubject, CredentialRef: installation.CredentialRef}
	if err := queueInitialInteractionAcknowledgement(ctx, tx, installation, event, interactionID, admission); err != nil {
		return domain.InteractionOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InteractionOutcome{}, fmt.Errorf("commit initial interaction acknowledgement: %w", err)
	}
	return domain.InteractionOutcome{Accepted: true, Reason: "review admission acknowledged"}, nil
}

func commandAllowed(role, command string) bool {
	if command == "help" || command == "status" || command == "explain" || command == "invalid" {
		return role == "owner" || role == "admin" || role == "rule_admin" || role == "reviewer" || role == "viewer"
	}
	return role == "owner" || role == "admin" || role == "rule_admin" || role == "reviewer"
}

// interactionTriggerKind translates a user command into the stable, database
// constrained provenance kind used by review runs. A new review is a comment
// trigger; only retry has its own lifecycle trigger kind.
func interactionTriggerKind(command string) (string, error) {
	switch command {
	case "review":
		return "comment", nil
	case "retry":
		return "retry", nil
	default:
		return "", fmt.Errorf("unsupported run-creating interaction command %q", command)
	}
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
	if body == "" {
		return queueInteractionResponseWithKeyMode(ctx, tx, installation, event, interactionID, "", domain.InteractionReactionEyes, releaseRunID, "interaction:"+interactionID.String()+":response", true)
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
	// Both supported providers expose reaction APIs for issue/MR notes.
	// Review/retry commands use it as the sole initial acknowledgement;
	// other commands may also publish a visible marker-keyed reply.
	if provider != domain.ProviderGitHub && provider != domain.ProviderGitLab {
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
	if err := finalizeReviewUsage(ctx, tx, run.ID, false); err != nil {
		return err
	}
	now := time.Now().UTC()
	run.State, run.CancelRequestedAt, run.FinishedAt = domain.RunCancelled, &now, &now
	return nil
}

func queueInteractionResponse(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, body string, reaction domain.InteractionReaction, releaseRunID *uuid.UUID) error {
	return queueInteractionResponseWithKey(ctx, tx, installation, event, interactionID, body, reaction, releaseRunID, "interaction:"+interactionID.String()+":response")
}

func queueInteractionResponseWithKey(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, body string, reaction domain.InteractionReaction, releaseRunID *uuid.UUID, dedupeKey string) error {
	return queueInteractionResponseWithKeyMode(ctx, tx, installation, event, interactionID, body, reaction, releaseRunID, dedupeKey, false)
}

func queueInteractionResponseWithKeyMode(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, body string, reaction domain.InteractionReaction, releaseRunID *uuid.UUID, dedupeKey string, reactionOnly bool) error {
	markerSince, err := interactionMarkerSince(ctx, tx, interactionID)
	if err != nil {
		return err
	}
	payload := map[string]any{
		// Provider OAuth credentials are tenant-scoped. Every response can be
		// consumed after the original admission transaction, so it must carry
		// that boundary as well as the credential reference.
		"tenant_id":                installation.TenantID.String(),
		"provider":                 installation.Provider,
		"api_base_url":             installation.APIBaseURL,
		"installation_external_id": installation.ExternalID,
		"credential_ref":           installation.CredentialRef,
		"repository":               event.Repository,
		"resource_kind":            "merge_request",
		"review_number":            event.ReviewNumber,
		"comment_external_id":      event.CommentExternalID,
		"reaction":                 reaction,
		"body":                     body,
		"marker":                   interactionResponseMarker(interactionID),
		"marker_since":             markerSince.Format(time.RFC3339Nano),
	}
	if reactionOnly {
		payload["reaction_only"] = true
	}
	if releaseRunID != nil {
		payload["release_run_id"] = releaseRunID.String()
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_messages (aggregate_type, aggregate_id, topic, dedupe_key, payload)
		VALUES ('review_interaction', $1, 'review.interaction.response', $2, $3::jsonb)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		interactionID, dedupeKey, jsonPayload(payload))
	if err != nil {
		return fmt.Errorf("queue interaction response: %w", err)
	}
	return nil
}

func interactionMarkerSince(ctx context.Context, tx pgx.Tx, interactionID uuid.UUID) (time.Time, error) {
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM review_interactions WHERE id=$1`, interactionID).Scan(&createdAt); err != nil {
		return time.Time{}, fmt.Errorf("load interaction marker boundary: %w", err)
	}
	return createdAt.UTC(), nil
}

func interactionResponseMarker(interactionID uuid.UUID) string {
	return "open-review-platform:interaction:" + interactionID.String()
}

func queueInitialInteractionAcknowledgement(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.CommentEvent, interactionID uuid.UUID, admission domain.InteractionAdmission) error {
	if admission.InteractionID != interactionID || !interactionEventMatchesInstallation(installation, admission.Event) || admission.Event.Repository != event.Repository || admission.Event.ReviewNumber != event.ReviewNumber || !admission.Mode.Valid() || strings.TrimSpace(admission.ActorSubject) == "" || strings.TrimSpace(admission.CredentialRef) == "" {
		return ErrConflict
	}
	markerSince, err := interactionMarkerSince(ctx, tx, interactionID)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"tenant_id":                   installation.TenantID.String(),
		"provider":                    installation.Provider,
		"api_base_url":                installation.APIBaseURL,
		"installation_external_id":    installation.ExternalID,
		"credential_ref":              installation.CredentialRef,
		"repository":                  event.Repository,
		"resource_kind":               "merge_request",
		"review_number":               event.ReviewNumber,
		"comment_external_id":         event.CommentExternalID,
		"reaction":                    interactionReaction(installation.Provider, true),
		"body":                        "",
		"reaction_only":               true,
		"marker":                      "open-review-platform:interaction:" + interactionID.String(),
		"marker_since":                markerSince.Format(time.RFC3339Nano),
		"admission_interaction_id":    interactionID.String(),
		"admission_clone_url":         admission.Event.CloneURL,
		"admission_actor_external_id": admission.Event.ActorExternalID,
		"admission_actor_subject":     admission.ActorSubject,
		"admission_mode":              admission.Mode,
	}
	if admission.RuleSetID != nil {
		payload["admission_rule_set_id"] = admission.RuleSetID.String()
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_messages (aggregate_type, aggregate_id, topic, dedupe_key, payload)
		VALUES ('review_interaction', $1, 'review.interaction.response', $2, $3::jsonb)`,
		interactionID, "interaction:"+interactionID.String()+":response", jsonPayload(payload))
	if err != nil {
		return fmt.Errorf("queue initial interaction acknowledgement: %w", err)
	}
	return nil
}

// interactionEventMatchesInstallation keeps GitHub's App installation ID as
// an exact boundary. GitLab Note Hooks identify the project instead, while a
// GitLab installation record has an opaque identity for its approved scope;
// resolveInboundInstallation has already verified that scope before an
// interaction reaches this acknowledgement path.
func interactionEventMatchesInstallation(installation domain.Installation, event domain.CommentEvent) bool {
	if event.Provider != installation.Provider || strings.TrimSpace(event.InstallationExternalID) == "" {
		return false
	}
	return installation.Provider != domain.ProviderGitHub || event.InstallationExternalID == installation.ExternalID
}

// ReleaseInitialInteractionAdmission is invoked by the response worker only
// after the provider has accepted the initial marker-keyed reply. It turns the
// stored command context into the next durable hop without relying on broker
// ordering across the response and admission queues.
func (s *PostgresStore) ReleaseInitialInteractionAdmission(ctx context.Context, admission domain.InteractionAdmission) error {
	if admission.InteractionID == uuid.Nil || !admission.Event.Provider.Valid() || strings.TrimSpace(admission.Event.APIBaseURL) == "" || strings.TrimSpace(admission.Event.InstallationExternalID) == "" || strings.TrimSpace(admission.Event.Repository) == "" || strings.TrimSpace(admission.Event.CloneURL) == "" || admission.Event.ReviewNumber < 1 || strings.TrimSpace(admission.Event.CommentExternalID) == "" || strings.TrimSpace(admission.Event.ActorExternalID) == "" || !admission.Mode.Valid() || strings.TrimSpace(admission.ActorSubject) == "" || strings.TrimSpace(admission.CredentialRef) == "" {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin initial interaction release: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	installation, err := resolveInboundInstallation(ctx, tx, admission.Event.Provider, admission.Event.APIBaseURL, admission.Event.InstallationExternalID, admission.Event.Repository)
	if err != nil {
		return err
	}
	if installation.CredentialRef != admission.CredentialRef {
		return ErrConflict
	}
	var interactionExists bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM review_interactions interaction
			JOIN review_requests request ON request.id=interaction.request_id
			WHERE interaction.id=$1 AND interaction.result='accepted' AND interaction.result_run_id IS NULL
			  AND interaction.provider=$2 AND interaction.actor_external_id=$3
			  AND request.tenant_id=$4 AND request.installation_id=$5
			  AND request.repository=$6 AND request.review_number=$7
		)`, admission.InteractionID, admission.Event.Provider, admission.Event.ActorExternalID,
		installation.TenantID, installation.ID, admission.Event.Repository, admission.Event.ReviewNumber).Scan(&interactionExists)
	if err != nil {
		return fmt.Errorf("check initial interaction release: %w", err)
	}
	if !interactionExists {
		// The responder may be redelivered after it already released this
		// admission and the worker linked a run (or posted a terminal failure).
		// The unique outbox key is the durable idempotency boundary.
		return tx.Commit(ctx)
	}
	payload := map[string]any{
		"interaction_id":           admission.InteractionID.String(),
		"tenant_id":                installation.TenantID.String(),
		"provider":                 installation.Provider,
		"api_base_url":             installation.APIBaseURL,
		"installation_external_id": installation.ExternalID,
		"credential_ref":           installation.CredentialRef,
		"repository":               admission.Event.Repository,
		"clone_url":                admission.Event.CloneURL,
		"review_number":            admission.Event.ReviewNumber,
		"comment_external_id":      admission.Event.CommentExternalID,
		"actor_external_id":        admission.Event.ActorExternalID,
		"actor_subject":            admission.ActorSubject,
		"mode":                     admission.Mode,
	}
	if admission.RuleSetID != nil {
		payload["rule_set_id"] = admission.RuleSetID.String()
	}
	if err := insertOutbox(ctx, tx, admission.InteractionID, "review.interaction.admission", "interaction:"+admission.InteractionID.String()+":admission", payload); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit initial interaction release: %w", err)
	}
	return nil
}

// AdmitInitialInteractionReview turns a previously acknowledged first-review
// command into a run only after the worker has read the provider's current
// immutable revision. The initial source reaction is written before this
// method is called; the follow-up idempotent reaction releases the run only
// after the provider accepts it.
func (s *PostgresStore) AdmitInitialInteractionReview(ctx context.Context, interactionID uuid.UUID, comment domain.CommentEvent, event domain.InboundEvent) (domain.ReviewRun, bool, error) {
	if interactionID == uuid.Nil || event.TriggerKind != "comment" || !event.DeferAcknowledgement || event.DeliveryID != "interaction-admission:"+interactionID.String() || event.Provider != comment.Provider || event.Repository != comment.Repository || event.ReviewNumber != comment.ReviewNumber || event.InstallationExternalID != comment.InstallationExternalID || strings.TrimSpace(event.CloneURL) == "" || strings.TrimSpace(event.BaseRef) == "" || strings.TrimSpace(event.BaseSHA) == "" || strings.TrimSpace(event.HeadRef) == "" || strings.TrimSpace(event.HeadSHA) == "" || !event.ReviewMode.Valid() {
		return domain.ReviewRun{}, false, ErrConflict
	}
	job, duplicate, err := s.Enqueue(ctx, event)
	if err != nil {
		return domain.ReviewRun{}, false, err
	}
	if job.State == domain.JobCancelled {
		if strings.Contains(job.ErrorMessage, "workspace setup is incomplete") {
			return domain.ReviewRun{}, false, ErrWorkspaceSetupIncomplete
		}
		return domain.ReviewRun{}, false, ErrConflict
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewRun{}, false, fmt.Errorf("begin initial interaction attachment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	installation, err := resolveInboundInstallation(ctx, tx, comment.Provider, comment.APIBaseURL, comment.InstallationExternalID, comment.Repository)
	if err != nil {
		return domain.ReviewRun{}, false, err
	}
	var requestID uuid.UUID
	var existingRunID *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT interaction.request_id, interaction.result_run_id
		FROM review_interactions interaction
		JOIN review_requests request ON request.id=interaction.request_id
		WHERE interaction.id=$1 AND interaction.result='accepted'
		  AND request.tenant_id=$2 AND request.installation_id=$3
		  AND request.repository=$4 AND request.review_number=$5
		FOR UPDATE`, interactionID, installation.TenantID, installation.ID, comment.Repository, comment.ReviewNumber).Scan(&requestID, &existingRunID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewRun{}, false, ErrNotFound
	}
	if err != nil {
		return domain.ReviewRun{}, false, fmt.Errorf("load initial interaction: %w", err)
	}

	var run domain.ReviewRun
	if existingRunID != nil {
		run, err = scanReviewRun(tx.QueryRow(ctx, `
			SELECT id, request_id, legacy_job_id, revision, state, trigger_kind, review_mode, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, rule_snapshot_id, created_at, started_at, finished_at
			FROM review_runs WHERE id=$1 AND request_id=$2`, *existingRunID, requestID))
	} else {
		run, err = scanReviewRun(tx.QueryRow(ctx, `
			SELECT run.id, run.request_id, run.legacy_job_id, run.revision, run.state, run.trigger_kind, run.review_mode, run.head_sha, run.base_sha, run.cancel_requested_at, run.superseded_by, run.failure_code, run.failure_message, run.rule_snapshot_id, run.created_at, run.started_at, run.finished_at
			FROM review_runs run
			JOIN review_jobs job ON job.id=run.legacy_job_id
			JOIN webhook_deliveries delivery ON delivery.id=job.delivery_id
			WHERE delivery.provider=$1 AND delivery.delivery_id=$2 AND run.request_id=$3`, event.Provider, event.DeliveryID, requestID))
		if errors.Is(err, pgx.ErrNoRows) && duplicate {
			err = tx.QueryRow(ctx, `SELECT current_run_id FROM review_requests WHERE id=$1`, requestID).Scan(&existingRunID)
			if err == nil && existingRunID != nil {
				run, err = scanReviewRun(tx.QueryRow(ctx, `
					SELECT id, request_id, legacy_job_id, revision, state, trigger_kind, review_mode, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, rule_snapshot_id, created_at, started_at, finished_at
					FROM review_runs WHERE id=$1 AND request_id=$2`, *existingRunID, requestID))
			}
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewRun{}, false, ErrConflict
	}
	if err != nil {
		return domain.ReviewRun{}, false, fmt.Errorf("load admitted initial interaction run: %w", err)
	}
	if existingRunID == nil {
		if _, err := tx.Exec(ctx, `UPDATE review_interactions SET result_run_id=$2 WHERE id=$1`, interactionID, run.ID); err != nil {
			return domain.ReviewRun{}, false, fmt.Errorf("link initial interaction run: %w", err)
		}
	}
	if err := queueInteractionResponseWithKeyMode(ctx, tx, installation, comment, interactionID,
		"", domain.InteractionReactionEyes,
		func() *uuid.UUID {
			if run.State == domain.RunAcknowledged {
				return &run.ID
			}
			return nil
		}(), "interaction:"+interactionID.String()+":admitted:"+run.ID.String(), true); err != nil {
		return domain.ReviewRun{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewRun{}, false, fmt.Errorf("commit initial interaction admission: %w", err)
	}
	// A provider redelivery must return the durable run for observability, but
	// only the first admission can require a subsequent acknowledgement release.
	return run, run.State == domain.RunAcknowledged && !duplicate, nil
}

// FailInitialInteractionAdmission publishes a safe, actionable failure after
// the initial reaction. The worker deliberately does not persist a
// provider response body or credential-derived detail.
func (s *PostgresStore) FailInitialInteractionAdmission(ctx context.Context, interactionID uuid.UUID, comment domain.CommentEvent, reason string) error {
	if interactionID == uuid.Nil || strings.TrimSpace(reason) == "" {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin initial interaction failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	installation, err := resolveInboundInstallation(ctx, tx, comment.Provider, comment.APIBaseURL, comment.InstallationExternalID, comment.Repository)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE review_interactions SET result='rejected' WHERE id=$1 AND result='accepted' AND result_run_id IS NULL`, interactionID)
	if err != nil {
		return fmt.Errorf("reject initial interaction: %w", err)
	}
	if command.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if err := queueInteractionResponseWithKey(ctx, tx, installation, comment, interactionID, "Unable to queue this review: "+reason, interactionReaction(installation.Provider, false), nil, "interaction:"+interactionID.String()+":admission-failed"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit initial interaction failure: %w", err)
	}
	return nil
}

// createCommentRun deliberately creates a new queueable job.  A command-triggered
// review must not merely create an audit run: without its own review_jobs row the
// runner would never claim it.  The last run's job supplies the immutable clone
// and ref metadata, while the issue_comment delivery makes the retry auditable.
func createCommentRun(ctx context.Context, tx pgx.Tx, requestID uuid.UUID, current domain.ReviewRun, installation domain.Installation, event domain.CommentEvent, triggerKind string, mode domain.ReviewMode, selectedRuleSetID *uuid.UUID, interactionID uuid.UUID) (domain.ReviewRun, error) {
	if current.LegacyJobID == nil {
		return domain.ReviewRun{}, fmt.Errorf("current review run has no execution job")
	}
	if selectedRuleSetID != nil {
		var baseRef string
		if err := tx.QueryRow(ctx, `SELECT base_ref FROM review_jobs WHERE id = $1 AND repository = $2`, *current.LegacyJobID, event.Repository).Scan(&baseRef); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ReviewRun{}, ErrSelectedRuleSetUnavailable
			}
			return domain.ReviewRun{}, fmt.Errorf("load selected rule set review target: %w", err)
		}
		active, err := ruleSetActiveForTarget(ctx, tx, installation.TenantID, reviewConfigScopeForInstallation(installation, event.Repository), baseRef, *selectedRuleSetID)
		if err != nil {
			return domain.ReviewRun{}, err
		}
		if !active {
			return domain.ReviewRun{}, ErrSelectedRuleSetUnavailable
		}
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
		RETURNING id, tenant_id, (SELECT slug FROM tenants WHERE id = review_jobs.tenant_id), installation_id, $5::text, $6::text, delivery_id, provider, api_base_url, repository, clone_url,
		          review_number, base_ref, base_sha, head_ref, head_sha, state, attempts,
		          locked_by, locked_until, error_message, created_at, started_at, finished_at`,
		deliveryID, current.BaseSHA, current.HeadSHA, *current.LegacyJobID, installation.ExternalID, installation.CredentialRef))
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("queue comment review job: %w", err)
	}

	var rolloutSelections []ruleRolloutRunSelection
	ruleSnapshotID, err := resolveRuleSnapshotWithCanary(ctx, tx, installation.TenantID, reviewConfigScopeForInstallation(installation, job.Repository), job.BaseRef, selectedRuleSetID, job.ReviewNumber, &rolloutSelections)
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("resolve comment rule snapshot: %w", err)
	}
	run, err := scanReviewRun(tx.QueryRow(ctx, `
		INSERT INTO review_runs (request_id, legacy_job_id, state, trigger_kind, review_mode, head_sha, base_sha, rule_snapshot_id)
		VALUES ($1, $2, 'acknowledged', $3, $4, $5, $6, $7)
		RETURNING id, request_id, legacy_job_id, revision, state, trigger_kind, review_mode, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, rule_snapshot_id, created_at, started_at, finished_at`, requestID, job.ID, triggerKind, mode, current.HeadSHA, current.BaseSHA, ruleSnapshotID))
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("create comment review run: %w", err)
	}
	if err := recordRuleRolloutRunSelections(ctx, tx, run.ID, rolloutSelections); err != nil {
		return domain.ReviewRun{}, err
	}
	if err := snapshotReviewConfigurations(ctx, tx, run.ID, installation.TenantID, reviewConfigScopeForInstallation(installation, job.Repository)); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("snapshot comment review configuration: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE review_requests SET current_run_id = $2, updated_at = now() WHERE id = $1`, requestID, run.ID); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("set comment review current run: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO review_run_stages (run_id, stage, state) SELECT $1, stage, 'pending' FROM unnest(ARRAY['ack', 'admit', 'prepare', 'analyze', 'normalize', 'publish']) AS stage`, run.ID); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("initialize comment run stages: %w", err)
	}
	if err := appendRunEvent(ctx, tx, run.ID, run.Revision, "run.acknowledged", "user", "", map[string]any{"interaction_id": interactionID.String(), "trigger": triggerKind, "review_mode": mode}); err != nil {
		return domain.ReviewRun{}, err
	}
	if _, err := admitRunUsage(ctx, tx, installation.TenantID, job.Repository, &run); err != nil {
		return domain.ReviewRun{}, err
	}
	return run, nil
}

// admitRunUsage runs in the same transaction that creates the review. A hard
// quota never leaves a queued job behind: the run remains visible and audited,
// but is terminal before any worker can claim it.
func admitRunUsage(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, repository string, run *domain.ReviewRun) (bool, error) {
	err := reserveReviewUsage(ctx, tx, tenantID, run.ID, repository)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, ErrQuotaExceeded) {
		return false, err
	}
	run.Revision++
	message := "monthly review quota exhausted before execution"
	if _, err := tx.Exec(ctx, `
		UPDATE review_runs
		SET state = 'failed', revision = $2, failure_code = 'quota_exceeded', failure_message = $3, finished_at = now()
		WHERE id = $1`, run.ID, run.Revision, message); err != nil {
		return false, fmt.Errorf("reject quota-exhausted run: %w", err)
	}
	if run.LegacyJobID != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE review_jobs
			SET state = 'failed', error_message = $2, finished_at = now(), locked_by = NULL, locked_until = NULL
			WHERE id = $1`, *run.LegacyJobID, message); err != nil {
			return false, fmt.Errorf("reject quota-exhausted job: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE review_run_stages
		SET state = 'skipped', finished_at = now(), details = jsonb_build_object('reason', 'quota_exceeded')
		WHERE run_id = $1 AND state = 'pending'`, run.ID); err != nil {
		return false, fmt.Errorf("skip quota-exhausted stages: %w", err)
	}
	if err := appendRunEvent(ctx, tx, run.ID, run.Revision, "run.failed", "system", "usage-admission", map[string]any{"failure_code": "quota_exceeded"}); err != nil {
		return false, err
	}
	if err := insertOutbox(ctx, tx, run.ID, "review.run.failed", "run:"+run.ID.String()+fmt.Sprintf(":%d:failed", run.Revision), map[string]any{"run_id": run.ID.String(), "revision": run.Revision, "failure_code": "quota_exceeded"}); err != nil {
		return false, err
	}
	if err := ensureReviewIntervention(ctx, tx, run.ID, domain.RunFailed); err != nil {
		return false, err
	}
	now := time.Now().UTC()
	run.State, run.FailureCode, run.FailureMessage, run.FinishedAt = domain.RunFailed, "quota_exceeded", message, &now
	return false, nil
}

// ReleaseAcknowledgedRun is the response-to-execution barrier for a command
// trigger. The interaction responder calls it only after the provider has
// accepted the source-comment reaction. A retry is safe because the durable
// outbox key is unique.
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
	if err := row.Scan(&run.ID, &run.RequestID, &run.LegacyJobID, &run.Revision, &run.State, &run.TriggerKind, &run.ReviewMode, &run.HeadSHA, &run.BaseSHA, &run.CancelRequestedAt, &run.SupersededBy, &failureCode, &failureMessage, &run.RuleSnapshotID, &run.CreatedAt, &run.StartedAt, &run.FinishedAt); err != nil {
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
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at,
		       request.provider, request.api_base_url, request.repository, request.review_number, request.title, request.author
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

// ListWorkQueue returns a bounded, filter-bound page of runs that require
// operational attention. It reads durable run state on the server so a busy
// tenant is never reduced to whichever rows happened to fit in a browser's
// initial history response.
const (
	workQueueActiveState       = "r.state IN ('acknowledged', 'admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')"
	workQueueInstallationReady = "installation.active = TRUE AND installation.verification_state IN ('legacy', 'verified')"
	// A comment run cannot skip its visible provider acknowledgement. A reply
	// whose final broker delivery was exhausted stays actionable even after the
	// installation is repaired; the database polling runner cannot claim it.
	workQueueExhaustedResponse = `r.state='acknowledged' AND r.trigger_kind IN ('comment','retry') AND EXISTS (
		SELECT 1 FROM review_interactions interaction
		JOIN LATERAL (
			SELECT response.id FROM outbox_messages response
			WHERE response.aggregate_id=interaction.id
			  AND response.topic='review.interaction.response'
			  AND response.payload->>'release_run_id'=r.id::text
			ORDER BY response.created_at DESC,response.id DESC LIMIT 1
		) latest ON TRUE
		JOIN inbox_messages inbox ON inbox.message_id=latest.id
		  AND inbox.consumer='interaction-responder-v1'
		WHERE interaction.result_run_id=r.id AND interaction.request_id=r.request_id
		  AND interaction.result='accepted'
		  AND interaction.command IN ('review','retry')
		  AND interaction.id=(
			SELECT selected.id FROM review_interactions selected
			WHERE selected.result_run_id=r.id AND selected.request_id=r.request_id
			  AND selected.result='accepted' AND selected.command IN ('review','retry')
			ORDER BY selected.created_at DESC,selected.id DESC LIMIT 1
		  )
		  AND inbox.state='released' AND inbox.attempt>=6
	)`
)

func (s *PostgresStore) ListWorkQueue(ctx context.Context, actor, tenantSlug string, filter domain.WorkQueueFilter) (domain.WorkQueuePage, error) {
	if !filter.Valid() {
		return domain.WorkQueuePage{}, ErrInvalidWorkQueueFilter
	}
	createdAt, cursorID, hasCursor, err := domain.DecodeWorkQueueCursor(filter)
	if err != nil {
		return domain.WorkQueuePage{}, ErrInvalidWorkQueueFilter
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.WorkQueuePage{}, err
	}
	counts, err := s.workQueueCounts(ctx, tenantID)
	if err != nil {
		return domain.WorkQueuePage{}, err
	}

	arguments := []any{tenantID}
	conditions := []string{"request.tenant_id = $1"}
	switch filter.View {
	case domain.WorkQueueRunning:
		conditions = append(conditions, workQueueActiveState, workQueueInstallationReady, "NOT ("+workQueueExhaustedResponse+")")
	case domain.WorkQueueNeedsAttention:
		conditions = append(conditions, "((r.state IN ('failed', 'needs_attention') AND intervention.id IS NOT NULL) OR ("+workQueueActiveState+" AND (NOT ("+workQueueInstallationReady+") OR ("+workQueueExhaustedResponse+"))))")
	}
	if repository := strings.TrimSpace(filter.Repository); repository != "" {
		arguments = append(arguments, repository)
		conditions = append(conditions, fmt.Sprintf("request.repository = $%d", len(arguments)))
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		arguments = append(arguments, "%"+query+"%")
		placeholder := fmt.Sprintf("$%d", len(arguments))
		conditions = append(conditions, "(request.repository ILIKE "+placeholder+" OR request.title ILIKE "+placeholder+" OR request.author ILIKE "+placeholder+" OR CAST(request.review_number AS text) ILIKE "+placeholder+" OR COALESCE(r.failure_code, '') ILIKE "+placeholder+" OR COALESCE(r.failure_message, '') ILIKE "+placeholder+")")
	}
	if hasCursor {
		arguments = append(arguments, createdAt, cursorID)
		createdPlaceholder := fmt.Sprintf("$%d", len(arguments)-1)
		idPlaceholder := fmt.Sprintf("$%d", len(arguments))
		if filter.CursorDirection == domain.WorkQueueCursorBefore {
			conditions = append(conditions, "(r.created_at > "+createdPlaceholder+" OR (r.created_at = "+createdPlaceholder+" AND r.id > "+idPlaceholder+"))")
		} else {
			conditions = append(conditions, "(r.created_at < "+createdPlaceholder+" OR (r.created_at = "+createdPlaceholder+" AND r.id < "+idPlaceholder+"))")
		}
	}
	arguments = append(arguments, filter.Limit+1)
	order := "r.created_at DESC, r.id DESC"
	if hasCursor && filter.CursorDirection == domain.WorkQueueCursorBefore {
		order = "r.created_at ASC, r.id ASC"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at,
		       request.provider, request.api_base_url, request.repository, request.review_number, request.title, request.author,
		       installation.id, installation.active, installation.verification_state,
		       (`+workQueueExhaustedResponse+`) AS acknowledgement_exhausted,
		       intervention.id, intervention.revision, intervention.state, intervention.assignee_subject,
		       intervention.opened_at, intervention.claimed_at, intervention.resolved_at,
		       intervention.resolved_by, intervention.resolution, intervention.reason,
		       intervention.created_at, intervention.updated_at
		FROM review_runs r
		JOIN review_requests request ON request.id = r.request_id
		JOIN provider_installations installation ON installation.id = request.installation_id
		LEFT JOIN review_run_interventions intervention ON intervention.run_id = r.id AND intervention.state IN ('open', 'claimed')
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY `+order+`
		LIMIT $`+strconv.Itoa(len(arguments)), arguments...)
	if err != nil {
		return domain.WorkQueuePage{}, fmt.Errorf("list work queue: %w", err)
	}
	defer rows.Close()
	runs := make([]domain.ReviewRunSummary, 0, filter.Limit)
	for rows.Next() {
		run, err := scanWorkQueueRun(rows)
		if err != nil {
			return domain.WorkQueuePage{}, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return domain.WorkQueuePage{}, fmt.Errorf("iterate work queue: %w", err)
	}
	hasMore := len(runs) > filter.Limit
	if hasMore {
		runs = runs[:filter.Limit]
	}
	if hasCursor && filter.CursorDirection == domain.WorkQueueCursorBefore {
		for left, right := 0, len(runs)-1; left < right; left, right = left+1, right-1 {
			runs[left], runs[right] = runs[right], runs[left]
		}
	}
	page := domain.WorkQueuePage{Runs: runs, Counts: counts}
	if len(runs) == 0 {
		return page, nil
	}
	if hasCursor && filter.CursorDirection == domain.WorkQueueCursorBefore {
		page.NextCursor = domain.EncodeWorkQueueCursor(filter, runs[len(runs)-1])
		if hasMore {
			page.PreviousCursor = domain.EncodeWorkQueueCursor(filter, runs[0])
		}
		return page, nil
	}
	if hasCursor {
		page.PreviousCursor = domain.EncodeWorkQueueCursor(filter, runs[0])
	}
	if hasMore {
		page.NextCursor = domain.EncodeWorkQueueCursor(filter, runs[len(runs)-1])
	}
	return page, nil
}

func (s *PostgresStore) workQueueCounts(ctx context.Context, tenantID uuid.UUID) (domain.WorkQueueCounts, error) {
	var counts domain.WorkQueueCounts
	err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE `+workQueueActiveState+` AND `+workQueueInstallationReady+` AND NOT (`+workQueueExhaustedResponse+`)),
			COUNT(*) FILTER (WHERE (`+workQueueActiveState+` AND (NOT (`+workQueueInstallationReady+`) OR (`+workQueueExhaustedResponse+`))) OR (r.state IN ('failed', 'needs_attention') AND EXISTS (
				SELECT 1 FROM review_run_interventions intervention
				WHERE intervention.run_id = r.id AND intervention.state IN ('open', 'claimed')
			)))
		FROM review_runs r
		JOIN review_requests request ON request.id = r.request_id
		JOIN provider_installations installation ON installation.id = request.installation_id
		WHERE request.tenant_id = $1`, tenantID).Scan(&counts.Running, &counts.NeedsAttention)
	if err != nil {
		return domain.WorkQueueCounts{}, fmt.Errorf("count work queue: %w", err)
	}
	return counts, nil
}

// ListPullRequests is the review-history index. Unlike ListReviewRuns, it
// resolves one current run for each durable review request before pagination;
// retries therefore update the existing pull-request row instead of crowding
// the page with historical executions.
func (s *PostgresStore) ListPullRequests(ctx context.Context, actor, tenantSlug string, filter domain.PullRequestFilter) (domain.PullRequestPage, error) {
	if !filter.Valid() {
		return domain.PullRequestPage{}, ErrInvalidPullRequestFilter
	}
	createdAt, cursorID, hasCursor, err := domain.DecodePullRequestCursor(filter)
	if err != nil {
		return domain.PullRequestPage{}, ErrInvalidPullRequestFilter
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.PullRequestPage{}, err
	}
	counts, err := s.pullRequestCounts(ctx, tenantID)
	if err != nil {
		return domain.PullRequestPage{}, err
	}

	arguments := []any{tenantID}
	conditions := []string{"TRUE"}
	switch filter.View {
	case domain.PullRequestActive:
		conditions = append(conditions, "r.state IN ('acknowledged', 'admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')")
	case domain.PullRequestAttention:
		conditions = append(conditions, "r.state IN ('failed', 'needs_attention')")
	case domain.PullRequestCompleted:
		conditions = append(conditions, "r.state IN ('completed', 'cancelled', 'superseded')")
	}
	if repository := strings.TrimSpace(filter.Repository); repository != "" {
		arguments = append(arguments, repository)
		conditions = append(conditions, fmt.Sprintf("r.repository = $%d", len(arguments)))
	}
	if query := strings.TrimPrefix(strings.TrimSpace(filter.Query), "#"); query != "" {
		arguments = append(arguments, "%"+query+"%")
		placeholder := fmt.Sprintf("$%d", len(arguments))
		conditions = append(conditions, "(r.repository ILIKE "+placeholder+" OR r.title ILIKE "+placeholder+" OR r.author ILIKE "+placeholder+" OR CAST(r.review_number AS text) ILIKE "+placeholder+" OR COALESCE(r.failure_code, '') ILIKE "+placeholder+" OR COALESCE(r.failure_message, '') ILIKE "+placeholder+")")
	}
	if hasCursor {
		arguments = append(arguments, createdAt, cursorID)
		createdPlaceholder := fmt.Sprintf("$%d", len(arguments)-1)
		idPlaceholder := fmt.Sprintf("$%d", len(arguments))
		if filter.CursorDirection == domain.WorkQueueCursorBefore {
			conditions = append(conditions, "(r.created_at > "+createdPlaceholder+" OR (r.created_at = "+createdPlaceholder+" AND r.id > "+idPlaceholder+"))")
		} else {
			conditions = append(conditions, "(r.created_at < "+createdPlaceholder+" OR (r.created_at = "+createdPlaceholder+" AND r.id < "+idPlaceholder+"))")
		}
	}
	arguments = append(arguments, filter.Limit+1)
	order := "r.created_at DESC, r.id DESC"
	if hasCursor && filter.CursorDirection == domain.WorkQueueCursorBefore {
		order = "r.created_at ASC, r.id ASC"
	}
	rows, err := s.pool.Query(ctx, currentPullRequestsSQL+`
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at,
		       r.provider, r.api_base_url, r.repository, r.review_number, r.title, r.author
		FROM current_runs r
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY `+order+`
		LIMIT $`+strconv.Itoa(len(arguments)), arguments...)
	if err != nil {
		return domain.PullRequestPage{}, fmt.Errorf("list pull requests: %w", err)
	}
	defer rows.Close()
	runs := make([]domain.ReviewRunSummary, 0, filter.Limit)
	for rows.Next() {
		run, err := scanReviewRunSummary(rows)
		if err != nil {
			return domain.PullRequestPage{}, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return domain.PullRequestPage{}, fmt.Errorf("iterate pull requests: %w", err)
	}
	hasMore := len(runs) > filter.Limit
	if hasMore {
		runs = runs[:filter.Limit]
	}
	if hasCursor && filter.CursorDirection == domain.WorkQueueCursorBefore {
		for left, right := 0, len(runs)-1; left < right; left, right = left+1, right-1 {
			runs[left], runs[right] = runs[right], runs[left]
		}
	}
	page := domain.PullRequestPage{Runs: runs, Counts: counts}
	if len(runs) == 0 {
		return page, nil
	}
	if hasCursor && filter.CursorDirection == domain.WorkQueueCursorBefore {
		page.NextCursor = domain.EncodePullRequestCursor(filter, runs[len(runs)-1])
		if hasMore {
			page.PreviousCursor = domain.EncodePullRequestCursor(filter, runs[0])
		}
		return page, nil
	}
	if hasCursor {
		page.PreviousCursor = domain.EncodePullRequestCursor(filter, runs[0])
	}
	if hasMore {
		page.NextCursor = domain.EncodePullRequestCursor(filter, runs[len(runs)-1])
	}
	return page, nil
}

const currentPullRequestsSQL = `
	WITH current_runs AS (
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at,
		       request.provider, request.api_base_url, request.repository, request.review_number, request.title, request.author
		FROM review_requests request
		JOIN review_runs r ON r.id = COALESCE(request.current_run_id, (
			SELECT candidate.id
			FROM review_runs candidate
			WHERE candidate.request_id = request.id
			ORDER BY candidate.created_at DESC, candidate.id DESC
			LIMIT 1
		))
		WHERE request.tenant_id = $1
	)`

func (s *PostgresStore) pullRequestCounts(ctx context.Context, tenantID uuid.UUID) (domain.PullRequestCounts, error) {
	var counts domain.PullRequestCounts
	err := s.pool.QueryRow(ctx, currentPullRequestsSQL+`
		SELECT
			COUNT(*) FILTER (WHERE state IN ('acknowledged', 'admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')),
			COUNT(*) FILTER (WHERE state IN ('failed', 'needs_attention')),
			COUNT(*) FILTER (WHERE state IN ('completed', 'cancelled', 'superseded')),
			COUNT(*)
		FROM current_runs`, tenantID).Scan(&counts.Active, &counts.Attention, &counts.Completed, &counts.All)
	if err != nil {
		return domain.PullRequestCounts{}, fmt.Errorf("count pull requests: %w", err)
	}
	return counts, nil
}

func (s *PostgresStore) GetReviewRun(ctx context.Context, actor, tenantSlug string, runID uuid.UUID) (domain.ReviewRunSummary, error) {
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewRunSummary{}, err
	}
	run, err := scanReviewRunSummary(s.pool.QueryRow(ctx, `
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at,
		       request.provider, request.api_base_url, request.repository, request.review_number, request.title, request.author
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

func (s *PostgresStore) GetReviewEvidence(ctx context.Context, actor, tenantSlug string, runID uuid.UUID) (domain.ReviewEvidence, error) {
	run, err := s.GetReviewRun(ctx, actor, tenantSlug, runID)
	if err != nil {
		return domain.ReviewEvidence{}, err
	}
	evidence, err := s.getReviewEvidenceForRun(ctx, run)
	if err != nil {
		return domain.ReviewEvidence{}, err
	}
	if run.State != domain.RunAcknowledged || (run.TriggerKind != "comment" && run.TriggerKind != "retry") {
		return evidence, nil
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewEvidence{}, err
	}
	if role != "owner" && role != "admin" {
		return evidence, nil
	}
	setupComplete, err := workspaceSetupAllowsReview(ctx, s.pool, tenantID)
	if err != nil {
		return domain.ReviewEvidence{}, err
	}
	if !setupComplete {
		return evidence, nil
	}
	evidence.AcknowledgementRecoveryAvailable, err = s.interactionResponseRecoveryAvailable(ctx, tenantID, runID)
	if err != nil {
		return domain.ReviewEvidence{}, err
	}
	return evidence, nil
}

func (s *PostgresStore) getReviewEvidenceForRun(ctx context.Context, run domain.ReviewRunSummary) (domain.ReviewEvidence, error) {
	runID := run.ID
	evidence := domain.ReviewEvidence{
		Run:                   run,
		RelatedRuns:           []domain.ReviewRunSummary{},
		ExecutionAttempts:     0,
		Findings:              []domain.ReviewFindingEvidence{},
		Stages:                []domain.ReviewRunStageEvidence{},
		Receipts:              []domain.PublicationReceiptEvidence{},
		ConfigurationSnapshot: []domain.ReviewConfigSnapshot{},
		Events:                []domain.RunEvent{},
	}
	providerChecks, err := s.loadProviderCheckObservation(ctx, run)
	if err != nil {
		return domain.ReviewEvidence{}, err
	}
	evidence.ProviderChecks = providerChecks

	runRows, err := s.pool.Query(ctx, `
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at,
		       request.provider, request.api_base_url, request.repository, request.review_number, request.title, request.author
		FROM review_runs r
		JOIN review_requests request ON request.id = r.request_id
		WHERE r.request_id = $1
		ORDER BY r.created_at DESC
		LIMIT 25`, run.RequestID)
	if err != nil {
		return domain.ReviewEvidence{}, fmt.Errorf("list related review runs: %w", err)
	}
	for runRows.Next() {
		related, scanErr := scanReviewRunSummary(runRows)
		if scanErr != nil {
			runRows.Close()
			return domain.ReviewEvidence{}, fmt.Errorf("scan related review run: %w", scanErr)
		}
		evidence.RelatedRuns = append(evidence.RelatedRuns, related)
	}
	if err := runRows.Err(); err != nil {
		runRows.Close()
		return domain.ReviewEvidence{}, fmt.Errorf("iterate related review runs: %w", err)
	}
	runRows.Close()

	stageRows, err := s.pool.Query(ctx, `
		SELECT id, stage, state, attempt, started_at, finished_at, details
		FROM review_run_stages
		WHERE run_id = $1
		ORDER BY CASE stage
			WHEN 'ack' THEN 1 WHEN 'admit' THEN 2 WHEN 'prepare' THEN 3
			WHEN 'analyze' THEN 4 WHEN 'normalize' THEN 5 WHEN 'publish' THEN 6
			ELSE 99 END, attempt`, runID)
	if err != nil {
		return domain.ReviewEvidence{}, fmt.Errorf("list review run stages: %w", err)
	}
	for stageRows.Next() {
		var stage domain.ReviewRunStageEvidence
		var details []byte
		if err := stageRows.Scan(&stage.ID, &stage.Stage, &stage.State, &stage.Attempt, &stage.StartedAt, &stage.FinishedAt, &details); err != nil {
			stageRows.Close()
			return domain.ReviewEvidence{}, fmt.Errorf("scan review run stage: %w", err)
		}
		stage.Details = map[string]any{}
		if len(details) > 0 {
			if err := json.Unmarshal(details, &stage.Details); err != nil {
				stageRows.Close()
				return domain.ReviewEvidence{}, fmt.Errorf("decode review run stage details: %w", err)
			}
		}
		evidence.Stages = append(evidence.Stages, stage)
	}
	if err := stageRows.Err(); err != nil {
		stageRows.Close()
		return domain.ReviewEvidence{}, fmt.Errorf("iterate review run stages: %w", err)
	}
	stageRows.Close()

	if run.LegacyJobID != nil {
		if err := s.pool.QueryRow(ctx, `SELECT attempts FROM review_jobs WHERE id = $1`, *run.LegacyJobID).Scan(&evidence.ExecutionAttempts); err != nil {
			return domain.ReviewEvidence{}, fmt.Errorf("load review execution attempts: %w", err)
		}
	}

	var plan domain.ReviewExecutionPlan
	var selectedPaths []byte
	var staticImpactSignals []byte
	var fileScopes []byte
	err = s.pool.QueryRow(ctx, `
		SELECT mode, selected_paths, deferred_files, static_impact_signals, file_scopes
		FROM review_execution_plans
		WHERE run_id = $1`, runID).Scan(&plan.Mode, &selectedPaths, &plan.DeferredFiles, &staticImpactSignals, &fileScopes)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewEvidence{}, fmt.Errorf("load review execution plan: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(selectedPaths, &plan.SelectedPaths); err != nil {
			return domain.ReviewEvidence{}, fmt.Errorf("decode review execution plan: %w", err)
		}
		if err := json.Unmarshal(staticImpactSignals, &plan.StaticImpactSignals); err != nil {
			return domain.ReviewEvidence{}, fmt.Errorf("decode review execution plan static impact signals: %w", err)
		}
		if err := json.Unmarshal(fileScopes, &plan.FileScopes); err != nil {
			return domain.ReviewEvidence{}, fmt.Errorf("decode review execution plan file scopes: %w", err)
		}
		normalized, valid := domain.NormalizeReviewExecutionPlan(plan)
		if !valid {
			return domain.ReviewEvidence{}, fmt.Errorf("stored review execution plan is invalid")
		}
		evidence.ExecutionPlan = &normalized
	}

	if run.LegacyJobID != nil {
		findingRows, err := s.pool.Query(ctx, `
			SELECT finding.id, finding.path, finding.start_line, finding.end_line,
			       finding.severity, finding.category, finding.body, finding.suggestion,
			       finding.code_excerpt, finding.code_excerpt_start_line, finding.proposed_patch,
			       finding.fingerprint, COALESCE(finding.provider_marker, ''),
			       COALESCE((
				   SELECT feedback.kind FROM finding_feedback feedback
				   WHERE feedback.finding_id = finding.id AND feedback.provider = 'console' AND feedback.retracted_at IS NULL
				   ORDER BY feedback.created_at DESC LIMIT 1
			       ), ''),
			       (SELECT count(*) FROM finding_feedback feedback WHERE feedback.finding_id = finding.id AND feedback.kind = 'useful' AND feedback.retracted_at IS NULL),
			       (SELECT count(*) FROM finding_feedback feedback WHERE feedback.finding_id = finding.id AND feedback.kind = 'false_positive' AND feedback.retracted_at IS NULL),
			       COALESCE((
				   SELECT jsonb_agg(jsonb_build_object(
				       'rule_key', attribution.rule_key,
				       'rule_version_id', version.id,
				       'rule_set_id', rule_set.id,
				       'rule_set_name', rule_set.name,
				       'version', version.version
				   ) ORDER BY attribution.rule_key, version.id)
				   FROM review_finding_rule_attributions attribution
				   JOIN rule_versions version ON version.id = attribution.rule_version_id
				   JOIN rule_sets rule_set ON rule_set.id = version.rule_set_id
				   WHERE attribution.finding_id = finding.id
			       ), '[]'::jsonb),
			       finding.created_at
			FROM review_findings finding
			WHERE finding.job_id = $1
			ORDER BY CASE finding.severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 WHEN 'low' THEN 4 ELSE 5 END,
			         finding.path, finding.start_line`, *run.LegacyJobID)
		if err != nil {
			return domain.ReviewEvidence{}, fmt.Errorf("list review findings: %w", err)
		}
		for findingRows.Next() {
			var finding domain.ReviewFindingEvidence
			var rawAttributions []byte
			if err := findingRows.Scan(
				&finding.ID, &finding.Path, &finding.StartLine, &finding.EndLine,
				&finding.Severity, &finding.Category, &finding.Body, &finding.Suggestion,
				&finding.CodeExcerpt, &finding.CodeExcerptStartLine, &finding.ProposedPatch,
				&finding.Fingerprint, &finding.ProviderMarker, &finding.Disposition,
				&finding.UsefulFeedbackCount, &finding.FalsePositiveFeedbackCount, &rawAttributions, &finding.CreatedAt,
			); err != nil {
				findingRows.Close()
				return domain.ReviewEvidence{}, fmt.Errorf("scan review finding: %w", err)
			}
			if err := json.Unmarshal(rawAttributions, &finding.RuleAttributions); err != nil {
				findingRows.Close()
				return domain.ReviewEvidence{}, fmt.Errorf("decode review finding rule attributions: %w", err)
			}
			evidence.Findings = append(evidence.Findings, finding)
		}
		if err := findingRows.Err(); err != nil {
			findingRows.Close()
			return domain.ReviewEvidence{}, fmt.Errorf("iterate review findings: %w", err)
		}
		findingRows.Close()
	}

	var mergeGate domain.ReviewMergeGateDecision
	err = s.pool.QueryRow(ctx, `
		SELECT enabled, threshold, conclusion, blocking_findings, finding_count,
		       configuration_content_sha256, origin_scope_kind, origin_scope_ref,
		       origin_revision, evaluation_version, decided_at
		FROM review_merge_gate_decisions
		WHERE run_id = $1`, runID).Scan(
		&mergeGate.Enabled, &mergeGate.Threshold, &mergeGate.Conclusion, &mergeGate.BlockingFindings, &mergeGate.FindingCount,
		&mergeGate.ConfigurationContentSHA256, &mergeGate.OriginScopeKind, &mergeGate.OriginScopeRef,
		&mergeGate.OriginRevision, &mergeGate.EvaluationVersion, &mergeGate.DecidedAt,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewEvidence{}, fmt.Errorf("load merge gate decision: %w", err)
	}
	if err == nil {
		if !mergeGate.Valid() {
			return domain.ReviewEvidence{}, fmt.Errorf("stored merge gate decision is invalid")
		}
		evidence.MergeGate = &mergeGate
	}

	receiptRows, err := s.pool.Query(ctx, `
		SELECT id, provider, receipt_kind, stable_marker, external_id, payload_hash,
		       published_at, COALESCE(last_error, ''), created_at, updated_at
		FROM publication_receipts
		WHERE run_id = $1
		ORDER BY created_at, receipt_kind, stable_marker`, runID)
	if err != nil {
		return domain.ReviewEvidence{}, fmt.Errorf("list publication receipts: %w", err)
	}
	for receiptRows.Next() {
		var receipt domain.PublicationReceiptEvidence
		if err := receiptRows.Scan(
			&receipt.ID, &receipt.Provider, &receipt.ReceiptKind, &receipt.StableMarker,
			&receipt.ExternalID, &receipt.PayloadHash, &receipt.PublishedAt,
			&receipt.LastError, &receipt.CreatedAt, &receipt.UpdatedAt,
		); err != nil {
			receiptRows.Close()
			return domain.ReviewEvidence{}, fmt.Errorf("scan publication receipt: %w", err)
		}
		evidence.Receipts = append(evidence.Receipts, receipt)
	}
	if err := receiptRows.Err(); err != nil {
		receiptRows.Close()
		return domain.ReviewEvidence{}, fmt.Errorf("iterate publication receipts: %w", err)
	}
	receiptRows.Close()

	configurationRows, err := s.pool.Query(ctx, `
		SELECT section, origin_scope_kind, origin_scope_ref, origin_scope_provider, origin_scope_api_base_url, origin_revision,
		       content_sha256, content, created_at
		FROM review_configuration_snapshots
		WHERE run_id = $1
		ORDER BY CASE section
			WHEN 'general' THEN 1 WHEN 'categories' THEN 2 WHEN 'filters' THEN 3
			WHEN 'prompts' THEN 4 WHEN 'summary' THEN 5 WHEN 'messages' THEN 6
			ELSE 99 END`, runID)
	if err != nil {
		return domain.ReviewEvidence{}, fmt.Errorf("list review configuration snapshots: %w", err)
	}
	for configurationRows.Next() {
		var snapshot domain.ReviewConfigSnapshot
		var content []byte
		if err := configurationRows.Scan(
			&snapshot.Section, &snapshot.OriginScopeKind, &snapshot.OriginScopeRef,
			&snapshot.OriginProvider, &snapshot.OriginAPIBaseURL, &snapshot.OriginRevision, &snapshot.ContentSHA256, &content, &snapshot.CreatedAt,
		); err != nil {
			configurationRows.Close()
			return domain.ReviewEvidence{}, fmt.Errorf("scan review configuration snapshot: %w", err)
		}
		snapshot.Content = append(json.RawMessage(nil), content...)
		evidence.ConfigurationSnapshot = append(evidence.ConfigurationSnapshot, snapshot)
	}
	if err := configurationRows.Err(); err != nil {
		configurationRows.Close()
		return domain.ReviewEvidence{}, fmt.Errorf("iterate review configuration snapshots: %w", err)
	}
	configurationRows.Close()

	evidence.Events, err = s.listRunEvents(ctx, runID, 0)
	if err != nil {
		return domain.ReviewEvidence{}, err
	}
	return evidence, nil
}

func (s *PostgresStore) ListRunEvents(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, afterRevision int) ([]domain.RunEvent, error) {
	if afterRevision < 0 {
		return nil, fmt.Errorf("after revision cannot be negative")
	}
	if _, err := s.GetReviewRun(ctx, actor, tenantSlug, runID); err != nil {
		return nil, err
	}
	return s.listRunEvents(ctx, runID, afterRevision)
}

func (s *PostgresStore) listRunEvents(ctx context.Context, runID uuid.UUID, afterRevision int) ([]domain.RunEvent, error) {
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
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at
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

// RequestRunRetry creates a fresh, queueable job for the exact base/head
// revision of a failed run. It deliberately does not mutate the source run:
// its evidence and terminal state remain available while the new run gets its
// own configuration snapshot, stages, usage reservation, and outbox message.
//
// The source-run row lock serializes requests for that run. Together with the
// retry-request primary key, this makes a lost HTTP response replayable without
// ever creating a second execution for the same idempotency key.
func (s *PostgresStore) RequestRunRetry(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, input domain.RunRetryInput) (domain.RunRetryResult, error) {
	input, valid := domain.NormalizeRunRetryInput(input)
	if !valid || runID == uuid.Nil {
		return domain.RunRetryResult{}, ErrInvalidRunRetry
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("begin review retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RunRetryResult{}, err
	}
	if role != "owner" && role != "admin" && role != "rule_admin" && role != "reviewer" {
		return domain.RunRetryResult{}, ErrForbidden
	}
	setupComplete, err := workspaceSetupAllowsReview(ctx, tx, tenantID)
	if err != nil {
		return domain.RunRetryResult{}, err
	}
	if !setupComplete {
		return domain.RunRetryResult{}, ErrWorkspaceSetupIncomplete
	}

	source, err := scanReviewRun(tx.QueryRow(ctx, `
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at
		FROM review_runs r
		JOIN review_requests request ON request.id = r.request_id
		WHERE r.id = $1 AND request.tenant_id = $2
		FOR UPDATE`, runID, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RunRetryResult{}, ErrNotFound
	}
	if err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("load review run for retry: %w", err)
	}

	replay, err := scanReviewRun(tx.QueryRow(ctx, `
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at
		FROM review_run_retry_requests retry
		JOIN review_runs r ON r.id = retry.retry_run_id
		WHERE retry.source_run_id = $1 AND retry.idempotency_key = $2`, source.ID, input.IdempotencyKey))
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.RunRetryResult{}, fmt.Errorf("commit replayed review retry: %w", err)
		}
		return domain.RunRetryResult{Run: replay, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.RunRetryResult{}, fmt.Errorf("lookup replayed review retry: %w", err)
	}
	if source.Revision != input.ExpectedRevision {
		return domain.RunRetryResult{}, ErrRevisionConflict
	}
	if source.State != domain.RunFailed && source.State != domain.RunNeedsAttention {
		return domain.RunRetryResult{}, ErrConflict
	}
	if source.LegacyJobID == nil {
		return domain.RunRetryResult{}, ErrConflict
	}
	var activeRunExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM review_runs
			WHERE request_id = $1
			  AND state IN ('acknowledged', 'admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
		)`, source.RequestID).Scan(&activeRunExists); err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("check active review retry: %w", err)
	}
	if activeRunExists {
		return domain.RunRetryResult{}, ErrConflict
	}

	var provider domain.Provider
	var apiBaseURL string
	err = tx.QueryRow(ctx, `
		SELECT j.provider, j.api_base_url
		FROM review_jobs j
		JOIN provider_installations installation ON installation.id = j.installation_id
		WHERE j.id = $1 AND installation.tenant_id = $2 AND installation.active = TRUE
		  AND installation.verification_state IN ('legacy', 'verified')`, *source.LegacyJobID, tenantID).Scan(&provider, &apiBaseURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RunRetryResult{}, ErrConflict
	}
	if err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("resolve retry installation: %w", err)
	}

	var deliveryID uuid.UUID
	deliveryToken := "manual-retry:" + source.ID.String() + ":" + input.IdempotencyKey
	if err := tx.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (provider, delivery_id, event_name, payload)
		VALUES ($1, $2, 'manual_retry', $3::jsonb)
		RETURNING id`, provider, deliveryToken, jsonPayload(map[string]any{
		"source_run_id": source.ID.String(), "source_revision": source.Revision, "actor": actor,
	})).Scan(&deliveryID); err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("record manual retry delivery: %w", err)
	}

	var jobID uuid.UUID
	var repository, baseRef string
	var reviewNumber int
	err = tx.QueryRow(ctx, `
		INSERT INTO review_jobs (
			tenant_id, installation_id, delivery_id, provider, api_base_url, repository, clone_url,
			review_number, base_ref, base_sha, head_ref, head_sha, state
		)
		SELECT tenant_id, installation_id, $1, provider, api_base_url, repository, clone_url,
		       review_number, base_ref, $2, head_ref, $3, 'queued'
		FROM review_jobs
		WHERE id = $4
		RETURNING id, repository, base_ref, review_number`, deliveryID, source.BaseSHA, source.HeadSHA, *source.LegacyJobID).
		Scan(&jobID, &repository, &baseRef, &reviewNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RunRetryResult{}, ErrConflict
	}
	if err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("queue manual retry job: %w", err)
	}
	var rolloutSelections []ruleRolloutRunSelection
	ruleSnapshotID, err := resolveRuleSnapshotWithCanary(ctx, tx, tenantID, domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: provider, APIBaseURL: apiBaseURL}, baseRef, nil, reviewNumber, &rolloutSelections)
	if err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("resolve manual retry rule snapshot: %w", err)
	}
	retry, err := scanReviewRun(tx.QueryRow(ctx, `
		INSERT INTO review_runs (request_id, legacy_job_id, state, trigger_kind, review_mode, head_sha, base_sha, rule_snapshot_id)
		VALUES ($1, $2, 'acknowledged', 'retry', $3, $4, $5, $6)
		RETURNING id, request_id, legacy_job_id, revision, state, trigger_kind, review_mode, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, rule_snapshot_id, created_at, started_at, finished_at`,
		source.RequestID, jobID, source.ReviewMode, source.HeadSHA, source.BaseSHA, ruleSnapshotID))
	if err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("create manual retry run: %w", err)
	}
	if err := recordRuleRolloutRunSelections(ctx, tx, retry.ID, rolloutSelections); err != nil {
		return domain.RunRetryResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_run_retry_requests (source_run_id, idempotency_key, retry_run_id, requested_by)
		VALUES ($1, $2, $3, $4)`, source.ID, input.IdempotencyKey, retry.ID, actor); err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("record manual retry idempotency: %w", err)
	}
	if err := snapshotReviewConfigurations(ctx, tx, retry.ID, tenantID, domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: provider, APIBaseURL: apiBaseURL}); err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("snapshot manual retry review configuration: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE review_requests SET current_run_id = $2, updated_at = now() WHERE id = $1`, source.RequestID, retry.ID); err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("set manual retry current run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_run_stages (run_id, stage, state)
		SELECT $1, stage, 'pending'
		FROM unnest(ARRAY['ack', 'admit', 'prepare', 'analyze', 'normalize', 'publish']) AS stage`, retry.ID); err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("initialize manual retry run stages: %w", err)
	}
	if err := appendRunEvent(ctx, tx, retry.ID, retry.Revision, "run.acknowledged", "user", actor, map[string]any{
		"trigger": "retry", "source_run_id": source.ID.String(), "source_revision": source.Revision, "review_mode": retry.ReviewMode,
	}); err != nil {
		return domain.RunRetryResult{}, err
	}
	if err := insertOutbox(ctx, tx, retry.ID, "review.run.acknowledged", "run:"+retry.ID.String()+":1:acknowledged", map[string]any{"run_id": retry.ID.String(), "revision": retry.Revision}); err != nil {
		return domain.RunRetryResult{}, err
	}
	if _, err := admitRunUsage(ctx, tx, tenantID, repository, &retry); err != nil {
		return domain.RunRetryResult{}, err
	}
	if err := resolveReviewInterventionForRetry(ctx, tx, tenantID, source.ID, retry.ID, actor); err != nil {
		return domain.RunRetryResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, 'review_run.retry_requested', $3, jsonb_build_object('source_run_id', $4::text, 'source_revision', $5::integer))`,
		tenantID, actor, retry.ID.String(), source.ID.String(), source.Revision); err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("audit manual review retry: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RunRetryResult{}, fmt.Errorf("commit manual review retry: %w", err)
	}
	return domain.RunRetryResult{Run: retry}, nil
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
		SELECT id, request_id, legacy_job_id, revision, state, trigger_kind, review_mode, head_sha, base_sha, cancel_requested_at, superseded_by, failure_code, failure_message, rule_snapshot_id, created_at, started_at, finished_at
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
	if err := advanceRunStageEvidence(ctx, tx, run.ID, run.State, next); err != nil {
		return domain.ReviewRun{}, err
	}
	if err := appendRunEvent(ctx, tx, run.ID, run.Revision, "run."+string(next), "worker", "legacy-runner", map[string]any{"legacy_job_id": jobID.String()}); err != nil {
		return domain.ReviewRun{}, err
	}
	if runStateHasBrokerConsumer(next) {
		payload := map[string]any{"run_id": run.ID.String(), "revision": run.Revision}
		if next == domain.RunAdmitted {
			// The AMQP relay maps this immutable run mode to a transport priority.
			// Consumers still authorize from the durable run and never trust this
			// payload field as a policy decision.
			payload["review_mode"] = string(run.ReviewMode)
		}
		if err := insertOutbox(ctx, tx, run.ID, "review.run."+string(next), "run:"+run.ID.String()+fmt.Sprintf(":%d:%s", run.Revision, next), payload); err != nil {
			return domain.ReviewRun{}, err
		}
	}
	if next == domain.RunFailed || next == domain.RunNeedsAttention {
		if err := ensureReviewIntervention(ctx, tx, run.ID, next); err != nil {
			return domain.ReviewRun{}, err
		}
	}
	if next.Terminal() {
		if err := finalizeReviewUsage(ctx, tx, run.ID, next == domain.RunCompleted); err != nil {
			return domain.ReviewRun{}, err
		}
	}
	run.State = next
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("commit legacy run transition: %w", err)
	}
	return run, nil
}

// Intermediate progress already has a transactional review_run_events record
// consumed by the Console SSE endpoint. Only states with an actual AMQP
// consumer belong in the outbox; publishing every internal stage with the
// mandatory flag creates an unroutable poison row that can never complete.
func runStateHasBrokerConsumer(state domain.RunState) bool {
	switch state {
	case domain.RunAcknowledged,
		domain.RunAdmitted,
		domain.RunCompleted,
		domain.RunFailed,
		domain.RunCancelled,
		domain.RunSuperseded,
		domain.RunNeedsAttention:
		return true
	default:
		return false
	}
}

func advanceRunStageEvidence(ctx context.Context, tx pgx.Tx, runID uuid.UUID, current, next domain.RunState) error {
	stages := []string{"ack", "admit", "prepare", "analyze", "normalize", "publish"}
	stateStage := map[domain.RunState]string{
		domain.RunAcknowledged: "ack",
		domain.RunAdmitted:     "admit",
		domain.RunPreparing:    "prepare",
		domain.RunAnalyzing:    "analyze",
		domain.RunNormalizing:  "normalize",
		domain.RunPublishing:   "publish",
	}
	stageIndex := func(stage string) int {
		for index, candidate := range stages {
			if candidate == stage {
				return index
			}
		}
		return -1
	}
	updateLatest := func(stage, state string, terminal bool, reason string) error {
		finished := ""
		if terminal {
			finished = ", finished_at = COALESCE(finished_at, now())"
		}
		details := ""
		arguments := []any{runID, stage, state}
		if reason != "" {
			details = ", details = details || jsonb_build_object('terminal_state', $4::text)"
			arguments = append(arguments, reason)
		}
		query := `UPDATE review_run_stages SET state = $3, started_at = COALESCE(started_at, now())` + finished + details + `
			WHERE id = (SELECT id FROM review_run_stages WHERE run_id = $1 AND stage = $2 ORDER BY attempt DESC LIMIT 1)`
		if _, err := tx.Exec(ctx, query, arguments...); err != nil {
			return fmt.Errorf("update %s stage evidence to %s: %w", stage, state, err)
		}
		return nil
	}

	if next == domain.RunCompleted {
		for _, stage := range stages {
			if err := updateLatest(stage, "succeeded", true, ""); err != nil {
				return err
			}
		}
		return nil
	}
	if next == domain.RunFailed || next == domain.RunNeedsAttention || next == domain.RunCancelled || next == domain.RunSuperseded {
		active := stateStage[current]
		activeIndex := stageIndex(active)
		for index, stage := range stages {
			state := "skipped"
			if index < activeIndex {
				state = "succeeded"
			} else if index == activeIndex && (next == domain.RunFailed || next == domain.RunNeedsAttention) {
				state = "failed"
			}
			if err := updateLatest(stage, state, true, string(next)); err != nil {
				return err
			}
		}
		return nil
	}

	active := stateStage[next]
	activeIndex := stageIndex(active)
	for index, stage := range stages {
		if index < activeIndex || (next == domain.RunAdmitted && index == activeIndex) {
			if err := updateLatest(stage, "succeeded", true, ""); err != nil {
				return err
			}
		}
	}
	if next != domain.RunAdmitted && activeIndex >= 0 {
		if err := updateLatest(active, "running", false, ""); err != nil {
			return err
		}
	}
	return nil
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
		WHERE t.slug = $1 AND m.subject = $2 AND m.active = TRUE`, tenantSlug, actor).Scan(&tenantID, &role)
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
		WHERE t.slug = $1 AND m.subject = $2 AND m.active = TRUE`, tenantSlug, actor).Scan(&tenantID, &role)
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
	if err := row.Scan(&summary.ID, &summary.RequestID, &summary.LegacyJobID, &summary.Revision, &summary.State, &summary.TriggerKind, &summary.ReviewMode, &summary.HeadSHA, &summary.BaseSHA, &summary.CancelRequestedAt, &summary.SupersededBy, &failureCode, &failureMessage, &summary.RuleSnapshotID, &summary.CreatedAt, &summary.StartedAt, &summary.FinishedAt, &summary.Provider, &summary.APIBaseURL, &summary.Repository, &summary.ReviewNumber, &summary.Title, &summary.Author); err != nil {
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

// scanWorkQueueRun is intentionally separate from scanReviewRunSummary. The
// queue is the only run index that carries the live human-intervention task;
// history and evidence pages remain immutable run projections.
func scanWorkQueueRun(row rowScanner) (domain.ReviewRunSummary, error) {
	var summary domain.ReviewRunSummary
	var failureCode, failureMessage *string
	var installationID uuid.UUID
	var installationActive bool
	var verificationState domain.InstallationVerificationState
	var acknowledgementExhausted bool
	var interventionID *uuid.UUID
	var interventionRevision *int
	var interventionState *string
	var assignee, resolvedBy, resolution, reason *string
	var openedAt, claimedAt, resolvedAt, interventionCreatedAt, interventionUpdatedAt *time.Time
	if err := row.Scan(
		&summary.ID, &summary.RequestID, &summary.LegacyJobID, &summary.Revision, &summary.State, &summary.TriggerKind, &summary.ReviewMode, &summary.HeadSHA, &summary.BaseSHA, &summary.CancelRequestedAt, &summary.SupersededBy, &failureCode, &failureMessage, &summary.RuleSnapshotID, &summary.CreatedAt, &summary.StartedAt, &summary.FinishedAt,
		&summary.Provider, &summary.APIBaseURL, &summary.Repository, &summary.ReviewNumber, &summary.Title, &summary.Author,
		&installationID, &installationActive, &verificationState, &acknowledgementExhausted,
		&interventionID, &interventionRevision, &interventionState, &assignee,
		&openedAt, &claimedAt, &resolvedAt, &resolvedBy, &resolution, &reason,
		&interventionCreatedAt, &interventionUpdatedAt,
	); err != nil {
		return domain.ReviewRunSummary{}, err
	}
	if failureCode != nil {
		summary.FailureCode = *failureCode
	}
	if failureMessage != nil {
		summary.FailureMessage = *failureMessage
	}
	if !summary.State.Terminal() && (!installationActive || !verificationState.EligibleForReview()) {
		reason := "verification_required"
		if !installationActive {
			reason = "installation_inactive"
		}
		summary.QueueBlock = &domain.ReviewQueueBlock{
			InstallationID: installationID, Reason: reason, VerificationState: verificationState,
		}
	} else if acknowledgementExhausted {
		summary.QueueBlock = &domain.ReviewQueueBlock{
			InstallationID: installationID, Reason: "acknowledgement_exhausted", VerificationState: verificationState,
		}
	}
	if interventionID != nil {
		intervention := &domain.ReviewIntervention{ID: *interventionID, RunID: summary.ID}
		if interventionRevision != nil {
			intervention.Revision = *interventionRevision
		}
		if interventionState != nil {
			intervention.State = domain.ReviewInterventionState(*interventionState)
		}
		if assignee != nil {
			intervention.AssigneeSubject = *assignee
		}
		if openedAt != nil {
			intervention.OpenedAt = *openedAt
		}
		intervention.ClaimedAt = claimedAt
		intervention.ResolvedAt = resolvedAt
		if resolvedBy != nil {
			intervention.ResolvedBy = *resolvedBy
		}
		if resolution != nil {
			intervention.Resolution = *resolution
		}
		if reason != nil {
			intervention.Reason = *reason
		}
		if interventionCreatedAt != nil {
			intervention.CreatedAt = *interventionCreatedAt
		}
		if interventionUpdatedAt != nil {
			intervention.UpdatedAt = *interventionUpdatedAt
		}
		summary.Intervention = intervention
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
			-- Keep durable recovery aligned with AMQP message priority. Without
			-- this ordering, a relay backlog can spend whole batches publishing
			-- ordinary work before it even hands an admitted security review to
			-- RabbitMQ. The broker remains the execution authority; this merely
			-- preserves the same urgency while recovering unpublished outbox rows.
			ORDER BY CASE
				WHEN topic = 'review.interaction.response' THEN 31
				WHEN topic = 'review.run.acknowledged' THEN 28
				WHEN topic = 'review.interaction.admission' THEN 24
				WHEN topic IN ('review.run.cancelled', 'review.run.superseded', 'review.run.failed', 'review.run.needs_attention') THEN 16
				WHEN topic = 'review.run.admitted' AND payload ->> 'review_mode' = 'security' THEN 8
				WHEN topic = 'external.issue.create' THEN 8
				WHEN topic = 'notification.destination.test' THEN 6
				ELSE 4
			END DESC, created_at, id
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
