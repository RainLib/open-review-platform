package agentadapter

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type countingCredentialSource struct {
	inner            RepositoryCredentialSource
	refreshToken     string
	refreshCloneBase string
	calls            int
}

func (source *countingCredentialSource) Resolve(ctx context.Context, scope RepositoryCredentialScope) (RepositoryCredential, error) {
	source.calls++
	credential, err := source.inner.Resolve(ctx, scope)
	if err == nil && source.calls > 1 && source.refreshToken != "" {
		credential.Token = source.refreshToken
		if source.refreshCloneBase != "" {
			credential.CloneBaseURL = source.refreshCloneBase
		}
	}
	return credential, err
}

// This exercises the complete adapter path without a live provider, write
// token, or installed coding CLI. Git operations use a temporary bare
// repository; the fixture emulates only GitLab's Issue and Draft MR APIs.
func TestPipelineExecutePublishesOnlyValidatedDraftChange(t *testing.T) {
	for _, scenario := range []struct {
		name               string
		campaignMode       string
		changedIssue       bool
		changedClone       bool
		checkpointRejected bool
		branchOccupied     bool
	}{
		{name: "draft_created"},
		{name: "campaign_replace", campaignMode: "replace"},
		{name: "campaign_base_changed", campaignMode: "replace", changedIssue: true},
		{name: "issue_changed_before_push", changedIssue: true},
		{name: "clone_origin_changed_before_push", changedClone: true},
		{name: "checkpoint_rejected_before_push", checkpointRejected: true},
		{name: "existing_branch_cannot_be_advanced", branchOccupied: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			const token = "disposable-test-token"
			const refreshedToken = "refreshed-test-token"
			const title = "Fix duplicate retries"
			const body = "A retry creates duplicate work. Acceptance: one result and a regression test."
			root := t.TempDir()
			bare := filepath.Join(root, "team", "project.git")
			seed := filepath.Join(root, "seed")
			if err := os.MkdirAll(filepath.Dir(bare), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"init", "--bare", "-q", bare},
				{"-C", bare, "config", "http.receivepack", "true"},
				{"-C", bare, "config", "uploadpack.allowFilter", "true"},
				{"init", "-q", seed},
			} {
				runGitFixture(t, args...)
			}
			if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("Baseline\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"-C", seed, "add", "README.md"},
				{"-C", seed, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-q", "-m", "baseline"},
				{"-C", seed, "branch", "-M", "main"},
				{"-C", seed, "push", "-q", bare, "main"},
				{"--git-dir=" + bare, "symbolic-ref", "HEAD", "refs/heads/main"},
			} {
				runGitFixture(t, args...)
			}
			baseSHA := strings.TrimSpace(runGitFixture(t, "-C", seed, "rev-parse", "HEAD"))
			if scenario.branchOccupied {
				runGitFixture(t, "-C", seed, "push", "-q", bare, baseSHA+":refs/heads/agent/test-task")
			}

			var issueReads, draftCreates atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.EscapedPath() {
				case "/api/v4/projects/team%2Fproject/repository/branches/main":
					readNumber := issueReads.Add(1)
					expectedToken := token
					if readNumber > 1 {
						expectedToken = refreshedToken
					}
					if request.Method != "GET" || request.Header.Get("Authorization") != "Bearer "+expectedToken {
						http.Error(writer, "invalid base read", 401)
						return
					}
					actual := baseSHA
					if scenario.changedIssue && readNumber > 1 {
						actual = strings.Repeat("b", 40)
					}
					_ = json.NewEncoder(writer).Encode(map[string]any{"commit": map[string]string{"id": actual}})
				case "/api/v4/projects/team%2Fproject/issues/19":
					readNumber := issueReads.Add(1)
					expectedToken := token
					if readNumber > 1 {
						expectedToken = refreshedToken
					}
					if request.Method != http.MethodGet || request.Header.Get("Authorization") != "Bearer "+expectedToken {
						http.Error(writer, "invalid Issue read", http.StatusUnauthorized)
						return
					}
					currentBody := body
					if scenario.changedIssue && readNumber > 1 {
						currentBody = "Issue changed after plan approval"
					}
					writer.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(writer).Encode(map[string]any{"state": "opened", "title": title, "description": currentBody, "labels": []string{}})
				case "/api/v4/projects/team%2Fproject/merge_requests":
					if request.Header.Get("Authorization") != "Bearer "+refreshedToken {
						http.Error(writer, "invalid MR credential", http.StatusUnauthorized)
						return
					}
					if request.Method == http.MethodGet {
						_, _ = writer.Write([]byte("[]"))
						return
					}
					if request.Method != http.MethodPost {
						http.Error(writer, "invalid MR method", http.StatusMethodNotAllowed)
						return
					}
					var input struct {
						Title        string `json:"title"`
						SourceBranch string `json:"source_branch"`
						TargetBranch string `json:"target_branch"`
						Description  string `json:"description"`
					}
					if err := json.NewDecoder(request.Body).Decode(&input); err != nil || !strings.HasPrefix(input.Title, "[Draft]") || input.SourceBranch != "agent/test-task" || input.TargetBranch != "main" || !strings.Contains(input.Description, testAgentDraftMarker()) || !strings.Contains(input.Description, "1 changed file(s)") || !strings.Contains(input.Description, "<code>README.md</code>") || !strings.Contains(input.Description, "## Verification") || (scenario.campaignMode == "" && !strings.Contains(input.Description, "not attested by this adapter")) {
						http.Error(writer, "invalid Draft MR", http.StatusBadRequest)
						return
					}
					output, err := exec.Command("git", "--git-dir="+bare, "rev-parse", "refs/heads/agent/test-task").Output()
					if err != nil {
						http.Error(writer, "agent branch was not pushed", http.StatusConflict)
						return
					}
					headSHA := strings.TrimSpace(string(output))
					draftCreates.Add(1)
					writer.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(writer).Encode(map[string]any{
						"web_url": server.URL + "/team/project/-/merge_requests/7", "description": input.Description,
						"iid": 7, "draft": true, "state": "opened", "sha": headSHA,
						"source_branch": input.SourceBranch, "target_branch": input.TargetBranch,
						"source_project_id": 4, "target_project_id": 4,
					})
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			agentBinary := filepath.Join(root, "fake-codex")
			binary := "#!/bin/sh\nset -eu\n[ \"$1\" = exec ]\n[ \"${AGENT_TASK_ADAPTER_SECRET+x}\" = \"\" ]\n[ \"${AGENT_ADAPTER_GITLAB_TOKEN+x}\" = \"\" ]\nprintf 'Validated fix\\n' >> README.md\n"
			if err := os.WriteFile(agentBinary, []byte(binary), 0o700); err != nil {
				t.Fatal(err)
			}
			gitBinary := filepath.Join(root, "fixture-git")
			// The real pipeline still invokes git clone/fetch/push. The wrapper
			// redirects only this fixture origin to a disposable bare repo and
			// removes shallow/partial flags that old local file:// Git cannot
			// satisfy. Other tests cover scoped HTTP credentials and origin matching.
			rewrite := "url.file://" + root + "/.insteadOf=" + server.URL + "/"
			encodedRefreshedToken := base64.StdEncoding.EncodeToString([]byte("oauth2:" + refreshedToken))
			wrapper := "#!/bin/sh\nset -eu\n" +
				"case \" $* \" in *\" push \"*) case \"${GIT_CONFIG_PARAMETERS:-}\" in *" + encodedRefreshedToken + "*) ;; *) exit 23;; esac;; esac\n" +
				"if [ \"${5:-}\" = clone ]; then\n" +
				"  exec git -c '" + strings.ReplaceAll(rewrite, "'", "'\\''") + "' clone --no-checkout \"${10}\" \"${11}\"\n" +
				"fi\nexec git -c '" + strings.ReplaceAll(rewrite, "'", "'\\''") + "' \"$@\"\n"
			if err := os.WriteFile(gitBinary, []byte(wrapper), 0o700); err != nil {
				t.Fatal(err)
			}
			workspaceRoot := filepath.Join(root, "workspaces")
			if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			credentialFile := filepath.Join(root, "adapter-credentials.json")
			credentials, err := json.Marshal(map[string]any{
				"version": 1,
				"entries": []map[string]string{{
					"installation_id": "11111111-1111-4111-8111-111111111111", "provider": "gitlab",
					"api_base_url": server.URL + "/api/v4", "repository": "team/project",
					"clone_base_url": server.URL, "token": token,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(credentialFile, credentials, 0o600); err != nil {
				t.Fatal(err)
			}
			submission := Submission{}
			submission.Task.InstallationID = "11111111-1111-4111-8111-111111111111"
			submission.Task.Provider = domain.ProviderGitLab
			submission.Task.APIBaseURL = server.URL + "/api/v4"
			submission.Task.Repository = "team/project"
			submission.Task.OriginKind = "issue"
			submission.Task.OriginNumber = 19
			submission.Task.OriginRevision = domain.AgentIssueRevision(domain.ProviderGitLab, submission.Task.APIBaseURL, submission.Task.Repository, 19, title, body)
			submission.Task.ExecutorProfile = "codex"
			submission.Task.SourceBaseRef = "main"
			submission.Task.SourceBaseSHA = baseSHA
			submission.Task.BranchName = "agent/test-task"
			submission.Plan.Summary = "Append one validated line to README.md."
			if scenario.campaignMode != "" {
				h := sha256.Sum256([]byte("Baseline\n"))
				b := domain.AgentCampaignBinding{CampaignID: uuid.New(), TargetID: uuid.New(), RequestSHA256: strings.Repeat("a", 64), Mode: scenario.campaignMode, Paths: []string{"README.md"}, Search: "Baseline", Replacement: "Baseline updated", Files: []domain.AgentCampaignFile{{Path: "README.md", SHA256: hex.EncodeToString(h[:]), Matches: 1, Bytes: 9}}, Requirements: "Observed behavior: README.md uses an old description. Expected behavior: update only the README documentation and preserve unrelated repository content.", Criteria: []string{"README documentation is updated"}}
				submission.Task.Campaign = &b
				submission.Task.OriginKind = "campaign"
				submission.Task.OriginNumber = 0
				submission.Task.OriginRevision = b.OriginRevision()
				submission.Plan.AcceptanceCriteria = b.Criteria
				submission.Limits.Workflow = domain.AgentWorkflowPolicy{Enabled: true, RequireCriterionEvidence: true, MaxTaskAttempts: 3, MaxRepairCycles: 2, RequiredChecks: []string{"CI / docs"}}
			}

			submission.Limits.DeadlineAt = time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano)
			credentialSource := &countingCredentialSource{inner: FileCredentialSource{Path: credentialFile}, refreshToken: refreshedToken}
			if scenario.changedClone {
				credentialSource.refreshCloneBase = server.URL + "/wrong"
			}
			pipeline := Pipeline{
				WorkspaceRoot: workspaceRoot, ExecutorKind: "codex", CodexBinary: agentBinary, GitBinary: gitBinary,
				AllowedPaths: []string{"README.md"}, CredentialSource: credentialSource, GitLabAllowHTTP: true,
			}
			if scenario.campaignMode != "" {
				// The deterministic replacement does not invoke a coding child.
				// Keep the production sandbox contract intact; the verifier hook
				// below checks the patch in this provider fixture, not in Docker.
				pool, err := NewExecutorIdentityPool(10002, 1)
				if err != nil {
					t.Fatal(err)
				}
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "deterministic replacement must not call the model", http.StatusBadRequest)
				}))
				defer upstream.Close()
				pipeline.ExecutorIdentityPool = pool
				pipeline.DockerSandbox = &DockerSandboxConfig{ImageID: "sha256:" + strings.Repeat("a", 64), InternalNetwork: "fixture-internal", WorkspaceVolume: "fixture-workspaces", AdapterHost: "fixture-adapter"}
				pipeline.CodeModelBroker = ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-key", Model: "gpt-6-sol", HTTPClient: upstream.Client()}
				profile := testVerificationProfile()
				profile.Provider = domain.ProviderGitLab
				profile.APIBaseURL = submission.Task.APIBaseURL
				profile.Repository = submission.Task.Repository
				profile.CriterionReportRequired = true
				config, err := json.Marshal(verificationProfileFile{Version: 1, Entries: []VerificationProfile{profile}})
				if err != nil {
					t.Fatal(err)
				}
				profilePath := filepath.Join(root, "campaign-verifier.json")
				if err = os.WriteFile(profilePath, config, 0600); err != nil {
					t.Fatal(err)
				}
				pipeline.VerificationProfileFile = profilePath
				pipeline.verifyCommand = func(_ context.Context, workspace string, _ VerificationProfile) (VerificationEvidence, error) {
					body, err := os.ReadFile(filepath.Join(workspace, "README.md"))
					if err != nil {
						return VerificationEvidence{}, err
					}
					expected := "Validated fix"
					if scenario.campaignMode == "replace" {
						expected = "Baseline updated"
					}
					if !strings.Contains(string(body), expected) {
						return VerificationEvidence{}, &verificationFailure{exit: 1, output: "documentation assertion failed"}
					}
					return VerificationEvidence{ProfileSHA256: strings.Repeat("d", 64), OutputSHA256: strings.Repeat("e", 64), OutputBytes: 12, Criteria: []domain.AgentCriterionResult{{Criterion: submission.Plan.AcceptanceCriteria[0], Status: "passed", Evidence: "Independent fixture checked updated README content"}}}, nil
				}
			}
			var checkpoints []string
			ctx := context.WithValue(context.Background(), publicationLeaseGuardKey{}, publicationLeaseGuard(func(_ context.Context, checkpoint string) error {
				checkpoints = append(checkpoints, checkpoint)
				return nil
			}))
			var retained *PublicationCheckpoint
			ctx = context.WithValue(ctx, publicationCheckpointKey{}, publicationCheckpointRecorder(func(_ context.Context, checkpoint PublicationCheckpoint) error {
				retained = &checkpoint
				if scenario.checkpointRejected {
					return errors.New("control plane rejected publication checkpoint")
				}
				return nil
			}))
			result, err := pipeline.Execute(ctx, "adapter-fixture", submission)
			if scenario.changedClone {
				if err == nil || !strings.Contains(err.Error(), "refreshed provider credential changed the admitted clone origin") || draftCreates.Load() != 0 || issueReads.Load() != 1 || strings.Join(checkpoints, ",") != "pre-credential-refresh" || credentialSource.calls != 2 {
					t.Fatalf("changed clone origin was published: result=%+v creates=%d checkpoints=%q err=%v", result, draftCreates.Load(), checkpoints, err)
				}
				if output, branchErr := exec.Command("git", "--git-dir="+bare, "show-ref", "--verify", "refs/heads/agent/test-task").CombinedOutput(); branchErr == nil {
					t.Fatalf("changed clone origin produced remote agent branch: %s", output)
				}
				return
			}
			if scenario.changedIssue {
				if err == nil || !strings.Contains(err.Error(), "origin before branch publication") || draftCreates.Load() != 0 || strings.Join(checkpoints, ",") != "pre-credential-refresh" || credentialSource.calls != 2 {
					t.Fatalf("changed Issue was published: result=%+v creates=%d checkpoints=%q err=%v", result, draftCreates.Load(), checkpoints, err)
				}
				if output, branchErr := exec.Command("git", "--git-dir="+bare, "show-ref", "--verify", "refs/heads/agent/test-task").CombinedOutput(); branchErr == nil {
					t.Fatalf("changed Issue produced remote agent branch: %s", output)
				}
				return
			}
			if scenario.checkpointRejected {
				if err == nil || !strings.Contains(err.Error(), "control plane rejected publication checkpoint") || retained == nil || draftCreates.Load() != 0 || issueReads.Load() != 2 || strings.Join(checkpoints, ",") != "pre-credential-refresh,pre-push" {
					t.Fatalf("rejected checkpoint permitted publication: result=%+v creates=%d reads=%d checkpoints=%q err=%v", result, draftCreates.Load(), issueReads.Load(), checkpoints, err)
				}
				if output, branchErr := exec.Command("git", "--git-dir="+bare, "show-ref", "--verify", "refs/heads/agent/test-task").CombinedOutput(); branchErr == nil {
					t.Fatalf("rejected checkpoint produced remote agent branch: %s", output)
				}
				return
			}
			if scenario.branchOccupied {
				if err == nil || !strings.Contains(err.Error(), "exact remote lease") || retained == nil || draftCreates.Load() != 0 || issueReads.Load() != 2 {
					t.Fatalf("occupied branch was published: result=%+v creates=%d reads=%d err=%v", result, draftCreates.Load(), issueReads.Load(), err)
				}
				if remoteSHA := strings.TrimSpace(runGitFixture(t, "--git-dir="+bare, "rev-parse", "refs/heads/agent/test-task")); remoteSHA != baseSHA {
					t.Fatalf("occupied branch advanced from %s to %s", baseSHA, remoteSHA)
				}
				return
			}
			if err != nil || result.PullRequestNumber != 7 || result.PullRequestURL != server.URL+"/team/project/-/merge_requests/7" || result.BranchName != "agent/test-task" || result.HeadSHA == baseSHA || draftCreates.Load() != 1 || issueReads.Load() != 2 || credentialSource.calls != 2 || strings.Join(checkpoints, ",") != "pre-credential-refresh,pre-push,pre-draft" {
				t.Fatalf("complete adapter chain failed: result=%+v reads=%d creates=%d checkpoints=%q err=%v", result, issueReads.Load(), draftCreates.Load(), checkpoints, err)
			}
			if retained == nil || retained.HeadSHA != result.HeadSHA || retained.PatchSHA256 != result.PatchSHA256 || retained.ChangedFileCount != result.ChangedFileCount || retained.DiffBytes != result.DiffBytes {
				t.Fatalf("validated publication was not retained before the provider write: %+v result=%+v", retained, result)
			}
			if remoteSHA := strings.TrimSpace(runGitFixture(t, "--git-dir="+bare, "rev-parse", "refs/heads/agent/test-task")); remoteSHA != result.HeadSHA {
				t.Fatalf("Draft MR reported %s but remote branch points at %s", result.HeadSHA, remoteSHA)
			}
		})
	}
}

// The GitHub Enterprise fixture uses a TLS API origin and a disposable bare
// repository. It proves the same Issue-to-Draft boundary without a provider
// write credential or a live GitHub installation.
func TestPipelineExecutePublishesGitHubDraftOnlyForCurrentIssue(t *testing.T) {
	scenarios := []struct {
		changedIssue    bool
		changedFeedback bool
		docker          bool
		claude          bool
	}{{}, {changedIssue: true}, {changedFeedback: true}, {docker: true}, {changedIssue: true, docker: true}, {changedFeedback: true, docker: true}}
	if os.Getenv("OPENREVIEW_TEST_DOCKER_CLAUDE_LIVE") == "1" {
		scenarios = append(scenarios, struct {
			changedIssue, changedFeedback, docker, claude bool
		}{docker: true, claude: true})
	}
	for _, scenario := range scenarios {
		if scenario.docker && !scenario.claude && os.Getenv("OPENREVIEW_TEST_DOCKER_SANDBOX") != "1" {
			continue
		}
		changedIssue := scenario.changedIssue
		name := "current_issue"
		if changedIssue {
			name = "issue_changed_before_push"
			if scenario.docker {
				name = "isolated_codex_stale_issue"
			}
		} else if scenario.changedFeedback {
			name = "feedback_edited_before_push"
			if scenario.docker {
				name = "isolated_codex_stale_feedback"
			}
		} else if scenario.claude {
			name = "isolated_claude_draft_feedback"
		} else if scenario.docker {
			name = "isolated_codex_draft"
		}
		t.Run(name, func(t *testing.T) {
			const token = "disposable-github-token"
			const refreshedToken = "refreshed-github-token"
			const title = "Fix duplicate retries"
			const body = "A retry creates duplicate work. Acceptance: one result and a regression test."
			root := t.TempDir()
			bare := filepath.Join(root, "acme", "project.git")
			seed := filepath.Join(root, "seed")
			if err := os.MkdirAll(filepath.Dir(bare), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"init", "--bare", "-q", bare},
				{"-C", bare, "config", "http.receivepack", "true"},
				{"-C", bare, "config", "uploadpack.allowFilter", "true"},
				{"init", "-q", seed},
			} {
				runGitFixture(t, args...)
			}
			if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("Baseline\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"-C", seed, "add", "README.md"},
				{"-C", seed, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-q", "-m", "baseline"},
				{"-C", seed, "branch", "-M", "main"},
				{"-C", seed, "push", "-q", bare, "main"},
				{"--git-dir=" + bare, "symbolic-ref", "HEAD", "refs/heads/main"},
			} {
				runGitFixture(t, args...)
			}
			baseSHA := strings.TrimSpace(runGitFixture(t, "-C", seed, "rev-parse", "HEAD"))
			var issueReads, draftLists, draftCreates, draftReads, feedbackReads atomic.Int32
			var draftBody atomic.Value
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
					http.Error(writer, "missing GitHub API version", http.StatusBadRequest)
					return
				}
				switch request.URL.Path {
				case "/api/v3/repos/acme/project/issues/19":
					readNumber := issueReads.Add(1)
					expectedToken := token
					if readNumber > 1 {
						expectedToken = refreshedToken
					}
					if request.Method != http.MethodGet || request.Header.Get("Authorization") != "Bearer "+expectedToken {
						http.Error(writer, "invalid Issue read", http.StatusUnauthorized)
						return
					}
					currentBody := body
					if changedIssue && readNumber > 1 {
						currentBody = "Issue changed after plan approval"
					}
					writer.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(writer).Encode(map[string]any{"state": "open", "title": title, "body": currentBody, "labels": []any{}})
				case "/api/v3/repos/acme/project/pulls":
					if request.Header.Get("Authorization") != "Bearer "+refreshedToken {
						http.Error(writer, "invalid Draft credential", http.StatusUnauthorized)
						return
					}
					if request.Method == http.MethodGet {
						if request.URL.Query().Get("head") != "acme:agent/test-task" || request.URL.Query().Get("base") != "main" || request.URL.Query().Get("state") != "open" {
							http.Error(writer, "invalid Draft lookup", http.StatusBadRequest)
							return
						}
						draftLists.Add(1)
						_, _ = writer.Write([]byte("[]"))
						return
					}
					var input struct {
						Title string `json:"title"`
						Head  string `json:"head"`
						Base  string `json:"base"`
						Draft bool   `json:"draft"`
						Body  string `json:"body"`
					}
					if request.Method != http.MethodPost || json.NewDecoder(request.Body).Decode(&input) != nil || !input.Draft || input.Head != "agent/test-task" || input.Base != "main" || !strings.Contains(input.Body, testAgentDraftMarker()) || !strings.Contains(input.Body, "1 changed file(s)") || !strings.Contains(input.Body, "## Verification") {
						http.Error(writer, "invalid Draft PR", http.StatusBadRequest)
						return
					}
					output, err := exec.Command("git", "--git-dir="+bare, "rev-parse", "refs/heads/agent/test-task").Output()
					if err != nil {
						http.Error(writer, "agent branch was not pushed", http.StatusConflict)
						return
					}
					headSHA := strings.TrimSpace(string(output))
					draftCreates.Add(1)
					draftBody.Store(input.Body)
					writer.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(writer).Encode(map[string]any{
						"html_url": server.URL + "/acme/project/pull/7", "body": input.Body,
						"number": 7, "draft": true, "state": "open",
						"head": map[string]any{"ref": input.Head, "sha": headSHA, "repo": map[string]any{"full_name": "acme/project"}},
						"base": map[string]any{"ref": input.Base},
					})
				case "/api/v3/repos/acme/project/pulls/7":
					if request.Method != http.MethodGet || (request.Header.Get("Authorization") != "Bearer "+token && request.Header.Get("Authorization") != "Bearer "+refreshedToken) {
						http.Error(writer, "invalid Draft read", http.StatusUnauthorized)
						return
					}
					output, err := exec.Command("git", "--git-dir="+bare, "rev-parse", "refs/heads/agent/test-task").Output()
					if err != nil {
						http.Error(writer, "agent Draft branch is unavailable", http.StatusConflict)
						return
					}
					headSHA := strings.TrimSpace(string(output))
					draftReads.Add(1)
					writer.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(writer).Encode(map[string]any{
						"html_url": server.URL + "/acme/project/pull/7", "body": draftBody.Load().(string),
						"number": 7, "draft": true, "state": "open",
						"head": map[string]any{"ref": "agent/test-task", "sha": headSHA, "repo": map[string]any{"full_name": "acme/project"}},
						"base": map[string]any{"ref": "main"},
					})
				case "/api/v3/repos/acme/project/issues/comments/91":
					if request.Method != http.MethodGet || (request.Header.Get("Authorization") != "Bearer "+token && request.Header.Get("Authorization") != "Bearer "+refreshedToken) {
						http.Error(writer, "invalid feedback read", http.StatusUnauthorized)
						return
					}
					body := "@openreview revise Add a focused feedback regression line."
					readNumber := feedbackReads.Add(1)
					if scenario.changedFeedback && readNumber > 1 {
						body += " Edited after approval."
					}
					writer.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(writer).Encode(map[string]any{
						"id": 91, "body": body,
						"issue_url": server.URL + "/api/v3/repos/acme/project/issues/7", "user": map[string]any{"id": 55},
					})
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			agentBinary := filepath.Join(root, "fake-codex")
			binary := "#!/bin/sh\nset -eu\n[ \"$1\" = exec ]\n[ \"${AGENT_TASK_ADAPTER_SECRET+x}\" = \"\" ]\n[ \"${AGENT_ADAPTER_GITHUB_TOKEN+x}\" = \"\" ]\nprintf 'Validated fix\\n' >> README.md\n"
			if err := os.WriteFile(agentBinary, []byte(binary), 0o700); err != nil {
				t.Fatal(err)
			}
			gitBinary := filepath.Join(root, "fixture-git")
			rewrite := "url.file://" + root + "/.insteadOf=" + server.URL + "/"
			encodedRefreshedToken := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + refreshedToken))
			wrapper := "#!/bin/sh\nset -eu\n" +
				"case \" $* \" in *\" push \"*) case \"${GIT_CONFIG_PARAMETERS:-}\" in *" + encodedRefreshedToken + "*) ;; *) exit 23;; esac;; esac\n" +
				"if [ \"${5:-}\" = clone ]; then\n" +
				"  exec git -c '" + strings.ReplaceAll(rewrite, "'", "'\\''") + "' clone --no-checkout \"${10}\" \"${11}\"\n" +
				"fi\nexec git -c '" + strings.ReplaceAll(rewrite, "'", "'\\''") + "' \"$@\"\n"
			if err := os.WriteFile(gitBinary, []byte(wrapper), 0o700); err != nil {
				t.Fatal(err)
			}
			workspaceRoot := filepath.Join(root, "workspaces")
			if scenario.docker {
				workspaceRoot = os.Getenv("OPENREVIEW_TEST_DOCKER_WORKSPACE_ROOT")
				if workspaceRoot == "" || os.Geteuid() != 0 {
					t.Skip("isolated Codex publication requires the disposable root Docker acceptance profile")
				}
			} else if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			credentialFile := filepath.Join(root, "adapter-credentials.json")
			credentials, err := json.Marshal(map[string]any{
				"version": 1, "entries": []map[string]string{{
					"installation_id": "11111111-1111-4111-8111-111111111111", "provider": "github",
					"api_base_url": server.URL + "/api/v3", "repository": "acme/project",
					"clone_base_url": server.URL, "token": token,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(credentialFile, credentials, 0o600); err != nil {
				t.Fatal(err)
			}
			submission := Submission{}
			submission.Task.InstallationID = "11111111-1111-4111-8111-111111111111"
			submission.Task.Provider = domain.ProviderGitHub
			submission.Task.APIBaseURL = server.URL + "/api/v3"
			submission.Task.Repository = "acme/project"
			submission.Task.OriginKind = "issue"
			submission.Task.OriginNumber = 19
			submission.Task.OriginRevision = domain.AgentIssueRevision(domain.ProviderGitHub, submission.Task.APIBaseURL, submission.Task.Repository, 19, title, body)
			submission.Task.ExecutorProfile = "codex"
			if scenario.claude {
				submission.Task.ExecutorProfile = "claude"
			}
			submission.Task.SourceBaseRef = "main"
			submission.Task.SourceBaseSHA = baseSHA
			submission.Task.BranchName = "agent/test-task"
			submission.Plan.Summary = "Append one validated line to README.md."
			submission.Limits.DeadlineAt = time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano)
			credentialSource := &countingCredentialSource{inner: FileCredentialSource{Path: credentialFile}, refreshToken: refreshedToken}
			pipeline := Pipeline{
				WorkspaceRoot: workspaceRoot, ExecutorKind: "codex", CodexBinary: agentBinary, GitBinary: gitBinary,
				AllowedPaths: []string{"README.md"}, CredentialSource: credentialSource, HTTPClient: server.Client(),
			}
			if scenario.claude {
				pipeline.ExecutorKind = "claude"
			}
			var modelCalls atomic.Int32
			if scenario.docker && !scenario.claude {
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer synthetic-upstream-key" {
						writer.WriteHeader(http.StatusUnauthorized)
						return
					}
					var fields map[string]any
					if err := json.NewDecoder(request.Body).Decode(&fields); err != nil || fields["store"] != false {
						writer.WriteHeader(http.StatusBadRequest)
						return
					}
					writer.Header().Set("Content-Type", "text/event-stream")
					writer.Header().Set("Cache-Control", "no-cache")
					if modelCalls.Add(1) == 1 {
						arguments, _ := json.Marshal(map[string]string{"cmd": "printf 'Validated fix\\n' >> README.md"})
						item := map[string]any{"id": "fc_draft", "type": "function_call", "name": "exec_command", "call_id": "call_draft", "arguments": string(arguments)}
						writeSyntheticResponseEvent(writer, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
						writeSyntheticResponseEvent(writer, "response.completed", syntheticCompletedResponse("resp_draft_1", fields["model"], []any{item}))
					} else {
						item := map[string]any{"id": "msg_draft", "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Done.", "annotations": []any{}}}}
						writeSyntheticResponseEvent(writer, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
						writeSyntheticResponseEvent(writer, "response.completed", syntheticCompletedResponse("resp_draft_2", fields["model"], []any{item}))
					}
				}))
				defer upstream.Close()
				pool, err := NewExecutorIdentityPool(10002, 1)
				if err != nil {
					t.Fatal(err)
				}
				pipeline.ExecutorIdentityPool = pool
				pipeline.DockerSandbox = &DockerSandboxConfig{
					DockerBinary: "docker", ImageID: os.Getenv("OPENREVIEW_TEST_DOCKER_IMAGE_ID"),
					InternalNetwork: os.Getenv("OPENREVIEW_TEST_DOCKER_NETWORK"), WorkspaceVolume: os.Getenv("OPENREVIEW_TEST_DOCKER_VOLUME"),
					AdapterHost: os.Getenv("OPENREVIEW_TEST_DOCKER_ADAPTER_HOST"),
				}
				pipeline.CodeModelBroker = ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-upstream-key", Model: "gpt-6-sol", HTTPClient: upstream.Client()}
			}
			if scenario.claude {
				pool, err := NewExecutorIdentityPool(10002, 1)
				if err != nil {
					t.Fatal(err)
				}
				pipeline.ExecutorIdentityPool = pool
				pipeline.DockerSandbox = &DockerSandboxConfig{
					DockerBinary: "docker", ImageID: os.Getenv("OPENREVIEW_TEST_DOCKER_IMAGE_ID"),
					InternalNetwork: os.Getenv("OPENREVIEW_TEST_DOCKER_NETWORK"), WorkspaceVolume: os.Getenv("OPENREVIEW_TEST_DOCKER_VOLUME"),
					AdapterHost: os.Getenv("OPENREVIEW_TEST_DOCKER_ADAPTER_HOST"),
				}
				pipeline.ClaudeModelBroker = ModelBrokerConfig{
					APIBaseURL: os.Getenv("AGENT_ADAPTER_CLAUDE_MODEL_API_BASE_URL"),
					APIKey:     os.Getenv("AGENT_ADAPTER_CLAUDE_MODEL_API_KEY"),
					Model:      os.Getenv("AGENT_ADAPTER_CLAUDE_MODEL"), WireAPI: "anthropic",
				}
			}
			var checkpoints []string
			ctx := context.WithValue(context.Background(), publicationLeaseGuardKey{}, publicationLeaseGuard(func(_ context.Context, checkpoint string) error {
				checkpoints = append(checkpoints, checkpoint)
				return nil
			}))
			var retained *PublicationCheckpoint
			ctx = context.WithValue(ctx, publicationCheckpointKey{}, publicationCheckpointRecorder(func(_ context.Context, checkpoint PublicationCheckpoint) error {
				retained = &checkpoint
				return nil
			}))
			result, err := pipeline.Execute(ctx, "github-adapter-fixture", submission)
			if changedIssue {
				if err == nil || !strings.Contains(err.Error(), "origin before branch publication") || issueReads.Load() != 2 || draftLists.Load() != 0 || draftCreates.Load() != 0 || retained != nil || strings.Join(checkpoints, ",") != "pre-credential-refresh" || (scenario.docker && !scenario.claude && modelCalls.Load() < 2) {
					t.Fatalf("stale GitHub Issue was published: result=%+v reads=%d lists=%d creates=%d checkpoints=%q err=%v", result, issueReads.Load(), draftLists.Load(), draftCreates.Load(), checkpoints, err)
				}
				if output, branchErr := exec.Command("git", "--git-dir="+bare, "show-ref", "--verify", "refs/heads/agent/test-task").CombinedOutput(); branchErr == nil {
					t.Fatalf("stale GitHub Issue produced remote agent branch: %s", output)
				}
				return
			}
			if err != nil || result.PullRequestNumber != 7 || result.PullRequestURL != server.URL+"/acme/project/pull/7" || result.HeadSHA == baseSHA || issueReads.Load() != 2 || draftLists.Load() != 1 || draftCreates.Load() != 1 || credentialSource.calls != 2 || strings.Join(checkpoints, ",") != "pre-credential-refresh,pre-push,pre-draft" || (scenario.docker && !scenario.claude && modelCalls.Load() < 2) {
				t.Fatalf("GitHub Draft chain failed: result=%+v reads=%d lists=%d creates=%d checkpoints=%q err=%v", result, issueReads.Load(), draftLists.Load(), draftCreates.Load(), checkpoints, err)
			}
			if retained == nil || retained.HeadSHA != result.HeadSHA || retained.PatchSHA256 != result.PatchSHA256 || retained.ChangedFileCount != 1 {
				t.Fatalf("GitHub Draft publication checkpoint is missing: retained=%+v result=%+v", retained, result)
			}
			if remoteSHA := strings.TrimSpace(runGitFixture(t, "--git-dir="+bare, "rev-parse", "refs/heads/agent/test-task")); remoteSHA != result.HeadSHA {
				t.Fatalf("GitHub Draft head %s differs from remote branch %s", result.HeadSHA, remoteSHA)
			}
			const feedbackInstruction = "Add a focused feedback regression line."
			feedbackDigest := sha256.Sum256([]byte(feedbackInstruction))
			feedback := submission
			feedback.Task.OriginKind = "pull_request"
			feedback.Task.OriginNumber = 7
			feedback.Task.OriginRevision = result.HeadSHA
			feedback.Task.SourceBaseRef = "agent/test-task"
			feedback.Task.SourceBaseSHA = result.HeadSHA
			feedback.Task.Feedback = &domain.AgentTaskFeedbackBinding{
				CommentExternalID: "91", ActorExternalID: "55", InstructionSHA256: hex.EncodeToString(feedbackDigest[:]), TargetBranch: "main",
			}
			feedback.Plan.Summary = "Append one focused feedback regression line to README.md."
			feedback.Limits.DeadlineAt = time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano)
			credentialSource.calls = 0
			modelCalls.Store(0)
			checkpoints = nil
			retained = nil
			feedbackResult, feedbackErr := pipeline.Execute(ctx, "github-feedback-fixture", feedback)
			if scenario.changedFeedback {
				if feedbackErr == nil || !strings.Contains(feedbackErr.Error(), "origin before branch publication") || draftCreates.Load() != 1 || feedbackReads.Load() != 2 || retained != nil || strings.Join(checkpoints, ",") != "pre-credential-refresh" || (scenario.docker && !scenario.claude && modelCalls.Load() < 2) {
					t.Fatalf("edited feedback was published: result=%+v reads=%d creates=%d checkpoints=%q err=%v", feedbackResult, feedbackReads.Load(), draftCreates.Load(), checkpoints, feedbackErr)
				}
				if remoteSHA := strings.TrimSpace(runGitFixture(t, "--git-dir="+bare, "rev-parse", "refs/heads/agent/test-task")); remoteSHA != result.HeadSHA {
					t.Fatalf("edited feedback advanced Draft head from %s to %s", result.HeadSHA, remoteSHA)
				}
				return
			}
			if feedbackErr != nil || feedbackResult.PullRequestNumber != 7 || feedbackResult.PullRequestURL != result.PullRequestURL || feedbackResult.BranchName != result.BranchName || feedbackResult.HeadSHA == result.HeadSHA || draftCreates.Load() != 1 || draftReads.Load() < 3 || credentialSource.calls != 2 || strings.Join(checkpoints, ",") != "pre-credential-refresh,pre-push,pre-draft" || (scenario.docker && !scenario.claude && modelCalls.Load() < 2) {
				t.Fatalf("GitHub Draft feedback chain failed: result=%+v reads=%d creates=%d checkpoints=%q err=%v", feedbackResult, draftReads.Load(), draftCreates.Load(), checkpoints, feedbackErr)
			}
			if retained == nil || retained.HeadSHA != feedbackResult.HeadSHA || retained.PatchSHA256 != feedbackResult.PatchSHA256 {
				t.Fatalf("feedback publication checkpoint is missing: retained=%+v result=%+v", retained, feedbackResult)
			}
			if remoteSHA := strings.TrimSpace(runGitFixture(t, "--git-dir="+bare, "rev-parse", "refs/heads/agent/test-task")); remoteSHA != feedbackResult.HeadSHA {
				t.Fatalf("feedback Draft head %s differs from the same remote branch %s", feedbackResult.HeadSHA, remoteSHA)
			}
		})
	}
}

func runGitFixture(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %q failed: %v: %s", args, err, output)
	}
	return string(output)
}
