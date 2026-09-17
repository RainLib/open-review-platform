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

func TestRenderSummaryGivesClearPassOrChangeVerdict(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}
	pass := renderSummary(job, nil, "marker")
	if !strings.Contains(pass, "AI review passed") || !strings.Contains(pass, "No actionable risks") {
		t.Fatalf("pass summary must state the verdict: %s", pass)
	}
	findings := []domain.Finding{{Path: "reader.go", StartLine: 12, EndLine: 12, Severity: "high", Category: "resource-leak", Body: "close the reader", Suggestion: "defer reader.Close()"}}
	changes := renderSummary(job, findings, "marker")
	for _, expected := range []string{"Changes recommended", "1 high", "reader.go:12", "close the reader", "inline suggestion"} {
		if !strings.Contains(changes, expected) {
			t.Fatalf("change summary missing %q: %s", expected, changes)
		}
	}
}

func TestPublishGitHubAlwaysPostsPRLevelVerdictForInlineFinding(t *testing.T) {
	var sawInline, sawSummary bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/pulls/4/comments":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/pulls/4/reviews":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"comments"`) || !strings.Contains(string(body), "reader.go") {
				t.Fatalf("expected inline review: %s", body)
			}
			sawInline = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/issues/4/comments":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/issues/4/comments":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "Changes recommended") || !strings.Contains(string(body), "reader.go:12") {
				t.Fatalf("expected PR-level verdict: %s", body)
			}
			sawSummary = true
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	job := domain.ReviewJob{ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL, InstallationExternalID: "42", CredentialRef: "github-app", Repository: "RainLib/demo", ReviewNumber: 4, HeadSHA: "head"}
	finding := domain.Finding{Path: "reader.go", StartLine: 12, EndLine: 12, Severity: "high", Category: "resource-leak", Body: "close the reader"}
	p := &HTTPPublisher{client: server.Client(), resolver: tokenResolver{}}
	if err := p.publishGitHub(context.Background(), job, "token", []domain.Finding{finding}); err != nil {
		t.Fatal(err)
	}
	if !sawInline || !sawSummary {
		t.Fatalf("expected inline and PR-level publication: inline=%v summary=%v", sawInline, sawSummary)
	}
}

func TestPublishInteractionResponseUpdatesExistingMarker(t *testing.T) {
	marker := "open-review-platform:interaction:test"
	var sawPatch, sawReaction bool
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
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/issues/comments/99/reactions":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"content":"eyes"`) {
				t.Fatalf("unexpected interaction reaction: %s", body)
			}
			sawReaction = true
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	publisher := &HTTPPublisher{client: server.Client(), resolver: tokenResolver{}}
	err := publisher.PublishInteractionResponse(context.Background(), domain.InteractionResponse{
		Provider: domain.ProviderGitHub, APIBaseURL: server.URL, InstallationExternalID: "42", CredentialRef: "github-app",
		Repository: "RainLib/demo", ReviewNumber: 4, CommentExternalID: "99", Reaction: domain.InteractionReactionEyes, Body: "Review is queued.", Marker: marker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawPatch || !sawReaction {
		t.Fatalf("expected interaction comment and reaction: patch=%v reaction=%v", sawPatch, sawReaction)
	}
}

func TestPublishInteractionResponseRejectsInvalidGitHubReactionComment(t *testing.T) {
	publisher := &HTTPPublisher{client: http.DefaultClient, resolver: tokenResolver{}}
	err := publisher.PublishInteractionResponse(context.Background(), domain.InteractionResponse{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.invalid", InstallationExternalID: "42", CredentialRef: "github-app",
		Repository: "RainLib/demo", ReviewNumber: 4, CommentExternalID: "not-a-number", Reaction: domain.InteractionReactionEyes, Body: "Review is queued.", Marker: "marker",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid GitHub interaction comment id") {
		t.Fatalf("expected invalid comment id error, got %v", err)
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
