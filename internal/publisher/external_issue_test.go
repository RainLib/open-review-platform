package publisher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func testExternalIssuePublication(provider domain.Provider) domain.ExternalIssuePublication {
	return domain.ExternalIssuePublication{
		ExternalIssueReceipt:   domain.ExternalIssueReceipt{ID: uuid.New(), IssueID: uuid.New(), Provider: provider, Repository: "RainLib/demo", Trigger: "first_seen"},
		InstallationExternalID: "42",
		CredentialRef:          "github-app",
		Marker:                 "open-review-platform:external-issue:test-marker",
		Title:                  "[Open Review] high security in server.go",
		Body:                   "Retained evidence\n\n<!-- open-review-platform:external-issue:test-marker -->",
		Labels:                 []string{"open-review", "security"},
		AssigneeExternalID:     "reviewer",
	}
}

func TestPublishGitHubExternalIssueCreatesOneMarkerKeyedIssue(t *testing.T) {
	var postBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/issues":
			if r.URL.Query().Get("state") != "all" || r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("page") != "1" {
				t.Fatalf("unexpected issue listing query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/issues":
			if err := json.NewDecoder(r.Body).Decode(&postBody); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":701,"number":17,"html_url":"https://github.example/RainLib/demo/issues/17"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	publication := testExternalIssuePublication(domain.ProviderGitHub)
	publication.APIBaseURL = server.URL
	p := NewHTTPWithResolver(tokenResolver{})
	p.client = server.Client()
	result, err := p.PublishExternalIssue(context.Background(), publication)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID != "17" || !strings.Contains(result.ExternalURL, "/issues/17") {
		t.Fatalf("unexpected result: %#v", result)
	}
	if postBody["title"] != publication.Title || !strings.Contains(postBody["body"].(string), publication.Marker) {
		t.Fatalf("publication body lost durable marker: %#v", postBody)
	}
	labels, ok := postBody["labels"].([]any)
	if !ok || len(labels) != 2 || postBody["assignees"].([]any)[0] != "reviewer" {
		t.Fatalf("publication did not retain routing metadata: %#v", postBody)
	}
}

func TestPublishGitHubExternalIssueReusesExistingMarkerOnRetry(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/repos/RainLib/demo/issues" {
			t.Fatalf("retry must not create duplicate issue: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"id":701,"number":17,"html_url":"https://github.example/RainLib/demo/issues/17","body":"<!-- open-review-platform:external-issue:test-marker -->"}]`))
	}))
	defer server.Close()
	publication := testExternalIssuePublication(domain.ProviderGitHub)
	publication.APIBaseURL = server.URL
	p := NewHTTPWithResolver(tokenResolver{})
	p.client = server.Client()
	result, err := p.PublishExternalIssue(context.Background(), publication)
	if err != nil || result.ExternalID != "17" || requests != 1 {
		t.Fatalf("result=%#v requests=%d err=%v", result, requests, err)
	}
}

func TestPublishGitLabExternalIssueCreatesProviderIssue(t *testing.T) {
	var sawCreate bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !strings.HasSuffix(r.URL.EscapedPath(), "/projects/RainLib%2Fdemo/issues") {
				t.Fatalf("unexpected GitLab project path: %s", r.URL.EscapedPath())
			}
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPost:
			sawCreate = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["description"] == nil || !strings.Contains(body["description"].(string), "test-marker") || body["labels"] != "open-review,security" {
				t.Fatalf("unexpected GitLab issue payload: %#v", body)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":701,"iid":9,"web_url":"https://gitlab.example/RainLib/demo/-/issues/9"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()
	publication := testExternalIssuePublication(domain.ProviderGitLab)
	publication.APIBaseURL = server.URL
	p := NewHTTPWithResolver(tokenResolver{})
	p.client = server.Client()
	result, err := p.PublishExternalIssue(context.Background(), publication)
	if err != nil || !sawCreate || result.ExternalID != "9" || !strings.Contains(result.ExternalURL, "/issues/9") {
		t.Fatalf("result=%#v created=%v err=%v", result, sawCreate, err)
	}
}

func TestPublishGitLabExternalIssueReusesExistingMarkerOnRetry(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.EscapedPath(), "/projects/RainLib%2Fdemo/issues") {
			t.Fatalf("retry must list the encoded GitLab project before creating: %s %s", r.Method, r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`[{"id":701,"iid":9,"web_url":"https://gitlab.example/RainLib/demo/-/issues/9","description":"<!-- open-review-platform:external-issue:test-marker -->"}]`))
	}))
	defer server.Close()
	publication := testExternalIssuePublication(domain.ProviderGitLab)
	publication.APIBaseURL = server.URL + "/api/v4"
	p := NewHTTPWithResolver(tokenResolver{})
	p.client = server.Client()
	result, err := p.PublishExternalIssue(context.Background(), publication)
	if err != nil || result.ExternalID != "9" || requests != 1 {
		t.Fatalf("result=%#v requests=%d err=%v", result, requests, err)
	}
}

func TestExternalIssueResultRequiresProviderDeepLink(t *testing.T) {
	_, err := externalIssueResult(providerExternalIssue{ID: 701, Number: 17}, "github")
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("missing provider URL must not be recorded as a completed external issue: %v", err)
	}
}

func TestCloseGitHubExternalIssueUsesRecordedNumber(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/RainLib/demo/issues/17" {
			t.Fatalf("unexpected GitHub close request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"number":17,"state":"closed"}`))
	}))
	defer server.Close()
	publication := testExternalIssuePublication(domain.ProviderGitHub)
	publication.APIBaseURL = server.URL
	publication.ExternalID = "17"
	publication.State = domain.ExternalIssuePublicationCreated
	p := NewHTTPWithResolver(tokenResolver{})
	p.client = server.Client()
	if err := p.CloseExternalIssue(context.Background(), publication); err != nil {
		t.Fatal(err)
	}
	if body["state"] != "closed" || body["state_reason"] != "completed" {
		t.Fatalf("unexpected GitHub close body: %#v", body)
	}
}

func TestCloseGitLabExternalIssueUsesRecordedIID(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.EscapedPath(), "/projects/RainLib%2Fdemo/issues/9") {
			t.Fatalf("unexpected GitLab close request: %s %s", r.Method, r.URL.EscapedPath())
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"iid":9,"state":"closed"}`))
	}))
	defer server.Close()
	publication := testExternalIssuePublication(domain.ProviderGitLab)
	publication.APIBaseURL = server.URL + "/api/v4"
	publication.ExternalID = "9"
	publication.State = domain.ExternalIssuePublicationCreated
	p := NewHTTPWithResolver(tokenResolver{})
	p.client = server.Client()
	if err := p.CloseExternalIssue(context.Background(), publication); err != nil {
		t.Fatal(err)
	}
	if body["state_event"] != "close" {
		t.Fatalf("unexpected GitLab close body: %#v", body)
	}
}
