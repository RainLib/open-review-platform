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

const testSHA = "0123456789abcdef0123456789abcdef01234567"

type testResolver struct{ token string }

func (r testResolver) Resolve(context.Context, domain.ReviewJob) (string, error) { return r.token, nil }

func TestGitHubChecksBindBothSourcesToExactSHA(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("provider credential was not sent to the provider")
		}
		switch r.URL.Path {
		case "/repos/RainLib/open-review-platform/commits/" + testSHA + "/check-runs":
			if r.URL.Query().Get("per_page") != "100" {
				t.Error("check runs were not bounded")
			}
			_, _ = fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"name":"CI / go","head_sha":%q,"status":"completed","conclusion":"success","html_url":"https://github.com/RainLib/open-review-platform/actions/runs/1","app":{"id":22}}]}`, testSHA)
		case "/repos/RainLib/open-review-platform/commits/" + testSHA + "/status":
			_, _ = fmt.Fprintf(w, `{"sha":%q,"total_count":1,"statuses":[{"context":"security/scan","state":"pending","target_url":"https://ci.example.com/scan/1"}]}`, testSHA)
		default:
			t.Errorf("unexpected provider path: %s", r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	snapshot, err := (Client{Resolver: testResolver{token: "test-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true, OwnGitHubAppID: 11}).Fetch(context.Background(), domain.ReviewJob{
		Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "RainLib/open-review-platform", HeadSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.HeadSHA != testSHA || snapshot.Truncated || len(snapshot.Checks) != 2 || snapshot.Checks[0].State != "success" || snapshot.Checks[0].Origin != "independent" || snapshot.Checks[1].State != "pending" || snapshot.Checks[1].Origin != "independent" {
		t.Fatalf("unexpected exact-revision snapshot: %#v", snapshot)
	}
}

func TestGitHubChecksDoNotCountOwnAppAsIndependentCI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			_, _ = fmt.Fprintf(w, `{"total_count":3,"check_runs":[{"name":"Open Review / Analysis","head_sha":%q,"status":"completed","conclusion":"success","app":{"id":4975689}},{"name":"CI / go","head_sha":%q,"status":"completed","conclusion":"failure","app":{"id":88}},{"name":"unattributed","head_sha":%q,"status":"queued"}]}`, testSHA, testSHA, testSHA)
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = fmt.Fprintf(w, `{"sha":%q,"total_count":1,"statuses":[{"context":"Open Review / Analysis","state":"success"}]}`, testSHA)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	snapshot, err := (Client{Resolver: testResolver{token: "test-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true, OwnGitHubAppID: 4975689}).Fetch(context.Background(), domain.ReviewJob{
		Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "RainLib/open-review-platform", HeadSHA: testSHA,
	})
	if err != nil || len(snapshot.Checks) != 4 {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	for index, want := range []string{"open_review", "independent", "unclassified", "unclassified"} {
		if snapshot.Checks[index].Origin != want {
			t.Fatalf("check %d origin=%q, want %q", index, snapshot.Checks[index].Origin, want)
		}
	}
}

func TestGitLabPipelinesPreserveSelfManagedPrefixAndRejectWrongSHA(t *testing.T) {
	wrongSHA := strings.Repeat("f", 40)
	returnWrong := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("GitLab credential missing")
		}
		switch r.URL.EscapedPath() {
		case "/gitlab/api/v4/projects/team%2Fservice/pipelines":
			if r.URL.Query().Get("sha") != testSHA || r.URL.Query().Get("per_page") != "5" {
				t.Errorf("unbounded GitLab pipeline query: %s", r.URL.String())
			}
			sha := testSHA
			if returnWrong {
				sha = wrongSHA
			}
			_, _ = fmt.Fprintf(w, `[{"id":31,"sha":%q,"status":"running","web_url":"https://gitlab.example/gitlab/team/service/-/pipelines/31"}]`, sha)
		case "/gitlab/api/v4/projects/team%2Fservice/pipelines/31/jobs":
			if r.URL.Query().Get("per_page") != "20" {
				t.Errorf("unbounded GitLab jobs query: %s", r.URL.String())
			}
			_, _ = w.Write([]byte(`[{"id":501,"name":"Open Review / Analysis","status":"success","pipeline":{"id":31}}, {"id":500,"name":"go tests","status":"failed","web_url":"https://gitlab.example/gitlab/team/service/-/jobs/500","pipeline":{"id":31}}]`))
		default:
			t.Errorf("unexpected GitLab request: %s", r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := Client{Resolver: testResolver{token: "test-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}
	job := domain.ReviewJob{Provider: domain.ProviderGitLab, APIBaseURL: server.URL + "/gitlab/api/v4", Repository: "team/service", HeadSHA: testSHA}
	snapshot, err := client.Fetch(context.Background(), job)
	if err != nil || len(snapshot.Checks) != 3 || snapshot.Checks[0].State != "running" || snapshot.Checks[0].Origin != "unclassified" || snapshot.Checks[1].Origin != "unclassified" || snapshot.Checks[2].Origin != "independent" || snapshot.Checks[2].State != "failed" {
		t.Fatalf("GitLab pipeline snapshot=%#v err=%v", snapshot, err)
	}
	returnWrong = true
	if _, err := client.Fetch(context.Background(), job); err == nil {
		t.Fatal("a pipeline for a different commit was accepted")
	}
}

func TestGitLabJobsFailClosedOnWrongPipelineAndUnreadableJobs(t *testing.T) {
	jobStatus := http.StatusOK
	jobPipelineID := int64(32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			w.WriteHeader(jobStatus)
			if jobStatus == http.StatusOK {
				_, _ = fmt.Fprintf(w, `[{"id":500,"name":"test","status":"success","pipeline":{"id":%d}}]`, jobPipelineID)
			}
			return
		}
		_, _ = fmt.Fprintf(w, `[{"id":31,"sha":%q,"status":"success"}]`, testSHA)
	}))
	defer server.Close()
	client := Client{Resolver: testResolver{token: "test-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}
	job := domain.ReviewJob{Provider: domain.ProviderGitLab, APIBaseURL: server.URL + "/api/v4", Repository: "team/service", HeadSHA: testSHA}
	if _, err := client.Fetch(context.Background(), job); err == nil {
		t.Fatal("a job from a different pipeline was counted")
	}
	jobPipelineID = 31
	jobStatus = http.StatusForbidden
	if _, err := client.Fetch(context.Background(), job); err == nil {
		t.Fatal("an unreadable job list was treated as complete CI evidence")
	}
}

func TestGitLabJobObservationIsBoundedAndPartial(t *testing.T) {
	jobReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			jobReads++
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = fmt.Fprintf(w, `[{"id":1,"sha":%q},{"id":2,"sha":%q},{"id":3,"sha":%q},{"id":4,"sha":%q},{"id":5,"sha":%q}]`, testSHA, testSHA, testSHA, testSHA, testSHA)
	}))
	defer server.Close()
	snapshot, err := (Client{Resolver: testResolver{token: "test-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}).Fetch(context.Background(), domain.ReviewJob{
		Provider: domain.ProviderGitLab, APIBaseURL: server.URL + "/api/v4", Repository: "team/service", HeadSHA: testSHA,
	})
	if err != nil || !snapshot.Truncated || jobReads != maxGitLabPipelines || len(snapshot.Checks) != maxGitLabPipelines {
		t.Fatalf("bounded GitLab observation=%#v jobReads=%d err=%v", snapshot, jobReads, err)
	}
}

func TestProviderChecksFailClosedOnRedirectAndInvalidTargets(t *testing.T) {
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != ""
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()
	client := Client{Resolver: testResolver{token: "test-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}
	job := domain.ReviewJob{Provider: domain.ProviderGitHub, APIBaseURL: redirect.URL, Repository: "RainLib/open-review-platform", HeadSHA: testSHA}
	if _, err := client.Fetch(context.Background(), job); err == nil || leaked {
		t.Fatalf("redirect was accepted or credential leaked: err=%v leaked=%v", err, leaked)
	}
	for _, malformed := range []domain.ReviewJob{
		{Provider: domain.ProviderGitHub, APIBaseURL: "http://127.0.0.1/api/v3", Repository: "RainLib/open-review-platform", HeadSHA: testSHA},
		{Provider: domain.ProviderGitHub, APIBaseURL: "https://user:pass@github.example/api/v3", Repository: "RainLib/open-review-platform", HeadSHA: testSHA},
		{Provider: domain.ProviderGitHub, APIBaseURL: "https://github.example/api/v3?next=evil", Repository: "RainLib/open-review-platform", HeadSHA: testSHA},
		{Provider: domain.ProviderGitHub, APIBaseURL: "https://github.example/api/v3", Repository: "RainLib/open-review-platform", HeadSHA: "main"},
	} {
		if _, err := (Client{Resolver: testResolver{token: "test-token"}}).Fetch(context.Background(), malformed); err == nil {
			t.Fatalf("invalid provider check target was accepted: %#v", malformed)
		}
	}
}

func TestProviderChecksDoNotTreatMissingOrPartialCIAsPassed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/check-runs") {
			_, _ = w.Write([]byte(`{"total_count":101,"check_runs":[]}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"sha":%q,"total_count":0,"statuses":[]}`, testSHA)
	}))
	defer server.Close()
	snapshot, err := (Client{Resolver: testResolver{token: "test-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}).Fetch(context.Background(), domain.ReviewJob{
		Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "RainLib/open-review-platform", HeadSHA: testSHA,
	})
	if err != nil || !snapshot.Truncated || len(snapshot.Checks) != 0 {
		t.Fatalf("partial CI was made complete: snapshot=%#v err=%v", snapshot, err)
	}
}
