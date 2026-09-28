package providermetadata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type fixedResolver struct{}

func (fixedResolver) Resolve(_ context.Context, job domain.ReviewJob) (string, error) {
	if job.CredentialRef != "gitlab-oauth:exact" {
		return "", fmt.Errorf("wrong installation credential")
	}
	return "private-test-token", nil
}

func authorJob(base string) domain.ReviewJob {
	return domain.ReviewJob{
		Provider: domain.ProviderGitLab, APIBaseURL: base,
		Repository: "group/subgroup/service", ReviewNumber: 9,
		HeadSHA: strings.Repeat("a", 40), CredentialRef: "gitlab-oauth:exact",
	}
}

func TestResolveGitLabAuthorFromExactSelfManagedRevision(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/gitlab/api/v4/projects/group%2Fsubgroup%2Fservice/merge_requests/9" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
		}
		if r.Header.Get("Authorization") != "Bearer private-test-token" {
			t.Error("request omitted the exact provider credential")
		}
		_, _ = w.Write([]byte(`{"iid":9,"sha":"` + strings.Repeat("a", 40) + `","author":{"id":321,"username":"original-author"}}`))
	}))
	defer server.Close()
	client := GitLabAuthorClient{Resolver: fixedResolver{}, HTTPClient: server.Client(), AllowInsecureHTTP: true}
	got, err := client.ResolveAuthor(context.Background(), authorJob(server.URL+"/gitlab/api/v4"), "321")
	if err != nil || got != "original-author" || requests.Load() != 1 {
		t.Fatalf("author=%q requests=%d error=%v", got, requests.Load(), err)
	}
}

func TestResolveGitLabAuthorRejectsStaleOrMismatchedResponse(t *testing.T) {
	for _, tc := range []struct{ name, response string }{
		{name: "different author", response: `{"iid":9,"sha":"` + strings.Repeat("a", 40) + `","author":{"id":999,"username":"event-actor"}}`},
		{name: "newer revision", response: `{"iid":9,"sha":"` + strings.Repeat("b", 40) + `","author":{"id":321,"username":"original-author"}}`},
		{name: "wrong MR", response: `{"iid":10,"sha":"` + strings.Repeat("a", 40) + `","author":{"id":321,"username":"original-author"}}`},
		{name: "missing author", response: `{"iid":9,"sha":"` + strings.Repeat("a", 40) + `","author":{"id":321,"username":""}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.response)) }))
			defer server.Close()
			client := GitLabAuthorClient{Resolver: fixedResolver{}, HTTPClient: server.Client(), AllowInsecureHTTP: true}
			if author, err := client.ResolveAuthor(context.Background(), authorJob(server.URL+"/api/v4"), "321"); err == nil || author != "" {
				t.Fatalf("mismatched response admitted author=%q err=%v", author, err)
			}
		})
	}
}

func TestResolveGitLabAuthorRejectsRedirectAndInvalidDestination(t *testing.T) {
	var destinationHit atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client := GitLabAuthorClient{Resolver: fixedResolver{}, HTTPClient: source.Client(), AllowInsecureHTTP: true}
	if _, err := client.ResolveAuthor(context.Background(), authorJob(source.URL+"/api/v4"), "321"); err == nil || destinationHit.Load() {
		t.Fatalf("redirect was followed or accepted: %v", err)
	}
	for _, badBase := range []string{source.URL + "/api/v4?next=evil", "http://gitlab.example/api/v4", "https://user@gitlab.example/api/v4", "https://gitlab.example/api/v3"} {
		if _, err := client.ResolveAuthor(context.Background(), authorJob(badBase), "321"); err == nil {
			t.Fatalf("invalid API base was accepted: %q", badBase)
		}
	}
}
