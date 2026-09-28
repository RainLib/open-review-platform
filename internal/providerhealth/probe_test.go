package providerhealth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type staticResolver struct {
	token string
}

func (r staticResolver) Resolve(context.Context, domain.ReviewJob) (string, error) {
	return r.token, nil
}

type githubAppAwareResolver struct {
	staticResolver
	configuration credentials.GitHubAppConfiguration
}

type recordingProbeStore struct {
	target      domain.ProviderProbeTarget
	claimedAt   time.Time
	lease       time.Duration
	completedAt time.Time
	result      domain.ProviderProbeResult
}

func (s *recordingProbeStore) ClaimProviderHealthProbe(_ context.Context, workerID string, lease time.Duration, _ *uuid.UUID) (*domain.ProviderProbeTarget, error) {
	s.claimedAt = time.Now()
	s.lease = lease
	s.target.WorkerID = workerID
	return &s.target, nil
}

func (s *recordingProbeStore) CompleteProviderHealthProbe(_ context.Context, _ domain.ProviderProbeTarget, result domain.ProviderProbeResult, _ time.Time) error {
	s.completedAt = time.Now()
	s.result = result
	return nil
}

type deadlineRecordingTransport struct {
	deadline time.Time
}

func (transport *deadlineRecordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.deadline, _ = request.Context().Deadline()
	return nil, fmt.Errorf("synthetic provider timeout")
}

type deadlineRecordingResolver struct {
	deadline time.Time
}

func (resolver *deadlineRecordingResolver) Resolve(ctx context.Context, _ domain.ReviewJob) (string, error) {
	resolver.deadline, _ = ctx.Deadline()
	return "provider-token", nil
}

func TestProcessorReservesClaimTimeForProbeCompletion(t *testing.T) {
	transport := &deadlineRecordingTransport{}
	resolver := &deadlineRecordingResolver{}
	probeStore := &recordingProbeStore{target: domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", RepositoryScope: domain.AllAuthorizedRepositoriesScope,
	}}}
	processor := Processor{
		Store: probeStore, WorkerID: "probe-test", Lease: 40 * time.Second,
		Client: Client{Resolver: resolver, HTTPClient: &http.Client{Transport: transport}},
	}
	claimed, err := processor.RunOnce(context.Background())
	if err != nil || !claimed || probeStore.completedAt.IsZero() || probeStore.result.HealthState != domain.HealthDegraded {
		t.Fatalf("probe result was not persisted after transport failure: claimed=%v err=%v result=%#v", claimed, err, probeStore.result)
	}
	if resolver.deadline.IsZero() || resolver.deadline.Before(probeStore.claimedAt.Add(34*time.Second)) || resolver.deadline.After(probeStore.claimedAt.Add(36*time.Second)) {
		t.Fatalf("probe deadline did not reserve five seconds from the lease: claim=%v deadline=%v lease=%v", probeStore.claimedAt, resolver.deadline, probeStore.lease)
	}
}

func TestInjectedProbeClientCannotFollowCredentialRedirect(t *testing.T) {
	var forwarded bool
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		forwarded = r.Header.Get("Authorization") != ""
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()
	result := (Client{
		Resolver: staticResolver{token: "provider-token"}, HTTPClient: &http.Client{},
		AllowPrivateNetworks: true, AllowInsecureHTTP: true,
	}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: redirect.URL,
	}})
	if forwarded || result.HealthState == domain.HealthLive {
		t.Fatalf("provider redirect was followed: forwarded=%v result=%#v", forwarded, result)
	}
}

func (r githubAppAwareResolver) GitHubAppConfiguration(context.Context) (credentials.GitHubAppConfiguration, error) {
	return r.configuration, nil
}

func TestClientProbesGitHubAccessAndRateLimit(t *testing.T) {
	reset := time.Now().Add(time.Hour).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/installation/repositories" || r.URL.Query().Get("per_page") != strconv.Itoa(probeRepositoryPageSize) {
			t.Fatalf("unexpected probe URL: %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer provider-token" {
			t.Fatalf("unexpected authorization header")
		}
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"repositories":[{"id":42,"full_name":"RainLib/open-review-platform","default_branch":"main","visibility":"private"}]}`))
	}))
	defer server.Close()
	result := (Client{
		Resolver:             staticResolver{token: "provider-token"},
		AllowPrivateNetworks: true, AllowInsecureHTTP: true,
	}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL,
	}})
	if result.HealthState != domain.HealthLive || len(result.Permissions) != 1 || result.Permissions[0] != "repository_inventory:read" || result.RateLimitRemaining == nil || *result.RateLimitRemaining != 4999 || result.RateLimitLimit == nil || *result.RateLimitLimit != 5000 || len(result.Repositories) != 1 || result.Repositories[0].Name != "RainLib/open-review-platform" || result.Repositories[0].ExternalID != "42" {
		t.Fatalf("unexpected probe result: %#v", result)
	}
}

func inventoryFixture(first, last int, github bool) string {
	items := make([]string, 0, last-first)
	for id := first; id < last; id++ {
		if github {
			items = append(items, fmt.Sprintf(`{"id":%d,"full_name":"team/repo-%d"}`, id, id))
		} else {
			items = append(items, fmt.Sprintf(`{"id":%d,"path_with_namespace":"team/repo-%d"}`, id, id))
		}
	}
	result := "[" + strings.Join(items, ",") + "]"
	if github {
		return `{"repositories":` + result + `}`
	}
	return result
}

func TestClientSynchronizesMoreThanOneInventoryPage(t *testing.T) {
	for _, provider := range []domain.Provider{domain.ProviderGitHub, domain.ProviderGitLab} {
		t.Run(string(provider), func(t *testing.T) {
			pages := make([]string, 0)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer provider-token" {
					t.Fatal("provider request lost the deployment credential")
				}
				if provider == domain.ProviderGitLab && r.URL.Path == "/user" {
					_, _ = w.Write([]byte(`{"id":7}`))
					return
				}
				if provider == domain.ProviderGitLab && r.URL.Path == "/groups/team/projects" {
					_, _ = w.Write([]byte(inventoryFixture(1, 2, false)))
					return
				}
				wantPath := "/installation/repositories"
				if provider == domain.ProviderGitLab {
					wantPath = "/projects"
				}
				if r.URL.Path != wantPath || r.URL.Query().Get("per_page") != strconv.Itoa(probeRepositoryPageSize) {
					t.Fatalf("unexpected inventory endpoint: %s", r.URL.String())
				}
				page := r.URL.Query().Get("page")
				if page == "" {
					page = "1"
				}
				pages = append(pages, page)
				first := 1
				last := probeRepositoryPageSize + 1
				if page == "2" {
					first, last = probeRepositoryPageSize+1, probeRepositoryPageSize+3
				}
				_, _ = w.Write([]byte(inventoryFixture(first, last, provider == domain.ProviderGitHub)))
			}))
			defer server.Close()
			scope := domain.AllAuthorizedRepositoriesScope
			if provider == domain.ProviderGitLab {
				scope = "team/*"
			}
			result := (Client{Resolver: staticResolver{token: "provider-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
				ID: uuid.New(), Provider: provider, APIBaseURL: server.URL, RepositoryScope: scope,
			}})
			if result.HealthState != domain.HealthLive || result.Receipt["inventory_state"] != "synchronized" || len(result.Repositories) != probeRepositoryPageSize+2 || result.Repositories[len(result.Repositories)-1].Name != fmt.Sprintf("team/repo-%d", probeRepositoryPageSize+2) || strings.Join(pages, ",") != "1,2" {
				t.Fatalf("two-page inventory result=%#v pages=%v", result, pages)
			}
		})
	}
}

func TestClientAcceptsBoundedLargeGitHubInventoryPage(t *testing.T) {
	items := make([]string, 0, probeRepositoryPageSize)
	for id := 1; id <= probeRepositoryPageSize; id++ {
		items = append(items, fmt.Sprintf(`{"id":%d,"full_name":"team/repo-%d","description":"%s"}`, id, id, strings.Repeat("x", 700)))
	}
	firstPage := `{"repositories":[` + strings.Join(items, ",") + `]}`
	if len(firstPage) <= maxProbeResponseBytes || len(firstPage) >= maxInventoryResponseBytes {
		t.Fatalf("fixture must exercise only the inventory response limit: %d bytes", len(firstPage))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"repositories":[]}`))
			return
		}
		_, _ = w.Write([]byte(firstPage))
	}))
	defer server.Close()
	result := (Client{Resolver: staticResolver{token: "provider-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL, RepositoryScope: domain.AllAuthorizedRepositoriesScope,
	}})
	if result.HealthState != domain.HealthLive || result.Receipt["inventory_state"] != "synchronized" || len(result.Repositories) != probeRepositoryPageSize {
		t.Fatalf("large inventory page was not synchronized: %#v", result)
	}
}

func TestClientRejectsInventoryResponseAboveBound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxInventoryResponseBytes+1)))
	}))
	defer server.Close()
	result := (Client{Resolver: staticResolver{token: "provider-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL,
	}})
	if result.HealthState != domain.HealthDegraded || result.ErrorCode != "provider_response_too_large" {
		t.Fatalf("oversized inventory response was accepted: %#v", result)
	}
}

func TestClientRetainsFirstInventoryPageWhenNextPageFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(inventoryFixture(1, probeRepositoryPageSize+1, true)))
	}))
	defer server.Close()
	result := (Client{Resolver: staticResolver{token: "provider-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL, RepositoryScope: domain.AllAuthorizedRepositoriesScope,
	}})
	if result.HealthState != domain.HealthLive || result.Receipt["inventory_state"] != "partial" || result.Receipt["inventory_error"] == nil || len(result.Repositories) != probeRepositoryPageSize {
		t.Fatalf("partial inventory lost verified first page or was claimed complete: %#v", result)
	}
}

func TestClientCapsInventoryWithoutClaimingCompletion(t *testing.T) {
	pageCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if value := r.URL.Query().Get("page"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				t.Fatal(err)
			}
			page = parsed
		}
		pageCount++
		if page > maxInventoryPages {
			t.Fatalf("unbounded inventory request: page %d", page)
		}
		_, _ = w.Write([]byte(inventoryFixture((page-1)*probeRepositoryPageSize+1, page*probeRepositoryPageSize+1, true)))
	}))
	defer server.Close()
	result := (Client{Resolver: staticResolver{token: "provider-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL, RepositoryScope: domain.AllAuthorizedRepositoriesScope,
	}})
	if pageCount != maxInventoryPages || result.HealthState != domain.HealthLive || result.Receipt["inventory_state"] != "partial" || len(result.Repositories) != maxInventoryPages*probeRepositoryPageSize {
		t.Fatalf("bounded inventory result=%#v requests=%d", result, pageCount)
	}
}

func TestClientObservesGitHubIssueTriageRegistration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/installation/repositories" {
			t.Fatalf("unexpected probe URL: %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"repositories":[]}`))
	}))
	defer server.Close()
	for _, test := range []struct {
		name         string
		events       []string
		permission   string
		contents     string
		pullRequests string
		want         string
		feedback     string
		wantAgent    string
	}{
		{name: "ready", events: []string{"issues", "pull_request"}, permission: "write", contents: "write", pullRequests: "write", want: "ready", feedback: "polling_required", wantAgent: "app_permissions_declared"},
		{name: "missing event", events: []string{"pull_request"}, permission: "write", contents: "write", pullRequests: "write", want: "missing_event", feedback: "polling_required", wantAgent: "app_permissions_declared"},
		{name: "missing Issue write permission", events: []string{"issues"}, permission: "read", contents: "write", pullRequests: "write", want: "missing_write_permission", feedback: "polling_required", wantAgent: "app_permissions_declared"},
		{name: "missing Contents write", events: []string{"issues"}, permission: "write", contents: "read", pullRequests: "write", want: "ready", feedback: "polling_required", wantAgent: "missing_contents_write"},
		{name: "missing Pull requests write", events: []string{"issues"}, permission: "write", contents: "write", pullRequests: "read", want: "ready", feedback: "polling_required", wantAgent: "missing_pull_requests_write"},
		{name: "missing Issues read", events: []string{"issues"}, permission: "none", contents: "write", pullRequests: "write", want: "missing_write_permission", feedback: "polling_required", wantAgent: "missing_issues_read"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := (Client{
				Resolver: githubAppAwareResolver{
					staticResolver: staticResolver{token: "provider-token"},
					configuration: credentials.GitHubAppConfiguration{
						Events: test.events, Permissions: map[string]string{"issues": test.permission, "contents": test.contents, "pull_requests": test.pullRequests},
					},
				},
				AllowPrivateNetworks: true, AllowInsecureHTTP: true,
			}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
				ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL,
				CredentialRef: "github-app", RepositoryScope: domain.AllAuthorizedRepositoriesScope,
			}})
			if result.HealthState != domain.HealthLive || result.Receipt["issue_triage_state"] != test.want || result.Receipt["issue_feedback_state"] != test.feedback || result.Receipt["agent_coding_app_permission_state"] != test.wantAgent ||
				!containsString(result.Permissions, "issue_triage:"+test.want) || !containsString(result.Permissions, "issue_feedback:"+test.feedback) || !containsString(result.Permissions, "agent_coding:"+test.wantAgent) {
				t.Fatalf("unexpected issue triage capability: %#v", result)
			}
		})
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestClientVerifiesDeclaredGitHubRepositoryOutsideInventoryPage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-token" {
			t.Fatalf("unexpected authorization header")
		}
		switch r.URL.Path {
		case "/installation/repositories":
			requests++
			_, _ = w.Write([]byte(`{"repositories":[{"id":1,"full_name":"RainLib/another-repository"}]}`))
		case "/repos/RainLib/open-review-platform":
			requests++
			_, _ = w.Write([]byte(`{"id":42,"full_name":"RainLib/open-review-platform","default_branch":"main","private":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	result := (Client{
		Resolver:             staticResolver{token: "provider-token"},
		AllowPrivateNetworks: true, AllowInsecureHTTP: true,
	}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL,
		RepositoryScope: "RainLib/open-review-platform",
	}})
	if requests != 2 || result.HealthState != domain.HealthLive || result.Receipt["scope_verification"] != "verified" || len(result.Repositories) != 2 || result.Repositories[1].Name != "RainLib/open-review-platform" {
		t.Fatalf("unexpected scope verification result: %#v", result)
	}
}

func TestClientRejectsGitHubRepositoryScopeWithoutInstallationAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/installation/repositories":
			_, _ = w.Write([]byte(`{"repositories":[]}`))
		case "/repos/RainLib/open-review-platform":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	result := (Client{
		Resolver:             staticResolver{token: "provider-token"},
		AllowPrivateNetworks: true, AllowInsecureHTTP: true,
	}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL,
		RepositoryScope: "RainLib/open-review-platform",
	}})
	if result.HealthState != domain.HealthCritical || result.ErrorCode != "repository_scope_not_authorized" || result.Receipt["scope_verification"] != "not_authorized" {
		t.Fatalf("unexpected scope rejection result: %#v", result)
	}
}

func TestClientSynchronizesGitLabRepositoryInventoryAfterIdentityProbe(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-token" {
			t.Fatalf("unexpected authorization header")
		}
		switch r.URL.Path {
		case "/user":
			requests++
			_, _ = w.Write([]byte(`{"id":7}`))
		case "/projects":
			requests++
			if r.URL.Query().Get("membership") != "true" || r.URL.Query().Get("per_page") != strconv.Itoa(probeRepositoryPageSize) {
				t.Fatalf("unexpected GitLab inventory URL: %s", r.URL.String())
			}
			_, _ = w.Write([]byte(`[{"id":99,"path_with_namespace":"RainLib/platform-api","default_branch":"main","visibility":"internal"}]`))
		case "/projects/RainLib/platform-api":
			requests++
			if r.URL.EscapedPath() != "/projects/RainLib%2Fplatform-api" {
				t.Fatalf("GitLab project path is not URL-encoded: %s", r.URL.EscapedPath())
			}
			_, _ = w.Write([]byte(`{"id":99,"path_with_namespace":"RainLib/platform-api","default_branch":"main","visibility":"internal"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	result := (Client{
		Resolver:             staticResolver{token: "provider-token"},
		AllowPrivateNetworks: true, AllowInsecureHTTP: true,
	}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitLab, APIBaseURL: server.URL,
		RepositoryScope: "RainLib/platform-api",
	}})
	if requests != 3 || result.HealthState != domain.HealthLive || len(result.Repositories) != 1 || result.Repositories[0].Name != "RainLib/platform-api" || result.Receipt["inventory_state"] != "synchronized" || result.Receipt["scope_verification"] != "verified" {
		t.Fatalf("unexpected GitLab probe result: %#v", result)
	}
}

func TestClientVerifiesGitLabGroupBeyondGeneralInventoryPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"id":7}`))
		case "/projects":
			_, _ = w.Write([]byte(`[{"id":1,"path_with_namespace":"other/first-page"}]`))
		case "/groups/org/team/projects":
			if r.URL.EscapedPath() != "/groups/org%2Fteam/projects" || r.URL.Query().Get("include_subgroups") != "true" || r.URL.Query().Get("with_shared") != "false" {
				t.Fatalf("unexpected GitLab group scope URL: %s", r.URL.String())
			}
			_, _ = w.Write([]byte(`[{"id":42,"path_with_namespace":"org/team/sub/service","default_branch":"main"}]`))
		default:
			t.Fatalf("unexpected GitLab path: %s", r.URL.String())
		}
	}))
	defer server.Close()
	result := (Client{Resolver: staticResolver{token: "provider-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitLab, APIBaseURL: server.URL, RepositoryScope: "org/team/*",
	}})
	if result.HealthState != domain.HealthLive || result.Receipt["scope_verification"] != "verified" || len(result.Repositories) != 2 || result.Repositories[1].Name != "org/team/sub/service" {
		t.Fatalf("GitLab group scope was not verified from the current provider response: %#v", result)
	}
}

func TestClientRejectsUnrelatedGitLabScopeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"id":7}`))
		case "/projects":
			_, _ = w.Write([]byte(`[]`))
		case "/projects/org/team/service":
			_, _ = w.Write([]byte(`{"id":42,"path_with_namespace":"other/service"}`))
		default:
			t.Fatalf("unexpected GitLab path: %s", r.URL.String())
		}
	}))
	defer server.Close()
	result := (Client{Resolver: staticResolver{token: "provider-token"}, AllowPrivateNetworks: true, AllowInsecureHTTP: true}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitLab, APIBaseURL: server.URL, RepositoryScope: "org/team/service",
	}})
	if result.HealthState != domain.HealthCritical || result.ErrorCode != "repository_scope_invalid" || result.Receipt["scope_verification"] != "invalid" {
		t.Fatalf("unrelated GitLab project was accepted: %#v", result)
	}
}

func TestClientKeepsAuthenticationFailuresCritical(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	result := (Client{
		Resolver:             staticResolver{token: "bad-token"},
		AllowPrivateNetworks: true, AllowInsecureHTTP: true,
	}).Probe(context.Background(), domain.ProviderProbeTarget{Installation: domain.Installation{
		ID: uuid.New(), Provider: domain.ProviderGitLab, APIBaseURL: server.URL,
	}})
	if result.HealthState != domain.HealthCritical || result.ErrorCode != "provider_auth_or_permission_failed" {
		t.Fatalf("unexpected probe result: %#v", result)
	}
}
