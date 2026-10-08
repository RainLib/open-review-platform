package agentworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type recoveryFixtureStore struct {
	fixtureStore
	reason, url string
	finished    bool
}

func (s *recoveryFixtureStore) ClaimAgentPublicationRecovery(context.Context, string) (*store.AgentPublicationRecoveryTarget, error) {
	return &store.AgentPublicationRecoveryTarget{Workflow: s.target, Checkpoint: domain.AgentTaskPublicationCheckpoint{HeadSHA: s.target.Acceptance.HeadSHA}}, nil
}
func (s *recoveryFixtureStore) FinishAgentPublicationRecovery(_ context.Context, _ string, _ store.AgentPublicationRecoveryTarget, e domain.InboundEvent, url, reason string) error {
	s.finished, s.event, s.url, s.reason = true, e, url, reason
	return nil
}

func TestPublicationRecoveryRequiresOwnedExactOpenDraftAndOnlyReads(t *testing.T) {
	for _, provider := range []domain.Provider{domain.ProviderGitHub, domain.ProviderGitLab} {
		for _, scenario := range []string{"exact", "previous head", "different head", "unowned", "ready", "closed", "wrong target", "redirect", "revoked"} {
			t.Run(string(provider)+"/"+scenario, func(t *testing.T) {
				sha, previous := strings.Repeat("a", 40), strings.Repeat("b", 40)
				head, marker, base, draft, state := sha, "", "main", true, "open"
				branch := "agent/" + uuid.NewString()
				if scenario != "unowned" {
					digest := sha256.Sum256([]byte(branch))
					marker = "<!-- open-review-agent:" + hex.EncodeToString(digest[:]) + " -->"
				}
				if scenario == "previous head" {
					head = previous
				}
				if scenario == "different head" {
					head = strings.Repeat("c", 40)
				}
				if scenario == "wrong target" {
					base = "release"
				}
				if scenario == "ready" {
					draft = false
				}
				if scenario == "closed" {
					state = "closed"
				}
				reads := 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reads++
					if r.Method != http.MethodGet {
						t.Fatal("recovery wrote to provider")
					}
					if scenario == "redirect" {
						http.Redirect(w, r, "https://other.invalid", 302)
						return
					}
					data := map[string]any{"number": 19, "state": state, "draft": draft, "body": marker, "html_url": "https://github.com/team/repo/pull/19",
						"head": map[string]any{"sha": head, "ref": branch, "repo": map[string]string{"full_name": "team/repo"}},
						"base": map[string]any{"sha": strings.Repeat("d", 40), "ref": base, "repo": map[string]string{"full_name": "team/repo"}}}
					if provider == domain.ProviderGitLab {
						if state == "open" {
							state = "opened"
						}
						data = map[string]any{"iid": 19, "state": state, "draft": draft, "description": marker, "web_url": "https://gitlab.example/team/repo/-/merge_requests/19",
							"sha": head, "source_branch": branch, "target_branch": base, "source_project_id": 42, "target_project_id": 42,
							"diff_refs": map[string]string{"base_sha": strings.Repeat("d", 40), "head_sha": head}}
					}
					_ = json.NewEncoder(w).Encode(data)
				}))
				defer server.Close()
				api := server.URL
				if provider == domain.ProviderGitLab {
					api += "/api/v4"
				}
				backend := &recoveryFixtureStore{fixtureStore: fixtureStore{target: store.AgentWorkflowTarget{RequireDraftOwnership: true,
					Task:    domain.AgentTask{ID: uuid.New(), ExecutionBranch: branch, SourceBaseSHA: previous},
					Attempt: domain.AgentTaskAttempt{ID: uuid.New(), PullRequestNumber: 19}, Acceptance: domain.AgentTaskAcceptance{HeadSHA: sha},
					Job: domain.ReviewJob{Provider: provider, APIBaseURL: api, Repository: "team/repo", ReviewNumber: 19, BaseRef: "main", CredentialRef: "fixture"}}}}
				if scenario == "revoked" {
					backend.target.Job.CredentialRef = ""
				}
				p := Processor{Store: backend, Resolver: fixtureResolver{}, HTTPClient: server.Client(), WorkerID: "reader"}
				if worked, err := p.RunOnce(context.Background()); !worked || err != nil {
					t.Fatalf("recovery: %v %v", worked, err)
				}
				if !backend.finished || backend.reviews != 0 {
					t.Fatal("recovery skipped confirmation or admitted review before completion")
				}
				if scenario == "exact" {
					if backend.reason != "" || backend.url == "" || backend.event.HeadSHA != sha {
						t.Fatalf("exact recovery: %+v", backend)
					}
				} else if backend.reason == "" {
					t.Fatal("unsafe recovery accepted")
				}
				if scenario == "previous head" && backend.reason != "provider_head_pending" {
					t.Fatal("stale provider read treated as permanent head drift")
				}
				if scenario == "revoked" && reads != 0 {
					t.Fatal("revoked provider credential was used")
				}
			})
		}
	}
}
