package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SaveReviewMergeGateDecision persists one policy decision per run. The
// general configuration source is selected from the run-scoped snapshot, so
// a workspace edit made while a review is running cannot alter its evidence.
// Retried publication may call this again; it returns the original decision
// and rejects any attempted semantic rewrite.
func (s *PostgresStore) SaveReviewMergeGateDecision(ctx context.Context, jobID uuid.UUID, input domain.ReviewMergeGateDecisionInput) (domain.ReviewMergeGateDecision, error) {
	if jobID == uuid.Nil {
		return domain.ReviewMergeGateDecision{}, ErrNotFound
	}
	input, valid := domain.NormalizeReviewMergeGateDecisionInput(input)
	if !valid {
		return domain.ReviewMergeGateDecision{}, ErrInvalidReviewConfig
	}
	var decision domain.ReviewMergeGateDecision
	err := s.pool.QueryRow(ctx, `
		INSERT INTO review_merge_gate_decisions (
			run_id, enabled, threshold, conclusion, blocking_findings, finding_count,
			configuration_content_sha256, origin_scope_kind, origin_scope_ref,
			origin_revision, evaluation_version
		)
		SELECT run.id, $2, $3, $4, $5, $6,
			snapshot.content_sha256, snapshot.origin_scope_kind, snapshot.origin_scope_ref,
			snapshot.origin_revision, $7
		FROM review_runs run
		JOIN review_configuration_snapshots snapshot
			ON snapshot.run_id = run.id AND snapshot.section = 'general'
		WHERE run.legacy_job_id = $1
		ON CONFLICT (run_id) DO UPDATE SET run_id = review_merge_gate_decisions.run_id
		RETURNING enabled, threshold, conclusion, blocking_findings, finding_count,
		          configuration_content_sha256, origin_scope_kind, origin_scope_ref,
		          origin_revision, evaluation_version, decided_at`,
		jobID, input.Enabled, input.Threshold, input.Conclusion, input.BlockingFindings, input.FindingCount, input.EvaluationVersion,
	).Scan(
		&decision.Enabled, &decision.Threshold, &decision.Conclusion, &decision.BlockingFindings, &decision.FindingCount,
		&decision.ConfigurationContentSHA256, &decision.OriginScopeKind, &decision.OriginScopeRef,
		&decision.OriginRevision, &decision.EvaluationVersion, &decision.DecidedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewMergeGateDecision{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewMergeGateDecision{}, fmt.Errorf("save merge gate decision: %w", err)
	}
	if decision.Enabled != input.Enabled || decision.Threshold != input.Threshold || decision.Conclusion != input.Conclusion || decision.BlockingFindings != input.BlockingFindings || decision.FindingCount != input.FindingCount || decision.EvaluationVersion != input.EvaluationVersion {
		return domain.ReviewMergeGateDecision{}, fmt.Errorf("stored merge gate decision differs from review result")
	}
	if !decision.Valid() {
		return domain.ReviewMergeGateDecision{}, fmt.Errorf("stored merge gate decision is invalid")
	}
	return decision, nil
}
