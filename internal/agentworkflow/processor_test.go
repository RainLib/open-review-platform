package agentworkflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type fixtureResolver struct{}

func (fixtureResolver) Resolve(context.Context, domain.ReviewJob) (string, error) {
	return "fixture-read-only", nil
}

type fixtureStore struct {
	target      store.AgentWorkflowTarget
	reviews     int
	head, state string
	event       domain.InboundEvent
}

func (s *fixtureStore) ClaimAgentWorkflow(context.Context, string) (*store.AgentWorkflowTarget, error) {
	return &s.target, nil
}
func (s *fixtureStore) EnsureAgentRereview(_ context.Context, _ store.AgentWorkflowTarget, e domain.InboundEvent) error {
	s.reviews++
	s.event = e
	return nil
}
func (s *fixtureStore) FinishAgentWorkflowObservation(_ context.Context, _ string, _ store.AgentWorkflowTarget, head, state string) error {
	s.head, s.state = head, state
	return nil
}

func TestDeliveryMonitorReadsCurrentProviderHeadAndNeverWritesProvider(t *testing.T) {
	for _, provider := range []domain.Provider{domain.ProviderGitHub, domain.ProviderGitLab} {
		for _, scenario := range []string{"current", "changed head", "wrong repository", "invalid sha", "redirect", "revoked"} {
			t.Run(string(provider)+"/"+scenario, func(t *testing.T) {
				sha := strings.Repeat("a", 40)
				observed := sha
				if scenario == "changed head" {
					observed = strings.Repeat("b", 40)
				}
				if scenario == "invalid sha" {
					observed = "abbreviated"
				}
				reads := 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reads++
					if r.Method != http.MethodGet {
						t.Fatal("delivery monitor wrote to provider")
					}
					if scenario == "redirect" {
						http.Redirect(w, r, "https://elsewhere.invalid/", http.StatusFound)
						return
					}
					if provider == domain.ProviderGitHub {
						if r.Header.Get("Authorization") != "Bearer fixture-read-only" || r.URL.Path != "/repos/team/repo/pulls/19" {
							t.Error("unexpected GitHub read")
						}
						repo := "team/repo"
						if scenario == "wrong repository" {
							repo = "foreign/repo"
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"number": 19, "state": "open", "draft": true, "head": map[string]any{"sha": observed, "ref": "agent/fixture", "repo": map[string]string{"full_name": repo}}, "base": map[string]any{"sha": strings.Repeat("c", 40), "ref": "main", "repo": map[string]string{"full_name": "team/repo"}}})
					} else {
						if r.Header.Get("PRIVATE-TOKEN") != "fixture-read-only" || !strings.HasSuffix(r.URL.Path, "/merge_requests/19") {
							t.Error("unexpected GitLab read")
						}
						source := 42
						if scenario == "wrong repository" {
							source = 99
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"iid": 19, "state": "opened", "draft": true, "sha": observed, "source_branch": "agent/fixture", "target_branch": "main", "source_project_id": source, "target_project_id": 42, "diff_refs": map[string]string{"head_sha": observed, "base_sha": strings.Repeat("c", 40)}})
					}
				}))
				defer server.Close()
				base := server.URL
				if provider == domain.ProviderGitLab {
					base += "/api/v4"
				}
				backend := &fixtureStore{target: store.AgentWorkflowTarget{Task: domain.AgentTask{ID: uuid.New(), ExecutionBranch: "agent/fixture", RequestedBy: "owner", Workflow: domain.AgentWorkflowPolicy{Enabled: true}}, Attempt: domain.AgentTaskAttempt{ID: uuid.New(), PullRequestNumber: 19}, Acceptance: domain.AgentTaskAcceptance{HeadSHA: sha}, Job: domain.ReviewJob{Provider: provider, APIBaseURL: base, Repository: "team/repo", ReviewNumber: 19, BaseRef: "main", InstallationExternalID: "fixture-installation", CredentialRef: "fixture"}}}
				if scenario == "revoked" {
					backend.target.Job.CredentialRef = ""
				}
				processor := Processor{Store: backend, Resolver: fixtureResolver{}, HTTPClient: server.Client(), WorkerID: "fixture"}
				if worked, err := processor.RunOnce(context.Background()); !worked || err != nil {
					t.Fatalf("monitor: %v %v", worked, err)
				}
				switch scenario {
				case "current":
					if backend.reviews != 1 || backend.head != sha || backend.event.TriggerKind != "manual" || backend.event.HeadRef != "agent/fixture" || backend.event.APIBaseURL != base || !backend.event.IsDraft {
						t.Fatalf("wrong exact-commit review: %+v", backend)
					}
				case "changed head":
					if backend.reviews != 0 || backend.head != observed {
						t.Fatal("changed head reused prior acceptance")
					}
				default:
					if backend.reviews != 0 || backend.state != "unavailable" {
						t.Fatalf("untrusted provider allowed: %+v", backend)
					}
				}
				if scenario == "revoked" && reads != 0 {
					t.Fatal("revoked credential was resolved")
				}
			})
		}
	}
}
