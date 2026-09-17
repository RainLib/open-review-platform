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
	ErrNotFound            = errors.New("resource not found")
)

// Store owns durable state transitions. A job can only be produced by a
// verified provider delivery and every delivery is recorded once per provider.
type Store interface {
	CreateTenant(ctx context.Context, actor, slug, name string) (domain.Tenant, error)
	UpsertMembership(ctx context.Context, actor, tenantSlug, subject, role string) (domain.Membership, error)
	CreateInstallation(ctx context.Context, actor, tenantSlug string, input domain.InstallationInput) (domain.Installation, error)
	CreateRuleSet(ctx context.Context, actor, tenantSlug string, input domain.RuleSetInput) (domain.RuleSetWithDraft, error)
	ListRuleSets(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleSet, error)
	PublishRuleVersion(ctx context.Context, actor, tenantSlug string, ruleSetID uuid.UUID, version int) (domain.RuleVersion, error)
	CreateRuleBinding(ctx context.Context, actor, tenantSlug string, input domain.RuleBindingInput) (domain.RuleBinding, error)
	ListRuleBindings(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleBinding, error)
	UpsertProviderIdentity(ctx context.Context, actor, tenantSlug string, input domain.ProviderIdentity) (domain.ProviderIdentity, error)
	ProcessInteraction(ctx context.Context, input domain.InteractionCommand) (domain.InteractionOutcome, error)
	ListReviewRuns(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.ReviewRunSummary, error)
	GetReviewRun(ctx context.Context, actor, tenantSlug string, runID uuid.UUID) (domain.ReviewRunSummary, error)
	ListRunEvents(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, afterRevision int) ([]domain.RunEvent, error)
	RequestRunCancellation(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, expectedRevision int) (domain.ReviewRun, error)
	// AdvanceRun is used by durable stage consumers that receive a review-run
	// message before a runner has claimed its backing job.
	AdvanceRun(ctx context.Context, runID uuid.UUID, next domain.RunState) (domain.ReviewRun, error)
	AdvanceLegacyRun(ctx context.Context, jobID uuid.UUID, next domain.RunState) (domain.ReviewRun, error)
	Enqueue(ctx context.Context, event domain.InboundEvent) (job domain.ReviewJob, duplicate bool, err error)
	Claim(ctx context.Context, workerID string) (*domain.ReviewJob, error)
	// ClaimForRun makes a broker message an execution hint for its own run,
	// rather than allowing a consumer to claim an unrelated tenant job.
	ClaimForRun(ctx context.Context, workerID string, runID uuid.UUID) (*domain.ReviewJob, error)
	SaveFindings(ctx context.Context, jobID uuid.UUID, findings []domain.Finding) error
	Succeed(ctx context.Context, jobID uuid.UUID, workerID string) error
	Fail(ctx context.Context, jobID uuid.UUID, workerID, message string) error
	Cancel(ctx context.Context, jobID uuid.UUID, workerID string) error
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
