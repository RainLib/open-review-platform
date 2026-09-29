package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) CreateRuleException(ctx context.Context, actor, tenantSlug string, input domain.RuleExceptionInput) (domain.RuleException, error) {
	if !normalizeRuleExceptionInput(&input) {
		return domain.RuleException{}, ErrInvalidRuleException
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.RuleException{}, err
	}
	if !canManageRules(role) {
		return domain.RuleException{}, ErrForbidden
	}
	if err := s.reconcileExpiredIssueExceptions(ctx, tenantID); err != nil {
		return domain.RuleException{}, err
	}
	if input.SourceIssueID != nil {
		var issueRepository, issueAPIBaseURL string
		var issueProvider domain.Provider
		var issueRevision int
		var issueStatus domain.IssueStatus
		err = s.pool.QueryRow(ctx, `
			SELECT provider, api_base_url, repository, revision, status
			FROM review_issues
			WHERE tenant_id = $1 AND id = $2`, tenantID, *input.SourceIssueID).
			Scan(&issueProvider, &issueAPIBaseURL, &issueRepository, &issueRevision, &issueStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RuleException{}, ErrNotFound
		}
		if err != nil {
			return domain.RuleException{}, fmt.Errorf("load source issue for rule exception: %w", err)
		}
		if input.ScopeKind != "repository" || input.ScopeRef != issueRepository || input.ScopeProvider != issueProvider || input.ScopeAPIBaseURL != issueAPIBaseURL || input.SourceIssueRevision != issueRevision || (issueStatus != domain.IssueOpen && issueStatus != domain.IssueRegressed) {
			return domain.RuleException{}, ErrInvalidRuleException
		}
	}
	var rawRules []byte
	err = s.pool.QueryRow(ctx, `
		SELECT version.rules
		FROM rule_versions version
		JOIN rule_sets rule_set ON rule_set.id = version.rule_set_id
		WHERE version.id = $1 AND rule_set.tenant_id = $2 AND version.state = 'published'`, input.RuleVersionID, tenantID).
		Scan(&rawRules)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleException{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleException{}, fmt.Errorf("load exception rule version: %w", err)
	}
	var versionRules []rules.Rule
	if err := json.Unmarshal(rawRules, &versionRules); err != nil {
		return domain.RuleException{}, fmt.Errorf("decode exception rule version: %w", err)
	}
	found := false
	for _, rule := range versionRules {
		if rule.Key == input.RuleKey {
			found = true
			break
		}
	}
	if !found {
		return domain.RuleException{}, fmt.Errorf("%w: rule key does not exist in version", ErrInvalidRuleException)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleException{}, fmt.Errorf("begin rule exception request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	scope := domain.ReviewConfigScope{Kind: domain.ReviewConfigScopeKind(input.ScopeKind), Ref: input.ScopeRef, Provider: input.ScopeProvider, APIBaseURL: input.ScopeAPIBaseURL}
	if err := ensureRepositoryReviewConfigScope(ctx, tx, tenantID, scope); err != nil {
		if errors.Is(err, ErrInvalidReviewConfig) {
			return domain.RuleException{}, ErrInvalidRuleException
		}
		return domain.RuleException{}, fmt.Errorf("authorize rule exception repository scope: %w", err)
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_exceptions (
			tenant_id, rule_version_id, rule_key, scope_kind, scope_ref, scope_provider, scope_api_base_url,
			target_branch_glob, reason, ticket_url, requested_by, expires_at,
			source_issue_id, source_issue_revision
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id`, tenantID, input.RuleVersionID, input.RuleKey, input.ScopeKind, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL,
		input.TargetBranchGlob, input.Reason, input.TicketURL, actor, input.ExpiresAt,
		input.SourceIssueID, nullablePositiveInteger(input.SourceIssueRevision)).Scan(&id)
	if err != nil {
		return domain.RuleException{}, fmt.Errorf("create rule exception: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, 'rule_exception.requested', $3, jsonb_build_object(
			'rule_version_id', $4::text, 'rule_key', $5::text, 'scope_kind', $6::text,
			'scope_ref', $7::text, 'scope_provider', $8::text, 'scope_api_base_url', $9::text,
			'expires_at', $10::text, 'source_issue_id', $11::text, 'source_issue_revision', $12::integer))`, tenantID, actor, id.String(),
		input.RuleVersionID, input.RuleKey, input.ScopeKind, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL, input.ExpiresAt.UTC().Format(time.RFC3339),
		nullableUUIDString(input.SourceIssueID), nullablePositiveInteger(input.SourceIssueRevision)); err != nil {
		return domain.RuleException{}, fmt.Errorf("audit rule exception request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleException{}, fmt.Errorf("commit rule exception request: %w", err)
	}
	return s.ruleExceptionByID(ctx, tenantID, role, actor, id)
}

func (s *PostgresStore) ListRuleExceptions(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleException, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("rule exception limit must be from 1 to 100")
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, ruleExceptionSelect+`
		WHERE exception.tenant_id = $1
		ORDER BY CASE WHEN exception.state = 'pending' THEN 0 ELSE 1 END,
		         exception.expires_at ASC, exception.created_at DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list rule exceptions: %w", err)
	}
	defer rows.Close()
	items := make([]domain.RuleException, 0)
	for rows.Next() {
		item, err := scanRuleException(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rule exception: %w", err)
		}
		decorateRuleException(&item, role, actor)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) DecideRuleException(ctx context.Context, actor, tenantSlug string, exceptionID uuid.UUID, input domain.RuleExceptionDecisionInput) (domain.RuleException, error) {
	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.Comment = strings.TrimSpace(input.Comment)
	if exceptionID == uuid.Nil || (input.Decision != "approved" && input.Decision != "rejected") || len(input.Comment) > 2000 {
		return domain.RuleException{}, ErrInvalidRuleException
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.RuleException{}, err
	}
	if !canManageRules(role) {
		return domain.RuleException{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleException{}, fmt.Errorf("begin rule exception decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var requestedBy, state, reason string
	var expiresAt time.Time
	var sourceIssueID *uuid.UUID
	var sourceIssueRevision *int
	err = tx.QueryRow(ctx, `
		SELECT requested_by, state, expires_at, reason, source_issue_id, source_issue_revision FROM rule_exceptions
		WHERE id = $1 AND tenant_id = $2 FOR UPDATE`, exceptionID, tenantID).
		Scan(&requestedBy, &state, &expiresAt, &reason, &sourceIssueID, &sourceIssueRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleException{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleException{}, fmt.Errorf("load rule exception decision: %w", err)
	}
	if state != "pending" || requestedBy == actor || !expiresAt.After(time.Now()) {
		return domain.RuleException{}, ErrInvalidRuleException
	}
	if _, err := tx.Exec(ctx, `
		UPDATE rule_exceptions
		SET state = $3, approved_by = $4, decision_comment = $5,
		    decided_at = now(), updated_at = now()
		WHERE id = $1 AND tenant_id = $2`, exceptionID, tenantID, input.Decision, actor, input.Comment); err != nil {
		return domain.RuleException{}, fmt.Errorf("decide rule exception: %w", err)
	}
	if input.Decision == "approved" && sourceIssueID != nil && sourceIssueRevision != nil {
		if err := suppressIssueForApprovedException(ctx, tx, tenantID, actor, exceptionID, *sourceIssueID, *sourceIssueRevision, expiresAt, reason); err != nil {
			return domain.RuleException{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, $3, $4, jsonb_build_object('decision_comment', $5::text))`,
		tenantID, actor, "rule_exception."+input.Decision, exceptionID.String(), input.Comment); err != nil {
		return domain.RuleException{}, fmt.Errorf("audit rule exception decision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleException{}, fmt.Errorf("commit rule exception decision: %w", err)
	}
	return s.ruleExceptionByID(ctx, tenantID, role, actor, exceptionID)
}

func (s *PostgresStore) RevokeRuleException(ctx context.Context, actor, tenantSlug string, exceptionID uuid.UUID) (domain.RuleException, error) {
	if exceptionID == uuid.Nil {
		return domain.RuleException{}, ErrInvalidRuleException
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.RuleException{}, err
	}
	if !canManageRules(role) {
		return domain.RuleException{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleException{}, fmt.Errorf("begin rule exception revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sourceIssueID *uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE rule_exceptions SET state = 'revoked', revoked_at = now(), updated_at = now()
		WHERE id = $1 AND tenant_id = $2 AND state = 'approved'
		RETURNING source_issue_id`, exceptionID, tenantID).Scan(&sourceIssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleException{}, ErrInvalidRuleException
	}
	if err != nil {
		return domain.RuleException{}, fmt.Errorf("revoke rule exception: %w", err)
	}
	if sourceIssueID != nil {
		if err := clearIssueExceptionSuppression(ctx, tx, tenantID, actor, exceptionID, *sourceIssueID, "exception_revoked"); err != nil {
			return domain.RuleException{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target)
		VALUES ($1, $2, 'rule_exception.revoked', $3)`, tenantID, actor, exceptionID.String()); err != nil {
		return domain.RuleException{}, fmt.Errorf("audit rule exception revocation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleException{}, fmt.Errorf("commit rule exception revocation: %w", err)
	}
	return s.ruleExceptionByID(ctx, tenantID, role, actor, exceptionID)
}

const ruleExceptionSelect = `
	SELECT exception.id, exception.tenant_id, rule_set.id, rule_set.name,
	       version.id, version.version, exception.rule_key, exception.scope_kind,
	       exception.scope_ref, exception.scope_provider, exception.scope_api_base_url,
	       exception.target_branch_glob, exception.reason,
	       exception.ticket_url, exception.requested_by, COALESCE(exception.approved_by, ''),
	       exception.decision_comment, exception.state,
	       CASE WHEN exception.expires_at <= now() AND exception.state IN ('pending', 'approved')
	            THEN 'expired' ELSE exception.state END,
	       exception.expires_at, exception.decided_at, exception.revoked_at,
	       exception.created_at, exception.updated_at, exception.source_issue_id,
	       exception.source_issue_revision
	FROM rule_exceptions exception
	JOIN rule_versions version ON version.id = exception.rule_version_id
	JOIN rule_sets rule_set ON rule_set.id = version.rule_set_id`

func (s *PostgresStore) ruleExceptionByID(ctx context.Context, tenantID uuid.UUID, role, actor string, exceptionID uuid.UUID) (domain.RuleException, error) {
	item, err := scanRuleException(s.pool.QueryRow(ctx, ruleExceptionSelect+` WHERE exception.id = $1 AND exception.tenant_id = $2`, exceptionID, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleException{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleException{}, fmt.Errorf("load rule exception: %w", err)
	}
	decorateRuleException(&item, role, actor)
	return item, nil
}

func scanRuleException(row rowScanner) (domain.RuleException, error) {
	var item domain.RuleException
	err := row.Scan(&item.ID, &item.TenantID, &item.RuleSetID, &item.RuleSetName,
		&item.RuleVersionID, &item.RuleVersion, &item.RuleKey, &item.ScopeKind,
		&item.ScopeRef, &item.ScopeProvider, &item.ScopeAPIBaseURL, &item.TargetBranchGlob, &item.Reason, &item.TicketURL,
		&item.RequestedBy, &item.ApprovedBy, &item.DecisionComment, &item.State,
		&item.EffectiveState, &item.ExpiresAt, &item.DecidedAt, &item.RevokedAt,
		&item.CreatedAt, &item.UpdatedAt, &item.SourceIssueID, &item.SourceIssueRevision)
	return item, err
}

func decorateRuleException(item *domain.RuleException, role, actor string) {
	canManage := canManageRules(role)
	item.CanDecide = canManage && item.State == "pending" && item.EffectiveState != "expired" && item.RequestedBy != actor
	item.CanRevoke = canManage && item.State == "approved" && item.EffectiveState != "expired"
}

func normalizeRuleExceptionInput(input *domain.RuleExceptionInput) bool {
	input.RuleKey = strings.TrimSpace(input.RuleKey)
	scope, scopeValid := normalizeProviderQualifiedRuleScope(input.ScopeKind, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL)
	input.ScopeKind, input.ScopeRef = string(scope.Kind), scope.Ref
	input.ScopeProvider, input.ScopeAPIBaseURL = scope.Provider, scope.APIBaseURL
	input.TargetBranchGlob = strings.TrimSpace(input.TargetBranchGlob)
	input.Reason = strings.TrimSpace(input.Reason)
	input.TicketURL = strings.TrimSpace(input.TicketURL)
	input.ExpiresAt = input.ExpiresAt.UTC()
	if input.RuleVersionID == uuid.Nil || !scopeValid || input.RuleKey == "" || len(input.RuleKey) > 200 || input.Reason == "" || len(input.Reason) > 4000 || len(input.ScopeRef) > 300 || len(input.TargetBranchGlob) > 255 || input.ExpiresAt.Before(time.Now().Add(5*time.Minute)) || input.ExpiresAt.After(time.Now().Add(366*24*time.Hour)) {
		return false
	}
	if (input.SourceIssueID == nil) != (input.SourceIssueRevision == 0) || (input.SourceIssueID != nil && (*input.SourceIssueID == uuid.Nil || input.SourceIssueRevision < 1)) {
		return false
	}
	if scope.Kind != domain.ReviewConfigTenantScope && scope.Kind != domain.ReviewConfigRepositoryScope {
		return false
	}
	if input.TargetBranchGlob != "" {
		if _, err := path.Match(input.TargetBranchGlob, "validation-branch"); err != nil {
			return false
		}
	}
	if input.TicketURL != "" {
		parsed, err := url.Parse(input.TicketURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || len(input.TicketURL) > 2000 {
			return false
		}
	}
	return true
}

func suppressIssueForApprovedException(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, actor string, exceptionID, issueID uuid.UUID, expectedRevision int, expiresAt time.Time, reason string) error {
	var revision int
	var previousStatus domain.IssueStatus
	var previousAssignee string
	err := tx.QueryRow(ctx, `
		SELECT revision, status, assignee_subject
		FROM review_issues WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, issueID).
		Scan(&revision, &previousStatus, &previousAssignee)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock source issue for exception approval: %w", err)
	}
	if revision != expectedRevision || (previousStatus != domain.IssueOpen && previousStatus != domain.IssueRegressed) {
		return ErrRevisionConflict
	}
	nextRevision := revision + 1
	if _, err := tx.Exec(ctx, `
		UPDATE review_issues SET revision = $3, status = 'suppressed', disposition_kind = 'exception',
		       disposition_reason = $4, active_exception_id = $5, suppression_expires_at = $6,
		       resolved_at = NULL, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenantID, issueID, nextRevision, reason, exceptionID, expiresAt); err != nil {
		return fmt.Errorf("suppress issue for approved exception: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_issue_events (
			tenant_id, issue_id, revision, actor_subject, action, previous_status, next_status,
			previous_assignee, next_assignee, reason
		) VALUES ($1,$2,$3,$4,'exception_approved',$5,'suppressed',$6,$6,$7)`,
		tenantID, issueID, nextRevision, actor, previousStatus, previousAssignee,
		fmt.Sprintf("Approved exception %s until %s: %s", exceptionID, expiresAt.UTC().Format(time.RFC3339), reason)); err != nil {
		return fmt.Errorf("record issue exception approval: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1,$2,'issue.exception_approved',$3,
		        jsonb_build_object('exception_id',$4::text,'expires_at',$5::text,'revision',$6::integer))`,
		tenantID, actor, issueID.String(), exceptionID.String(), expiresAt.UTC().Format(time.RFC3339), nextRevision); err != nil {
		return fmt.Errorf("audit issue exception approval: %w", err)
	}
	return nil
}

func clearIssueExceptionSuppression(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, actor string, exceptionID, issueID uuid.UUID, action string) error {
	var revision, activeCount int
	var status domain.IssueStatus
	var assignee string
	err := tx.QueryRow(ctx, `
		SELECT revision, status, active_occurrence_count, assignee_subject
		FROM review_issues
		WHERE tenant_id = $1 AND id = $2 AND active_exception_id = $3 FOR UPDATE`, tenantID, issueID, exceptionID).
		Scan(&revision, &status, &activeCount, &assignee)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock exception-suppressed issue: %w", err)
	}
	nextStatus := domain.IssueResolved
	if activeCount > 0 {
		nextStatus = domain.IssueOpen
	}
	nextRevision := revision + 1
	if _, err := tx.Exec(ctx, `
		UPDATE review_issues SET revision = $3, status = $4, disposition_kind = '',
		       disposition_reason = '', active_exception_id = NULL, suppression_expires_at = NULL,
		       resolved_at = CASE WHEN $4 = 'resolved' THEN COALESCE(resolved_at, now()) ELSE NULL END,
		       updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenantID, issueID, nextRevision, nextStatus); err != nil {
		return fmt.Errorf("clear issue exception suppression: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_issue_events (
			tenant_id, issue_id, revision, actor_subject, action, previous_status, next_status,
			previous_assignee, next_assignee, reason
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$9)`, tenantID, issueID, nextRevision,
		actor, action, status, nextStatus, assignee, fmt.Sprintf("Rule exception %s no longer applies.", exceptionID)); err != nil {
		return fmt.Errorf("record issue exception clearance: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1,$2,$3,$4,jsonb_build_object('exception_id',$5::text,'revision',$6::integer))`,
		tenantID, actor, "issue."+action, issueID.String(), exceptionID.String(), nextRevision); err != nil {
		return fmt.Errorf("audit issue exception clearance: %w", err)
	}
	return nil
}

func nullablePositiveInteger(value int) any {
	if value < 1 {
		return nil
	}
	return value
}

func nullableUUIDString(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return value.String()
}
