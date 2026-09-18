package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/identity"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type recordingStore struct {
	event                  domain.InboundEvent
	interaction            domain.InteractionCommand
	installation           domain.Installation
	installationInput      domain.InstallationInput
	called                 bool
	interactionCalled      bool
	createRuleSetErr       error
	requestRuleApprovalErr error
	decideRuleApprovalErr  error
	createBindingErr       error
	updateBindingErr       error
	providerIdentityErr    error
	listRunEventsErr       error
}

type fixedAuthenticator struct {
	err error
}

func (a fixedAuthenticator) Authenticate(context.Context, *http.Request) (identity.Principal, error) {
	if a.err != nil {
		return identity.Principal{}, a.err
	}
	return identity.Principal{Subject: "operator"}, nil
}

func (s *recordingStore) CreateTenant(context.Context, string, string, string) (domain.Tenant, error) {
	return domain.Tenant{}, nil
}

func (s *recordingStore) UpsertMembership(context.Context, string, string, string, string) (domain.Membership, error) {
	return domain.Membership{}, nil
}

func (s *recordingStore) CreateInstallation(_ context.Context, _ string, _ string, input domain.InstallationInput) (domain.Installation, error) {
	s.installationInput = input
	return s.installation, nil
}

func (*recordingStore) ListInstallations(context.Context, string, string, int) ([]domain.InstallationSummary, error) {
	return nil, nil
}

func (s *recordingStore) CreateRuleSet(context.Context, string, string, domain.RuleSetInput) (domain.RuleSetWithDraft, error) {
	return domain.RuleSetWithDraft{}, s.createRuleSetErr
}

func (*recordingStore) ListRuleSets(context.Context, string, string, int) ([]domain.RuleSet, error) {
	return nil, nil
}

func (s *recordingStore) RequestRuleApproval(context.Context, string, string, uuid.UUID, int, domain.RuleApprovalRequestInput) (domain.RuleApprovalRequest, error) {
	return domain.RuleApprovalRequest{}, s.requestRuleApprovalErr
}

func (s *recordingStore) DecideRuleApproval(context.Context, string, string, uuid.UUID, domain.RuleApprovalDecisionInput) (domain.RuleApprovalRequest, error) {
	return domain.RuleApprovalRequest{}, s.decideRuleApprovalErr
}

func (*recordingStore) PublishRuleVersion(context.Context, string, string, uuid.UUID, int) (domain.RuleVersion, error) {
	return domain.RuleVersion{}, nil
}

func (s *recordingStore) CreateRuleBinding(context.Context, string, string, domain.RuleBindingInput) (domain.RuleBinding, error) {
	return domain.RuleBinding{}, s.createBindingErr
}

func (s *recordingStore) UpdateRuleBinding(context.Context, string, string, uuid.UUID, domain.RuleBindingUpdateInput) (domain.RuleBinding, error) {
	return domain.RuleBinding{}, s.updateBindingErr
}

func (*recordingStore) ListRuleBindings(context.Context, string, string, int) ([]domain.RuleBinding, error) {
	return nil, nil
}

func (s *recordingStore) UpsertProviderIdentity(context.Context, string, string, domain.ProviderIdentity) (domain.ProviderIdentity, error) {
	return domain.ProviderIdentity{}, s.providerIdentityErr
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

func (s *recordingStore) ListRunEvents(context.Context, string, string, uuid.UUID, int) ([]domain.RunEvent, error) {
	return nil, s.listRunEventsErr
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
func (*recordingStore) RenewClaim(context.Context, uuid.UUID, string, time.Duration) error {
	return nil
}
func (*recordingStore) Close() {}

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

func TestManagementMutationErrorsAreClassified(t *testing.T) {
	validBindingID := uuid.New()
	validRuleSetID := uuid.New()
	validApprovalRequestID := uuid.New()
	tests := []struct {
		name          string
		method        string
		path          string
		body          string
		configure     func(*recordingStore, error)
		validationErr error
		expectedError string
	}{
		{
			name:          "create rule set",
			method:        http.MethodPost,
			path:          "/v1/tenants/acme/rule-sets",
			body:          `{}`,
			configure:     func(s *recordingStore, err error) { s.createRuleSetErr = err },
			validationErr: store.ErrInvalidRuleSet,
			expectedError: "rule set is invalid",
		},
		{
			name:          "request rule approval",
			method:        http.MethodPost,
			path:          "/v1/tenants/acme/rule-sets/" + validRuleSetID.String() + "/versions/1/approval-requests",
			body:          `{"required_approvals":1}`,
			configure:     func(s *recordingStore, err error) { s.requestRuleApprovalErr = err },
			validationErr: store.ErrInvalidRuleApproval,
			expectedError: "rule approval request is invalid",
		},
		{
			name:          "decide rule approval",
			method:        http.MethodPost,
			path:          "/v1/tenants/acme/rule-approval-requests/" + validApprovalRequestID.String() + "/decisions",
			body:          `{"decision":"approved"}`,
			configure:     func(s *recordingStore, err error) { s.decideRuleApprovalErr = err },
			validationErr: store.ErrInvalidRuleApproval,
			expectedError: "rule approval decision is invalid",
		},
		{
			name:          "create binding",
			method:        http.MethodPost,
			path:          "/v1/tenants/acme/rule-bindings",
			body:          `{}`,
			configure:     func(s *recordingStore, err error) { s.createBindingErr = err },
			validationErr: store.ErrInvalidRuleBinding,
			expectedError: "rule binding is invalid",
		},
		{
			name:          "update binding",
			method:        http.MethodPatch,
			path:          "/v1/tenants/acme/rule-bindings/" + validBindingID.String(),
			body:          `{"state":"active"}`,
			configure:     func(s *recordingStore, err error) { s.updateBindingErr = err },
			validationErr: store.ErrInvalidRuleBinding,
			expectedError: "rule binding state is invalid",
		},
		{
			name:          "upsert provider identity",
			method:        http.MethodPut,
			path:          "/v1/tenants/acme/provider-identities/github/42",
			body:          `{"subject":"operator"}`,
			configure:     func(s *recordingStore, err error) { s.providerIdentityErr = err },
			validationErr: store.ErrInvalidProviderIdentity,
			expectedError: "provider identity is invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, result := range []struct {
				name       string
				err        error
				statusCode int
			}{
				{name: "validation", err: test.validationErr, statusCode: http.StatusBadRequest},
				{name: "storage failure", err: errors.New("database unavailable"), statusCode: http.StatusInternalServerError},
			} {
				t.Run(result.name, func(t *testing.T) {
					recording := &recordingStore{}
					test.configure(recording, result.err)
					server := New(recording, fixedAuthenticator{}, "", "")
					mux := http.NewServeMux()
					server.Register(mux)

					request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, request)
					if response.Code != result.statusCode {
						t.Fatalf("status=%d, want %d; body=%s", response.Code, result.statusCode, response.Body.String())
					}
					if result.statusCode == http.StatusBadRequest && !strings.Contains(response.Body.String(), test.expectedError) {
						t.Fatalf("body=%q does not contain %q", response.Body.String(), test.expectedError)
					}
				})
			}
		})
	}
}

func TestInstallationResponseDoesNotExposeCredentialReference(t *testing.T) {
	recording := &recordingStore{
		installation: domain.Installation{
			ID:              uuid.New(),
			Provider:        domain.ProviderGitHub,
			ExternalID:      "123",
			RepositoryScope: "RainLib/*",
			APIBaseURL:      "https://api.github.com",
			CredentialRef:   "github-app",
			Active:          true,
		},
	}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations", strings.NewReader(`{"provider":"github","external_id":"123","repository_scope":"RainLib/*","api_base_url":"https://api.github.com","credential_ref":"github-app"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "credential_ref") || strings.Contains(response.Body.String(), "github-app") {
		t.Fatalf("credential reference must not be serialized: %s", response.Body.String())
	}
}

func TestInstallationEndpointComesFromTrustedProviderConfiguration(t *testing.T) {
	recording := &recordingStore{installation: domain.Installation{ID: uuid.New(), Active: true}}
	server := NewWithProviderAPIURLs(
		recording,
		fixedAuthenticator{},
		"",
		"",
		"https://github.example.com/api/v3",
		"https://gitlab.example.com/api/v4",
	)
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/acme/installations",
		strings.NewReader(`{"provider":"GitHub","external_id":"123","repository_scope":"acme/*","api_base_url":"https://attacker.invalid/api","credential_ref":"github-app"}`),
	)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if recording.installationInput.Provider != domain.ProviderGitHub {
		t.Fatalf("provider=%q, want github", recording.installationInput.Provider)
	}
	if recording.installationInput.APIBaseURL != "https://github.example.com/api/v3" {
		t.Fatalf("api base URL=%q, want trusted GitHub endpoint", recording.installationInput.APIBaseURL)
	}
}

func TestRunEventStreamReportsTerminalReadErrors(t *testing.T) {
	runID := uuid.New()
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "not found", err: store.ErrNotFound, code: "not_found"},
		{name: "store failure", err: errors.New("database unavailable"), code: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recording := &recordingStore{listRunEventsErr: test.err}
			server := New(recording, fixedAuthenticator{}, "", "")
			mux := http.NewServeMux()
			server.Register(mux)

			request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/runs/"+runID.String()+"/events", nil)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
			}
			if !response.Flushed || !strings.Contains(response.Body.String(), "event: error\ndata: {\"code\":\""+test.code+"\"}") {
				t.Fatalf("expected flushed terminal event %q, got flushed=%v body=%q", test.code, response.Flushed, response.Body.String())
			}
		})
	}
}

func githubSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return fmt.Sprintf("sha256=%x", mac.Sum(nil))
}
