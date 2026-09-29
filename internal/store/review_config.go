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

func (s *PostgresStore) GetReviewConfig(ctx context.Context, actor, tenantSlug string, section domain.ReviewConfigSection, scope domain.ReviewConfigScope) (domain.ReviewConfigView, error) {
	normalizedScope, valid := scope.Normalize()
	if !section.Valid() || !valid {
		return domain.ReviewConfigView{}, ErrInvalidReviewConfig
	}
	scope = normalizedScope
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewConfigView{}, err
	}
	view, err := s.loadReviewConfig(ctx, tenantID, section, scope)
	if err == nil {
		setRequestedReviewConfigScope(&view, scope)
		return view, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return domain.ReviewConfigView{}, err
	}
	if scope.Kind == domain.ReviewConfigRepositoryScope && scope.QualifiedRepository() {
		// Existing installations may have a raw repository override created
		// before provider-qualified scopes. It remains a lower-priority
		// compatibility fallback until an administrator publishes a scoped
		// replacement.
		legacy := scope
		legacy.Provider, legacy.APIBaseURL = "", ""
		view, err = s.loadReviewConfig(ctx, tenantID, section, legacy)
		if err == nil {
			setRequestedReviewConfigScope(&view, scope)
			// A qualified Models page can replace a legacy shared override with
			// its own scoped route. The legacy version is not that route's base.
			if section == domain.ReviewConfigModels {
				view.Inherited = true
			}
			return view, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return domain.ReviewConfigView{}, err
		}
	}
	if scope.Kind == domain.ReviewConfigRepositoryScope {
		view, err = s.loadReviewConfig(ctx, tenantID, section, domain.ReviewConfigScope{Kind: domain.ReviewConfigTenantScope})
		if err == nil {
			setRequestedReviewConfigScope(&view, scope)
			view.Inherited = true
			return view, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return domain.ReviewConfigView{}, err
		}
	}
	content := domain.DefaultReviewConfig(section)
	canonical, hash, valid := domain.CanonicalReviewConfig(section, content)
	if !valid {
		return domain.ReviewConfigView{}, fmt.Errorf("default %s review configuration is invalid", section)
	}
	return domain.ReviewConfigView{
		Section:             section,
		RequestedScopeKind:  scope.Kind,
		RequestedScopeRef:   scope.Ref,
		RequestedProvider:   scope.Provider,
		RequestedAPIBaseURL: scope.APIBaseURL,
		OriginScopeKind:     "default",
		Inherited:           scope.Kind == domain.ReviewConfigRepositoryScope,
		ContentSHA256:       hash,
		Content:             canonical,
	}, nil
}

func (s *PostgresStore) ListReviewConfigVersions(ctx context.Context, actor, tenantSlug string, section domain.ReviewConfigSection, scope domain.ReviewConfigScope, limit int) (domain.ReviewConfigHistory, error) {
	if limit < 1 || limit > 100 {
		return domain.ReviewConfigHistory{}, ErrInvalidReviewConfig
	}
	view, err := s.GetReviewConfig(ctx, actor, tenantSlug, section, scope)
	if err != nil {
		return domain.ReviewConfigHistory{}, err
	}
	history := domain.ReviewConfigHistory{
		RequestedScopeKind:  view.RequestedScopeKind,
		RequestedScopeRef:   view.RequestedScopeRef,
		RequestedProvider:   view.RequestedProvider,
		RequestedAPIBaseURL: view.RequestedAPIBaseURL,
		OriginScopeKind:     view.OriginScopeKind,
		OriginScopeRef:      view.OriginScopeRef,
		OriginProvider:      view.OriginProvider,
		OriginAPIBaseURL:    view.OriginAPIBaseURL,
		Inherited:           view.Inherited,
		Versions:            []domain.ReviewConfigVersion{},
	}
	if view.OriginScopeKind == "default" {
		return history, nil
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewConfigHistory{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT version.revision, version.content_sha256, version.created_by, version.created_at
		FROM review_configurations configuration
		JOIN review_configuration_versions version ON version.configuration_id = configuration.id
		WHERE configuration.tenant_id = $1 AND configuration.section = $2
		  AND configuration.scope_kind = $3 AND configuration.scope_ref = $4
		  AND configuration.scope_provider = $5 AND configuration.scope_api_base_url = $6
		ORDER BY version.revision DESC LIMIT $7`, tenantID, section, view.OriginScopeKind, view.OriginScopeRef, view.OriginProvider, view.OriginAPIBaseURL, limit)
	if err != nil {
		return domain.ReviewConfigHistory{}, fmt.Errorf("list review configuration versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var version domain.ReviewConfigVersion
		if err := rows.Scan(&version.Revision, &version.ContentSHA256, &version.CreatedBy, &version.CreatedAt); err != nil {
			return domain.ReviewConfigHistory{}, fmt.Errorf("scan review configuration version: %w", err)
		}
		history.Versions = append(history.Versions, version)
	}
	if err := rows.Err(); err != nil {
		return domain.ReviewConfigHistory{}, fmt.Errorf("iterate review configuration versions: %w", err)
	}
	return history, nil
}

func (s *PostgresStore) loadReviewConfig(ctx context.Context, tenantID uuid.UUID, section domain.ReviewConfigSection, scope domain.ReviewConfigScope) (domain.ReviewConfigView, error) {
	var view domain.ReviewConfigView
	var content []byte
	err := s.pool.QueryRow(ctx, `
		SELECT configuration.scope_kind, configuration.scope_ref, configuration.scope_provider, configuration.scope_api_base_url,
		       version.revision, version.content_sha256, version.content,
		       version.created_by, version.created_at
		FROM review_configurations configuration
		JOIN LATERAL (
			SELECT revision, content_sha256, content, created_by, created_at
			FROM review_configuration_versions
			WHERE configuration_id = configuration.id
			ORDER BY revision DESC LIMIT 1
		) version ON TRUE
		WHERE configuration.tenant_id = $1 AND configuration.section = $2
		  AND configuration.scope_kind = $3 AND configuration.scope_ref = $4
		  AND configuration.scope_provider = $5 AND configuration.scope_api_base_url = $6
		  AND configuration.active = TRUE`, tenantID, section, scope.Kind, scope.Ref, scope.Provider, scope.APIBaseURL).Scan(
		&view.OriginScopeKind, &view.OriginScopeRef, &view.OriginProvider, &view.OriginAPIBaseURL, &view.Revision,
		&view.ContentSHA256, &content, &view.UpdatedBy, &view.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewConfigView{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("load review configuration: %w", err)
	}
	view.Section = section
	view.Content = json.RawMessage(content)
	return view, nil
}

func setRequestedReviewConfigScope(view *domain.ReviewConfigView, scope domain.ReviewConfigScope) {
	view.RequestedScopeKind = scope.Kind
	view.RequestedScopeRef = scope.Ref
	view.RequestedProvider = scope.Provider
	view.RequestedAPIBaseURL = scope.APIBaseURL
}

// ensureRepositoryReviewConfigScope makes the provider installation the
// authority for a repository override. A browser cannot manufacture a GitHub,
// GitLab, or arbitrary self-managed endpoint tuple and attach policy to it.
func ensureRepositoryReviewConfigScope(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scope domain.ReviewConfigScope) error {
	if scope.Kind == domain.ReviewConfigTenantScope {
		return nil
	}
	if !scope.QualifiedRepository() {
		// Legacy callers can still address existing unqualified records. New
		// Models and Issue-triage saves explicitly require qualified identity
		// at their write boundary.
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT repository_scope
		FROM provider_installations
		WHERE tenant_id = $1 AND provider = $2 AND api_base_url = $3
		  AND active = TRUE AND verification_state IN ('legacy', 'verified')`, tenantID, scope.Provider, scope.APIBaseURL)
	if err != nil {
		return fmt.Errorf("list scoped provider installations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var repositoryScope string
		if err := rows.Scan(&repositoryScope); err != nil {
			return fmt.Errorf("scan scoped provider installation: %w", err)
		}
		if repositoryScopeAllows(repositoryScope, scope.Ref) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate scoped provider installations: %w", err)
	}
	return ErrInvalidReviewConfig
}

func (s *PostgresStore) SaveReviewConfig(ctx context.Context, actor, tenantSlug string, input domain.ReviewConfigInput) (domain.ReviewConfigView, error) {
	scope, scopeValid := input.Scope().Normalize()
	input.ScopeKind, input.ScopeRef = scope.Kind, scope.Ref
	input.ScopeProvider, input.ScopeAPIBaseURL = scope.Provider, scope.APIBaseURL
	if input.Section == domain.ReviewConfigModels && input.ScopeKind == domain.ReviewConfigRepositoryScope {
		var err error
		input.Content, err = withoutRepositoryModelConcurrency(input.Content)
		if err != nil {
			return domain.ReviewConfigView{}, ErrInvalidReviewConfig
		}
	}
	canonical, hash, valid := domain.CanonicalReviewConfig(input.Section, input.Content)
	if !valid || !scopeValid || ((input.Section == domain.ReviewConfigIssueTriage || input.Section == domain.ReviewConfigModels) && scope.Kind == domain.ReviewConfigRepositoryScope && !scope.QualifiedRepository()) || input.ExpectedRevision < 0 {
		return domain.ReviewConfigView{}, ErrInvalidReviewConfig
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("begin review configuration save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewConfigView{}, err
	}
	if role != "owner" && role != "admin" && role != "rule_admin" {
		return domain.ReviewConfigView{}, ErrForbidden
	}
	if err := ensureRepositoryReviewConfigScope(ctx, tx, tenantID, scope); err != nil {
		return domain.ReviewConfigView{}, err
	}

	var configurationID uuid.UUID
	var active bool
	err = tx.QueryRow(ctx, `
		SELECT id, active FROM review_configurations
		WHERE tenant_id = $1 AND section = $2 AND scope_kind = $3 AND scope_ref = $4
		  AND scope_provider = $5 AND scope_api_base_url = $6
		FOR UPDATE`, tenantID, input.Section, input.ScopeKind, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL).Scan(&configurationID, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		configurationID = uuid.New()
		active = false
		if _, err := tx.Exec(ctx, `INSERT INTO review_configurations (id, tenant_id, section, scope_kind, scope_ref, scope_provider, scope_api_base_url, active) VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)`, configurationID, tenantID, input.Section, input.ScopeKind, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL); err != nil {
			return domain.ReviewConfigView{}, fmt.Errorf("create review configuration: %w", err)
		}
	} else if err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("lock review configuration: %w", err)
	}
	currentRevision := 0
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision), 0) FROM review_configuration_versions WHERE configuration_id = $1`, configurationID).Scan(&currentRevision); err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("load review configuration revision: %w", err)
	}
	if (active && input.ExpectedRevision != currentRevision) || (!active && input.ExpectedRevision != 0) {
		return domain.ReviewConfigView{}, ErrRevisionConflict
	}
	if active && input.Section == domain.ReviewConfigModels {
		return domain.ReviewConfigView{}, ErrReviewConfigApprovalRequired
	}
	revision := currentRevision + 1
	var updatedAt time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO review_configuration_versions (configuration_id, revision, content, content_sha256, created_by)
		VALUES ($1, $2, $3::jsonb, $4, $5)
		RETURNING created_at`, configurationID, revision, canonical, hash, actor).Scan(&updatedAt); err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("version review configuration: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE review_configurations SET active = TRUE, updated_at = now() WHERE id = $1`, configurationID); err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("activate review configuration: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'review_config.saved', $3, jsonb_build_object('section', $4::text, 'scope_kind', $5::text, 'scope_ref', $6::text, 'scope_provider', $7::text, 'scope_api_base_url', $8::text, 'revision', $9::integer, 'content_sha256', $10::text))`, tenantID, actor, configurationID.String(), input.Section, input.ScopeKind, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL, revision, hash); err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("audit review configuration save: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("commit review configuration save: %w", err)
	}
	return domain.ReviewConfigView{
		Section:             input.Section,
		RequestedScopeKind:  input.ScopeKind,
		RequestedScopeRef:   input.ScopeRef,
		RequestedProvider:   input.ScopeProvider,
		RequestedAPIBaseURL: input.ScopeAPIBaseURL,
		OriginScopeKind:     string(input.ScopeKind),
		OriginScopeRef:      input.ScopeRef,
		OriginProvider:      input.ScopeProvider,
		OriginAPIBaseURL:    input.ScopeAPIBaseURL,
		Revision:            revision,
		ContentSHA256:       hash,
		Content:             canonical,
		UpdatedBy:           actor,
		UpdatedAt:           &updatedAt,
	}, nil
}

// withoutRepositoryModelConcurrency prevents a repository route override from
// impersonating a tenant scheduler setting. Capacity is owned only by the
// workspace Models configuration; repository routes retain routing fields.
func withoutRepositoryModelConcurrency(content json.RawMessage) (json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(content, &value); err != nil {
		return nil, fmt.Errorf("decode repository model route: %w", err)
	}
	if value == nil {
		return nil, fmt.Errorf("decode repository model route: object is required")
	}
	delete(value, "max_concurrent_runs")
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode repository model route: %w", err)
	}
	return normalized, nil
}

func (s *PostgresStore) RestoreInheritedReviewConfig(ctx context.Context, actor, tenantSlug string, input domain.ReviewConfigInput) (domain.ReviewConfigView, error) {
	scope, scopeValid := input.Scope().Normalize()
	input.ScopeKind, input.ScopeRef = scope.Kind, scope.Ref
	input.ScopeProvider, input.ScopeAPIBaseURL = scope.Provider, scope.APIBaseURL
	if !input.Section.Valid() || !scopeValid || input.ScopeKind != domain.ReviewConfigRepositoryScope || (input.Section == domain.ReviewConfigIssueTriage && !scope.QualifiedRepository()) || input.ExpectedRevision < 1 {
		return domain.ReviewConfigView{}, ErrInvalidReviewConfig
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("begin review configuration restore: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewConfigView{}, err
	}
	if role != "owner" && role != "admin" && role != "rule_admin" {
		return domain.ReviewConfigView{}, ErrForbidden
	}
	if err := ensureRepositoryReviewConfigScope(ctx, tx, tenantID, scope); err != nil {
		return domain.ReviewConfigView{}, err
	}
	var configurationID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM review_configurations WHERE tenant_id = $1 AND section = $2 AND scope_kind = 'repository' AND scope_ref = $3 AND scope_provider = $4 AND scope_api_base_url = $5 AND active = TRUE FOR UPDATE`, tenantID, input.Section, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL).Scan(&configurationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewConfigView{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("lock repository review configuration: %w", err)
	}
	var currentRevision int
	if err := tx.QueryRow(ctx, `SELECT MAX(revision) FROM review_configuration_versions WHERE configuration_id = $1`, configurationID).Scan(&currentRevision); err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("load repository review configuration revision: %w", err)
	}
	if currentRevision != input.ExpectedRevision {
		return domain.ReviewConfigView{}, ErrRevisionConflict
	}
	if input.Section == domain.ReviewConfigModels {
		return domain.ReviewConfigView{}, ErrReviewConfigApprovalRequired
	}
	if _, err := tx.Exec(ctx, `UPDATE review_configurations SET active = FALSE, updated_at = now() WHERE id = $1`, configurationID); err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("restore inherited review configuration: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'review_config.inheritance_restored', $3, jsonb_build_object('section', $4::text, 'scope_ref', $5::text, 'scope_provider', $6::text, 'scope_api_base_url', $7::text, 'previous_revision', $8::integer))`, tenantID, actor, configurationID.String(), input.Section, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL, currentRevision); err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("audit review configuration restore: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewConfigView{}, fmt.Errorf("commit review configuration restore: %w", err)
	}
	return s.GetReviewConfig(ctx, actor, tenantSlug, input.Section, scope)
}

// ModelRouteForJob returns the immutable route captured at admission. It does
// not resolve the credential reference and therefore cannot expose a secret.
func (s *PostgresStore) ModelRouteForJob(ctx context.Context, jobID uuid.UUID) (domain.ReviewConfigSnapshot, error) {
	return s.ReviewConfigSnapshotForJob(ctx, jobID, domain.ReviewConfigModels)
}

// ReviewConfigSnapshotForJob returns exactly one immutable section captured
// with the admitted review. Callers must not substitute mutable workspace
// configuration after a review has started.
func (s *PostgresStore) ReviewConfigSnapshotForJob(ctx context.Context, jobID uuid.UUID, section domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, error) {
	if jobID == uuid.Nil || !section.Valid() {
		return domain.ReviewConfigSnapshot{}, ErrInvalidReviewConfig
	}
	var snapshot domain.ReviewConfigSnapshot
	var content []byte
	err := s.pool.QueryRow(ctx, `
		SELECT snapshot.section, snapshot.origin_scope_kind, snapshot.origin_scope_ref, snapshot.origin_scope_provider, snapshot.origin_scope_api_base_url,
		       snapshot.origin_revision, snapshot.content_sha256, snapshot.content, snapshot.created_at
		FROM review_runs run
		JOIN review_configuration_snapshots snapshot ON snapshot.run_id = run.id
		WHERE run.legacy_job_id = $1 AND snapshot.section = $2`, jobID, section).Scan(
		&snapshot.Section, &snapshot.OriginScopeKind, &snapshot.OriginScopeRef, &snapshot.OriginProvider, &snapshot.OriginAPIBaseURL,
		&snapshot.OriginRevision, &snapshot.ContentSHA256, &content, &snapshot.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewConfigSnapshot{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewConfigSnapshot{}, fmt.Errorf("load review configuration snapshot: %w", err)
	}
	snapshot.Content = append(json.RawMessage(nil), content...)
	return snapshot, nil
}

// PublicationMinimumForJob reads the installation severity frozen on the
// run, never the potentially edited installation setting. NULL is retained
// for historical runs admitted before this snapshot existed.
func (s *PostgresStore) PublicationMinimumForJob(ctx context.Context, jobID uuid.UUID) (string, error) {
	if jobID == uuid.Nil {
		return "", ErrNotFound
	}
	var minimum *string
	err := s.pool.QueryRow(ctx, `SELECT publication_minimum_severity FROM review_runs WHERE legacy_job_id=$1`, jobID).Scan(&minimum)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load review publication minimum snapshot: %w", err)
	}
	if minimum == nil {
		return "", nil
	}
	return *minimum, nil
}

// ReviewRunIDForJob resolves the Console-native durable run identity for one
// immutable execution job. Provider reports must navigate to this run id;
// legacy job ids are internal and are not accepted by the Console route.
func (s *PostgresStore) ReviewRunIDForJob(ctx context.Context, jobID uuid.UUID) (uuid.UUID, error) {
	if jobID == uuid.Nil {
		return uuid.Nil, ErrNotFound
	}
	var runID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM review_runs WHERE legacy_job_id = $1`, jobID).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("load review run for job: %w", err)
	}
	return runID, nil
}
