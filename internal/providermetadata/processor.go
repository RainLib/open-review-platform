package providermetadata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/RainLib/open-review-platform/internal/webhook"
)

type AuthorAdmissionStore interface {
	ClaimGitLabAuthorAdmission(context.Context, string, time.Duration) (*store.GitLabAuthorAdmissionTarget, error)
	CompleteGitLabAuthorAdmission(context.Context, store.GitLabAuthorAdmissionTarget, domain.InboundEvent) error
	SkipGitLabAuthorAdmission(context.Context, store.GitLabAuthorAdmissionTarget, string) error
	RetryGitLabAuthorAdmission(context.Context, store.GitLabAuthorAdmissionTarget, time.Time, string) error
}

type AuthorReader interface {
	ResolveAuthor(context.Context, domain.ReviewJob, string) (string, error)
}

type Processor struct {
	Store       AuthorAdmissionStore
	Client      AuthorReader
	WorkerID    string
	Lease       time.Duration
	TaskStarted func() func()
}

// RunOnce converts one already-verified GitLab webhook into an admission only
// after an exact provider reread. Provider errors retain a durable retry;
// mismatched author/head evidence cannot start review work.
func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	if p.Store == nil || p.Client == nil || strings.TrimSpace(p.WorkerID) == "" {
		return false, fmt.Errorf("GitLab author processor is not configured")
	}
	lease := p.Lease
	if lease <= 0 {
		lease = time.Minute
	}
	target, err := p.Store.ClaimGitLabAuthorAdmission(ctx, p.WorkerID, lease)
	if errors.Is(err, store.ErrNoGitLabAuthorAdmission) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if p.TaskStarted != nil {
		defer p.TaskStarted()()
	}
	event, accepted, err := webhook.NormalizeGitLab(target.DeliveryExternalID, "Merge Request Hook", target.Payload, target.ReceivedAt)
	if err != nil || !accepted || event.Provider != domain.ProviderGitLab || event.Repository != target.Job.Repository ||
		event.ReviewNumber != target.Job.ReviewNumber || !strings.EqualFold(event.HeadSHA, target.Job.HeadSHA) ||
		event.AuthorExternalID != target.ExpectedAuthorID || event.APIBaseURL != target.Job.APIBaseURL ||
		event.InstallationExternalID != target.Job.InstallationExternalID {
		return true, p.Store.SkipGitLabAuthorAdmission(ctx, *target, "webhook_identity_mismatch")
	}
	author, err := p.Client.ResolveAuthor(ctx, target.Job, target.ExpectedAuthorID)
	if errors.Is(err, ErrStaleMergeRequest) {
		return true, p.Store.SkipGitLabAuthorAdmission(ctx, *target, "provider_revision_changed")
	}
	if err != nil {
		// A broker redelivery cannot create a second job: the delivery remains
		// leased in PostgreSQL and retries with a bounded attempt budget.
		backoff := time.Duration(1<<min(target.Attempt, 6)) * time.Minute
		if retryErr := p.Store.RetryGitLabAuthorAdmission(ctx, *target, time.Now().Add(backoff), "provider_read_unavailable"); retryErr != nil {
			return true, retryErr
		}
		return true, nil
	}
	event.Author = author
	return true, p.Store.CompleteGitLabAuthorAdmission(ctx, *target, event)
}
