package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ReconcileCanaryFailures rolls back active Canaries whose selected candidate
// reviews failed within their configured window. A PR/MR counts only once even
// when several heads fail. Reconciliation is safe to repeat and to run from
// multiple workers: each decision rechecks evidence while holding the rollout
// row lock, and historical run snapshots remain unchanged.
func (s *PostgresStore) ReconcileCanaryFailures(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, ErrInvalidRuleRollout
	}
	rows, err := s.pool.Query(ctx, `
		SELECT rollout.id
		FROM rule_rollouts rollout
		WHERE rollout.mode='canary' AND rollout.state='active'
		  AND (
			SELECT COUNT(DISTINCT run.request_id)
			FROM rule_rollout_run_selections selection
			JOIN review_runs run ON run.id=selection.run_id
			WHERE selection.rollout_id=rollout.id AND selection.selected_candidate
			  AND run.state='failed' AND run.failure_code IS DISTINCT FROM 'quota_exceeded'
			  AND run.finished_at >= GREATEST(rollout.created_at, now()-make_interval(mins => rollout.auto_rollback_window_minutes))
		  ) >= rollout.auto_rollback_failed_runs
		ORDER BY rollout.created_at, rollout.id LIMIT $1`, limit)
	if err != nil {
		return 0, fmt.Errorf("list failed canary rollouts: %w", err)
	}
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan failed canary rollout: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate failed canary rollouts: %w", err)
	}
	rows.Close()
	count := 0
	for _, id := range ids {
		rolledBack, err := s.rollbackFailedCanary(ctx, id)
		if err != nil {
			return count, err
		}
		if rolledBack {
			count++
		}
	}
	return count, nil
}

func (s *PostgresStore) rollbackFailedCanary(ctx context.Context, rolloutID uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin canary rollback: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID uuid.UUID
	var threshold, windowMinutes int
	err = tx.QueryRow(ctx, `
		SELECT tenant_id,auto_rollback_failed_runs,auto_rollback_window_minutes
		FROM rule_rollouts WHERE id=$1 AND mode='canary' AND state='active'
		FOR UPDATE SKIP LOCKED`, rolloutID).Scan(&tenantID, &threshold, &windowMinutes)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock failed canary rollout: %w", err)
	}
	var distinctRequests int
	err = tx.QueryRow(ctx, `
		SELECT COUNT(DISTINCT run.request_id)
		FROM rule_rollout_run_selections selection
		JOIN review_runs run ON run.id=selection.run_id
		JOIN rule_rollouts rollout ON rollout.id=selection.rollout_id
		WHERE selection.rollout_id=$1 AND selection.selected_candidate
		  AND run.state='failed' AND run.failure_code IS DISTINCT FROM 'quota_exceeded'
		  AND run.finished_at >= GREATEST(rollout.created_at, now()-make_interval(mins => rollout.auto_rollback_window_minutes))`, rolloutID).Scan(&distinctRequests)
	if err != nil {
		return false, fmt.Errorf("recheck canary failure evidence: %w", err)
	}
	if distinctRequests < threshold {
		return false, tx.Commit(ctx)
	}
	reason := fmt.Sprintf("%d distinct candidate reviews failed within %d minutes (threshold %d)", distinctRequests, windowMinutes, threshold)
	if _, err := tx.Exec(ctx, `
		UPDATE rule_rollouts
		SET state='rolled_back',revision=revision+1,auto_rollback_reason=$2,updated_at=now()
		WHERE id=$1`, rolloutID, reason); err != nil {
		return false, fmt.Errorf("roll back failed canary: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata)
		VALUES($1,'system:rule-rollout-monitor','rule_rollout.auto_rolled_back',$2,
			jsonb_build_object('failed_distinct_reviews',$3::int,'threshold',$4::int,'window_minutes',$5::int,'reason',$6::text))`,
		tenantID, rolloutID.String(), distinctRequests, threshold, windowMinutes, reason); err != nil {
		return false, fmt.Errorf("audit failed canary rollback: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit failed canary rollback: %w", err)
	}
	return true, nil
}
