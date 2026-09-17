package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) CreateRuleSet(ctx context.Context, actor, tenantSlug string, input domain.RuleSetInput) (domain.RuleSetWithDraft, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.Name) > 160 || len(input.Description) > 4_000 || len(input.Rules) == 0 {
		return domain.RuleSetWithDraft{}, fmt.Errorf("rule set name, description, or rules are invalid")
	}
	var parsed []rules.Rule
	if err := json.Unmarshal(input.Rules, &parsed); err != nil || len(parsed) == 0 {
		return domain.RuleSetWithDraft{}, fmt.Errorf("rule set rules must be a non-empty array")
	}
	compiled, err := rules.Compile([]rules.Source{{VersionID: "draft", Precedence: 0, Rules: parsed}})
	if err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("validate rule set: %w", err)
	}
	canonicalRules, err := json.Marshal(parsed)
	if err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("encode rule set rules: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("begin rule set creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT t.id FROM tenants t
		JOIN memberships m ON m.tenant_id = t.id
		WHERE t.slug = $1 AND m.subject = $2 AND m.role IN ('owner', 'admin')`, tenantSlug, actor).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleSetWithDraft{}, ErrForbidden
	}
	if err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("authorize rule set creation: %w", err)
	}

	result := domain.RuleSetWithDraft{}
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_sets (tenant_id, name, description, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, tenant_id, name, description, created_by, created_at, updated_at`, tenantID, input.Name, input.Description, actor).
		Scan(&result.RuleSet.ID, &result.RuleSet.TenantID, &result.RuleSet.Name, &result.RuleSet.Description, &result.RuleSet.CreatedBy, &result.RuleSet.CreatedAt, &result.RuleSet.UpdatedAt)
	if isUniqueViolation(err) {
		return domain.RuleSetWithDraft{}, ErrConflict
	}
	if err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("create rule set: %w", err)
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_versions (rule_set_id, version, state, rules, content_sha256, created_by)
		VALUES ($1, 1, 'draft', $2::jsonb, $3, $4)
		RETURNING id, rule_set_id, version, revision, state, rules, content_sha256, created_by, created_at, updated_at`,
		result.RuleSet.ID, string(canonicalRules), compiled.SHA256, actor).
		Scan(&result.Draft.ID, &result.Draft.RuleSetID, &result.Draft.Version, &result.Draft.Revision, &result.Draft.State, &result.Draft.Rules, &result.Draft.ContentSHA256, &result.Draft.CreatedBy, &result.Draft.CreatedAt, &result.Draft.UpdatedAt)
	if err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("create draft rule version: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'rule_set.created', $3, jsonb_build_object('rule_version_id', $4, 'content_sha256', $5))`, tenantID, actor, result.RuleSet.ID.String(), result.Draft.ID.String(), result.Draft.ContentSHA256); err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("audit rule set creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("commit rule set creation: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ListRuleSets(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleSet, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("rule set limit must be from 1 to 100")
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, name, description, created_by, created_at, updated_at
		FROM rule_sets WHERE tenant_id = $1 ORDER BY updated_at DESC, id DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list rule sets: %w", err)
	}
	defer rows.Close()
	sets := make([]domain.RuleSet, 0)
	for rows.Next() {
		var item domain.RuleSet
		if err := rows.Scan(&item.ID, &item.TenantID, &item.Name, &item.Description, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan rule set: %w", err)
		}
		sets = append(sets, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule sets: %w", err)
	}
	return sets, nil
}
