package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type recordingStore struct {
	event             domain.InboundEvent
	interaction       domain.InteractionCommand
	called            bool
	interactionCalled bool
}

func (s *recordingStore) CreateTenant(context.Context, string, string, string) (domain.Tenant, error) {
	return domain.Tenant{}, nil
}

func (s *recordingStore) UpsertMembership(context.Context, string, string, string, string) (domain.Membership, error) {
	return domain.Membership{}, nil
}

func (s *recordingStore) CreateInstallation(context.Context, string, string, domain.InstallationInput) (domain.Installation, error) {
	return domain.Installation{}, nil
}

func (*recordingStore) CreateRuleSet(context.Context, string, string, domain.RuleSetInput) (domain.RuleSetWithDraft, error) {
	return domain.RuleSetWithDraft{}, nil
}

func (*recordingStore) ListRuleSets(context.Context, string, string, int) ([]domain.RuleSet, error) {
	return nil, nil
}

func (*recordingStore) RequestRuleApproval(context.Context, string, string, uuid.UUID, int, domain.RuleApprovalRequestInput) (domain.RuleApprovalRequest, error) {
	return domain.RuleApprovalRequest{}, nil
}

func (*recordingStore) DecideRuleApproval(context.Context, string, string, uuid.UUID, domain.RuleApprovalDecisionInput) (domain.RuleApprovalRequest, error) {
	return domain.RuleApprovalRequest{}, nil
}

func (*recordingStore) PublishRuleVersion(context.Context, string, string, uuid.UUID, int) (domain.RuleVersion, error) {
	return domain.RuleVersion{}, nil
}

func (*recordingStore) CreateRuleBinding(context.Context, string, string, domain.RuleBindingInput) (domain.RuleBinding, error) {
	return domain.RuleBinding{}, nil
}

func (*recordingStore) ListRuleBindings(context.Context, string, string, int) ([]domain.RuleBinding, error) {
	return nil, nil
}

func (s *recordingStore) UpsertProviderIdentity(context.Context, string, string, domain.ProviderIdentity) (domain.ProviderIdentity, error) {
	return domain.ProviderIdentity{}, nil
}

func (s *recordingStore) ProcessInteraction(_ context.Context, input domain.InteractionCommand) (domain.InteractionOutcome, error) {
	s.interaction, s.interactionCalled = input, true
	return domain.InteractionOutcome{Accepted: true}, nil
}

func (*recordingStore) ListReviewRuns(context.Context, string, string, int) ([]domain.ReviewRunSummary, error) {
	return nil, nil
}

func (*recordingStore) GetReviewRun(context.Context, string, string, uuid.UUID) (domain.ReviewRunSummary, error) {
	return domain.ReviewRunSummary{}, nil
}

func (*recordingStore) GetRuleSnapshot(context.Context, string, string, uuid.UUID) (domain.RuleSnapshot, error) {
	return domain.RuleSnapshot{}, nil
}

func (*recordingStore) ListRunEvents(context.Context, string, string, uuid.UUID, int) ([]domain.RunEvent, error) {
	return nil, nil
}

func (*recordingStore) RequestRunCancellation(context.Context, string, string, uuid.UUID, int) (domain.ReviewRun, error) {
	return domain.ReviewRun{}, nil
}

func (*recordingStore) AdvanceRun(context.Context, uuid.UUID, domain.RunState) (domain.ReviewRun, error) {
	return domain.ReviewRun{}, nil
}

func (*recordingStore) AdvanceLegacyRun(context.Context, uuid.UUID, domain.RunState) (domain.ReviewRun, error) {
	return domain.ReviewRun{}, nil
}

func TestGitHubIssueCommentCommandIsVerifiedAndNormalized(t *testing.T) {
	secret := "secret"
	body := []byte(`{"action":"created","installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"issue":{"number":42,"pull_request":{"url":"https://api.github.com/repos/acme/api/pulls/42"}},"comment":{"id":99,"body":"@openreview review --mode=deep","user":{"id":7}}}`)
	recording := &recordingStore{}
	server := New(recording, nil, secret, "")
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "issue_comment")
	request.Header.Set("X-GitHub-Delivery", "delivery-comment-1")
	request.Header.Set("X-Hub-Signature-256", githubSignature(secret, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !recording.interactionCalled {
		t.Fatalf("expected accepted interaction, status=%d called=%v", response.Code, recording.interactionCalled)
	}
	if recording.interaction.Command != "review" || recording.interaction.Mode != "deep" || recording.interaction.Event.ActorExternalID != "7" {
		t.Fatalf("unexpected interaction: %#v", recording.interaction)
	}

	recording.interactionCalled = false
	request = httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "issue_comment")
	request.Header.Set("X-GitHub-Delivery", "delivery-comment-2")
	request.Header.Set("X-Hub-Signature-256", "sha256=wrong")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || recording.interactionCalled {
		t.Fatalf("invalid signature must not process command, status=%d called=%v", response.Code, recording.interactionCalled)
	}
}

func (s *recordingStore) Enqueue(_ context.Context, event domain.InboundEvent) (domain.ReviewJob, bool, error) {
	s.called, s.event = true, event
	return domain.ReviewJob{ID: uuid.New()}, false, nil
}

func (*recordingStore) Claim(context.Context, string) (*domain.ReviewJob, error) {
	return nil, store.ErrNoQueuedJob
}

func (*recordingStore) ClaimForRun(context.Context, string, uuid.UUID) (*domain.ReviewJob, error) {
	return nil, store.ErrNoQueuedJob
}

func (*recordingStore) RuleSnapshotForJob(context.Context, uuid.UUID) (domain.RuleSnapshot, error) {
	return domain.RuleSnapshot{}, store.ErrNotFound
}

func (*recordingStore) SaveFindings(context.Context, uuid.UUID, []domain.Finding) error { return nil }
func (*recordingStore) Succeed(context.Context, uuid.UUID, string) error                { return nil }
func (*recordingStore) Fail(context.Context, uuid.UUID, string, string) error           { return nil }
func (*recordingStore) Cancel(context.Context, uuid.UUID, string) error                 { return nil }
func (*recordingStore) Close()                                                          {}

func TestGitHubWebhookVerifiesBeforeQueueing(t *testing.T) {
	secret := "secret"
	body := []byte(`{"action":"opened","number":42,"installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"pull_request":{"base":{"ref":"main","sha":"base"},"head":{"ref":"feature","sha":"head"}}}`)
	store := &recordingStore{}
	server := New(store, nil, secret, "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "pull_request")
	request.Header.Set("X-GitHub-Delivery", "delivery-1")
	request.Header.Set("X-Hub-Signature-256", githubSignature(secret, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !store.called {
		t.Fatalf("expected accepted queued webhook, status=%d called=%v", response.Code, store.called)
	}
	if store.event.APIBaseURL != "https://api.github.com" || store.event.InstallationExternalID != "123" {
		t.Fatalf("unexpected event: %#v", store.event)
	}

	store.called = false
	request = httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "pull_request")
	request.Header.Set("X-GitHub-Delivery", "delivery-2")
	request.Header.Set("X-Hub-Signature-256", "sha256=wrong")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || store.called {
		t.Fatalf("invalid signature must not queue, status=%d called=%v", response.Code, store.called)
	}
}

func githubSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return fmt.Sprintf("sha256=%x", mac.Sum(nil))
}
