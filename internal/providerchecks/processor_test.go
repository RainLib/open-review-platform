package providerchecks

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

func TestCommitStatusPermissionFailureCannotBecomePartialCISuccess(t *testing.T) {
	for _, forbiddenEndpoint := range []string{"/status", "/check-runs"} {
		t.Run(forbiddenEndpoint, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("read must use the installed provider identity")
				}
				if strings.HasSuffix(r.URL.Path, forbiddenEndpoint) {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"message":"synthetic-secret-provider-body"}`))
					return
				}
				_, _ = fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"name":"CI / tests","head_sha":%q,"status":"completed","conclusion":"success","app":{"id":22}}]}`, testSHA)
			}))
			defer server.Close()
			st := &probeStoreStub{target: &domain.ProviderCheckTarget{RunID: uuid.New(), Attempt: 1, Job: domain.ReviewJob{Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "RainLib/open-review-platform", HeadSHA: testSHA}}}
			worked, err := (Processor{Store: st, WorkerID: "worker", Client: Client{Resolver: testResolver{token: "test-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true, OwnGitHubAppID: 11}}).RunOnce(context.Background())
			want := "provider_read_failed"
			if forbiddenEndpoint == "/status" {
				want = "github_commit_status_read_forbidden"
			}
			if !worked || err == nil || st.completed != nil || st.failedCode != want || strings.Contains(err.Error(), "synthetic-secret") || !st.nextAt.After(time.Now()) {
				t.Fatalf("partial CI success or unsafe permission diagnostic: worked=%v error=%v completed=%v code=%s", worked, err, st.completed, st.failedCode)
			}
		})
	}
}

type probeStoreStub struct {
	target     *domain.ProviderCheckTarget
	completed  *domain.ProviderCheckObservation
	failedCode string
	nextAt     time.Time
}

func (s *probeStoreStub) ClaimProviderCheckProbe(context.Context, string, time.Duration) (*domain.ProviderCheckTarget, error) {
	if s.target == nil {
		return nil, store.ErrNoProviderCheckProbe
	}
	return s.target, nil
}

func (s *probeStoreStub) CompleteProviderCheckProbe(_ context.Context, _ domain.ProviderCheckTarget, observation domain.ProviderCheckObservation, nextAt time.Time) error {
	s.completed = &observation
	s.nextAt = nextAt
	return nil
}

func (s *probeStoreStub) FailProviderCheckProbe(_ context.Context, _ domain.ProviderCheckTarget, code string, nextAt time.Time) error {
	s.failedCode = code
	s.nextAt = nextAt
	return nil
}

func TestProcessorPersistsExactHeadWithoutClaimingCIPass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/RainLib/open-review-platform/commits/"+testSHA+"/check-runs" {
			_, _ = w.Write([]byte(`{"total_count":0,"check_runs":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"sha":"` + testSHA + `","total_count":0,"statuses":[]}`))
	}))
	defer server.Close()
	st := &probeStoreStub{target: &domain.ProviderCheckTarget{
		RunID: uuid.New(), WorkerID: "worker", Attempt: 1,
		Job: domain.ReviewJob{Provider: domain.ProviderGitHub, APIBaseURL: server.URL,
			Repository: "RainLib/open-review-platform", HeadSHA: testSHA},
	}}
	worked, err := (Processor{Store: st, WorkerID: "worker", Client: Client{
		Resolver: testResolver{token: "test-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true,
	}}).RunOnce(context.Background())
	if err != nil || !worked || st.completed == nil || st.failedCode != "" {
		t.Fatalf("worked=%v err=%v completion=%#v failed=%q", worked, err, st.completed, st.failedCode)
	}
	if st.completed.HeadSHA != testSHA || st.completed.State != "observed" || st.completed.ObservedAt == nil || len(st.completed.Checks) != 0 {
		t.Fatalf("empty provider check set must remain observed but not pass: %#v", st.completed)
	}
}

func TestProcessorRecordsBoundedFailureCode(t *testing.T) {
	st := &probeStoreStub{target: &domain.ProviderCheckTarget{
		RunID: uuid.New(), WorkerID: "worker", Attempt: 1,
		Job: domain.ReviewJob{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com",
			Repository: "RainLib/open-review-platform", HeadSHA: "not-a-commit"},
	}}
	worked, err := (Processor{Store: st, WorkerID: "worker", Client: Client{Resolver: testResolver{token: "secret"}}}).RunOnce(context.Background())
	if !worked || err == nil || st.failedCode != "provider_read_failed" || st.completed != nil {
		t.Fatalf("worked=%v err=%v failed=%q completion=%#v", worked, err, st.failedCode, st.completed)
	}
}
