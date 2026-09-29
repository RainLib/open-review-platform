package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const ruleRolloutColumns = `id,tenant_id,baseline_binding_id,candidate_binding_id,approved_shadow_comparison_id,mode,state,canary_basis_points,auto_rollback_failed_runs,auto_rollback_window_minutes,COALESCE(auto_rollback_reason,''),cohort_salt,revision,created_by,created_at,updated_at`

// CreateRuleRollout records a frozen binding pair. It deliberately does not
// change either binding: the admission engine owns candidate selection and a
// later promotion is a separate, revisioned operation.
func (s *PostgresStore) CreateRuleRollout(ctx context.Context, actor, tenantSlug string, input domain.RuleRolloutInput) (domain.RuleRollout, error) {
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	if input.AutoRollbackFailedRuns == 0 {
		input.AutoRollbackFailedRuns = 1
	}
	if input.AutoRollbackWindowMinutes == 0 {
		input.AutoRollbackWindowMinutes = 60
	}
	if !input.Valid() {
		return domain.RuleRollout{}, ErrInvalidRuleRollout
	}
	if input.Mode == "canary" && input.CanaryBasisPoints != 100 && input.CanaryBasisPoints != 500 {
		return domain.RuleRollout{}, ErrInvalidRuleRollout
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleRollout{}, fmt.Errorf("begin rule rollout: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RuleRollout{}, err
	}
	if !canManageRules(role) {
		return domain.RuleRollout{}, ErrForbidden
	}
	if input.Mode == "canary" && role != "owner" && role != "admin" {
		return domain.RuleRollout{}, ErrForbidden
	}
	baseline, err := loadRuleRolloutBinding(ctx, tx, tenantID, input.BaselineBindingID)
	if err != nil {
		return domain.RuleRollout{}, err
	}
	candidate, err := loadRuleRolloutBinding(ctx, tx, tenantID, input.CandidateBindingID)
	if err != nil {
		return domain.RuleRollout{}, err
	}
	if baseline.binding.State != "active" || candidate.binding.State != "shadow" || baseline.binding.RuleVersionID == candidate.binding.RuleVersionID || baseline.ruleSetID != candidate.ruleSetID || !sameRuleRolloutScope(baseline.binding, candidate.binding) {
		return domain.RuleRollout{}, ErrInvalidRuleBinding
	}
	var approvedComparisonID *uuid.UUID
	if input.Mode == "canary" {
		var comparisonID uuid.UUID
		var comparisonState, shadowAuthor string
		err = tx.QueryRow(ctx, `
			SELECT comparison.id,comparison.state,shadow.created_by
			FROM rule_rollout_comparisons comparison
			JOIN rule_rollouts shadow ON shadow.id=comparison.rollout_id
			WHERE shadow.tenant_id=$1 AND shadow.mode='shadow' AND shadow.state='active'
			  AND shadow.baseline_binding_id=$2 AND shadow.candidate_binding_id=$3
			ORDER BY comparison.created_at DESC,comparison.id DESC LIMIT 1`,
			tenantID, baseline.binding.ID, candidate.binding.ID).Scan(&comparisonID, &comparisonState, &shadowAuthor)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RuleRollout{}, ErrInvalidRuleRollout
		}
		if err != nil {
			return domain.RuleRollout{}, fmt.Errorf("verify independent shadow comparison: %w", err)
		}
		if comparisonState != "completed" || shadowAuthor == actor {
			return domain.RuleRollout{}, ErrInvalidRuleRollout
		}
		approvedComparisonID = &comparisonID
	}
	result, err := scanRuleRollout(tx.QueryRow(ctx, `
		INSERT INTO rule_rollouts(tenant_id,baseline_binding_id,candidate_binding_id,approved_shadow_comparison_id,mode,canary_basis_points,auto_rollback_failed_runs,auto_rollback_window_minutes,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING `+ruleRolloutColumns, tenantID, baseline.binding.ID, candidate.binding.ID, approvedComparisonID, input.Mode, input.CanaryBasisPoints, input.AutoRollbackFailedRuns, input.AutoRollbackWindowMinutes, actor))
	if isUniqueViolation(err) {
		return domain.RuleRollout{}, ErrConflict
	}
	if err != nil {
		return domain.RuleRollout{}, fmt.Errorf("create rule rollout: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata)
		VALUES($1,$2,'rule_rollout.created',$3,jsonb_build_object(
			'baseline_binding_id',$4::text,'candidate_binding_id',$5::text,
			'mode',$6::text,'canary_basis_points',$7::int,'revision',$8::int,
			'approved_shadow_comparison_id',$9::text,
			'auto_rollback_failed_runs',$10::int,'auto_rollback_window_minutes',$11::int))`,
		tenantID, actor, result.ID.String(), result.BaselineBindingID, result.CandidateBindingID, result.Mode, result.CanaryBasisPoints, result.Revision, approvedComparisonID, result.AutoRollbackFailedRuns, result.AutoRollbackWindowMinutes); err != nil {
		return domain.RuleRollout{}, fmt.Errorf("audit rule rollout creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleRollout{}, fmt.Errorf("commit rule rollout: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ListRuleRollouts(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleRollout, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidRuleRollout
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+ruleRolloutColumns+` FROM rule_rollouts WHERE tenant_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list rule rollouts: %w", err)
	}
	defer rows.Close()
	rollouts := make([]domain.RuleRollout, 0)
	for rows.Next() {
		rollout, err := scanRuleRollout(rows)
		if err != nil {
			return nil, err
		}
		rollouts = append(rollouts, rollout)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule rollouts: %w", err)
	}
	return rollouts, nil
}

func (s *PostgresStore) ListRuleRolloutComparisons(ctx context.Context, actor, tenantSlug string, rolloutID uuid.UUID, limit int) ([]domain.RuleRolloutComparison, error) {
	if rolloutID == uuid.Nil || limit < 1 || limit > 100 {
		return nil, ErrInvalidRuleRollout
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,rollout_id,baseline_run_id,candidate_test_run_id,baseline_snapshot_id,candidate_snapshot_id,state,
		       baseline_finding_count,candidate_finding_count,added_finding_count,removed_finding_count,matched_finding_count,
		       error_message,created_at,completed_at
		FROM rule_rollout_comparisons
		WHERE rollout_id=$1 AND EXISTS(SELECT 1 FROM rule_rollouts WHERE id=$1 AND tenant_id=$2)
		ORDER BY created_at DESC,id DESC LIMIT $3`, rolloutID, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list rule rollout comparisons: %w", err)
	}
	defer rows.Close()
	items := make([]domain.RuleRolloutComparison, 0)
	for rows.Next() {
		var item domain.RuleRolloutComparison
		if err := rows.Scan(&item.ID, &item.RolloutID, &item.BaselineRunID, &item.CandidateTestRunID, &item.BaselineSnapshotID, &item.CandidateSnapshotID, &item.State, &item.BaselineFindingCount, &item.CandidateFindingCount, &item.AddedFindingCount, &item.RemovedFindingCount, &item.MatchedFindingCount, &item.ErrorMessage, &item.CreatedAt, &item.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan rule rollout comparison: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule rollout comparisons: %w", err)
	}
	return items, nil
}

// UpdateRuleRollout advances Canary exposure only after an observed, successful
// candidate stage. Permanent promotion atomically switches the binding pair;
// frozen run snapshots and their cohort-selection records are never rewritten.
func (s *PostgresStore) UpdateRuleRollout(ctx context.Context, actor, tenantSlug string, rolloutID uuid.UUID, input domain.RuleRolloutUpdateInput) (domain.RuleRollout, error) {
	input.State = strings.ToLower(strings.TrimSpace(input.State))
	if rolloutID == uuid.Nil || !input.Valid() {
		return domain.RuleRollout{}, ErrInvalidRuleRollout
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleRollout{}, fmt.Errorf("begin rule rollout update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RuleRollout{}, err
	}
	if !canManageRules(role) {
		return domain.RuleRollout{}, ErrForbidden
	}
	current, err := scanRuleRollout(tx.QueryRow(ctx, `SELECT `+ruleRolloutColumns+` FROM rule_rollouts WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, rolloutID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleRollout{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleRollout{}, fmt.Errorf("load rule rollout: %w", err)
	}
	if current.Revision != input.Revision {
		return domain.RuleRollout{}, ErrConflict
	}
	advancing := current.Mode == "canary" && current.State == "active" && input.State == "active" && input.CanaryBasisPoints > 0
	promoting := current.Mode == "canary" && current.State == "active" && input.State == "promoted" && input.CanaryBasisPoints == 0
	if !advancing && !promoting && (input.CanaryBasisPoints != 0 || !validRuleRolloutTransition(current.State, input.State)) {
		return domain.RuleRollout{}, ErrConflict
	}
	if advancing || promoting {
		if role != "owner" && role != "admin" {
			return domain.RuleRollout{}, ErrForbidden
		}
		if advancing && !validCanaryAdvance(current.CanaryBasisPoints, input.CanaryBasisPoints) {
			return domain.RuleRollout{}, ErrInvalidRuleRollout
		}
		if promoting && (current.CanaryBasisPoints != 10000 || actor == current.CreatedBy) {
			return domain.RuleRollout{}, ErrInvalidRuleRollout
		}
		stageHistoryValid, err := canaryStageHistoryValid(ctx, tx, tenantID, current)
		if err != nil {
			return domain.RuleRollout{}, err
		}
		if !stageHistoryValid {
			return domain.RuleRollout{}, ErrInvalidRuleRollout
		}
		baseline, err := loadRuleRolloutBinding(ctx, tx, tenantID, current.BaselineBindingID)
		if err != nil {
			return domain.RuleRollout{}, err
		}
		candidate, err := loadRuleRolloutBinding(ctx, tx, tenantID, current.CandidateBindingID)
		if err != nil {
			return domain.RuleRollout{}, err
		}
		if current.ApprovedShadowComparisonID == nil || baseline.binding.State != "active" || candidate.binding.State != "shadow" || baseline.ruleSetID != candidate.ruleSetID || baseline.binding.RuleVersionID == candidate.binding.RuleVersionID || !sameRuleRolloutScope(baseline.binding, candidate.binding) {
			return domain.RuleRollout{}, ErrInvalidRuleBinding
		}
		completed, failed, pending, observed, err := canaryStageEvidence(ctx, tx, current)
		if err != nil {
			return domain.RuleRollout{}, err
		}
		if !observed || completed < 1 || failed > 0 || pending > 0 {
			return domain.RuleRollout{}, ErrRuleRolloutEvidence
		}
		if advancing {
			result, err := scanRuleRollout(tx.QueryRow(ctx, `
				UPDATE rule_rollouts SET canary_basis_points=$3,revision=revision+1,updated_at=now()
				WHERE tenant_id=$1 AND id=$2 RETURNING `+ruleRolloutColumns, tenantID, rolloutID, input.CanaryBasisPoints))
			if err != nil {
				return domain.RuleRollout{}, fmt.Errorf("advance canary stage: %w", err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'rule_rollout.stage_advanced',$3,jsonb_build_object('from_basis_points',$4::int,'to_basis_points',$5::int,'completed_distinct_reviews',$6::int,'failed_reviews',$7::int,'pending_reviews',$8::int,'revision',$9::int))`, tenantID, actor, rolloutID.String(), current.CanaryBasisPoints, result.CanaryBasisPoints, completed, failed, pending, result.Revision); err != nil {
				return domain.RuleRollout{}, fmt.Errorf("audit canary advance: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return domain.RuleRollout{}, fmt.Errorf("commit canary advance: %w", err)
			}
			return result, nil
		}
		for _, change := range []struct {
			id       uuid.UUID
			from, to string
		}{{current.BaselineBindingID, "active", "disabled"}, {current.CandidateBindingID, "shadow", "active"}} {
			command, err := tx.Exec(ctx, `UPDATE rule_bindings SET state=$3,updated_at=now() WHERE tenant_id=$1 AND id=$2 AND state=$4`, tenantID, change.id, change.to, change.from)
			if err != nil {
				return domain.RuleRollout{}, fmt.Errorf("promote rule binding: %w", err)
			}
			if command.RowsAffected() != 1 {
				return domain.RuleRollout{}, ErrConflict
			}
			if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'rule_binding.state_updated',$3,jsonb_build_object('state',$4::text,'rollout_id',$5::text))`, tenantID, actor, change.id.String(), change.to, rolloutID.String()); err != nil {
				return domain.RuleRollout{}, fmt.Errorf("audit promoted binding: %w", err)
			}
		}
		result, err := scanRuleRollout(tx.QueryRow(ctx, `UPDATE rule_rollouts SET state='promoted',revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2 RETURNING `+ruleRolloutColumns, tenantID, rolloutID))
		if err != nil {
			return domain.RuleRollout{}, fmt.Errorf("promote canary rollout: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			WITH promoted_shadow AS (
				UPDATE rule_rollouts SET state='promoted',revision=revision+1,updated_at=now()
				WHERE tenant_id=$1 AND mode='shadow' AND state IN ('active','paused')
				  AND baseline_binding_id=$2 AND candidate_binding_id=$3
				RETURNING id,revision
			)
			INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata)
			SELECT $1,$4,'rule_rollout.shadow_promoted',id::text,
			       jsonb_build_object('canary_rollout_id',$5::text,'revision',revision)
			FROM promoted_shadow`, tenantID, current.BaselineBindingID, current.CandidateBindingID, actor, rolloutID.String()); err != nil {
			return domain.RuleRollout{}, fmt.Errorf("retire promoted shadow: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'rule_rollout.promoted',$3,jsonb_build_object('baseline_binding_id',$4::text,'candidate_binding_id',$5::text,'approved_shadow_comparison_id',$6::text,'completed_distinct_reviews',$7::int,'revision',$8::int))`, tenantID, actor, result.ID.String(), current.BaselineBindingID.String(), current.CandidateBindingID.String(), current.ApprovedShadowComparisonID.String(), completed, result.Revision); err != nil {
			return domain.RuleRollout{}, fmt.Errorf("audit canary promotion: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.RuleRollout{}, fmt.Errorf("commit canary promotion: %w", err)
		}
		return result, nil
	}
	result, err := scanRuleRollout(tx.QueryRow(ctx, `
		UPDATE rule_rollouts SET state=$3,revision=revision+1,updated_at=now()
		WHERE tenant_id=$1 AND id=$2
		RETURNING `+ruleRolloutColumns, tenantID, rolloutID, input.State))
	if err != nil {
		return domain.RuleRollout{}, fmt.Errorf("update rule rollout: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'rule_rollout.state_updated',$3,jsonb_build_object('state',$4::text,'revision',$5::int))`, tenantID, actor, result.ID.String(), result.State, result.Revision); err != nil {
		return domain.RuleRollout{}, fmt.Errorf("audit rule rollout update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleRollout{}, fmt.Errorf("commit rule rollout update: %w", err)
	}
	return result, nil
}

func validCanaryAdvance(current, next int) bool {
	return (current == 100 && next == 500) || (current == 500 && next == 2500) || (current == 2500 && next == 10000)
}

// Older releases permitted a Canary to start at 25% or 100%. Those rows
// cannot be treated as if they passed the new staged observation gates.
func canaryStageHistoryValid(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, rollout domain.RuleRollout) (bool, error) {
	if rollout.CanaryBasisPoints <= 500 {
		return true, nil
	}
	var reached25, reached100 int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE metadata->>'from_basis_points'='500' AND metadata->>'to_basis_points'='2500'),
		       COUNT(*) FILTER (WHERE metadata->>'from_basis_points'='2500' AND metadata->>'to_basis_points'='10000')
		FROM audit_events
		WHERE tenant_id=$1 AND target=$2 AND action='rule_rollout.stage_advanced'`, tenantID, rollout.ID.String()).Scan(&reached25, &reached100)
	if err != nil {
		return false, fmt.Errorf("load Canary stage history: %w", err)
	}
	return reached25 == 1 && (rollout.CanaryBasisPoints != 10000 || reached100 == 1), nil
}

func canaryStageEvidence(ctx context.Context, tx pgx.Tx, rollout domain.RuleRollout) (completed, failed, pending int, observed bool, err error) {
	if time.Since(rollout.UpdatedAt) < time.Duration(rollout.AutoRollbackWindowMinutes)*time.Minute {
		return 0, 0, 0, false, nil
	}
	err = tx.QueryRow(ctx, `
		SELECT COUNT(DISTINCT run.request_id) FILTER (WHERE run.state='completed' AND run.finished_at IS NOT NULL AND gate.conclusion='success'),
		       COUNT(*) FILTER (WHERE (run.state='failed' AND run.failure_code IS DISTINCT FROM 'quota_exceeded')
		                            OR (run.state='completed' AND gate.conclusion IS DISTINCT FROM 'success')),
		       COUNT(*) FILTER (WHERE run.state NOT IN ('completed','failed','cancelled','superseded'))
		FROM rule_rollout_run_selections selection
		JOIN review_runs run ON run.id=selection.run_id
		LEFT JOIN review_merge_gate_decisions gate ON gate.run_id=run.id
		WHERE selection.rollout_id=$1 AND selection.selected_candidate
		  AND run.created_at >= $2`, rollout.ID, rollout.UpdatedAt).Scan(&completed, &failed, &pending)
	if err != nil {
		return 0, 0, 0, false, fmt.Errorf("load canary stage evidence: %w", err)
	}
	return completed, failed, pending, true, nil
}

type ruleRolloutBinding struct {
	binding   domain.RuleBinding
	ruleSetID uuid.UUID
}

type canaryRuleBinding struct {
	rolloutID, baselineBindingID, candidateBindingID uuid.UUID
	baselineVersionID, candidateVersionID            uuid.UUID
	candidateRules                                   []byte
	bucket                                           int
	selected                                         bool
}

type ruleRolloutRunSelection struct {
	rolloutID, baselineBindingID, candidateBindingID uuid.UUID
	selectedBindingID, selectedVersionID             uuid.UUID
	bucket                                           int
	selected                                         bool
}

// loadActiveCanaryRuleBindings reads deployment-owned rollout decisions in
// admission's transaction. The locks fence a concurrent pause/rollback until
// the exact run snapshot and selection evidence have committed. Invalid or
// out-of-scope rollout pairs never widen the active baseline rule set.
func loadActiveCanaryRuleBindings(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scope domain.ReviewConfigScope, selectedRuleSetID *uuid.UUID, reviewNumber int) (map[uuid.UUID]canaryRuleBinding, error) {
	result := make(map[uuid.UUID]canaryRuleBinding)
	if reviewNumber < 1 {
		return result, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT rollout.id,rollout.canary_basis_points,rollout.cohort_salt,
		       baseline.id,baseline.rule_version_id,candidate.id,candidate.rule_version_id,candidate_version.rules
		FROM rule_rollouts rollout
		JOIN rule_bindings baseline ON baseline.id=rollout.baseline_binding_id
		JOIN rule_bindings candidate ON candidate.id=rollout.candidate_binding_id
		JOIN rule_versions baseline_version ON baseline_version.id=baseline.rule_version_id
		JOIN rule_versions candidate_version ON candidate_version.id=candidate.rule_version_id
		WHERE rollout.tenant_id=$1 AND rollout.mode='canary' AND rollout.state='active'
		  AND rollout.approved_shadow_comparison_id IS NOT NULL
		  AND baseline.tenant_id=$1 AND candidate.tenant_id=$1
		  AND baseline.state='active' AND candidate.state='shadow'
		  AND baseline_version.state='published' AND candidate_version.state='published'
		  AND baseline_version.rule_set_id=candidate_version.rule_set_id
		  AND baseline.rule_version_id<>candidate.rule_version_id
		  AND baseline.scope_kind=candidate.scope_kind
		  AND baseline.scope_ref=candidate.scope_ref
		  AND baseline.scope_provider=candidate.scope_provider
		  AND baseline.scope_api_base_url=candidate.scope_api_base_url
		  AND baseline.precedence=candidate.precedence
		  AND baseline.target_branch_glob=candidate.target_branch_glob
		  AND baseline.path_include_glob=candidate.path_include_glob
		  AND baseline.path_exclude_glob=candidate.path_exclude_glob
		  AND (candidate.starts_at IS NULL OR candidate.starts_at<=now())
		  AND (candidate.ends_at IS NULL OR candidate.ends_at>now())
		  AND (baseline.scope_kind='tenant' OR (
		       baseline.scope_kind='repository' AND baseline.scope_ref=$2
		       AND ((baseline.scope_provider=$3 AND baseline.scope_api_base_url=$4)
		         OR (baseline.scope_provider='' AND baseline.scope_api_base_url=''))))
		  AND ($5::uuid IS NULL OR baseline_version.rule_set_id=$5)
		ORDER BY rollout.id
		FOR SHARE OF rollout,baseline,candidate`,
		tenantID, scope.Ref, scope.Provider, scope.APIBaseURL, selectedRuleSetID)
	if err != nil {
		return nil, fmt.Errorf("select active canary rollouts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rollout domain.RuleRollout
		var binding canaryRuleBinding
		if err := rows.Scan(&rollout.ID, &rollout.CanaryBasisPoints, &rollout.CohortSalt,
			&binding.baselineBindingID, &binding.baselineVersionID,
			&binding.candidateBindingID, &binding.candidateVersionID, &binding.candidateRules); err != nil {
			return nil, fmt.Errorf("scan active canary rollout: %w", err)
		}
		binding.rolloutID = rollout.ID
		bucket, valid := domain.CohortBucket(tenantID, scope.Provider, scope.APIBaseURL, scope.Ref, reviewNumber, rollout.CohortSalt)
		if !valid {
			return nil, ErrInvalidRuleRollout
		}
		binding.bucket = bucket
		binding.selected = bucket < rollout.CanaryBasisPoints
		if _, exists := result[binding.baselineBindingID]; exists {
			return nil, ErrConflict
		}
		result[binding.baselineBindingID] = binding
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active canary rollouts: %w", err)
	}
	return result, nil
}

func recordRuleRolloutRunSelections(ctx context.Context, tx pgx.Tx, runID uuid.UUID, selections []ruleRolloutRunSelection) error {
	for _, selection := range selections {
		if _, err := tx.Exec(ctx, `
			INSERT INTO rule_rollout_run_selections(
				run_id,rollout_id,baseline_binding_id,candidate_binding_id,
				selected_binding_id,selected_rule_version_id,cohort_bucket,selected_candidate)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			runID, selection.rolloutID, selection.baselineBindingID,
			selection.candidateBindingID, selection.selectedBindingID,
			selection.selectedVersionID, selection.bucket, selection.selected); err != nil {
			return fmt.Errorf("record rule rollout admission: %w", err)
		}
	}
	return nil
}

func loadRuleRolloutBinding(ctx context.Context, tx pgx.Tx, tenantID, bindingID uuid.UUID) (ruleRolloutBinding, error) {
	var result ruleRolloutBinding
	err := tx.QueryRow(ctx, `
		SELECT b.id,b.tenant_id,b.rule_version_id,b.scope_kind,b.scope_ref,b.scope_provider,b.scope_api_base_url,b.precedence,b.target_branch_glob,b.path_include_glob,b.path_exclude_glob,b.state,b.created_by,b.created_at,b.updated_at,v.rule_set_id
		FROM rule_bindings b JOIN rule_versions v ON v.id=b.rule_version_id
		WHERE b.tenant_id=$1 AND b.id=$2 FOR UPDATE`, tenantID, bindingID).
		Scan(&result.binding.ID, &result.binding.TenantID, &result.binding.RuleVersionID, &result.binding.ScopeKind, &result.binding.ScopeRef, &result.binding.ScopeProvider, &result.binding.ScopeAPIBaseURL, &result.binding.Precedence, &result.binding.TargetBranchGlob, &result.binding.PathIncludeGlob, &result.binding.PathExcludeGlob, &result.binding.State, &result.binding.CreatedBy, &result.binding.CreatedAt, &result.binding.UpdatedAt, &result.ruleSetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ruleRolloutBinding{}, ErrNotFound
	}
	if err != nil {
		return ruleRolloutBinding{}, fmt.Errorf("load rule rollout binding: %w", err)
	}
	return result, nil
}

func sameRuleRolloutScope(left, right domain.RuleBinding) bool {
	return left.ScopeKind == right.ScopeKind && left.ScopeRef == right.ScopeRef && left.ScopeProvider == right.ScopeProvider && left.ScopeAPIBaseURL == right.ScopeAPIBaseURL && left.Precedence == right.Precedence && left.TargetBranchGlob == right.TargetBranchGlob && left.PathIncludeGlob == right.PathIncludeGlob && left.PathExcludeGlob == right.PathExcludeGlob
}

func validRuleRolloutTransition(current, next string) bool {
	return (current == "active" && (next == "paused" || next == "rolled_back")) || (current == "paused" && (next == "active" || next == "rolled_back"))
}

// QueueShadowRuleTests creates an isolated candidate replay only after the
// source run has persisted its exact execution plan and findings. It never
// changes provider-visible state: the existing Rule Lab processor consumes the
// resulting rule_test_run without a publisher dependency.
func (s *PostgresStore) QueueShadowRuleTests(ctx context.Context, jobID uuid.UUID) error {
	if jobID == uuid.Nil {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin shadow rollout queue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID, sourceRunID, baselineSnapshotID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT request.tenant_id, run.id, run.rule_snapshot_id
		FROM review_runs run
		JOIN review_requests request ON request.id=run.request_id
		WHERE run.legacy_job_id=$1
		FOR UPDATE`, jobID).Scan(&tenantID, &sourceRunID, &baselineSnapshotID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load shadow rollout source: %w", err)
	}
	var retainedPlan bool
	if err := tx.QueryRow(ctx, `SELECT TRUE FROM review_execution_plans WHERE run_id=$1`, sourceRunID).Scan(&retainedPlan); err != nil {
		return fmt.Errorf("verify shadow rollout execution plan: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT rollout.id, baseline.id, baseline.rule_version_id, baseline.precedence,
		       candidate.id, candidate.rule_version_id
		FROM rule_rollouts rollout
		JOIN rule_bindings baseline ON baseline.id=rollout.baseline_binding_id
		JOIN rule_bindings candidate ON candidate.id=rollout.candidate_binding_id
		WHERE rollout.tenant_id=$1 AND rollout.mode='shadow' AND rollout.state='active'
		  AND baseline.state='active' AND candidate.state='shadow'
		  AND EXISTS (
			SELECT 1 FROM rule_snapshot_sources source
			WHERE source.snapshot_id=$2 AND source.rule_version_id=baseline.rule_version_id
			  AND source.precedence=baseline.precedence)
		ORDER BY rollout.created_at, rollout.id
		FOR UPDATE OF rollout`, tenantID, baselineSnapshotID)
	if err != nil {
		return fmt.Errorf("select shadow rollouts: %w", err)
	}
	defer rows.Close()
	type rolloutTarget struct {
		rolloutID, baselineBindingID, baselineVersionID, candidateBindingID, candidateVersionID uuid.UUID
		precedence                                                                              int
	}
	targets := make([]rolloutTarget, 0)
	for rows.Next() {
		var target rolloutTarget
		if err := rows.Scan(&target.rolloutID, &target.baselineBindingID, &target.baselineVersionID, &target.precedence, &target.candidateBindingID, &target.candidateVersionID); err != nil {
			return fmt.Errorf("scan shadow rollout: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate shadow rollouts: %w", err)
	}
	rows.Close()
	for _, target := range targets {
		candidateSnapshotID, err := composeShadowCandidateSnapshot(ctx, tx, tenantID, baselineSnapshotID, target.baselineVersionID, target.candidateVersionID, target.precedence)
		if err != nil {
			return fmt.Errorf("compose shadow rollout %s: %w", target.rolloutID, err)
		}
		var testRunID uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO rule_test_runs(tenant_id,rule_version_id,source_run_id,source_job_id,snapshot_id,rollout_id,requested_by)
			VALUES($1,$2,$3,$4,$5,$6,'system:shadow-rollout')
			ON CONFLICT (rollout_id,source_run_id) WHERE rollout_id IS NOT NULL DO NOTHING
			RETURNING id`, tenantID, target.candidateVersionID, sourceRunID, jobID, candidateSnapshotID, target.rolloutID).Scan(&testRunID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("queue shadow rule test: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload)
			VALUES('rule_test_run',$1::uuid,'rule.test.requested',$2,jsonb_build_object('test_run_id',$1::uuid::text,'rollout_id',$3::uuid::text,'publication_allowed',false))`,
			testRunID, "shadow-rollout:"+target.rolloutID.String()+":"+sourceRunID.String(), target.rolloutID); err != nil {
			return fmt.Errorf("queue shadow rollout message: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata)
			VALUES($1,'system:shadow-rollout','rule_rollout.shadow_queued',$2,jsonb_build_object(
				'rollout_id',$3::text,'source_run_id',$4::text,'candidate_snapshot_id',$5::text,'publication_allowed',false))`,
			tenantID, testRunID.String(), target.rolloutID, sourceRunID, candidateSnapshotID); err != nil {
			return fmt.Errorf("audit queued shadow rollout: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit shadow rollout queue: %w", err)
	}
	return nil
}

func composeShadowCandidateSnapshot(ctx context.Context, tx pgx.Tx, tenantID, baselineSnapshotID, baselineVersionID, candidateVersionID uuid.UUID, candidatePrecedence int) (uuid.UUID, error) {
	var baselinePayload []byte
	if err := tx.QueryRow(ctx, `SELECT canonical_payload FROM rule_snapshots WHERE id=$1 AND tenant_id=$2`, baselineSnapshotID, tenantID).Scan(&baselinePayload); err != nil {
		return uuid.Nil, fmt.Errorf("load baseline snapshot: %w", err)
	}
	var baseline rules.Snapshot
	if err := json.Unmarshal(baselinePayload, &baseline); err != nil {
		return uuid.Nil, fmt.Errorf("decode baseline snapshot: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT source.rule_version_id,source.precedence,version.rules
		FROM rule_snapshot_sources source
		JOIN rule_versions version ON version.id=source.rule_version_id
		WHERE source.snapshot_id=$1
		ORDER BY source.precedence,source.rule_version_id`, baselineSnapshotID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("load baseline snapshot sources: %w", err)
	}
	sources := make([]rules.Source, 0)
	seenBaseline := false
	for rows.Next() {
		var versionID uuid.UUID
		var precedence int
		var rawRules []byte
		if err := rows.Scan(&versionID, &precedence, &rawRules); err != nil {
			rows.Close()
			return uuid.Nil, fmt.Errorf("scan baseline snapshot source: %w", err)
		}
		if versionID == baselineVersionID {
			if precedence != candidatePrecedence || seenBaseline {
				rows.Close()
				return uuid.Nil, ErrInvalidRuleRollout
			}
			seenBaseline = true
			continue
		}
		var parsed []rules.Rule
		if err := json.Unmarshal(rawRules, &parsed); err != nil {
			rows.Close()
			return uuid.Nil, fmt.Errorf("decode baseline source rules: %w", err)
		}
		sources = append(sources, rules.Source{VersionID: versionID.String(), Precedence: precedence, Rules: parsed})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return uuid.Nil, fmt.Errorf("iterate baseline snapshot sources: %w", err)
	}
	rows.Close()
	if !seenBaseline {
		return uuid.Nil, ErrInvalidRuleRollout
	}
	var rawCandidate []byte
	if err := tx.QueryRow(ctx, `SELECT rules FROM rule_versions WHERE id=$1`, candidateVersionID).Scan(&rawCandidate); err != nil {
		return uuid.Nil, fmt.Errorf("load shadow candidate rules: %w", err)
	}
	var candidateRules []rules.Rule
	if err := json.Unmarshal(rawCandidate, &candidateRules); err != nil {
		return uuid.Nil, fmt.Errorf("decode shadow candidate rules: %w", err)
	}
	sources = append(sources, rules.Source{VersionID: candidateVersionID.String(), Precedence: candidatePrecedence, Rules: candidateRules})
	compiled, err := rules.Compile(sources)
	if err != nil {
		return uuid.Nil, fmt.Errorf("compile shadow candidate: %w", err)
	}
	compiled.Snapshot.Include = append([]string(nil), baseline.Include...)
	compiled.Snapshot.Exclude = append([]string(nil), baseline.Exclude...)
	compiled.Canonical, err = json.Marshal(compiled.Snapshot)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode shadow candidate: %w", err)
	}
	digest := sha256.Sum256(compiled.Canonical)
	compiled.SHA256 = hex.EncodeToString(digest[:])
	if len(baseline.AppliedExceptions) > 0 {
		compiled, err = rules.ApplyExceptions(compiled, baseline.AppliedExceptions)
		if err != nil {
			return uuid.Nil, fmt.Errorf("apply frozen shadow exceptions: %w", err)
		}
	}
	var snapshotID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO rule_snapshots(tenant_id,sha256,compiler_version,engine,canonical_payload)
		VALUES($1,$2,'rules-v2','ocr',$3::jsonb)
		ON CONFLICT(tenant_id,sha256) DO UPDATE SET sha256=EXCLUDED.sha256
		RETURNING id`, tenantID, compiled.SHA256, string(compiled.Canonical)).Scan(&snapshotID); err != nil {
		return uuid.Nil, fmt.Errorf("store shadow candidate snapshot: %w", err)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].VersionID < sources[j].VersionID })
	for _, source := range sources {
		versionID, err := uuid.Parse(source.VersionID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("parse shadow snapshot source: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO rule_snapshot_sources(snapshot_id,rule_version_id,precedence) VALUES($1,$2,$3) ON CONFLICT(snapshot_id,rule_version_id) DO NOTHING`, snapshotID, versionID, source.Precedence); err != nil {
			return uuid.Nil, fmt.Errorf("store shadow snapshot source: %w", err)
		}
	}
	return snapshotID, nil
}

func scanRuleRollout(row rowScanner) (domain.RuleRollout, error) {
	var rollout domain.RuleRollout
	err := row.Scan(&rollout.ID, &rollout.TenantID, &rollout.BaselineBindingID, &rollout.CandidateBindingID, &rollout.ApprovedShadowComparisonID, &rollout.Mode, &rollout.State, &rollout.CanaryBasisPoints, &rollout.AutoRollbackFailedRuns, &rollout.AutoRollbackWindowMinutes, &rollout.AutoRollbackReason, &rollout.CohortSalt, &rollout.Revision, &rollout.CreatedBy, &rollout.CreatedAt, &rollout.UpdatedAt)
	return rollout, err
}
