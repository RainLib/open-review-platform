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
