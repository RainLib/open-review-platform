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

func testReportJob() domain.ReviewJob {
	return domain.ReviewJob{ID: uuid.New(), Provider: domain.ProviderGitHub, ReviewNumber: 3, BaseSHA: "base", HeadSHA: "head"}
}

func TestFindingMarkerIsStableAndRendered(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}
	finding := domain.Finding{Path: "api.go", StartLine: 5, EndLine: 5, Category: "bug", Body: "nil value", Severity: "high"}
	marker := findingMarker(job, finding)
	if marker != findingMarker(job, finding) {
		t.Fatal("expected deterministic marker")
	}
	if !strings.Contains(FindingReport(job, finding, marker), marker) {
		t.Fatal("expected marker in rendered comment")
	}
	if !canInline(job, finding) {
		t.Fatal("expected finding to be inline eligible")
	}
}

func TestRenderSummaryGivesClearPassOrChangeVerdict(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}
	passFindings := []domain.Finding(nil)
	pass := CompletedReport(job, ReviewContext{}, ReviewResult{Findings: passFindings, Gate: EvaluateMergeGate(passFindings, "critical")}, "marker")
	if !strings.Contains(pass, "Review passed") || !strings.Contains(pass, "no actionable risks") {
		t.Fatalf("pass summary must state the verdict: %s", pass)
	}
	findings := []domain.Finding{{Path: "reader.go", StartLine: 12, EndLine: 12, Severity: "high", Category: "resource-leak", Body: "close the reader", Suggestion: "defer reader.Close()"}}
	changes := CompletedReport(job, ReviewContext{}, ReviewResult{Findings: findings, Gate: EvaluateMergeGate(findings, "high")}, "marker")
	for _, expected := range []string{"Merge blocked", "1 high", "reader.go:12", "close the reader", "Acceptance mapping", "Provenance"} {
		if !strings.Contains(changes, expected) {
			t.Fatalf("change summary missing %q: %s", expected, changes)
		}
	}
}

func TestPublishGitHubAlwaysPostsPRLevelVerdictForInlineFinding(t *testing.T) {
	var sawInline, sawSummary bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/pulls/4":
			_, _ = w.Write([]byte(`{"title":"Review evidence","html_url":"https://example.test/pr/4","changed_files":1,"additions":2,"deletions":1}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/pulls/4/files":
			_, _ = w.Write([]byte(`[{"filename":"reader.go","status":"modified","additions":2,"deletions":1,"changes":3}]`))
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
			if !strings.Contains(string(body), "Merge blocked") || !strings.Contains(string(body), "reader.go:12") {
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
	if err := p.publishGitHub(context.Background(), job, "token", ReviewResult{Findings: []domain.Finding{finding}, Gate: EvaluateMergeGate([]domain.Finding{finding}, "high")}); err != nil {
		t.Fatal(err)
	}
	if !sawInline || !sawSummary {
		t.Fatalf("expected inline and PR-level publication: inline=%v summary=%v", sawInline, sawSummary)
	}
}

func TestPublishStartedCreatesUpdatableScopeReport(t *testing.T) {
	var posted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/pulls/8":
			_, _ = w.Write([]byte(`{"title":"Add review evidence","html_url":"https://example.test/pr/8","changed_files":2,"additions":12,"deletions":4}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/pulls/8/files":
			_, _ = w.Write([]byte(`[{"filename":"api.go","blob_url":"https://github.com/RainLib/demo/blob/head/api.go","status":"modified","additions":8,"deletions":2},{"filename":"api_test.go","blob_url":"https://github.com/RainLib/demo/blob/head/api_test.go","status":"modified","additions":4,"deletions":2}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/issues/8/comments":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/issues/8/comments":
			body, _ := io.ReadAll(r.Body)
			for _, expected := range []string{"Code review started", "Changed files (2)", "[`api.go`](https://github.com/RainLib/demo/blob/head/api.go)", "Open Review / Analysis", "open-review-platform:summary:"} {
				if !strings.Contains(string(body), expected) {
					t.Fatalf("start report missing %q: %s", expected, body)
				}
			}
			posted = true
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	p := &HTTPPublisher{client: server.Client(), resolver: tokenResolver{}}
	job := domain.ReviewJob{ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL, InstallationExternalID: "42", CredentialRef: "github-app", Repository: "RainLib/demo", ReviewNumber: 8, BaseSHA: "1234567890abcdef", HeadSHA: "abcdef1234567890"}
	if err := p.PublishStarted(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Fatal("expected start report to be posted")
	}
}

func TestReportsDistinguishBlockedFailureAndSupersededStates(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), Provider: domain.ProviderGitHub, ReviewNumber: 3, BaseSHA: "base", HeadSHA: "head"}
	finding := domain.Finding{Path: "auth.go", StartLine: 7, EndLine: 7, Severity: "critical", Category: "security", Body: "authorization can be bypassed"}
	result := ReviewResult{Findings: []domain.Finding{finding}, Gate: EvaluateMergeGate([]domain.Finding{finding}, "high"), EngineVersion: "1.12.4", RuleSnapshotID: uuid.NewString(), RuleSnapshotSHA: "1234567890abcdef", CompilerVersion: "v1"}
	completed := CompletedReport(job, ReviewContext{TotalFiles: 2}, result, "marker")
	for _, expected := range []string{"Merge blocked", "Data classification", "Acceptance mapping", "not supplied to this run", "Rollout", "Rollback", "OpenCodeReview 1.12.4", "Rule snapshot"} {
		if !strings.Contains(completed, expected) {
			t.Fatalf("completed report missing %q: %s", expected, completed)
		}
	}
	if terminal := TerminalReport(job, LifecycleFailed, "marker"); !strings.Contains(terminal, "could not complete") || !strings.Contains(terminal, "retry") {
		t.Fatalf("unexpected failed report: %s", terminal)
	}
	if terminal := TerminalReport(job, LifecycleSuperseded, "marker"); !strings.Contains(terminal, "superseded") || !strings.Contains(terminal, "discarded") {
		t.Fatalf("unexpected superseded report: %s", terminal)
	}
	inline := FindingReport(job, finding, "finding-marker")
	for _, expected := range []string{"category-Security", "severity-critical", "Context for coding agent", "finding-marker"} {
		if !strings.Contains(inline, expected) {
			t.Fatalf("finding report missing %q: %s", expected, inline)
		}
	}
}

func TestCountUnifiedDiffExcludesFileHeaders(t *testing.T) {
	additions, deletions := countUnifiedDiff("--- a/demo.go\n+++ b/demo.go\n@@ -1 +1,2 @@\n-old\n+new\n+more")
	if additions != 2 || deletions != 1 {
		t.Fatalf("unexpected diff stats: +%d -%d", additions, deletions)
	}
}

func TestChangedFileLinksRejectUntrustedSchemes(t *testing.T) {
	job := testReportJob()
	report := StartedReport(job, ReviewContext{
		TotalFiles:   1,
		ChangedFiles: []ChangedFile{{Path: "safe.go", URL: "javascript:alert(1)", Status: "modified"}},
	}, "marker")
	if strings.Contains(report, "javascript:") || strings.Contains(report, "[`safe.go`](") {
		t.Fatalf("unsafe file link was rendered: %s", report)
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
