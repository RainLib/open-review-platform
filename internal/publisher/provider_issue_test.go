package publisher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestPublishProviderIssueAnalysisCreatesThenUpdatesMarkerComment(t *testing.T) {
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/issues/9/comments":
			if created {
				_ = json.NewEncoder(w).Encode([]providerComment{{ID: 41, Body: "<!-- open-review-platform:issue-triage:job -->"}})
			} else {
				_ = json.NewEncoder(w).Encode([]providerComment{})
			}
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/issues/9/comments":
			created = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/RainLib/demo/issues/comments/41":
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if !strings.Contains(payload["body"], "Final analysis") || !strings.Contains(payload["body"], "open-review-platform:issue-triage:job") {
				t.Fatalf("unexpected update body: %s", payload["body"])
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	publisher := NewHTTPWithResolver(tokenResolver{})
	job := domain.ProviderIssueAnalysisJob{
		Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "RainLib/demo", IssueNumber: 9,
		StableMarker: "open-review-platform:issue-triage:job",
	}
	if err := publisher.PublishProviderIssueAnalysis(context.Background(), job, "Queued"); err != nil {
		t.Fatal(err)
	}
	if err := publisher.PublishProviderIssueAnalysis(context.Background(), job, "Final analysis"); err != nil {
		t.Fatal(err)
	}
}

func TestPublishProviderIssueAcknowledgementCommentsThenReactsOnGitHub(t *testing.T) {
	events := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/issues/9/comments":
			events = append(events, "comments-listed")
			_ = json.NewEncoder(w).Encode([]providerComment{})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/issues/9/comments":
			events = append(events, "comment-created")
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/issues/9/reactions":
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["content"] != "eyes" {
				t.Fatalf("reaction=%q", payload["content"])
			}
			events = append(events, "reaction-created")
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	publisher := NewHTTPWithResolver(tokenResolver{})
	job := domain.ProviderIssueAnalysisJob{Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "RainLib/demo", IssueNumber: 9, StableMarker: "open-review-platform:issue-triage:job"}
	if err := publisher.PublishProviderIssueAcknowledgement(context.Background(), job, "## 👀 Started"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "comments-listed,comment-created,reaction-created" {
		t.Fatalf("publication order=%v", events)
	}
}

func TestPublishProviderIssueAcknowledgementReactsOnGitLab(t *testing.T) {
	var reacted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/projects/acme/demo/issues/9/notes":
			_ = json.NewEncoder(w).Encode([]providerComment{})
		case r.Method == http.MethodPost && r.URL.Path == "/projects/acme/demo/issues/9/notes":
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/projects/acme/demo/issues/9/award_emoji":
			if r.URL.Query().Get("name") != "eyes" {
				t.Fatalf("emoji=%q", r.URL.Query().Get("name"))
			}
			reacted = true
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	publisher := NewHTTPWithResolver(tokenResolver{})
	job := domain.ProviderIssueAnalysisJob{Provider: domain.ProviderGitLab, APIBaseURL: server.URL, Repository: "acme/demo", IssueNumber: 9, StableMarker: "open-review-platform:issue-triage:job"}
	if err := publisher.PublishProviderIssueAcknowledgement(context.Background(), job, "## 👀 Started"); err != nil {
		t.Fatal(err)
	}
	if !reacted {
		t.Fatal("expected GitLab eyes reaction")
	}
}
