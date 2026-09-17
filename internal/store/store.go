package store

import (
	"context"
	"errors"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

var (
	ErrUnknownInstallation = errors.New("unknown or inactive provider installation")
	ErrNoQueuedJob         = errors.New("no queued review job")
	ErrJobClaimLost        = errors.New("review job is no longer claimed by this runner")
	ErrForbidden           = errors.New("actor is not allowed to manage this tenant")
	ErrConflict            = errors.New("resource already exists")
	ErrRevisionConflict    = errors.New("review run revision does not match")
	ErrInboxClaimLost      = errors.New("inbox message claim is no longer held")
)

// Store owns durable state transitions. A job can only be produced by a
// verified provider delivery and every delivery is recorded once per provider.
type Store interface {
	CreateTenant(ctx context.Context, actor, slug, name string) (domain.Tenant, error)
	UpsertMembership(ctx context.Context, actor, tenantSlug, subject, role string) (domain.Membership, error)
	CreateInstallation(ctx context.Context, actor, tenantSlug string, input domain.InstallationInput) (domain.Installation, error)
	Enqueue(ctx context.Context, event domain.InboundEvent) (job domain.ReviewJob, duplicate bool, err error)
	Claim(ctx context.Context, workerID string) (*domain.ReviewJob, error)
	SaveFindings(ctx context.Context, jobID uuid.UUID, findings []domain.Finding) error
	Succeed(ctx context.Context, jobID uuid.UUID, workerID string) error
	Fail(ctx context.Context, jobID uuid.UUID, workerID, message string) error
	Close()
}

// WorkflowStore is intentionally separate from the legacy runner Store. It
// allows the polling runner to remain a recovery path while new workers use
// revisioned runs and durable message hand-off.
type WorkflowStore interface {
	ClaimOutbox(ctx context.Context, relayID string, limit int) ([]domain.OutboxMessage, error)
	MarkOutboxPublished(ctx context.Context, messageID uuid.UUID, relayID string) error
	ReleaseOutbox(ctx context.Context, messageID uuid.UUID, relayID, reason string) error
	ClaimInbox(ctx context.Context, consumer string, messageID uuid.UUID) (claimToken uuid.UUID, claimed bool, err error)
	CompleteInbox(ctx context.Context, consumer string, messageID, claimToken uuid.UUID) error
	ReleaseInbox(ctx context.Context, consumer string, messageID, claimToken uuid.UUID, reason string) error
}
