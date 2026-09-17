package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
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
	if _, err := rules.OCRRuleFileForSnapshot(compiled.Snapshot); err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("validate rule set OCR adapter: %w", err)
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
	var draftRules []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_versions (rule_set_id, version, state, rules, content_sha256, created_by)
		VALUES ($1, 1, 'draft', $2::jsonb, $3, $4)
		RETURNING id, rule_set_id, version, revision, state, rules, content_sha256, created_by, created_at, updated_at`,
		result.RuleSet.ID, string(canonicalRules), compiled.SHA256, actor).
		Scan(&result.Draft.ID, &result.Draft.RuleSetID, &result.Draft.Version, &result.Draft.Revision, &result.Draft.State, &draftRules, &result.Draft.ContentSHA256, &result.Draft.CreatedBy, &result.Draft.CreatedAt, &result.Draft.UpdatedAt)
	if err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("create draft rule version: %w", err)
	}
	result.Draft.Rules = json.RawMessage(draftRules)
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'rule_set.created', $3, jsonb_build_object('rule_version_id', $4::text, 'content_sha256', $5::text))`, tenantID, actor, result.RuleSet.ID.String(), result.Draft.ID.String(), result.Draft.ContentSHA256); err != nil {
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

// PublishRuleVersion makes an already validated draft immutable. Bindings can
// only reference published versions, so a review admission never compiles a
// mutable draft accidentally.
func (s *PostgresStore) PublishRuleVersion(ctx context.Context, actor, tenantSlug string, ruleSetID uuid.UUID, version int) (domain.RuleVersion, error) {
	if ruleSetID == uuid.Nil || version < 1 {
		return domain.RuleVersion{}, fmt.Errorf("rule set id or version is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleVersion{}, fmt.Errorf("begin rule version publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RuleVersion{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.RuleVersion{}, ErrForbidden
	}
	var result domain.RuleVersion
	var rawRules []byte
	err = tx.QueryRow(ctx, `
		UPDATE rule_versions v
		SET state = 'published', published_at = now(), revision = revision + 1, updated_at = now()
		FROM rule_sets rs
		WHERE v.rule_set_id = rs.id
		  AND rs.tenant_id = $1
		  AND rs.id = $2
		  AND v.version = $3
		  AND v.state = 'draft'
		RETURNING v.id, v.rule_set_id, v.version, v.revision, v.state, v.rules, v.content_sha256, v.created_by, v.created_at, v.updated_at`, tenantID, ruleSetID, version).
		Scan(&result.ID, &result.RuleSetID, &result.Version, &result.Revision, &result.State, &rawRules, &result.ContentSHA256, &result.CreatedBy, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleVersion{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleVersion{}, fmt.Errorf("publish rule version: %w", err)
	}
	result.Rules = json.RawMessage(rawRules)
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'rule_version.published', $3, jsonb_build_object('version', $4::integer, 'content_sha256', $5::text))`, tenantID, actor, result.ID.String(), result.Version, result.ContentSHA256); err != nil {
		return domain.RuleVersion{}, fmt.Errorf("audit rule version publication: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleVersion{}, fmt.Errorf("commit rule version publication: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) CreateRuleBinding(ctx context.Context, actor, tenantSlug string, input domain.RuleBindingInput) (domain.RuleBinding, error) {
	if !validRuleBindingInput(&input) {
		return domain.RuleBinding{}, fmt.Errorf("rule binding is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleBinding{}, fmt.Errorf("begin rule binding creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RuleBinding{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.RuleBinding{}, ErrForbidden
	}
	var published bool
	err = tx.QueryRow(ctx, `
		SELECT TRUE
		FROM rule_versions v JOIN rule_sets rs ON rs.id = v.rule_set_id
		WHERE v.id = $1 AND v.state = 'published' AND rs.tenant_id = $2`, input.RuleVersionID, tenantID).Scan(&published)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleBinding{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleBinding{}, fmt.Errorf("authorize published rule version: %w", err)
	}
	result := domain.RuleBinding{}
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_bindings (tenant_id, rule_version_id, scope_kind, scope_ref, precedence, target_branch_glob, path_include_glob, path_exclude_glob, state, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, tenant_id, rule_version_id, scope_kind, scope_ref, precedence, target_branch_glob, path_include_glob, path_exclude_glob, state, created_by, created_at, updated_at`,
		tenantID, input.RuleVersionID, input.ScopeKind, input.ScopeRef, input.Precedence, input.TargetBranchGlob, input.PathIncludeGlob, input.PathExcludeGlob, input.State, actor).
		Scan(&result.ID, &result.TenantID, &result.RuleVersionID, &result.ScopeKind, &result.ScopeRef, &result.Precedence, &result.TargetBranchGlob, &result.PathIncludeGlob, &result.PathExcludeGlob, &result.State, &result.CreatedBy, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return domain.RuleBinding{}, fmt.Errorf("create rule binding: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'rule_binding.created', $3, jsonb_build_object('rule_version_id', $4::text, 'scope_kind', $5::text, 'scope_ref', $6::text, 'precedence', $7::integer))`, tenantID, actor, result.ID.String(), result.RuleVersionID.String(), result.ScopeKind, result.ScopeRef, result.Precedence); err != nil {
		return domain.RuleBinding{}, fmt.Errorf("audit rule binding creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleBinding{}, fmt.Errorf("commit rule binding creation: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ListRuleBindings(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleBinding, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("rule binding limit must be from 1 to 100")
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, rule_version_id, scope_kind, scope_ref, precedence, target_branch_glob, path_include_glob, path_exclude_glob, state, created_by, created_at, updated_at
		FROM rule_bindings
		WHERE tenant_id = $1
		ORDER BY precedence ASC, created_at DESC, id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list rule bindings: %w", err)
	}
	defer rows.Close()
	bindings := make([]domain.RuleBinding, 0)
	for rows.Next() {
		var item domain.RuleBinding
		if err := rows.Scan(&item.ID, &item.TenantID, &item.RuleVersionID, &item.ScopeKind, &item.ScopeRef, &item.Precedence, &item.TargetBranchGlob, &item.PathIncludeGlob, &item.PathExcludeGlob, &item.State, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan rule binding: %w", err)
		}
		bindings = append(bindings, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule bindings: %w", err)
	}
	return bindings, nil
}

// RuleSnapshotForJob is used by the isolated runner immediately before OCR
// execution. The join is by durable job/run IDs only; no mutable bindings are
// consulted once admission has completed.
func (s *PostgresStore) RuleSnapshotForJob(ctx context.Context, jobID uuid.UUID) (domain.RuleSnapshot, error) {
	if jobID == uuid.Nil {
		return domain.RuleSnapshot{}, ErrNotFound
	}
	var snapshot domain.RuleSnapshot
	var payload []byte
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.sha256, s.compiler_version, s.engine, s.canonical_payload, s.created_at
		FROM review_runs r
		JOIN rule_snapshots s ON s.id = r.rule_snapshot_id
		WHERE r.legacy_job_id = $1`, jobID).
		Scan(&snapshot.ID, &snapshot.SHA256, &snapshot.CompilerVersion, &snapshot.Engine, &payload, &snapshot.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleSnapshot{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleSnapshot{}, fmt.Errorf("load rule snapshot for job: %w", err)
	}
	snapshot.CanonicalPayload = append(json.RawMessage(nil), payload...)
	return snapshot, nil
}

func validRuleBindingInput(input *domain.RuleBindingInput) bool {
	input.ScopeKind = strings.ToLower(strings.TrimSpace(input.ScopeKind))
	input.ScopeRef = strings.TrimSpace(input.ScopeRef)
	input.TargetBranchGlob = strings.TrimSpace(input.TargetBranchGlob)
	input.PathIncludeGlob = strings.TrimSpace(input.PathIncludeGlob)
	input.PathExcludeGlob = strings.TrimSpace(input.PathExcludeGlob)
	input.State = strings.ToLower(strings.TrimSpace(input.State))
	if input.RuleVersionID == uuid.Nil || input.Precedence < 0 || (input.State != "active" && input.State != "shadow") {
		return false
	}
	switch input.ScopeKind {
	case "tenant":
		if input.ScopeRef != "" {
			return false
		}
	case "repository":
		if input.ScopeRef == "" || strings.ContainsAny(input.ScopeRef, "\t\n\r ") || !strings.Contains(input.ScopeRef, "/") {
			return false
		}
	default:
		return false
	}
	for _, pattern := range []string{input.TargetBranchGlob, input.PathIncludeGlob, input.PathExcludeGlob} {
		if pattern == "" {
			continue
		}
		if _, err := path.Match(pattern, "validation-target"); err != nil {
			return false
		}
	}
	return true
}
