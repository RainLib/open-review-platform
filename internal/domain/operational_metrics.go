package domain

// OperationalMetricsSnapshot contains only fleet-wide, low-cardinality
// counters and ages suitable for a private operator scrape. It deliberately
// carries no tenant, repository, provider identity, payload, or credential.
type OperationalMetricsSnapshot struct {
	QueuedReviewJobs                 int64
	OldestQueuedReviewJobSeconds     int64
	UnpublishedOutboxMessages        int64
	OldestUnpublishedOutboxSeconds   int64
	AcknowledgementSLABreaches       int64
	ExpiredReviewLeases              int64
	StaleWorkerHeartbeats            int64
	PublicationFailuresLastHour      int64
	FailedRunsLastHour               int64
	TerminalRunsLastHour             int64
	PendingNotifications             int64
	OldestPendingNotificationSeconds int64
}
