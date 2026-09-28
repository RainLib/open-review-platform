package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidReviewIntervention = errors.New("review intervention is invalid")

// ReviewInterventionStore is an additive capability. Older control-plane
// adapters can keep serving review evidence without accidentally claiming they
// implement the human-intervention workflow.
type ReviewInterventionStore interface {
	ListReviewInterventions(context.Context, string, string, domain.ReviewInterventionFilter) ([]domain.ReviewIntervention, error)
	ClaimReviewIntervention(context.Context, string, string, uuid.UUID, int) (domain.ReviewIntervention, error)
	ResolveReviewIntervention(context.Context, string, string, uuid.UUID, domain.ReviewInterventionResolutionInput) (domain.ReviewIntervention, error)
}

var _ ReviewInterventionStore = (*PostgresStore)(nil)

const reviewInterventionColumns = `
	id, tenant_id, run_id, revision, state, assignee_subject,
	opened_at, claimed_at, resolved_at, resolved_by, resolution, reason,
	created_at, updated_at`

const reviewInterventionQualifiedColumns = `
	intervention.id, intervention.tenant_id, intervention.run_id, intervention.revision, intervention.state, intervention.assignee_subject,
	intervention.opened_at, intervention.claimed_at, intervention.resolved_at, intervention.resolved_by, intervention.resolution, intervention.reason,
	intervention.created_at, intervention.updated_at`

func interventionRoleAllowed(role string) bool {
	return role == "owner" || role == "admin" || role == "rule_admin" || role == "reviewer"
}

func (s *PostgresStore) ListReviewInterventions(ctx context.Context, actor, tenantSlug string, filter domain.ReviewInterventionFilter) ([]domain.ReviewIntervention, error) {
	if !filter.Valid() {
		return nil, ErrInvalidReviewIntervention
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	arguments := []any{tenantID}
	conditions := []string{"intervention.tenant_id = $1"}
	if filter.ActiveOnly {
		conditions = append(conditions, "intervention.state IN ('open', 'claimed')")
	}
	if filter.RunID != nil {
		arguments = append(arguments, *filter.RunID)
		conditions = append(conditions, fmt.Sprintf("intervention.run_id = $%d", len(arguments)))
	}
	arguments = append(arguments, filter.Limit)
	rows, err := s.pool.Query(ctx, `
		SELECT `+reviewInterventionQualifiedColumns+`,
		       run.revision, run.state, run.failure_code, run.failure_message,
		       request.provider, request.api_base_url, request.repository,
		       request.review_number, request.title, request.author
		FROM review_run_interventions intervention
		JOIN review_runs run ON run.id = intervention.run_id
		JOIN review_requests request ON request.id = run.request_id
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY CASE intervention.state WHEN 'claimed' THEN 0 ELSE 1 END,
		         intervention.opened_at ASC, intervention.id ASC
		LIMIT $`+fmt.Sprintf("%d", len(arguments)), arguments...)
	if err != nil {
		return nil, fmt.Errorf("list review interventions: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ReviewIntervention, 0)
	for rows.Next() {
		item, err := scanReviewInterventionSummary(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review interventions: %w", err)
	}
	return items, nil
}

func (s *PostgresStore) ClaimReviewIntervention(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, expectedRevision int) (domain.ReviewIntervention, error) {
	if runID == uuid.Nil || expectedRevision < 1 {
		return domain.ReviewIntervention{}, ErrInvalidReviewIntervention
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewIntervention{}, fmt.Errorf("begin claim review intervention: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewIntervention{}, err
	}
	if !interventionRoleAllowed(role) {
		return domain.ReviewIntervention{}, ErrForbidden
	}
	intervention, err := scanReviewIntervention(tx.QueryRow(ctx, `
		SELECT `+reviewInterventionColumns+`
		FROM review_run_interventions
		WHERE tenant_id=$1 AND run_id=$2
		FOR UPDATE`, tenantID, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewIntervention{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewIntervention{}, fmt.Errorf("load review intervention for claim: %w", err)
	}
	if intervention.Revision != expectedRevision {
		return domain.ReviewIntervention{}, ErrRevisionConflict
	}
	if intervention.State != domain.ReviewInterventionOpen {
		return domain.ReviewIntervention{}, ErrConflict
	}
	intervention, err = scanReviewIntervention(tx.QueryRow(ctx, `
		UPDATE review_run_interventions
		SET state='claimed', assignee_subject=$3, claimed_at=now(), revision=revision+1, updated_at=now()
		WHERE tenant_id=$1 AND run_id=$2
		RETURNING `+reviewInterventionColumns, tenantID, runID, actor))
	if err != nil {
		return domain.ReviewIntervention{}, fmt.Errorf("claim review intervention: %w", err)
	}
	if err := auditReviewIntervention(ctx, tx, tenantID, actor, "review_intervention.claimed", intervention, map[string]any{"run_id": runID.String()}); err != nil {
		return domain.ReviewIntervention{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewIntervention{}, fmt.Errorf("commit review intervention claim: %w", err)
	}
	return intervention, nil
}

func (s *PostgresStore) ResolveReviewIntervention(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, input domain.ReviewInterventionResolutionInput) (domain.ReviewIntervention, error) {
	input, valid := domain.NormalizeReviewInterventionResolutionInput(input)
	if !valid || runID == uuid.Nil {
		return domain.ReviewIntervention{}, ErrInvalidReviewIntervention
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewIntervention{}, fmt.Errorf("begin resolve review intervention: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ReviewIntervention{}, err
	}
	if !interventionRoleAllowed(role) {
		return domain.ReviewIntervention{}, ErrForbidden
	}
	intervention, err := scanReviewIntervention(tx.QueryRow(ctx, `
		SELECT `+reviewInterventionColumns+`
		FROM review_run_interventions
		WHERE tenant_id=$1 AND run_id=$2
		FOR UPDATE`, tenantID, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewIntervention{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewIntervention{}, fmt.Errorf("load review intervention for resolution: %w", err)
	}
	if intervention.Revision != input.ExpectedRevision {
		return domain.ReviewIntervention{}, ErrRevisionConflict
	}
	if !intervention.State.Active() {
		return domain.ReviewIntervention{}, ErrConflict
	}
	if intervention.State == domain.ReviewInterventionClaimed && intervention.AssigneeSubject != actor && role != "owner" && role != "admin" {
		return domain.ReviewIntervention{}, ErrForbidden
	}
	intervention, err = scanReviewIntervention(tx.QueryRow(ctx, `
		UPDATE review_run_interventions
		SET state='resolved', resolved_at=now(), resolved_by=$3, resolution='acknowledged', reason=$4,
		    revision=revision+1, updated_at=now()
		WHERE tenant_id=$1 AND run_id=$2
		RETURNING `+reviewInterventionColumns, tenantID, runID, actor, input.Reason))
	if err != nil {
		return domain.ReviewIntervention{}, fmt.Errorf("resolve review intervention: %w", err)
	}
	if err := auditReviewIntervention(ctx, tx, tenantID, actor, "review_intervention.acknowledged", intervention, map[string]any{"run_id": runID.String(), "reason": input.Reason}); err != nil {
		return domain.ReviewIntervention{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewIntervention{}, fmt.Errorf("commit review intervention resolution: %w", err)
	}
	return intervention, nil
}

// ensureReviewIntervention runs in the same transaction as the terminal run
// transition. A rollback can therefore never leave a failed run invisible to
// the operators responsible for its next decision.
func ensureReviewIntervention(ctx context.Context, tx pgx.Tx, runID uuid.UUID, state domain.RunState) error {
	if state != domain.RunFailed && state != domain.RunNeedsAttention {
		return nil
	}
	var tenantID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT request.tenant_id
		FROM review_runs run
		JOIN review_requests request ON request.id = run.request_id
		WHERE run.id=$1`, runID).Scan(&tenantID)
	if err != nil {
		return fmt.Errorf("resolve review intervention tenant: %w", err)
	}
	var interventionID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO review_run_interventions (tenant_id, run_id, state, reason)
		VALUES ($1,$2,'open',$3)
		ON CONFLICT (run_id) DO NOTHING
		RETURNING id`, tenantID, runID, "review reached terminal state: "+string(state)).Scan(&interventionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create review intervention: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1,'system:workflow','review_intervention.opened',$2,
		        jsonb_build_object('run_id',$3::text,'run_state',$4::text))`, tenantID, interventionID.String(), runID.String(), state); err != nil {
		return fmt.Errorf("audit review intervention creation: %w", err)
	}
	return nil
}

// resolveReviewInterventionForRetry is intentionally separate from the retry
// audit. It only resolves an active queue item after a new run and its durable
// admission records have been created successfully in this same transaction.
func resolveReviewInterventionForRetry(ctx context.Context, tx pgx.Tx, tenantID, sourceRunID, retryRunID uuid.UUID, actor string) error {
	var intervention domain.ReviewIntervention
	err := tx.QueryRow(ctx, `
		UPDATE review_run_interventions
		SET state='resolved', resolved_at=now(), resolved_by=$3, resolution='retry_requested',
		    revision=revision+1, updated_at=now()
		WHERE tenant_id=$1 AND run_id=$2 AND state IN ('open','claimed')
		RETURNING `+reviewInterventionColumns, tenantID, sourceRunID, actor).Scan(
		&intervention.ID, &intervention.TenantID, &intervention.RunID, &intervention.Revision, &intervention.State, &intervention.AssigneeSubject,
		&intervention.OpenedAt, &intervention.ClaimedAt, &intervention.ResolvedAt, &intervention.ResolvedBy, &intervention.Resolution, &intervention.Reason,
		&intervention.CreatedAt, &intervention.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve review intervention for retry: %w", err)
	}
	if err := auditReviewIntervention(ctx, tx, tenantID, actor, "review_intervention.retry_requested", intervention, map[string]any{"retry_run_id": retryRunID.String()}); err != nil {
		return err
	}
	return nil
}

func auditReviewIntervention(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, actor, action string, intervention domain.ReviewIntervention, metadata map[string]any) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1,$2,$3,$4,$5::jsonb)`, tenantID, actor, action, intervention.ID.String(), jsonPayload(metadata)); err != nil {
		return fmt.Errorf("audit review intervention: %w", err)
	}
	return nil
}

func scanReviewIntervention(row rowScanner) (domain.ReviewIntervention, error) {
	var intervention domain.ReviewIntervention
	var state string
	if err := row.Scan(
		&intervention.ID, &intervention.TenantID, &intervention.RunID, &intervention.Revision, &state, &intervention.AssigneeSubject,
		&intervention.OpenedAt, &intervention.ClaimedAt, &intervention.ResolvedAt, &intervention.ResolvedBy, &intervention.Resolution, &intervention.Reason,
		&intervention.CreatedAt, &intervention.UpdatedAt,
	); err != nil {
		return domain.ReviewIntervention{}, err
	}
	intervention.State = domain.ReviewInterventionState(state)
	return intervention, nil
}

func scanReviewInterventionSummary(row rowScanner) (domain.ReviewIntervention, error) {
	var intervention domain.ReviewIntervention
	var state string
	var failureCode, failureMessage *string
	if err := row.Scan(
		&intervention.ID, &intervention.TenantID, &intervention.RunID, &intervention.Revision, &state, &intervention.AssigneeSubject,
		&intervention.OpenedAt, &intervention.ClaimedAt, &intervention.ResolvedAt, &intervention.ResolvedBy, &intervention.Resolution, &intervention.Reason,
		&intervention.CreatedAt, &intervention.UpdatedAt,
		&intervention.RunRevision, &intervention.RunState, &failureCode, &failureMessage,
		&intervention.Provider, &intervention.APIBaseURL, &intervention.Repository, &intervention.ReviewNumber, &intervention.Title, &intervention.Author,
	); err != nil {
		return domain.ReviewIntervention{}, err
	}
	intervention.State = domain.ReviewInterventionState(state)
	if failureCode != nil {
		intervention.FailureCode = *failureCode
	}
	if failureMessage != nil {
		intervention.FailureMessage = *failureMessage
	}
	return intervention, nil
}
