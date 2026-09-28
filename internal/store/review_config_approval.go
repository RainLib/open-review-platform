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

// Model routes choose credentials, providers and execution capacity. A route
// that is already active therefore changes only through this small, durable
// two-person workflow. Other review-settings sections keep their established
// optimistic-concurrency workflow until they receive their own policy.
func (s *PostgresStore) RequestReviewConfigChange(ctx context.Context, actor, tenantSlug string, input domain.ReviewConfigChangeRequestInput) (domain.ReviewConfigChangeRequest, error) {
	input.ScopeRef = strings.TrimSpace(input.ScopeRef)
	scope, validScope := (domain.ReviewConfigScope{Kind: input.ScopeKind, Ref: input.ScopeRef, Provider: input.ScopeProvider, APIBaseURL: input.ScopeAPIBaseURL}).Normalize()
	input.ScopeKind, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL = scope.Kind, scope.Ref, scope.Provider, scope.APIBaseURL
	input.Reason = strings.TrimSpace(input.Reason)
	input.Operation = strings.ToLower(strings.TrimSpace(input.Operation))
	if input.Operation == "" {
		input.Operation = domain.ReviewConfigChangeUpsert
	}
	if input.Section != domain.ReviewConfigModels || !validScope || input.ExpectedRevision < 1 || len(input.Reason) < 3 || len(input.Reason) > 2_000 || (input.Operation != domain.ReviewConfigChangeUpsert && input.Operation != domain.ReviewConfigChangeRestoreInheritance) {
		return domain.ReviewConfigChangeRequest{}, ErrInvalidReviewConfigApproval
	}
	if input.Operation == domain.ReviewConfigChangeRestoreInheritance && input.ScopeKind != domain.ReviewConfigRepositoryScope {
		return domain.ReviewConfigChangeRequest{}, ErrInvalidReviewConfigApproval
	}
	if input.ScopeKind == domain.ReviewConfigRepositoryScope && input.Operation == domain.ReviewConfigChangeUpsert {
		var err error
		input.Content, err = withoutRepositoryModelConcurrency(input.Content)
		if err != nil {
			return domain.ReviewConfigChangeRequest{}, ErrInvalidReviewConfigApproval
		}
	}
	// An upsert carries the candidate route. A restore-inheritance request does
	// not: it only disables the current repository override. Its proposal is
	// derived below from the locked active version so an arbitrary browser body
	// cannot become misleading approval evidence.
	var canonical json.RawMessage
	var proposedHash string
	if input.Operation == domain.ReviewConfigChangeUpsert {
		var valid bool
		canonical, proposedHash, valid = domain.CanonicalReviewConfig(input.Section, input.Content)
		if !valid {
			return domain.ReviewConfigChangeRequest{}, ErrInvalidReviewConfigApproval
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("begin review configuration change request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewConfigChangeRequest{}, err
	}
	if !canManageReviewConfig(role) {
		return domain.ReviewConfigChangeRequest{}, ErrForbidden
	}
	if err := ensureRepositoryReviewConfigScope(ctx, tx, tenantID, scope); err != nil {
		return domain.ReviewConfigChangeRequest{}, err
	}

	var configurationID uuid.UUID
	var baseRevision int
	var baseHash string
	var baseContent []byte
	err = tx.QueryRow(ctx, `
		SELECT configuration.id, version.revision, version.content_sha256, version.content
		FROM review_configurations configuration
		JOIN LATERAL (
			SELECT revision, content_sha256, content FROM review_configuration_versions
			WHERE configuration_id = configuration.id ORDER BY revision DESC LIMIT 1
		) version ON TRUE
		WHERE configuration.tenant_id = $1 AND configuration.section = $2
		  AND configuration.scope_kind = $3 AND configuration.scope_ref = $4
		  AND configuration.scope_provider = $5 AND configuration.scope_api_base_url = $6
		  AND configuration.active = TRUE
		FOR UPDATE OF configuration`, tenantID, input.Section, input.ScopeKind, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL).Scan(&configurationID, &baseRevision, &baseHash, &baseContent)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewConfigChangeRequest{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("lock active model route: %w", err)
	}
	if baseRevision != input.ExpectedRevision {
		return domain.ReviewConfigChangeRequest{}, ErrRevisionConflict
	}
	if input.Operation == domain.ReviewConfigChangeRestoreInheritance {
		var valid bool
		canonical, proposedHash, valid = domain.CanonicalReviewConfig(input.Section, baseContent)
		if !valid || proposedHash != baseHash {
			return domain.ReviewConfigChangeRequest{}, fmt.Errorf("load canonical active model route: %w", ErrInvalidReviewConfigApproval)
		}
	}
	if input.Operation == domain.ReviewConfigChangeUpsert && baseHash == proposedHash {
		return domain.ReviewConfigChangeRequest{}, ErrConflict
	}

	result := domain.ReviewConfigChangeRequest{}
	err = tx.QueryRow(ctx, `
		INSERT INTO review_config_change_requests
			(tenant_id, section, scope_kind, scope_ref, base_revision, base_content_sha256, proposed_content, proposed_content_sha256, requested_by, reason, operation, scope_provider, scope_api_base_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11, $12, $13)
		RETURNING id, tenant_id, section, scope_kind, scope_ref, base_revision, base_content_sha256,
		          proposed_content, proposed_content_sha256, requested_by, reason, operation, state, COALESCE(applied_revision, 0), created_at, decided_at, scope_provider, scope_api_base_url`,
		tenantID, input.Section, input.ScopeKind, input.ScopeRef, baseRevision, baseHash, canonical, proposedHash, actor, input.Reason, input.Operation, input.ScopeProvider, input.ScopeAPIBaseURL).Scan(
		&result.ID, &result.TenantID, &result.Section, &result.ScopeKind, &result.ScopeRef, &result.BaseRevision, &result.BaseContentSHA256,
		&result.ProposedContent, &result.ProposedContentSHA256, &result.RequestedBy, &result.Reason, &result.Operation, &result.State, &result.AppliedRevision, &result.CreatedAt, &result.DecidedAt, &result.ScopeProvider, &result.ScopeAPIBaseURL,
	)
	if isUniqueViolation(err) {
		return domain.ReviewConfigChangeRequest{}, ErrConflict
	}
	if err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("create review configuration change request: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'review_config_change.requested', $3, jsonb_build_object('section', $4::text, 'scope_kind', $5::text, 'scope_ref', $6::text, 'base_revision', $7::integer, 'base_content_sha256', $8::text, 'proposed_content_sha256', $9::text, 'operation', $10::text, 'scope_provider', $11::text, 'scope_api_base_url', $12::text))`, tenantID, actor, result.ID.String(), input.Section, input.ScopeKind, input.ScopeRef, baseRevision, baseHash, proposedHash, input.Operation, input.ScopeProvider, input.ScopeAPIBaseURL); err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("audit review configuration change request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("commit review configuration change request: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ListReviewConfigChangeRequests(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.ReviewConfigChangeRequest, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidReviewConfigApproval
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT request.id, request.tenant_id, request.section, request.scope_kind, request.scope_ref,
		       request.base_revision, request.base_content_sha256, request.proposed_content, request.proposed_content_sha256,
		       request.requested_by, request.reason, request.operation, request.state, COALESCE(request.applied_revision, 0),
		       COUNT(approval.id) FILTER (WHERE approval.decision = 'approved' AND approval.content_sha256 = request.proposed_content_sha256),
		       COUNT(approval.id) FILTER (WHERE approval.decision = 'rejected' AND approval.content_sha256 = request.proposed_content_sha256),
		       COALESCE(MAX(approval.decision) FILTER (WHERE approval.approver_subject = $2), ''),
		       request.created_at, request.decided_at, request.scope_provider, request.scope_api_base_url
		FROM review_config_change_requests request
		LEFT JOIN review_config_change_approvals approval ON approval.request_id = request.id
		WHERE request.tenant_id = $1
		GROUP BY request.id
		ORDER BY CASE request.state WHEN 'pending' THEN 0 WHEN 'approved' THEN 1 ELSE 2 END, request.created_at DESC, request.id DESC
		LIMIT $3`, tenantID, actor, limit)
	if err != nil {
		return nil, fmt.Errorf("list review configuration change requests: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ReviewConfigChangeRequest, 0)
	for rows.Next() {
		var item domain.ReviewConfigChangeRequest
		var content []byte
		if err := rows.Scan(&item.ID, &item.TenantID, &item.Section, &item.ScopeKind, &item.ScopeRef, &item.BaseRevision, &item.BaseContentSHA256, &content, &item.ProposedContentSHA256, &item.RequestedBy, &item.Reason, &item.Operation, &item.State, &item.AppliedRevision, &item.ApprovalCount, &item.RejectionCount, &item.ActorDecision, &item.CreatedAt, &item.DecidedAt, &item.ScopeProvider, &item.ScopeAPIBaseURL); err != nil {
			return nil, fmt.Errorf("scan review configuration change request: %w", err)
		}
		item.ProposedContent = append(json.RawMessage(nil), content...)
		item.CanDecide = canApproveReviewConfig(role) && item.State == domain.ReviewConfigChangePending && item.RequestedBy != actor && item.ActorDecision == ""
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review configuration change requests: %w", err)
	}
	return items, nil
}

// DecideReviewConfigChange records a decision and writes a new immutable model
// route version atomically on approval. If the route moved since proposal, the
// request is made superseded instead of applying stale content.
func (s *PostgresStore) DecideReviewConfigChange(ctx context.Context, actor, tenantSlug string, requestID uuid.UUID, input domain.ReviewConfigChangeDecisionInput) (domain.ReviewConfigChangeRequest, error) {
	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.Comment = strings.TrimSpace(input.Comment)
	if requestID == uuid.Nil || (input.Decision != "approved" && input.Decision != "rejected") || len(input.Comment) > 2_000 {
		return domain.ReviewConfigChangeRequest{}, ErrInvalidReviewConfigApproval
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("begin review configuration change decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewConfigChangeRequest{}, err
	}
	if !canApproveReviewConfig(role) {
		return domain.ReviewConfigChangeRequest{}, ErrForbidden
	}

	var result domain.ReviewConfigChangeRequest
	var proposed []byte
	err = tx.QueryRow(ctx, `SELECT id, tenant_id, section, scope_kind, scope_ref, base_revision, base_content_sha256, proposed_content, proposed_content_sha256, requested_by, reason, operation, state, COALESCE(applied_revision, 0), created_at, decided_at, scope_provider, scope_api_base_url FROM review_config_change_requests WHERE id = $1 AND tenant_id = $2 FOR UPDATE`, requestID, tenantID).Scan(
		&result.ID, &result.TenantID, &result.Section, &result.ScopeKind, &result.ScopeRef, &result.BaseRevision, &result.BaseContentSHA256, &proposed, &result.ProposedContentSHA256, &result.RequestedBy, &result.Reason, &result.Operation, &result.State, &result.AppliedRevision, &result.CreatedAt, &result.DecidedAt, &result.ScopeProvider, &result.ScopeAPIBaseURL,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewConfigChangeRequest{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("lock review configuration change request: %w", err)
	}
	result.ProposedContent = append(json.RawMessage(nil), proposed...)
	if result.State != domain.ReviewConfigChangePending {
		return domain.ReviewConfigChangeRequest{}, ErrConflict
	}
	if result.RequestedBy == actor {
		return domain.ReviewConfigChangeRequest{}, ErrSeparationOfDuties
	}

	var configurationID uuid.UUID
	var currentRevision int
	var currentHash string
	err = tx.QueryRow(ctx, `SELECT configuration.id, version.revision, version.content_sha256 FROM review_configurations configuration JOIN LATERAL (SELECT revision, content_sha256 FROM review_configuration_versions WHERE configuration_id = configuration.id ORDER BY revision DESC LIMIT 1) version ON TRUE WHERE configuration.tenant_id = $1 AND configuration.section = $2 AND configuration.scope_kind = $3 AND configuration.scope_ref = $4 AND configuration.scope_provider = $5 AND configuration.scope_api_base_url = $6 AND configuration.active = TRUE FOR UPDATE OF configuration`, tenantID, result.Section, result.ScopeKind, result.ScopeRef, result.ScopeProvider, result.ScopeAPIBaseURL).Scan(&configurationID, &currentRevision, &currentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewConfigChangeRequest{}, ErrConflict
	}
	if err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("lock proposed model route: %w", err)
	}
	if currentRevision != result.BaseRevision || currentHash != result.BaseContentSHA256 {
		if _, err := tx.Exec(ctx, `UPDATE review_config_change_requests SET state = 'superseded', decided_at = now() WHERE id = $1`, result.ID); err != nil {
			return domain.ReviewConfigChangeRequest{}, fmt.Errorf("supersede stale review configuration change: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'review_config_change.superseded', $3, jsonb_build_object('expected_revision', $4::integer, 'observed_revision', $5::integer))`, tenantID, actor, result.ID.String(), result.BaseRevision, currentRevision); err != nil {
			return domain.ReviewConfigChangeRequest{}, fmt.Errorf("audit superseded review configuration change: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.ReviewConfigChangeRequest{}, fmt.Errorf("commit superseded review configuration change: %w", err)
		}
		now := time.Now().UTC()
		result.State, result.DecidedAt = domain.ReviewConfigChangeSuperseded, &now
		return result, nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO review_config_change_approvals (request_id, approver_subject, decision, content_sha256, comment) VALUES ($1, $2, $3, $4, $5)`, result.ID, actor, input.Decision, result.ProposedContentSHA256, input.Comment); isUniqueViolation(err) {
		return domain.ReviewConfigChangeRequest{}, ErrConflict
	} else if err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("record review configuration change decision: %w", err)
	}

	if input.Decision == "rejected" {
		if _, err := tx.Exec(ctx, `UPDATE review_config_change_requests SET state = 'rejected', decided_at = now() WHERE id = $1`, result.ID); err != nil {
			return domain.ReviewConfigChangeRequest{}, fmt.Errorf("reject review configuration change: %w", err)
		}
		result.State, result.RejectionCount = domain.ReviewConfigChangeRejected, 1
	} else if result.Operation == domain.ReviewConfigChangeRestoreInheritance {
		if _, err := tx.Exec(ctx, `UPDATE review_configurations SET active = FALSE, updated_at = now() WHERE id = $1`, configurationID); err != nil {
			return domain.ReviewConfigChangeRequest{}, fmt.Errorf("restore approved model inheritance: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE review_config_change_requests SET state = 'approved', applied_revision = $2, decided_at = now() WHERE id = $1`, result.ID, currentRevision); err != nil {
			return domain.ReviewConfigChangeRequest{}, fmt.Errorf("approve model inheritance restore: %w", err)
		}
		result.State, result.AppliedRevision, result.ApprovalCount = domain.ReviewConfigChangeApproved, currentRevision, 1
	} else {
		newRevision := currentRevision + 1
		if _, err := tx.Exec(ctx, `INSERT INTO review_configuration_versions (configuration_id, revision, content, content_sha256, created_by) VALUES ($1, $2, $3::jsonb, $4, $5)`, configurationID, newRevision, result.ProposedContent, result.ProposedContentSHA256, result.RequestedBy); err != nil {
			return domain.ReviewConfigChangeRequest{}, fmt.Errorf("version approved model route: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE review_configurations SET updated_at = now() WHERE id = $1`, configurationID); err != nil {
			return domain.ReviewConfigChangeRequest{}, fmt.Errorf("touch approved model route: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE review_config_change_requests SET state = 'approved', applied_revision = $2, decided_at = now() WHERE id = $1`, result.ID, newRevision); err != nil {
			return domain.ReviewConfigChangeRequest{}, fmt.Errorf("approve review configuration change: %w", err)
		}
		result.State, result.AppliedRevision, result.ApprovalCount = domain.ReviewConfigChangeApproved, newRevision, 1
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'review_config_change.decided', $3, jsonb_build_object('decision', $4::text, 'operation', $5::text, 'proposed_content_sha256', $6::text, 'applied_revision', $7::integer))`, tenantID, actor, result.ID.String(), input.Decision, result.Operation, result.ProposedContentSHA256, result.AppliedRevision); err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("audit review configuration change decision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewConfigChangeRequest{}, fmt.Errorf("commit review configuration change decision: %w", err)
	}
	now := time.Now().UTC()
	result.DecidedAt = &now
	return result, nil
}

func canManageReviewConfig(role string) bool {
	return role == "owner" || role == "admin" || role == "rule_admin"
}
func canApproveReviewConfig(role string) bool { return role == "owner" || role == "admin" }
