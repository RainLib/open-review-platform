package interaction

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type admissionResolver struct {
	job   domain.ReviewJob
	token string
	err   error
}

func (r *admissionResolver) Resolve(_ context.Context, job domain.ReviewJob) (string, error) {
	r.job = job
	return r.token, r.err
}

func TestAdmitterReadsGitHubPullRequestAtTheCurrentRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/RainLib/open-review-platform/pulls/42" || request.Method != http.MethodGet {
			t.Fatalf("request=%s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer installation-token" || request.Header.Get("X-GitHub-Api-Version") == "" {
			t.Fatalf("provider credential was not supplied as expected")
		}
		_, _ = w.Write([]byte(`{"title":"Guard interaction admission","draft":false,"user":{"login":"rainlib"},"labels":[{"name":"security"}],"base":{"ref":"main","sha":"base-sha"},"head":{"ref":"feature/command","sha":"head-sha"}}`))
	}))
	defer server.Close()
	resolver := &admissionResolver{token: "installation-token"}
	event, err := (Admitter{Resolver: resolver, Now: func() time.Time { return time.Date(2026, 9, 20, 1, 2, 3, 0, time.UTC) }}).Resolve(context.Background(), AdmissionRequest{
		InteractionID: uuid.New(), Event: domain.CommentEvent{Provider: domain.ProviderGitHub, APIBaseURL: server.URL, InstallationExternalID: "123", Repository: "RainLib/open-review-platform", CloneURL: "https://github.com/RainLib/open-review-platform.git", ReviewNumber: 42},
		Mode: domain.ReviewModeSecurity, ActorSubject: "owner", CredentialRef: "github-app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.DeliveryID == "" || !event.DeferAcknowledgement || event.TriggerKind != "comment" || event.ActorKind != "user" || event.ActorSubject != "owner" || event.ReviewMode != domain.ReviewModeSecurity || event.BaseSHA != "base-sha" || event.HeadSHA != "head-sha" || event.BaseRef != "main" || event.HeadRef != "feature/command" || event.CloneURL != "https://github.com/RainLib/open-review-platform.git" || event.Title != "Guard interaction admission" || len(event.Labels) != 1 {
		t.Fatalf("event=%#v", event)
	}
	if resolver.job.Provider != domain.ProviderGitHub || resolver.job.CredentialRef != "github-app" || resolver.job.InstallationExternalID != "123" {
		t.Fatalf("resolver job=%#v", resolver.job)
	}
	if strings.Contains(string(event.Payload), "installation-token") || !strings.Contains(string(event.Payload), "head-sha") {
		t.Fatalf("payload must be safe, revision-only provenance: %s", event.Payload)
	}
}

func TestAdmitterReadsGitLabMergeRequestAtTheCurrentRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.EscapedPath() != "/projects/acme%2Fplatform/merge_requests/7" || request.Method != http.MethodGet {
			t.Fatalf("request=%s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer gitlab-token" {
			t.Fatalf("provider credential was not supplied as expected")
		}
		_, _ = w.Write([]byte(`{"title":"Review latest MR","work_in_progress":true,"author":{"username":"maintainer"},"labels":["backend","security"],"target_branch":"main","source_branch":"feature/command","sha":"head-sha","diff_refs":{"base_sha":"base-sha"}}`))
	}))
	defer server.Close()
	event, err := (Admitter{Resolver: &admissionResolver{token: "gitlab-token"}}).Resolve(context.Background(), AdmissionRequest{
		InteractionID: uuid.New(), Event: domain.CommentEvent{Provider: domain.ProviderGitLab, APIBaseURL: server.URL, InstallationExternalID: "456", Repository: "acme/platform", CloneURL: "https://gitlab.example/acme/platform.git", ReviewNumber: 7},
		Mode: domain.ReviewModeDeep, ActorSubject: "reviewer", CredentialRef: "gitlab-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.IsDraft != true || event.BaseRef != "main" || event.HeadRef != "feature/command" || event.BaseSHA != "base-sha" || event.HeadSHA != "head-sha" || event.Author != "maintainer" || len(event.Labels) != 2 {
		t.Fatalf("event=%#v", event)
	}
}

func TestAdmitterRejectsIncompleteProviderMetadataAndResolverFailures(t *testing.T) {
	resolverErr := errors.New("credential unavailable")
	request := AdmissionRequest{InteractionID: uuid.New(), Event: domain.CommentEvent{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", InstallationExternalID: "123", Repository: "RainLib/open-review-platform", CloneURL: "https://github.com/RainLib/open-review-platform.git", ReviewNumber: 1}, Mode: domain.ReviewModeConfigured, ActorSubject: "owner", CredentialRef: "github-app"}
	if _, err := (Admitter{Resolver: &admissionResolver{err: resolverErr}}).Resolve(context.Background(), request); !errors.Is(err, resolverErr) {
		t.Fatalf("resolver error=%v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"base":{"ref":"main","sha":"base"},"head":{"ref":"feature"}}`))
	}))
	defer server.Close()
	request.Event.APIBaseURL = server.URL
	if _, err := (Admitter{Resolver: &admissionResolver{token: "token"}}).Resolve(context.Background(), request); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete metadata error=%v", err)
	}
}

func TestAdmissionRequestFromPayloadAcceptsOnlyCompleteCommandContext(t *testing.T) {
	interactionID := uuid.New()
	ruleSetID := uuid.New()
	request, err := AdmissionRequestFromPayload(map[string]any{
		"interaction_id": interactionID.String(), "provider": "github", "api_base_url": "https://api.github.com",
		"installation_external_id": "123", "credential_ref": "github-app", "repository": "RainLib/open-review-platform",
		"clone_url": "https://github.com/RainLib/open-review-platform.git", "review_number": float64(7),
		"comment_external_id": "comment-7", "actor_external_id": "42", "actor_subject": "member-42", "mode": "security", "rule_set_id": ruleSetID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.InteractionID != interactionID || request.Event.Provider != domain.ProviderGitHub || request.Event.ReviewNumber != 7 || request.Mode != domain.ReviewModeSecurity || request.RuleSetID == nil || *request.RuleSetID != ruleSetID || request.ActorSubject != "member-42" {
		t.Fatalf("request=%#v", request)
	}
	_, err = AdmissionRequestFromPayload(map[string]any{
		"interaction_id": interactionID.String(), "provider": "github", "api_base_url": "https://api.github.com",
		"installation_external_id": "123", "credential_ref": "github-app", "repository": "RainLib/open-review-platform",
		"clone_url": "https://github.com/RainLib/open-review-platform.git", "review_number": 7.5,
		"comment_external_id": "comment-7", "actor_subject": "member-42", "mode": "security",
	})
	if err == nil {
		t.Fatal("fractional review number must be rejected")
	}
}
