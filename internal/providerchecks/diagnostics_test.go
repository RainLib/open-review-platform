package providerchecks

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestCIFailureDiagnosticsAreExactCommitBoundedAndRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("credential missing")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"id":42,"name":"go tests","head_sha":%q,"status":"completed","conclusion":"failure","app":{"id":88},"output":{"annotations_count":1,"summary":"Test failed"}}]}`, testSHA)
		case strings.HasSuffix(r.URL.Path, "/annotations"):
			fmt.Fprint(w, `[{"path":"worker.go","start_line":3,"message":"--- FAIL: TestRetry expected: two retries. token=test-token password=hunter2"}]`)
		default:
			fmt.Fprintf(w, `{"sha":%q,"statuses":[]}`, testSHA)
		}
	}))
	defer server.Close()
	snapshot, err := (Client{Resolver: testResolver{"test-token"}, OwnGitHubAppID: 11, CollectFailureDiagnostics: true, AllowInsecureHTTP: true, AllowPrivateNetworks: true}).Fetch(context.Background(), domain.ReviewJob{Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "team/repo", HeadSHA: testSHA})
	if err != nil || len(snapshot.Checks) != 1 {
		t.Fatalf("%+v %v", snapshot, err)
	}
	check := snapshot.Checks[0]
	if check.FailureClass != "code" || !strings.Contains(check.Diagnostics, "worker.go") || strings.Contains(check.Diagnostics, "test-token") || strings.Contains(check.Diagnostics, "hunter2") {
		t.Fatalf("unsafe diagnostic: %+v", check)
	}
}

func TestCIFailureClassificationDoesNotRepairInfrastructureOrGuessMissingLogs(t *testing.T) {
	for _, tc := range []struct{ input, class string }{
		{"AssertionError: expected: 2, got 3", "code"},
		{"test failed because connection refused by runner", "infrastructure"},
		{"process exited 1", "unknown"},
		{strings.Repeat("a", 12001), "unknown"},
	} {
		_, class := classifyDiagnostics(tc.input, "fixture-token")
		if class != tc.class {
			t.Fatalf("got %s want %s", class, tc.class)
		}
	}
}

func TestGitLabTraceReadIsBoundedAndNeverFollowsRedirects(t *testing.T) {
	leaked := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(302)
	}))
	defer server.Close()
	_, err := (Client{}).getText(context.Background(), server.URL, "private-token", domain.ProviderGitLab)
	if err == nil || leaked {
		t.Fatal("trace redirect followed")
	}
}
