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
