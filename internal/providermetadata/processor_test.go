package providermetadata

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type authorAdmissionStore struct {
	target    *store.GitLabAuthorAdmissionTarget
	completed domain.InboundEvent
	skipped   string
	retried   string
}

func (s *authorAdmissionStore) ClaimGitLabAuthorAdmission(context.Context, string, time.Duration) (*store.GitLabAuthorAdmissionTarget, error) {
	if s.target == nil {
		return nil, store.ErrNoGitLabAuthorAdmission
	}
	return s.target, nil
}
func (s *authorAdmissionStore) CompleteGitLabAuthorAdmission(_ context.Context, _ store.GitLabAuthorAdmissionTarget, event domain.InboundEvent) error {
	s.completed = event
	return nil
}
func (s *authorAdmissionStore) SkipGitLabAuthorAdmission(_ context.Context, _ store.GitLabAuthorAdmissionTarget, reason string) error {
	s.skipped = reason
	return nil
}
func (s *authorAdmissionStore) RetryGitLabAuthorAdmission(_ context.Context, _ store.GitLabAuthorAdmissionTarget, _ time.Time, reason string) error {
	s.retried = reason
	return nil
}

type authorReaderFunc func(context.Context, domain.ReviewJob, string) (string, error)

func (fn authorReaderFunc) ResolveAuthor(ctx context.Context, job domain.ReviewJob, id string) (string, error) {
	return fn(ctx, job, id)
}

func authorAdmissionFixture() *store.GitLabAuthorAdmissionTarget {
	head := strings.Repeat("a", 40)
	payload := []byte(`{"user":{"id":999,"username":"updater"},"project":{"id":17,"path_with_namespace":"group/service","git_http_url":"https://gitlab.example/group/service.git"},"object_attributes":{"action":"update","author_id":321,"title":"Review change","iid":9,"target_branch":"main","source_branch":"feature","last_commit":{"id":"` + head + `"}}}`)
	return &store.GitLabAuthorAdmissionTarget{
		DeliveryID: uuid.New(), DeliveryExternalID: "delivery-1", ReceivedAt: time.Now(),
		Payload: payload, ExpectedAuthorID: "321", WorkerID: "author-1", Attempt: 1,
		Job: domain.ReviewJob{Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab.example/api/v4", InstallationExternalID: "17", Repository: "group/service", ReviewNumber: 9, HeadSHA: head},
	}
}

func TestAuthorProcessorAdmitsVerifiedAuthorNotWebhookActor(t *testing.T) {
	s := &authorAdmissionStore{target: authorAdmissionFixture()}
	p := Processor{Store: s, WorkerID: "author-1", Client: authorReaderFunc(func(_ context.Context, job domain.ReviewJob, id string) (string, error) {
		if id != "321" || job.HeadSHA != s.target.Job.HeadSHA {
			t.Fatalf("reader received wrong identity: %#v / %q", job, id)
		}
		return "original-author", nil
	})}
	if worked, err := p.RunOnce(context.Background()); err != nil || !worked {
		t.Fatalf("processor worked=%t err=%v", worked, err)
	}
	if s.completed.Author != "original-author" || s.completed.AuthorExternalID != "321" || s.completed.DeliveryID != "delivery-1" || s.skipped != "" {
		t.Fatalf("incorrect GitLab author admission: %#v", s)
	}
}

func TestAuthorProcessorSkipsStaleAndRetriesTransientRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"stale", ErrStaleMergeRequest, "provider_revision_changed"},
		{"temporary", errors.New("provider unavailable"), "provider_read_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &authorAdmissionStore{target: authorAdmissionFixture()}
			p := Processor{Store: s, WorkerID: "author-1", Client: authorReaderFunc(func(context.Context, domain.ReviewJob, string) (string, error) {
				return "", tc.err
			})}
			if worked, err := p.RunOnce(context.Background()); err != nil || !worked {
				t.Fatalf("processor worked=%t err=%v", worked, err)
			}
			if s.completed.Author != "" || (s.skipped != tc.want && s.retried != tc.want) {
				t.Fatalf("failure was not kept outside admission: %#v", s)
			}
		})
	}
}
