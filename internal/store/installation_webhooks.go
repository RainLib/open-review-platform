package store

import (
	"context"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

// ListInstallationWebhookReceipts exposes only the accepted webhook-to-job
// trail for a connection. It is deliberately not a provider delivery log: a
// callback rejected before installation admission has neither a trusted
// installation association nor a safe console audience.
func (s *PostgresStore) ListInstallationWebhookReceipts(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID, limit int) ([]domain.InstallationWebhookReceipt, error) {
	if installationID == uuid.Nil {
		return nil, ErrNotFound
	}
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("installation webhook receipt limit must be from 1 to 100")
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_installations WHERE id=$1 AND tenant_id=$2)`, installationID, tenantID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check installation webhook receipt scope: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		WITH admitted AS (
			SELECT delivery.id,delivery.event_name,delivery.received_at,
			       'pull_request'::text AS resource_kind,''::text AS action,0::integer AS revision,
			       job.id AS job_id,job.repository,job.review_number,job.state::text AS job_state,
			       run.id AS run_id,run.state::text AS run_state,run.trigger_kind::text AS trigger_kind
			FROM review_jobs job
			JOIN webhook_deliveries delivery ON delivery.id=job.delivery_id
			LEFT JOIN review_runs run ON run.legacy_job_id=job.id
			WHERE job.tenant_id=$1 AND job.installation_id=$2
			UNION ALL
			SELECT delivery.id,delivery.event_name,delivery.received_at,
			       'issue'::text,receipt.action,receipt.revision,
			       receipt.job_id,receipt.repository,receipt.issue_number,
			       CASE analysis.state
			         WHEN 'completed' THEN 'succeeded'
			         WHEN 'failed' THEN 'failed'
			         WHEN 'acknowledged' THEN 'running'
			         ELSE 'queued'
			       END,
			       NULL::uuid,NULL::text,'provider_issue'::text
			FROM provider_issue_analysis_receipts receipt
			JOIN webhook_deliveries delivery ON delivery.id=receipt.delivery_id
			JOIN provider_issue_analysis_jobs analysis ON analysis.id=receipt.job_id
			WHERE receipt.tenant_id=$1 AND receipt.installation_id=$2
		)
		SELECT id,event_name,received_at,resource_kind,action,revision,
		       job_id,repository,review_number,job_state,run_id,run_state,trigger_kind
		FROM admitted
		ORDER BY received_at DESC,id DESC
		LIMIT $3`, tenantID, installationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list installation webhook receipts: %w", err)
	}
	defer rows.Close()
	receipts := make([]domain.InstallationWebhookReceipt, 0)
	for rows.Next() {
		var item domain.InstallationWebhookReceipt
		var runState *string
		var triggerKind *string
		if err := rows.Scan(
			&item.ID, &item.EventName, &item.ReceivedAt,
			&item.ResourceKind, &item.Action, &item.Revision,
			&item.JobID, &item.Repository, &item.ReviewNumber, &item.JobState,
			&item.RunID, &runState, &triggerKind,
		); err != nil {
			return nil, fmt.Errorf("scan installation webhook receipt: %w", err)
		}
		if runState != nil {
			state := domain.RunState(*runState)
			item.RunState = &state
		}
		if triggerKind != nil {
			item.TriggerKind = *triggerKind
		}
		receipts = append(receipts, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate installation webhook receipts: %w", err)
	}
	return receipts, nil
}
