package providerfeedback

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type staticResolver struct{}

func (staticResolver) Resolve(context.Context, domain.ReviewJob) (string, error) {
	return "installation-token", nil
}

func TestClientPollsThumbsOnMarkerComment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer installation-token" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/repos/RainLib/open-review-platform/issues/9/comments":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 41, "body": "unrelated"},
				{"id": 42, "body": "Analysis\n<!-- open-review-platform:issue-triage:job -->"},
			})
		case "/repos/RainLib/open-review-platform/issues/comments/42/reactions":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 91, "content": "+1", "user": map[string]any{"id": 7}},
				{"id": 92, "content": "-1", "user": map[string]any{"id": 8}},
				{"id": 93, "content": "heart", "user": map[string]any{"id": 9}},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	reactions, err := (Client{Resolver: staticResolver{}, HTTPClient: server.Client()}).Poll(context.Background(), domain.ProviderIssueAnalysisJob{
		Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "RainLib/open-review-platform",
		IssueNumber: 9, StableMarker: "open-review-platform:issue-triage:job",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reactions) != 2 || reactions[0].ExternalID != "91" || reactions[0].Kind != "useful" || reactions[1].ExternalID != "92" || reactions[1].Kind != "not_useful" {
		t.Fatalf("reactions=%#v", reactions)
	}
}

func TestClientFailsWhenMarkerCommentIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer server.Close()
	_, err := (Client{Resolver: staticResolver{}, HTTPClient: server.Client()}).Poll(context.Background(), domain.ProviderIssueAnalysisJob{
		Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "RainLib/open-review-platform",
		IssueNumber: 9, StableMarker: "open-review-platform:issue-triage:missing",
	})
	if err == nil || !strings.Contains(err.Error(), "comment is unavailable") {
		t.Fatalf("error=%v", err)
	}
}
