package store

import (
	"context"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// GetOperationalMetrics returns one fleet-wide snapshot for the private
// Prometheus endpoint. Labels are intentionally absent: tenant/repository
// dimensions belong in authenticated read models, not shared monitoring.
func (s *PostgresStore) GetOperationalMetrics(ctx context.Context) (domain.OperationalMetricsSnapshot, error) {
	var snapshot domain.OperationalMetricsSnapshot
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM review_jobs WHERE state='queued'),
			(SELECT COALESCE(EXTRACT(EPOCH FROM (now()-MIN(created_at))),0)::bigint FROM review_jobs WHERE state='queued'),
			(SELECT COUNT(*) FROM outbox_messages WHERE published_at IS NULL),
			(SELECT COALESCE(EXTRACT(EPOCH FROM (now()-MIN(created_at))),0)::bigint FROM outbox_messages WHERE published_at IS NULL),
			(SELECT COUNT(*) FROM outbox_messages WHERE published_at IS NULL AND topic IN ('review.run.acknowledged','review.interaction.response') AND created_at < now()-interval '5 seconds'),
			(SELECT COUNT(*) FROM review_jobs WHERE state='running' AND locked_until < now()),
			(SELECT COUNT(*) FROM (
				SELECT kind,MAX(expires_at) AS latest_expires_at
				FROM worker_heartbeats
				GROUP BY kind
			) worker_kind WHERE latest_expires_at < now()),
			(SELECT COUNT(*) FROM publication_receipts WHERE last_error IS NOT NULL AND updated_at > now()-interval '1 hour'),
			(SELECT COUNT(*) FROM review_runs WHERE state='failed' AND finished_at > now()-interval '1 hour'),
			(SELECT COUNT(*) FROM review_runs WHERE state IN ('completed','failed','cancelled','superseded','needs_attention') AND finished_at > now()-interval '1 hour'),
			(SELECT COUNT(*) FROM notification_deliveries WHERE state='pending'),
			(SELECT COALESCE(EXTRACT(EPOCH FROM (now()-MIN(created_at))),0)::bigint FROM notification_deliveries WHERE state='pending')`).Scan(
		&snapshot.QueuedReviewJobs,
		&snapshot.OldestQueuedReviewJobSeconds,
		&snapshot.UnpublishedOutboxMessages,
		&snapshot.OldestUnpublishedOutboxSeconds,
		&snapshot.AcknowledgementSLABreaches,
		&snapshot.ExpiredReviewLeases,
		&snapshot.StaleWorkerHeartbeats,
		&snapshot.PublicationFailuresLastHour,
		&snapshot.FailedRunsLastHour,
		&snapshot.TerminalRunsLastHour,
		&snapshot.PendingNotifications,
		&snapshot.OldestPendingNotificationSeconds,
	)
	if err != nil {
		return domain.OperationalMetricsSnapshot{}, fmt.Errorf("sample operational metrics: %w", err)
	}
	return snapshot, nil
}
