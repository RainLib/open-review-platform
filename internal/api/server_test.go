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
	event  domain.InboundEvent
	called bool
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

func (s *recordingStore) Enqueue(_ context.Context, event domain.InboundEvent) (domain.ReviewJob, bool, error) {
	s.called, s.event = true, event
	return domain.ReviewJob{ID: uuid.New()}, false, nil
}

func (*recordingStore) Claim(context.Context, string) (*domain.ReviewJob, error) {
	return nil, store.ErrNoQueuedJob
}

func (*recordingStore) SaveFindings(context.Context, uuid.UUID, []domain.Finding) error { return nil }
func (*recordingStore) Succeed(context.Context, uuid.UUID, string) error                { return nil }
func (*recordingStore) Fail(context.Context, uuid.UUID, string, string) error           { return nil }
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
