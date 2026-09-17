package publisher

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type tokenResolver struct{}

func (tokenResolver) Resolve(context.Context, domain.ReviewJob) (string, error) { return "token", nil }

func TestFindingMarkerIsStableAndRendered(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}
	finding := domain.Finding{Path: "api.go", StartLine: 5, EndLine: 5, Category: "bug", Body: "nil value", Severity: "high"}
	marker := findingMarker(job, finding)
	if marker != findingMarker(job, finding) {
		t.Fatal("expected deterministic marker")
	}
	if !strings.Contains(renderFinding(finding, marker), marker) {
		t.Fatal("expected marker in rendered comment")
	}
	if !canInline(job, finding) {
		t.Fatal("expected finding to be inline eligible")
	}
}

func TestPublishInteractionResponseUpdatesExistingMarker(t *testing.T) {
	marker := "open-review-platform:interaction:test"
	var sawPatch bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/issues/4/comments":
			_, _ = w.Write([]byte(`[{"id":7,"body":"<!-- open-review-platform:interaction:test -->"}]`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/RainLib/demo/issues/comments/7":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "queued") || !strings.Contains(string(body), marker) {
				t.Fatalf("unexpected interaction body: %s", body)
			}
			sawPatch = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	publisher := &HTTPPublisher{client: server.Client(), resolver: tokenResolver{}}
	err := publisher.PublishInteractionResponse(context.Background(), domain.InteractionResponse{
		Provider: domain.ProviderGitHub, APIBaseURL: server.URL, InstallationExternalID: "42", CredentialRef: "github-app",
		Repository: "RainLib/demo", ReviewNumber: 4, Body: "Review is queued.", Marker: marker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawPatch {
		t.Fatal("expected existing interaction comment to be updated")
	}
}

func TestPublishGitLabInteractionResponseCreatesNote(t *testing.T) {
	marker := "open-review-platform:interaction:gitlab"
	var sawPost bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/projects/acme/demo/merge_requests/4/notes":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/projects/acme/demo/merge_requests/4/notes":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "queued") || !strings.Contains(string(body), marker) {
				t.Fatalf("unexpected interaction body: %s", body)
			}
			sawPost = true
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	publisher := &HTTPPublisher{client: server.Client(), resolver: tokenResolver{}}
	err := publisher.PublishInteractionResponse(context.Background(), domain.InteractionResponse{
		Provider: domain.ProviderGitLab, APIBaseURL: server.URL, InstallationExternalID: "42", CredentialRef: "gitlab-token",
		Repository: "acme/demo", ReviewNumber: 4, Body: "Review is queued.", Marker: marker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawPost {
		t.Fatal("expected interaction note to be created")
	}
}

func TestGitHubAnalysisCheckCreatesAndFinalizesStableCheck(t *testing.T) {
	var created, completed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/commits/head/check-runs":
			if !created {
				_, _ = w.Write([]byte(`{"check_runs":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"check_runs":[{"id":77,"status":"in_progress"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/check-runs":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), AnalysisCheckName) || !strings.Contains(string(body), "in_progress") {
				t.Fatalf("unexpected check create: %s", body)
			}
			created = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/RainLib/demo/check-runs/77":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"conclusion":"success"`) || !strings.Contains(string(body), `"status":"completed"`) {
				t.Fatalf("unexpected check completion: %s", body)
			}
			completed = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	p := &HTTPPublisher{client: server.Client(), resolver: tokenResolver{}}
	job := domain.ReviewJob{Provider: domain.ProviderGitHub, APIBaseURL: server.URL, InstallationExternalID: "42", CredentialRef: "github-app", Repository: "RainLib/demo", HeadSHA: "head"}
	if err := p.StartCheck(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := p.CompleteCheck(context.Background(), job, CheckSuccess, "complete"); err != nil {
		t.Fatal(err)
	}
	if !created || !completed {
		t.Fatalf("check lifecycle incomplete: created=%v completed=%v", created, completed)
	}
}

func TestGitHubAnalysisCheckReusesOnlyInProgressCheck(t *testing.T) {
	var patched bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"check_runs":[{"id":91,"status":"in_progress"}]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/RainLib/demo/check-runs/91":
			patched = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	p := &HTTPPublisher{client: server.Client(), resolver: tokenResolver{}}
	job := domain.ReviewJob{Provider: domain.ProviderGitHub, APIBaseURL: server.URL, InstallationExternalID: "42", CredentialRef: "github-app", Repository: "RainLib/demo", HeadSHA: "head"}
	if err := p.StartCheck(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if !patched {
		t.Fatal("expected in-progress check to be reused")
	}
}
