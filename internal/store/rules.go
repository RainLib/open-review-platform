package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/rulecatalog"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) CreateRuleSet(ctx context.Context, actor, tenantSlug string, input domain.RuleSetInput) (domain.RuleSetWithDraft, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.Name) > 160 || len(input.Description) > 4_000 || len(input.Rules) == 0 {
		return domain.RuleSetWithDraft{}, fmt.Errorf("%w: name, description, or rules", ErrInvalidRuleSet)
	}
	var parsed []rules.Rule
	if err := json.Unmarshal(input.Rules, &parsed); err != nil || len(parsed) == 0 {
		return domain.RuleSetWithDraft{}, fmt.Errorf("%w: rules must be a non-empty array", ErrInvalidRuleSet)
	}
	compiled, err := rules.Compile([]rules.Source{{VersionID: "draft", Precedence: 0, Rules: parsed}})
	if err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("%w: validate rules: %v", ErrInvalidRuleSet, err)
	}
	if _, err := rules.OCRRuleFileForSnapshot(compiled.Snapshot); err != nil {
		return domain.RuleSetWithDraft{}, fmt.Errorf("%w: validate OCR adapter: %v", ErrInvalidRuleSet, err)
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
		WHERE t.slug = $1 AND m.subject = $2 AND m.active = TRUE AND m.role IN ('owner', 'admin', 'rule_admin')`, tenantSlug, actor).Scan(&tenantID)
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
		SELECT rule_set.id,
		       rule_set.tenant_id,
		       rule_set.name,
		       rule_set.description,
		       COALESCE(rule_set.catalog_id, ''),
		       COALESCE(rule_set.catalog_version, ''),
		       COALESCE(rule_set.catalog_content_sha256, ''),
		       COALESCE(rule_set.catalog_origin, ''),
		       rule_set.created_by,
		       rule_set.created_at,
		       rule_set.updated_at,
		       latest.id,
		       latest.version,
		       latest.revision,
		       latest.state,
		       latest.content_sha256,
		       latest.created_by,
		       latest.created_at,
		       latest.updated_at
		FROM rule_sets rule_set
		JOIN LATERAL (
			SELECT id, version, revision, state, content_sha256, created_by, created_at, updated_at
			FROM rule_versions
			WHERE rule_set_id = rule_set.id
			ORDER BY version DESC
			LIMIT 1
		) latest ON TRUE
		WHERE rule_set.tenant_id = $1
		ORDER BY rule_set.updated_at DESC, rule_set.id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list rule sets: %w", err)
	}
	defer rows.Close()
	sets := make([]domain.RuleSet, 0)
	for rows.Next() {
		var item domain.RuleSet
		var catalogID, catalogVersion, catalogContentSHA256, catalogOrigin string
		latest := domain.RuleVersionSummary{}
		if err := rows.Scan(
			&item.ID,
			&item.TenantID,
			&item.Name,
			&item.Description,
			&catalogID,
			&catalogVersion,
			&catalogContentSHA256,
			&catalogOrigin,
			&item.CreatedBy,
			&item.CreatedAt,
			&item.UpdatedAt,
			&latest.ID,
			&latest.Version,
			&latest.Revision,
			&latest.State,
			&latest.ContentSHA256,
			&latest.CreatedBy,
			&latest.CreatedAt,
			&latest.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan rule set: %w", err)
		}
		if catalogID != "" {
			item.Catalog = &domain.RuleCatalogProvenance{ID: catalogID, Version: catalogVersion, ContentSHA256: catalogContentSHA256, Origin: catalogOrigin}
		}
		item.LatestVersion = &latest
		sets = append(sets, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule sets: %w", err)
	}
	return sets, nil
}

// ListRuleCatalog returns only release-bundled templates. It deliberately does
// not derive a recommendation from repository code, review comments, or a
// model response: all browser-visible choices have a fixed source and digest.
func (s *PostgresStore) ListRuleCatalog(ctx context.Context, actor, tenantSlug string) ([]domain.RuleCatalogEntry, error) {
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT rule_set.catalog_id,
		       rule_set.catalog_version,
		       rule_set.id,
		       rule_set.name,
		       latest.version,
		       latest.state
		FROM rule_sets rule_set
		JOIN LATERAL (
			SELECT version, state
			FROM rule_versions
			WHERE rule_set_id = rule_set.id
			ORDER BY version DESC
			LIMIT 1
		) latest ON TRUE
		WHERE rule_set.tenant_id = $1
		  AND rule_set.catalog_id IS NOT NULL
		ORDER BY rule_set.catalog_id, rule_set.catalog_version`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list rule catalog installations: %w", err)
	}
	defer rows.Close()
	installed := make(map[string]domain.RuleCatalogInstallation)
	for rows.Next() {
		var catalogID, catalogVersion string
		var installation domain.RuleCatalogInstallation
		if err := rows.Scan(&catalogID, &catalogVersion, &installation.RuleSetID, &installation.RuleSetName, &installation.RuleVersion, &installation.RuleVersionState); err != nil {
			return nil, fmt.Errorf("scan rule catalog installation: %w", err)
		}
		installed[catalogIdentity(catalogID, catalogVersion)] = installation
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule catalog installations: %w", err)
	}
	entries := rulecatalog.Entries()
	result := make([]domain.RuleCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		item := domain.RuleCatalogEntry{
			ID:            entry.ID,
			Version:       entry.Version,
			Title:         entry.Title,
			Description:   entry.Description,
			Tags:          append([]string(nil), entry.Tags...),
			RuleCount:     len(entry.Rules),
			ContentSHA256: entry.ContentSHA256,
			Origin:        rulecatalog.Origin,
			CanInstall:    canManageRules(role),
		}
		if installation, ok := installed[catalogIdentity(entry.ID, entry.Version)]; ok {
			copy := installation
			item.Installation = &copy
		}
		result = append(result, item)
	}
	return result, nil
}

// InstallRuleCatalogEntry creates one ordinary governed draft from an exact
// release-pinned catalog payload. A catalog install never creates a binding or
// publishes a version, so it cannot silently alter a future review.
func (s *PostgresStore) InstallRuleCatalogEntry(ctx context.Context, actor, tenantSlug, catalogID string, input domain.RuleCatalogInstallInput) (domain.RuleCatalogInstallResult, error) {
	catalogID = strings.TrimSpace(catalogID)
	input.Version = strings.TrimSpace(input.Version)
	input.ContentSHA256 = strings.ToLower(strings.TrimSpace(input.ContentSHA256))
	entry, ok := rulecatalog.Find(catalogID, input.Version, input.ContentSHA256)
	if !ok {
		return domain.RuleCatalogInstallResult{}, fmt.Errorf("%w: catalog id, version, or content digest", ErrInvalidRuleCatalog)
	}
	canonicalRules, err := json.Marshal(entry.Rules)
	if err != nil {
		return domain.RuleCatalogInstallResult{}, fmt.Errorf("encode catalog rules: %w", err)
	}
	compiled, err := rules.Compile([]rules.Source{{VersionID: "draft", Precedence: 0, Rules: entry.Rules}})
	if err != nil {
		return domain.RuleCatalogInstallResult{}, fmt.Errorf("validate catalog rules: %w", err)
	}
	if _, err := rules.OCRRuleFileForSnapshot(compiled.Snapshot); err != nil {
		return domain.RuleCatalogInstallResult{}, fmt.Errorf("validate catalog rules for OCR: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleCatalogInstallResult{}, fmt.Errorf("begin catalog installation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RuleCatalogInstallResult{}, err
	}
	if !canManageRules(role) {
		return domain.RuleCatalogInstallResult{}, ErrForbidden
	}

	result := domain.RuleCatalogInstallResult{}
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_sets (tenant_id, name, description, catalog_id, catalog_version, catalog_content_sha256, catalog_origin, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant_id, catalog_id, catalog_version) WHERE catalog_id IS NOT NULL DO NOTHING
		RETURNING id, tenant_id, name, description, created_by, created_at, updated_at`,
		tenantID, "Core · "+entry.Title, entry.Description, entry.ID, entry.Version, entry.ContentSHA256, rulecatalog.Origin, actor).
		Scan(&result.RuleSet.ID, &result.RuleSet.TenantID, &result.RuleSet.Name, &result.RuleSet.Description, &result.RuleSet.CreatedBy, &result.RuleSet.CreatedAt, &result.RuleSet.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		result.Replayed = true
		if err := scanCatalogInstallation(tx.QueryRow(ctx, `
			SELECT rule_set.id, rule_set.tenant_id, rule_set.name, rule_set.description,
			       rule_set.created_by, rule_set.created_at, rule_set.updated_at,
			       version.id, version.rule_set_id, version.version, version.revision,
			       version.state, version.rules, version.content_sha256, version.created_by,
			       version.created_at, version.updated_at
			FROM rule_sets rule_set
			JOIN LATERAL (
				SELECT id, rule_set_id, version, revision, state, rules, content_sha256, created_by, created_at, updated_at
				FROM rule_versions
				WHERE rule_set_id = rule_set.id
				ORDER BY version DESC
				LIMIT 1
			) version ON TRUE
			WHERE rule_set.tenant_id = $1 AND rule_set.catalog_id = $2 AND rule_set.catalog_version = $3`, tenantID, entry.ID, entry.Version), &result); err != nil {
			return domain.RuleCatalogInstallResult{}, fmt.Errorf("load existing catalog installation: %w", err)
		}
		result.RuleSet.Catalog = &domain.RuleCatalogProvenance{ID: entry.ID, Version: entry.Version, ContentSHA256: entry.ContentSHA256, Origin: rulecatalog.Origin}
		if err := tx.Commit(ctx); err != nil {
			return domain.RuleCatalogInstallResult{}, fmt.Errorf("commit existing catalog installation: %w", err)
		}
		return result, nil
	}
	if isUniqueViolation(err) {
		return domain.RuleCatalogInstallResult{}, ErrConflict
	}
	if err != nil {
		return domain.RuleCatalogInstallResult{}, fmt.Errorf("create catalog rule set: %w", err)
	}
	result.RuleSet.Catalog = &domain.RuleCatalogProvenance{ID: entry.ID, Version: entry.Version, ContentSHA256: entry.ContentSHA256, Origin: rulecatalog.Origin}
	var draftRules []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_versions (rule_set_id, version, state, rules, content_sha256, created_by)
		VALUES ($1, 1, 'draft', $2::jsonb, $3, $4)
		RETURNING id, rule_set_id, version, revision, state, rules, content_sha256, created_by, created_at, updated_at`,
		result.RuleSet.ID, string(canonicalRules), compiled.SHA256, actor).
		Scan(&result.Draft.ID, &result.Draft.RuleSetID, &result.Draft.Version, &result.Draft.Revision, &result.Draft.State, &draftRules, &result.Draft.ContentSHA256, &result.Draft.CreatedBy, &result.Draft.CreatedAt, &result.Draft.UpdatedAt)
	if err != nil {
		return domain.RuleCatalogInstallResult{}, fmt.Errorf("create catalog draft version: %w", err)
	}
	result.Draft.Rules = json.RawMessage(draftRules)
	result.RuleSet.LatestVersion = &domain.RuleVersionSummary{
		ID: result.Draft.ID, Version: result.Draft.Version, Revision: result.Draft.Revision,
		State: result.Draft.State, ContentSHA256: result.Draft.ContentSHA256,
		CreatedBy: result.Draft.CreatedBy, CreatedAt: result.Draft.CreatedAt, UpdatedAt: result.Draft.UpdatedAt,
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, 'rule_catalog.installed', $3,
		jsonb_build_object('catalog_id', $4::text, 'catalog_version', $5::text, 'catalog_content_sha256', $6::text, 'catalog_origin', $7::text, 'rule_version_id', $8::text, 'rule_version_content_sha256', $9::text))`,
		tenantID, actor, result.RuleSet.ID.String(), entry.ID, entry.Version, entry.ContentSHA256, rulecatalog.Origin, result.Draft.ID.String(), result.Draft.ContentSHA256); err != nil {
		return domain.RuleCatalogInstallResult{}, fmt.Errorf("audit catalog installation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleCatalogInstallResult{}, fmt.Errorf("commit catalog installation: %w", err)
	}
	return result, nil
}

func catalogIdentity(id, version string) string { return id + "\x00" + version }

func scanCatalogInstallation(row rowScanner, result *domain.RuleCatalogInstallResult) error {
	var draftRules []byte
	err := row.Scan(
		&result.RuleSet.ID, &result.RuleSet.TenantID, &result.RuleSet.Name, &result.RuleSet.Description,
		&result.RuleSet.CreatedBy, &result.RuleSet.CreatedAt, &result.RuleSet.UpdatedAt,
		&result.Draft.ID, &result.Draft.RuleSetID, &result.Draft.Version, &result.Draft.Revision,
		&result.Draft.State, &draftRules, &result.Draft.ContentSHA256, &result.Draft.CreatedBy,
		&result.Draft.CreatedAt, &result.Draft.UpdatedAt,
	)
	if err != nil {
		return err
	}
	result.Draft.Rules = json.RawMessage(draftRules)
	result.RuleSet.LatestVersion = &domain.RuleVersionSummary{
		ID: result.Draft.ID, Version: result.Draft.Version, Revision: result.Draft.Revision,
		State: result.Draft.State, ContentSHA256: result.Draft.ContentSHA256,
		CreatedBy: result.Draft.CreatedBy, CreatedAt: result.Draft.CreatedAt, UpdatedAt: result.Draft.UpdatedAt,
	}
	return nil
}

// ListRuleApprovalRequests returns an actor-aware governance view. The query
// derives vote counts from immutable decisions instead of copying counters
// onto the request, so the evidence shown in the console cannot drift.
func (s *PostgresStore) ListRuleApprovalRequests(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleApprovalSummary, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("rule approval limit must be from 1 to 100")
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT request.id,
		       rule_set.id,
		       rule_set.name,
		       rule_version.id,
		       rule_version.version,
		       rule_version.state,
		       request.content_sha256,
		       request.requested_by,
		       request.required_approvals,
		       COUNT(approval.id) FILTER (WHERE approval.decision = 'approved' AND approval.content_sha256 = request.content_sha256),
		       COUNT(approval.id) FILTER (WHERE approval.decision = 'rejected' AND approval.content_sha256 = request.content_sha256),
		       COALESCE(MAX(approval.decision) FILTER (WHERE approval.approver_subject = $2), ''),
		       request.state,
		       request.created_at,
		       request.decided_at
		FROM rule_approval_requests request
		JOIN rule_versions rule_version ON rule_version.id = request.rule_version_id
		JOIN rule_sets rule_set ON rule_set.id = rule_version.rule_set_id
		LEFT JOIN rule_approvals approval ON approval.request_id = request.id
		WHERE request.tenant_id = $1
		GROUP BY request.id, rule_set.id, rule_set.name, rule_version.id, rule_version.version, rule_version.state
		ORDER BY CASE request.state WHEN 'pending' THEN 0 WHEN 'approved' THEN 1 ELSE 2 END,
		         request.created_at DESC,
		         request.id DESC
		LIMIT $3`, tenantID, actor, limit)
	if err != nil {
		return nil, fmt.Errorf("list rule approval requests: %w", err)
	}
	defer rows.Close()
	requests := make([]domain.RuleApprovalSummary, 0)
	for rows.Next() {
		var item domain.RuleApprovalSummary
		if err := rows.Scan(
			&item.ID,
			&item.RuleSetID,
			&item.RuleSetName,
			&item.RuleVersionID,
			&item.Version,
			&item.VersionState,
			&item.ContentSHA256,
			&item.RequestedBy,
			&item.RequiredApprovals,
			&item.ApprovalCount,
			&item.RejectionCount,
			&item.ActorDecision,
			&item.State,
			&item.CreatedAt,
			&item.DecidedAt,
		); err != nil {
			return nil, fmt.Errorf("scan rule approval request: %w", err)
		}
		item.CanDecide = canManageRules(role) && item.State == "pending" && item.RequestedBy != actor && item.ActorDecision == ""
		item.CanPublish = canManageRules(role) && item.State == "approved" && item.VersionState == "approved"
		requests = append(requests, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule approval requests: %w", err)
	}
	return requests, nil
}

// PreviewRuleVersionImpact compiles a candidate against the exact active
// control-plane bindings for a repository and branch. It is deliberately
// read-only: no review run, provider write, snapshot row, or approval is
// created by Test Lab.
func (s *PostgresStore) PreviewRuleVersionImpact(ctx context.Context, actor, tenantSlug string, ruleSetID uuid.UUID, version int, input domain.RuleImpactPreviewInput) (domain.RuleImpactPreview, error) {
	scope, scopeValid := normalizeProviderQualifiedRuleScope("repository", input.Repository, input.Provider, input.APIBaseURL)
	input.Repository, input.Provider, input.APIBaseURL = scope.Ref, scope.Provider, scope.APIBaseURL
	input.TargetBranch = strings.TrimSpace(input.TargetBranch)
	if ruleSetID == uuid.Nil || version < 1 || !scopeValid || !scope.QualifiedRepository() || len(input.Repository) > 300 || input.TargetBranch == "" || len(input.TargetBranch) > 255 || input.Precedence < 0 || input.Precedence > 10_000 {
		return domain.RuleImpactPreview{}, ErrInvalidRuleImpact
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.RuleImpactPreview{}, err
	}

	result := domain.RuleImpactPreview{
		RuleSetID:       ruleSetID,
		Repository:      input.Repository,
		Provider:        input.Provider,
		APIBaseURL:      input.APIBaseURL,
		TargetBranch:    input.TargetBranch,
		Precedence:      input.Precedence,
		AddedRuleKeys:   []string{},
		ChangedRuleKeys: []string{},
		RemovedRuleKeys: []string{},
		Uncertainty: []string{
			"Static preview only; OCR and the configured LLM were not executed.",
			"Historical coverage summarizes the last 90 days and does not predict finding quality.",
			"Latency and token cost require a fixture or historical replay run.",
		},
	}
	var rawCandidate []byte
	err = s.pool.QueryRow(ctx, `
		SELECT rule_set.name,
		       rule_version.id,
		       rule_version.version,
		       rule_version.state,
		       rule_version.content_sha256,
		       rule_version.rules
		FROM rule_versions rule_version
		JOIN rule_sets rule_set ON rule_set.id = rule_version.rule_set_id
		WHERE rule_set.tenant_id = $1
		  AND rule_set.id = $2
		  AND rule_version.version = $3`, tenantID, ruleSetID, version).
		Scan(&result.RuleSetName, &result.RuleVersionID, &result.Version, &result.VersionState, &result.ContentSHA256, &rawCandidate)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleImpactPreview{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleImpactPreview{}, fmt.Errorf("load candidate rule version: %w", err)
	}
	var candidateRules []rules.Rule
	if err := json.Unmarshal(rawCandidate, &candidateRules); err != nil {
		return domain.RuleImpactPreview{}, fmt.Errorf("decode candidate rule version: %w", err)
	}
	result.CandidateCounts = impactRuleCounts(candidateRules)

	baselineSources, matchedBindings, err := s.activeRuleSourcesForPreview(ctx, tenantID, scope, input.TargetBranch)
	if err != nil {
		return domain.RuleImpactPreview{}, err
	}
	result.MatchedBindings = matchedBindings
	baseline, err := rules.Compile(baselineSources)
	if err != nil {
		return domain.RuleImpactPreview{}, fmt.Errorf("compile current rule baseline: %w", err)
	}
	baselineExceptions, _, err := resolveApplicableRuleExceptions(ctx, s.pool, tenantID, scope, input.TargetBranch, ruleSourceVersions(baselineSources))
	if err != nil {
		return domain.RuleImpactPreview{}, err
	}
	baseline, err = rules.ApplyExceptions(baseline, baselineExceptions)
	if err != nil {
		return domain.RuleImpactPreview{}, fmt.Errorf("apply baseline rule exceptions: %w", err)
	}
	result.BaselineSHA256 = baseline.SHA256
	result.BaselineRuleCount = len(baseline.Snapshot.Rules)

	candidateSources := append(append([]rules.Source(nil), baselineSources...), rules.Source{
		VersionID:  "candidate:" + result.RuleVersionID.String(),
		Precedence: input.Precedence,
		Rules:      candidateRules,
	})
	candidate, compileErr := rules.Compile(candidateSources)
	if compileErr != nil {
		result.Conflict = compileErr.Error()
	} else {
		candidateExceptions, _, exceptionErr := resolveApplicableRuleExceptions(ctx, s.pool, tenantID, scope, input.TargetBranch, ruleSourceVersions(candidateSources))
		if exceptionErr != nil {
			return domain.RuleImpactPreview{}, exceptionErr
		}
		candidate, exceptionErr = rules.ApplyExceptions(candidate, candidateExceptions)
		if exceptionErr != nil {
			return domain.RuleImpactPreview{}, fmt.Errorf("apply candidate rule exceptions: %w", exceptionErr)
		}
		result.Valid = true
		result.CandidateSHA256 = candidate.SHA256
		result.CandidateRuleCount = len(candidate.Snapshot.Rules)
		result.AddedRuleKeys, result.ChangedRuleKeys, result.RemovedRuleKeys = diffEffectiveRuleKeys(baseline.Snapshot, candidate.Snapshot)
	}

	var oldestRunAt, newestRunAt *time.Time
	err = s.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT job.id),
		       COUNT(finding.id),
		       COUNT(finding.id) FILTER (WHERE finding.severity IN ('high', 'critical')),
		       MIN(job.created_at),
		       MAX(job.created_at)
		FROM review_jobs job
		LEFT JOIN review_findings finding ON finding.job_id = job.id
		WHERE job.tenant_id = $1
		  AND job.repository = $2
		  AND job.provider = $3
		  AND job.api_base_url = $4
		  AND job.created_at >= now() - interval '90 days'`, tenantID, input.Repository, input.Provider, input.APIBaseURL).
		Scan(
			&result.HistoricalSample.RunCount,
			&result.HistoricalSample.FindingCount,
			&result.HistoricalSample.HighRiskFinding,
			&oldestRunAt,
			&newestRunAt,
		)
	if err != nil {
		return domain.RuleImpactPreview{}, fmt.Errorf("summarize historical rule sample: %w", err)
	}
	result.HistoricalSample.OldestRunAt = oldestRunAt
	result.HistoricalSample.NewestRunAt = newestRunAt
	if result.HistoricalSample.RunCount == 0 {
		result.Uncertainty = append(result.Uncertainty, "No historical runs were available for this repository.")
	}
	return result, nil
}

func ruleSourceVersions(sources []rules.Source) map[string]int {
	versions := make(map[string]int, len(sources))
	for _, source := range sources {
		versions[source.VersionID] = source.Precedence
	}
	return versions
}

func (s *PostgresStore) activeRuleSourcesForPreview(ctx context.Context, tenantID uuid.UUID, scope domain.ReviewConfigScope, targetBranch string) ([]rules.Source, int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT version.id,
		       binding.precedence,
		       version.rules,
		       binding.target_branch_glob,
		       binding.path_include_glob,
		       binding.path_exclude_glob
		FROM rule_bindings binding
		JOIN rule_versions version ON version.id = binding.rule_version_id
		JOIN rule_sets rule_set ON rule_set.id = version.rule_set_id
		WHERE binding.tenant_id = $1
		  AND binding.state = 'active'
		  AND version.state = 'published'
		  AND rule_set.tenant_id = $1
		  AND (binding.starts_at IS NULL OR binding.starts_at <= now())
		  AND (binding.ends_at IS NULL OR binding.ends_at > now())
		  AND (binding.scope_kind = 'tenant' OR (
			binding.scope_kind = 'repository' AND binding.scope_ref = $2
			AND ((binding.scope_provider = $3 AND binding.scope_api_base_url = $4)
			     OR (binding.scope_provider = '' AND binding.scope_api_base_url = ''))
		  ))
		ORDER BY binding.precedence ASC, version.id ASC`, tenantID, scope.Ref, scope.Provider, scope.APIBaseURL)
	if err != nil {
		return nil, 0, fmt.Errorf("select preview rule bindings: %w", err)
	}
	defer rows.Close()
	sources := make([]rules.Source, 0)
	seenVersion := make(map[string]int)
	matchedBindings := 0
	for rows.Next() {
		var versionID uuid.UUID
		var precedence int
		var rawRules []byte
		var branchGlob, includeGlob, excludeGlob string
		if err := rows.Scan(&versionID, &precedence, &rawRules, &branchGlob, &includeGlob, &excludeGlob); err != nil {
			return nil, 0, fmt.Errorf("scan preview rule binding: %w", err)
		}
		if !matchesRuleTargetBranch(branchGlob, targetBranch) {
			continue
		}
		matchedBindings++
		versionKey := versionID.String()
		if previous, exists := seenVersion[versionKey]; exists {
			if previous != precedence {
				return nil, 0, fmt.Errorf("published rule version %s is bound at conflicting precedences", versionKey)
			}
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
			return nil, 0, fmt.Errorf("decode preview rule version %s: %w", versionKey, err)
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
		return nil, 0, fmt.Errorf("iterate preview rule bindings: %w", err)
	}
	return sources, matchedBindings, nil
}

func impactRuleCounts(candidateRules []rules.Rule) domain.RuleImpactCounts {
	counts := domain.RuleImpactCounts{Total: len(candidateRules)}
	for _, rule := range candidateRules {
		if rule.Enforcement == rules.Mandatory {
			counts.Mandatory++
		}
		switch strings.ToLower(strings.TrimSpace(rule.Severity)) {
		case "critical":
			counts.Critical++
		case "high":
			counts.High++
		case "medium":
			counts.Medium++
		case "low":
			counts.Low++
		}
	}
	return counts
}

func diffEffectiveRuleKeys(baseline, candidate rules.Snapshot) (added, changed, removed []string) {
	baselineSignatures := effectiveRuleSignatures(baseline)
	candidateSignatures := effectiveRuleSignatures(candidate)
	for key, signature := range candidateSignatures {
		baselineSignature, exists := baselineSignatures[key]
		if !exists {
			added = append(added, key)
		} else if baselineSignature != signature {
			changed = append(changed, key)
		}
	}
	for key := range baselineSignatures {
		if _, exists := candidateSignatures[key]; !exists {
			removed = append(removed, key)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(removed)
	return added, changed, removed
}

func effectiveRuleSignatures(snapshot rules.Snapshot) map[string]string {
	grouped := make(map[string][]rules.EffectiveRule)
	for _, rule := range snapshot.Rules {
		grouped[rule.Key] = append(grouped[rule.Key], rule)
	}
	result := make(map[string]string, len(grouped))
	for key, candidates := range grouped {
		encoded, _ := json.Marshal(candidates)
		result[key] = string(encoded)
	}
	return result
}

// RequestRuleApproval moves an immutable draft into governance review. The
// request records the version's content SHA so a stale approval can never
// authorize a changed rule payload.
func (s *PostgresStore) RequestRuleApproval(ctx context.Context, actor, tenantSlug string, ruleSetID uuid.UUID, version int, input domain.RuleApprovalRequestInput) (domain.RuleApprovalRequest, error) {
	if ruleSetID == uuid.Nil || version < 1 || input.RequiredApprovals < 0 || input.RequiredApprovals > 5 {
		return domain.RuleApprovalRequest{}, ErrInvalidRuleApproval
	}
	if input.RequiredApprovals == 0 {
		input.RequiredApprovals = 1
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleApprovalRequest{}, fmt.Errorf("begin rule approval request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RuleApprovalRequest{}, err
	}
	if !canManageRules(role) {
		return domain.RuleApprovalRequest{}, ErrForbidden
	}
	var versionID uuid.UUID
	var contentSHA256 string
	err = tx.QueryRow(ctx, `
		UPDATE rule_versions rule_version
		SET state = 'in_review', revision = revision + 1, updated_at = now()
		FROM rule_sets rule_set
		WHERE rule_version.rule_set_id = rule_set.id
		  AND rule_set.tenant_id = $1
		  AND rule_set.id = $2
		  AND rule_version.version = $3
		  AND rule_version.state = 'draft'
		RETURNING rule_version.id, rule_version.content_sha256`, tenantID, ruleSetID, version).Scan(&versionID, &contentSHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleApprovalRequest{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleApprovalRequest{}, fmt.Errorf("move rule version into review: %w", err)
	}
	result := domain.RuleApprovalRequest{}
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_approval_requests (tenant_id, rule_version_id, content_sha256, requested_by, required_approvals)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, tenant_id, rule_version_id, content_sha256, requested_by, required_approvals, state, created_at, decided_at`, tenantID, versionID, contentSHA256, actor, input.RequiredApprovals).
		Scan(&result.ID, &result.TenantID, &result.RuleVersionID, &result.ContentSHA256, &result.RequestedBy, &result.RequiredApprovals, &result.State, &result.CreatedAt, &result.DecidedAt)
	if isUniqueViolation(err) {
		return domain.RuleApprovalRequest{}, ErrConflict
	}
	if err != nil {
		return domain.RuleApprovalRequest{}, fmt.Errorf("create rule approval request: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'rule_approval.requested', $3, jsonb_build_object('rule_version_id', $4::text, 'content_sha256', $5::text, 'required_approvals', $6::integer))`, tenantID, actor, result.ID.String(), versionID.String(), contentSHA256, input.RequiredApprovals); err != nil {
		return domain.RuleApprovalRequest{}, fmt.Errorf("audit rule approval request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleApprovalRequest{}, fmt.Errorf("commit rule approval request: %w", err)
	}
	return result, nil
}

// DecideRuleApproval records one independent reviewer decision. The requester
// cannot self-approve, and an approved request only unlocks publication after
// the configured number of matching-content approvals has committed.
func (s *PostgresStore) DecideRuleApproval(ctx context.Context, actor, tenantSlug string, requestID uuid.UUID, input domain.RuleApprovalDecisionInput) (domain.RuleApprovalRequest, error) {
	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.Comment = strings.TrimSpace(input.Comment)
	if requestID == uuid.Nil || (input.Decision != "approved" && input.Decision != "rejected") || len(input.Comment) > 2_000 {
		return domain.RuleApprovalRequest{}, ErrInvalidRuleApproval
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleApprovalRequest{}, fmt.Errorf("begin rule approval decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RuleApprovalRequest{}, err
	}
	if !canManageRules(role) {
		return domain.RuleApprovalRequest{}, ErrForbidden
	}
	result := domain.RuleApprovalRequest{}
	var currentVersionSHA256 string
	err = tx.QueryRow(ctx, `
		SELECT request.id, request.tenant_id, request.rule_version_id, request.content_sha256, request.requested_by, request.required_approvals, request.state, request.created_at, request.decided_at, rule_version.content_sha256
		FROM rule_approval_requests request
		JOIN rule_versions rule_version ON rule_version.id = request.rule_version_id
		WHERE request.id = $1 AND request.tenant_id = $2
		FOR UPDATE OF request, rule_version`, requestID, tenantID).
		Scan(&result.ID, &result.TenantID, &result.RuleVersionID, &result.ContentSHA256, &result.RequestedBy, &result.RequiredApprovals, &result.State, &result.CreatedAt, &result.DecidedAt, &currentVersionSHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleApprovalRequest{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleApprovalRequest{}, fmt.Errorf("load rule approval request: %w", err)
	}
	if result.State != "pending" || result.ContentSHA256 != currentVersionSHA256 {
		return domain.RuleApprovalRequest{}, ErrConflict
	}
	if result.RequestedBy == actor {
		return domain.RuleApprovalRequest{}, ErrForbidden
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rule_approvals (request_id, approver_subject, decision, content_sha256, comment) VALUES ($1, $2, $3, $4, $5)`, result.ID, actor, input.Decision, result.ContentSHA256, input.Comment); isUniqueViolation(err) {
		return domain.RuleApprovalRequest{}, ErrConflict
	} else if err != nil {
		return domain.RuleApprovalRequest{}, fmt.Errorf("record rule approval decision: %w", err)
	}
	if input.Decision == "rejected" {
		if _, err := tx.Exec(ctx, `UPDATE rule_approval_requests SET state = 'rejected', decided_at = now() WHERE id = $1`, result.ID); err != nil {
			return domain.RuleApprovalRequest{}, fmt.Errorf("reject rule approval request: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE rule_versions SET state = 'draft', revision = revision + 1, updated_at = now() WHERE id = $1 AND state = 'in_review'`, result.RuleVersionID); err != nil {
			return domain.RuleApprovalRequest{}, fmt.Errorf("return rejected rule version to draft: %w", err)
		}
		result.State = "rejected"
		now := time.Now().UTC()
		result.DecidedAt = &now
	} else {
		var approvals int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM rule_approvals WHERE request_id = $1 AND decision = 'approved' AND content_sha256 = $2`, result.ID, result.ContentSHA256).Scan(&approvals); err != nil {
			return domain.RuleApprovalRequest{}, fmt.Errorf("count rule approvals: %w", err)
		}
		if approvals >= result.RequiredApprovals {
			if _, err := tx.Exec(ctx, `UPDATE rule_approval_requests SET state = 'approved', decided_at = now() WHERE id = $1`, result.ID); err != nil {
				return domain.RuleApprovalRequest{}, fmt.Errorf("approve rule approval request: %w", err)
			}
			if _, err := tx.Exec(ctx, `UPDATE rule_versions SET state = 'approved', revision = revision + 1, updated_at = now() WHERE id = $1 AND state = 'in_review' AND content_sha256 = $2`, result.RuleVersionID, result.ContentSHA256); err != nil {
				return domain.RuleApprovalRequest{}, fmt.Errorf("approve rule version: %w", err)
			}
			result.State = "approved"
			now := time.Now().UTC()
			result.DecidedAt = &now
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'rule_approval.decided', $3, jsonb_build_object('decision', $4::text, 'content_sha256', $5::text))`, tenantID, actor, result.ID.String(), input.Decision, result.ContentSHA256); err != nil {
		return domain.RuleApprovalRequest{}, fmt.Errorf("audit rule approval decision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleApprovalRequest{}, fmt.Errorf("commit rule approval decision: %w", err)
	}
	return result, nil
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
	if !canManageRules(role) {
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
		  AND v.state = 'approved'
		  AND EXISTS (
		      SELECT 1 FROM rule_approval_requests request
		      WHERE request.rule_version_id = v.id
		        AND request.state = 'approved'
		        AND request.content_sha256 = v.content_sha256
		  )
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
		return domain.RuleBinding{}, ErrInvalidRuleBinding
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
	if !canManageRules(role) {
		return domain.RuleBinding{}, ErrForbidden
	}
	scope := domain.ReviewConfigScope{Kind: domain.ReviewConfigScopeKind(input.ScopeKind), Ref: input.ScopeRef, Provider: input.ScopeProvider, APIBaseURL: input.ScopeAPIBaseURL}
	if err := ensureRepositoryReviewConfigScope(ctx, tx, tenantID, scope); err != nil {
		if errors.Is(err, ErrInvalidReviewConfig) {
			return domain.RuleBinding{}, ErrInvalidRuleBinding
		}
		return domain.RuleBinding{}, fmt.Errorf("authorize rule binding repository scope: %w", err)
	}
	var ruleSetID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT v.rule_set_id
		FROM rule_versions v JOIN rule_sets rs ON rs.id = v.rule_set_id
		WHERE v.id = $1 AND v.state = 'published' AND rs.tenant_id = $2`, input.RuleVersionID, tenantID).Scan(&ruleSetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleBinding{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleBinding{}, fmt.Errorf("authorize published rule version: %w", err)
	}
	if input.State == "active" {
		var existingBaseline bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM rule_bindings b
				JOIN rule_versions v ON v.id=b.rule_version_id
				WHERE b.tenant_id=$1 AND b.state='active' AND v.rule_set_id=$2
				  AND b.rule_version_id<>$3 AND b.scope_kind=$4 AND b.scope_ref=$5
				  AND b.scope_provider=$6 AND b.scope_api_base_url=$7
				  AND b.precedence=$8 AND b.target_branch_glob=$9
				  AND b.path_include_glob=$10 AND b.path_exclude_glob=$11)`,
			tenantID, ruleSetID, input.RuleVersionID, input.ScopeKind, input.ScopeRef,
			input.ScopeProvider, input.ScopeAPIBaseURL, input.Precedence, input.TargetBranchGlob,
			input.PathIncludeGlob, input.PathExcludeGlob).Scan(&existingBaseline)
		if err != nil {
			return domain.RuleBinding{}, fmt.Errorf("check active binding baseline: %w", err)
		}
		if existingBaseline {
			return domain.RuleBinding{}, ErrRuleRolloutRequired
		}
	}
	result := domain.RuleBinding{}
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_bindings (tenant_id, rule_version_id, scope_kind, scope_ref, scope_provider, scope_api_base_url, precedence, target_branch_glob, path_include_glob, path_exclude_glob, state, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, tenant_id, rule_version_id, scope_kind, scope_ref, scope_provider, scope_api_base_url, precedence, target_branch_glob, path_include_glob, path_exclude_glob, state, created_by, created_at, updated_at`,
		tenantID, input.RuleVersionID, input.ScopeKind, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL, input.Precedence, input.TargetBranchGlob, input.PathIncludeGlob, input.PathExcludeGlob, input.State, actor).
		Scan(&result.ID, &result.TenantID, &result.RuleVersionID, &result.ScopeKind, &result.ScopeRef, &result.ScopeProvider, &result.ScopeAPIBaseURL, &result.Precedence, &result.TargetBranchGlob, &result.PathIncludeGlob, &result.PathExcludeGlob, &result.State, &result.CreatedBy, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return domain.RuleBinding{}, fmt.Errorf("create rule binding: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'rule_binding.created', $3, jsonb_build_object('rule_version_id', $4::text, 'scope_kind', $5::text, 'scope_ref', $6::text, 'scope_provider', $7::text, 'scope_api_base_url', $8::text, 'precedence', $9::integer))`, tenantID, actor, result.ID.String(), result.RuleVersionID.String(), result.ScopeKind, result.ScopeRef, result.ScopeProvider, result.ScopeAPIBaseURL, result.Precedence); err != nil {
		return domain.RuleBinding{}, fmt.Errorf("audit rule binding creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleBinding{}, fmt.Errorf("commit rule binding creation: %w", err)
	}
	return result, nil
}

// UpdateRuleBinding changes only the lifecycle state of an existing binding.
// This makes shadow rollout and immediate rollback explicit audit events while
// preserving the immutable snapshot used by already-admitted review runs.
func (s *PostgresStore) UpdateRuleBinding(ctx context.Context, actor, tenantSlug string, bindingID uuid.UUID, input domain.RuleBindingUpdateInput) (domain.RuleBinding, error) {
	input.State = strings.ToLower(strings.TrimSpace(input.State))
	if bindingID == uuid.Nil || (input.State != "active" && input.State != "shadow" && input.State != "disabled") {
		return domain.RuleBinding{}, ErrInvalidRuleBinding
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleBinding{}, fmt.Errorf("begin rule binding update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RuleBinding{}, err
	}
	if !canManageRules(role) {
		return domain.RuleBinding{}, ErrForbidden
	}
	var currentState string
	err = tx.QueryRow(ctx, `SELECT state FROM rule_bindings WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, bindingID, tenantID).Scan(&currentState)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleBinding{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleBinding{}, fmt.Errorf("load rule binding state: %w", err)
	}
	if currentState != input.State {
		var activeCanary bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM rule_rollouts
			              WHERE tenant_id=$1 AND mode='canary' AND state IN ('active','paused')
			                AND (baseline_binding_id=$2 OR candidate_binding_id=$2))`, tenantID, bindingID).Scan(&activeCanary)
		if err != nil {
			return domain.RuleBinding{}, fmt.Errorf("check active Canary binding: %w", err)
		}
		if activeCanary {
			return domain.RuleBinding{}, ErrActiveRuleRollout
		}
	}
	if currentState == "shadow" && input.State == "active" {
		var governedCandidate bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM rule_rollouts WHERE tenant_id=$1 AND candidate_binding_id=$2)
			    OR EXISTS(
				SELECT 1 FROM rule_bindings candidate
				JOIN rule_versions candidate_version ON candidate_version.id=candidate.rule_version_id
				JOIN rule_bindings baseline ON baseline.tenant_id=candidate.tenant_id
				JOIN rule_versions baseline_version ON baseline_version.id=baseline.rule_version_id
				WHERE candidate.id=$2 AND candidate.tenant_id=$1
				  AND baseline.id<>candidate.id AND baseline.state='active'
				  AND baseline_version.rule_set_id=candidate_version.rule_set_id
				  AND baseline.rule_version_id<>candidate.rule_version_id
				  AND baseline.scope_kind=candidate.scope_kind AND baseline.scope_ref=candidate.scope_ref
				  AND baseline.scope_provider=candidate.scope_provider AND baseline.scope_api_base_url=candidate.scope_api_base_url
				  AND baseline.precedence=candidate.precedence
				  AND baseline.target_branch_glob=candidate.target_branch_glob
				  AND baseline.path_include_glob=candidate.path_include_glob
				  AND baseline.path_exclude_glob=candidate.path_exclude_glob
			)`, tenantID, bindingID).Scan(&governedCandidate)
		if err != nil {
			return domain.RuleBinding{}, fmt.Errorf("check governed binding rollout: %w", err)
		}
		if governedCandidate {
			return domain.RuleBinding{}, ErrRuleRolloutRequired
		}
	}
	result := domain.RuleBinding{}
	err = tx.QueryRow(ctx, `
		UPDATE rule_bindings
		SET state = $3, updated_at = now()
		WHERE id = $1 AND tenant_id = $2
		RETURNING id, tenant_id, rule_version_id, scope_kind, scope_ref, scope_provider, scope_api_base_url, precedence, target_branch_glob, path_include_glob, path_exclude_glob, state, created_by, created_at, updated_at`, bindingID, tenantID, input.State).
		Scan(&result.ID, &result.TenantID, &result.RuleVersionID, &result.ScopeKind, &result.ScopeRef, &result.ScopeProvider, &result.ScopeAPIBaseURL, &result.Precedence, &result.TargetBranchGlob, &result.PathIncludeGlob, &result.PathExcludeGlob, &result.State, &result.CreatedBy, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleBinding{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleBinding{}, fmt.Errorf("update rule binding: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'rule_binding.state_updated', $3, jsonb_build_object('state', $4::text))`, tenantID, actor, result.ID.String(), result.State); err != nil {
		return domain.RuleBinding{}, fmt.Errorf("audit rule binding update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleBinding{}, fmt.Errorf("commit rule binding update: %w", err)
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
		SELECT id, tenant_id, rule_version_id, scope_kind, scope_ref, scope_provider, scope_api_base_url, precedence, target_branch_glob, path_include_glob, path_exclude_glob, state, created_by, created_at, updated_at
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
		if err := rows.Scan(&item.ID, &item.TenantID, &item.RuleVersionID, &item.ScopeKind, &item.ScopeRef, &item.ScopeProvider, &item.ScopeAPIBaseURL, &item.Precedence, &item.TargetBranchGlob, &item.PathIncludeGlob, &item.PathExcludeGlob, &item.State, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

// GetRuleSnapshot exposes the immutable resolution used by one authorized
// review run. It is intentionally separate from mutable rule-set endpoints so
// task history continues to explain prior behavior after bindings change.
func (s *PostgresStore) GetRuleSnapshot(ctx context.Context, actor, tenantSlug string, runID uuid.UUID) (domain.RuleSnapshot, error) {
	if runID == uuid.Nil {
		return domain.RuleSnapshot{}, ErrNotFound
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.RuleSnapshot{}, err
	}
	var snapshot domain.RuleSnapshot
	var payload []byte
	err = s.pool.QueryRow(ctx, `
		SELECT s.id, s.sha256, s.compiler_version, s.engine, s.canonical_payload, s.created_at
		FROM review_runs r
		JOIN review_requests request ON request.id = r.request_id
		JOIN rule_snapshots s ON s.id = r.rule_snapshot_id
		WHERE r.id = $1 AND request.tenant_id = $2`, runID, tenantID).
		Scan(&snapshot.ID, &snapshot.SHA256, &snapshot.CompilerVersion, &snapshot.Engine, &payload, &snapshot.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleSnapshot{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleSnapshot{}, fmt.Errorf("load rule snapshot: %w", err)
	}
	snapshot.CanonicalPayload = append(json.RawMessage(nil), payload...)
	rows, err := s.pool.Query(ctx, `
		SELECT source.rule_version_id, version.rule_set_id, version.version, source.precedence
		FROM rule_snapshot_sources source
		JOIN rule_versions version ON version.id = source.rule_version_id
		WHERE source.snapshot_id = $1
		ORDER BY source.precedence ASC, source.rule_version_id ASC`, snapshot.ID)
	if err != nil {
		return domain.RuleSnapshot{}, fmt.Errorf("list rule snapshot sources: %w", err)
	}
	snapshot.Sources = make([]domain.RuleSnapshotSource, 0)
	for rows.Next() {
		var source domain.RuleSnapshotSource
		if err := rows.Scan(&source.RuleVersionID, &source.RuleSetID, &source.Version, &source.Precedence); err != nil {
			return domain.RuleSnapshot{}, fmt.Errorf("scan rule snapshot source: %w", err)
		}
		snapshot.Sources = append(snapshot.Sources, source)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.RuleSnapshot{}, fmt.Errorf("iterate rule snapshot sources: %w", err)
	}
	rows.Close()
	exceptionRows, err := s.pool.Query(ctx, `
		SELECT exception_id, rule_version_id, rule_key, expires_at
		FROM rule_snapshot_exceptions
		WHERE snapshot_id = $1
		ORDER BY exception_id`, snapshot.ID)
	if err != nil {
		return domain.RuleSnapshot{}, fmt.Errorf("list rule snapshot exceptions: %w", err)
	}
	defer exceptionRows.Close()
	snapshot.Exceptions = make([]domain.RuleSnapshotException, 0)
	for exceptionRows.Next() {
		var exception domain.RuleSnapshotException
		if err := exceptionRows.Scan(&exception.ExceptionID, &exception.RuleVersionID, &exception.RuleKey, &exception.ExpiresAt); err != nil {
			return domain.RuleSnapshot{}, fmt.Errorf("scan rule snapshot exception: %w", err)
		}
		snapshot.Exceptions = append(snapshot.Exceptions, exception)
	}
	if err := exceptionRows.Err(); err != nil {
		return domain.RuleSnapshot{}, fmt.Errorf("iterate rule snapshot exceptions: %w", err)
	}
	return snapshot, nil
}

func validRuleBindingInput(input *domain.RuleBindingInput) bool {
	scope, scopeValid := normalizeProviderQualifiedRuleScope(input.ScopeKind, input.ScopeRef, input.ScopeProvider, input.ScopeAPIBaseURL)
	input.ScopeKind, input.ScopeRef = string(scope.Kind), scope.Ref
	input.ScopeProvider, input.ScopeAPIBaseURL = scope.Provider, scope.APIBaseURL
	input.TargetBranchGlob = strings.TrimSpace(input.TargetBranchGlob)
	input.PathIncludeGlob = strings.TrimSpace(input.PathIncludeGlob)
	input.PathExcludeGlob = strings.TrimSpace(input.PathExcludeGlob)
	input.State = strings.ToLower(strings.TrimSpace(input.State))
	if input.RuleVersionID == uuid.Nil || !scopeValid || input.Precedence < 0 || (input.State != "active" && input.State != "shadow") {
		return false
	}
	if scope.Kind != domain.ReviewConfigTenantScope && scope.Kind != domain.ReviewConfigRepositoryScope {
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

// normalizeProviderQualifiedRuleScope shares the control-plane repository
// identity model with review configuration. Empty provider metadata is only a
// compatibility form for records read from older schemas; all new repository
// policy writes require a provider and API base URL.
func normalizeProviderQualifiedRuleScope(kind, ref string, provider domain.Provider, apiBaseURL string) (domain.ReviewConfigScope, bool) {
	scope, valid := (domain.ReviewConfigScope{
		Kind:       domain.ReviewConfigScopeKind(strings.ToLower(strings.TrimSpace(kind))),
		Ref:        strings.TrimSpace(ref),
		Provider:   provider,
		APIBaseURL: apiBaseURL,
	}).Normalize()
	if !valid || (scope.Kind == domain.ReviewConfigRepositoryScope && !scope.QualifiedRepository()) {
		return scope, false
	}
	return scope, scope.Kind == domain.ReviewConfigTenantScope || scope.Kind == domain.ReviewConfigRepositoryScope
}

func canManageRules(role string) bool {
	return role == "owner" || role == "admin" || role == "rule_admin"
}
