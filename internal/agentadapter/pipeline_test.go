package agentadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestPipelineRejectsDiffBudgetLargerThanGitOutputLimit(t *testing.T) {
	pipeline := Pipeline{
		WorkspaceRoot: t.TempDir(), ExecutorKind: "codex", AllowedPaths: []string{"README.md"},
		MaxDiffBytes: maxGitOutputBytes + 1,
	}
	if err := pipeline.valid(); err == nil || !strings.Contains(err.Error(), "diff-byte budget") {
		t.Fatalf("oversized diff budget was not rejected: %v", err)
	}
}

func TestPublicationBranchLeaseBindsInitialCreationAndFeedbackHead(t *testing.T) {
	var submission Submission
	submission.Task.BranchName = "agent/approved-task"
	submission.Task.OriginKind = "issue"
	lease, err := publicationBranchLease(submission)
	if err != nil || lease != "--force-with-lease=refs/heads/agent/approved-task:" {
		t.Fatalf("initial branch must be absent: lease=%q err=%v", lease, err)
	}
	submission.Task.OriginKind = "pull_request"
	submission.Task.SourceBaseRef = submission.Task.BranchName
	submission.Task.SourceBaseSHA = "0123456789abcdef0123456789abcdef01234567"
	lease, err = publicationBranchLease(submission)
	if err != nil || lease != "--force-with-lease=refs/heads/agent/approved-task:"+submission.Task.SourceBaseSHA {
		t.Fatalf("feedback must retain its frozen head: lease=%q err=%v", lease, err)
	}
	submission.Task.SourceBaseRef = "main"
	if _, err := publicationBranchLease(submission); err == nil {
		t.Fatal("feedback tried to publish outside its admitted branch")
	}
	submission.Task.SourceBaseRef = submission.Task.BranchName
	submission.Task.SourceBaseSHA = "invalid"
	if _, err := publicationBranchLease(submission); err == nil {
		t.Fatal("feedback tried to publish without an exact admitted head")
	}
}

func TestPipelineResolvesOnlyAnInstalledFixedExecutor(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "fixed-codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "codex-link")
	if err := os.Symlink(binary, alias); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := (Pipeline{ExecutorKind: "codex", CodexBinary: alias}).ResolveExecutorBinary()
	if err != nil || resolved != canonical {
		t.Fatalf("fixed CLI path: got %q, error %v", resolved, err)
	}
	for _, pipeline := range []Pipeline{
		{ExecutorKind: "codex", CodexBinary: filepath.Join(root, "missing")},
		{ExecutorKind: "claude", ClaudeBinary: root},
		{ExecutorKind: "unsupported", CodexBinary: binary},
	} {
		if path, err := pipeline.ResolveExecutorBinary(); err == nil {
			t.Errorf("invalid executor resolved to %q: %+v", path, pipeline)
		}
	}
}

func TestPipelineReverifiesGitHubIssueBeforeExecution(t *testing.T) {
	issueBody := "Observed retries duplicate work. Expected one result. Acceptance criteria: a regression test proves the boundary."
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/acme/api/issues/42" || request.Header.Get("Authorization") != "Bearer provider-token" {
			http.Error(writer, "unexpected provider request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"state":"open","title":"Retry loses state","body":` + strconv.Quote(issueBody) + `,"labels":[]}`))
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitHub
	submission.Task.APIBaseURL = server.URL
	submission.Task.Repository = "acme/api"
	submission.Task.OriginKind = "issue"
	submission.Task.OriginNumber = 42
	submission.Task.OriginRevision = domain.AgentIssueRevision(domain.ProviderGitHub, server.URL, "acme/api", 42, "Retry loses state", issueBody)
	pipeline := Pipeline{GitHubToken: "provider-token", HTTPClient: server.Client()}
	if err := pipeline.verifyOrigin(context.Background(), submission); err != nil {
		t.Fatalf("current Issue should be admitted: %v", err)
	}
	issueBody = "The Issue was edited after plan approval"
	if err := pipeline.verifyOrigin(context.Background(), submission); err == nil {
		t.Fatal("edited Issue must stop the adapter before code or branch publication")
	}
}

func TestPipelineReverifiesDraftFeedbackCommentBeforeExecution(t *testing.T) {
	head := "abcdef0123456789abcdef0123456789abcdef01"
	branch := "agent/approved-feedback"
	instruction := "Preserve the existing Draft MR and add a focused regression test for retry recovery."
	digest := sha256.Sum256([]byte(instruction))
	var noteBody atomic.Value
	noteBody.Store("@openreview revise " + instruction)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer provider-token" {
			http.Error(writer, "missing provider credential", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/api/v4/projects/acme/api/merge_requests/42":
			_, _ = writer.Write([]byte(`{"draft":true,"source_branch":"` + branch + `","target_branch":"main","sha":"` + head + `"}`))
		case "/api/v4/projects/acme/api/merge_requests/42/notes/88":
			_, _ = writer.Write([]byte(`{"id":88,"noteable_type":"MergeRequest","noteable_iid":42,"author":{"id":55},"body":"` + noteBody.Load().(string) + `"}`))
		default:
			http.Error(writer, "unexpected path", http.StatusNotFound)
		}
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = server.URL + "/api/v4"
	submission.Task.Repository = "acme/api"
	submission.Task.OriginKind = "pull_request"
	submission.Task.OriginNumber = 42
	submission.Task.OriginRevision = head
	submission.Task.BranchName = branch
	submission.Task.Feedback = &domain.AgentTaskFeedbackBinding{CommentExternalID: "88", ActorExternalID: "55", InstructionSHA256: hex.EncodeToString(digest[:]), TargetBranch: "main"}
	pipeline := Pipeline{GitLabToken: "provider-token", GitLabAllowHTTP: true}
	if err := pipeline.verifyOrigin(context.Background(), submission); err != nil {
		t.Fatalf("unchanged Draft feedback was rejected: %v", err)
	}
	noteBody.Store("@openreview revise " + instruction + " edited")
	if err := pipeline.verifyOrigin(context.Background(), submission); err == nil {
		t.Fatal("edited feedback was accepted by the execution adapter")
	}
}

func TestPipelineDoesNotFollowDraftWriteRedirect(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		writer.WriteHeader(http.StatusCreated)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			_, _ = writer.Write([]byte("[]"))
			return
		}
		writer.Header().Set("Location", destination.URL+"/capture")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = source.URL + "/api/v4"
	submission.Task.Repository = "team/project"
	submission.Task.OriginKind = "issue"
	submission.Task.OriginNumber = 7
	submission.Task.BranchName = "agent/test-task"
	submission.Task.SourceBaseRef = "main"
	pipeline := Pipeline{GitLabToken: "write-token", GitLabAllowHTTP: true}
	if _, err := pipeline.createDraftPullRequest(context.Background(), submission, headSHA); err == nil {
		t.Fatal("redirected Draft MR creation must fail closed")
	}
	if redirected.Load() {
		t.Fatal("provider write request was forwarded to a redirect destination")
	}
}

func TestPipelineReverifiesAutomaticGitLabIssueLabels(t *testing.T) {
	labels := `["openreview:implement"]`
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.EscapedPath() != "/api/v4/projects/acme%2Fapi/issues/19" || request.Header.Get("Authorization") != "Bearer provider-token" {
			http.Error(writer, "unexpected provider request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"state":"opened","title":"Retry loses state","description":"Observed duplicate work. Expected one result. Acceptance criteria: a focused regression test.","labels":` + labels + `}`))
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = server.URL + "/api/v4"
	submission.Task.Repository = "acme/api"
	submission.Task.OriginKind = "issue"
	submission.Task.OriginNumber = 19
	submission.Task.OriginRevision = domain.AgentAutomaticIssueRevision(domain.ProviderGitLab, submission.Task.APIBaseURL, "acme/api", 19, "Retry loses state", "Observed duplicate work. Expected one result. Acceptance criteria: a focused regression test.", []string{"openreview:implement"})
	pipeline := Pipeline{GitLabToken: "provider-token", HTTPClient: server.Client()}
	if err := pipeline.verifyOrigin(context.Background(), submission); err != nil {
		t.Fatalf("labeled Issue should be admitted: %v", err)
	}
	labels = `[]`
	if err := pipeline.verifyOrigin(context.Background(), submission); err == nil {
		t.Fatal("removing the opt-in label must stop automatic Agent execution")
	}
}

func TestPipelineGitLabCloneOriginRequiresExactAdmittedAPIAndDevOptIn(t *testing.T) {
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = "http://gitlab:8929/api/v4"
	submission.Task.Repository = "team/project"
	pipeline := Pipeline{GitLabToken: "local-only-token", GitLabCloneBaseURL: "http://gitlab:8929"}
	if _, _, err := pipeline.cloneURL(submission); err == nil {
		t.Fatal("plaintext GitLab clone was allowed without explicit development opt-in")
	}
	pipeline.GitLabAllowHTTP = true
	cloneURL, header, err := pipeline.cloneURL(submission)
	if err != nil || cloneURL != "http://gitlab:8929/team/project.git" || header == "" || strings.Contains(cloneURL, "local-only-token") {
		t.Fatalf("local GitLab clone URL=%q header-present=%t err=%v", cloneURL, header != "", err)
	}
	for _, mismatched := range []string{"http://other-gitlab:8929", "https://gitlab:8929", "http://gitlab:8929/other"} {
		pipeline.GitLabCloneBaseURL = mismatched
		if _, _, err := pipeline.cloneURL(submission); err == nil {
			t.Fatalf("mismatched GitLab clone base %q was accepted", mismatched)
		}
	}
	github := Submission{}
	github.Task.Provider = domain.ProviderGitHub
	github.Task.Repository = "team/project"
	pipeline.GitHubToken = "local-only-token"
	pipeline.GitHubCloneBaseURL = "http://github.example.test"
	if _, _, err := pipeline.cloneURL(github); err == nil {
		t.Fatal("GitHub plaintext clone was allowed by GitLab-only opt-in")
	}
}

func TestPipelineBindsGitHubAPIToDeploymentCloneOrigin(t *testing.T) {
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitHub
	submission.Task.APIBaseURL = "https://api.github.com"
	submission.Task.Repository = "acme/widgets"
	pipeline := Pipeline{GitHubToken: "scoped-test-token"}
	if _, _, err := pipeline.cloneURL(submission); err != nil {
		t.Fatalf("hosted GitHub pair rejected: %v", err)
	}
	submission.Task.APIBaseURL = "https://attacker.example"
	if _, _, err := pipeline.cloneURL(submission); err == nil {
		t.Fatal("hosted GitHub token could be sent to an unrelated API origin")
	}
	pipeline.GitHubCloneBaseURL = "https://github.enterprise.example"
	submission.Task.APIBaseURL = "https://github.enterprise.example/api/v3"
	if _, _, err := pipeline.cloneURL(submission); err != nil {
		t.Fatalf("same-origin GitHub Enterprise pair rejected: %v", err)
	}
	submission.Task.APIBaseURL = "https://api.other.example/api/v3"
	if _, _, err := pipeline.cloneURL(submission); err == nil {
		t.Fatal("GitHub Enterprise token could be sent to another host")
	}
}

func TestPipelineRejectsMismatchedProviderOriginBeforeFirstCredentialedRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(writer, "credential should not reach this host", http.StatusForbidden)
	}))
	defer server.Close()
	workspaceRoot := t.TempDir()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitHub
	submission.Task.APIBaseURL = server.URL
	submission.Task.Repository = "acme/widgets"
	submission.Task.ExecutorProfile = "codex"
	submission.Limits.DeadlineAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	pipeline := Pipeline{WorkspaceRoot: workspaceRoot, AllowedPaths: []string{"README.md"}, ExecutorKind: "codex", GitHubToken: "scoped-test-token", HTTPClient: server.Client()}
	if _, err := pipeline.Execute(context.Background(), "job", submission); err == nil {
		t.Fatal("mismatched provider API origin was accepted")
	}
	if requests.Load() != 0 {
		t.Fatalf("provider token was sent before origin binding: requests=%d", requests.Load())
	}
}

func TestPipelineAllowsOnlyExplicitNonSensitivePaths(t *testing.T) {
	pipeline := Pipeline{AllowedPaths: []string{"internal/**", "README.md"}}
	for _, test := range []struct {
		path string
		want bool
	}{
		{"internal/feature/handler.go", true}, {"README.md", true}, {".github/workflows/release.yml", false},
		{".env", false}, {"config/service.pem", false}, {"apps/web/page.tsx", false},
		{"internal/.env.local", false}, {"internal/secret.PEM", false}, {"internal/.gitignore", false},
		{"internal/.gitattributes", false}, {"internal/.gitmodules", false},
		{"internal/../../outside.go", false}, {"/internal/feature.go", false},
		{"internal/feature.go ", false}, {`internal\feature.go`, false},
	} {
		if got := pipeline.allowedPath(test.path); got != test.want {
			t.Fatalf("allowedPath(%q)=%v want %v", test.path, got, test.want)
		}
	}
}

func TestPublicationRequiresLiveControlPlaneLease(t *testing.T) {
	if err := requirePublicationLease(context.Background(), "pre-push"); err == nil {
		t.Fatal("publication proceeded without an adapter-owned lease guard")
	}
	called := ""
	ctx := context.WithValue(context.Background(), publicationLeaseGuardKey{}, publicationLeaseGuard(func(_ context.Context, checkpoint string) error {
		called = checkpoint
		return nil
	}))
	if err := requirePublicationLease(ctx, "pre-draft"); err != nil || called != "pre-draft" {
		t.Fatalf("lease guard was not called at publication checkpoint: called=%q err=%v", called, err)
	}
}

func TestGitCredentialIsScopedAndAdapterEnvironmentIsNotInherited(t *testing.T) {
	t.Setenv("AGENT_TASK_ADAPTER_SECRET", "must-not-reach-git")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "http.extraHeader")
	t.Setenv("GIT_CONFIG_VALUE_0", "Authorization: attacker-controlled")
	credential := gitCredential{URL: "https://github.com/acme/api.git", Header: "Authorization: Basic test-only"}
	environment, err := gitEnvironment(credential)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range environment {
		if strings.Contains(item, "must-not-reach-git") || strings.Contains(item, "attacker-controlled") || strings.HasPrefix(item, "AGENT_TASK_ADAPTER_SECRET=") {
			t.Fatalf("adapter or inherited Git credential reached subprocess environment: %q", item)
		}
	}
	pipeline := Pipeline{}
	matched, err := pipeline.git(context.Background(), t.TempDir(), credential, "config", "--get-urlmatch", "http.extraHeader", credential.URL)
	if err != nil || strings.TrimSpace(matched) != credential.Header {
		t.Fatalf("scoped repository credential was not available for its exact URL: output=%q err=%v", matched, err)
	}
	for _, foreign := range []string{"https://github.com/acme/other.git", "https://attacker.example/acme/api.git"} {
		if output, err := pipeline.git(context.Background(), t.TempDir(), credential, "config", "--get-urlmatch", "http.extraHeader", foreign); err == nil {
			t.Fatalf("credential matched foreign URL %q: %q", foreign, output)
		}
	}
	if _, err := gitEnvironment(gitCredential{URL: "https://github.com/a'b.git", Header: credential.Header}); err == nil {
		t.Fatal("a URL that cannot be safely quoted for Git configuration was accepted")
	}
}

func TestPipelineRejectsGitMetadataChangedByExecutor(t *testing.T) {
	workspace := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-q", "-m", "base"}, {"switch", "-q", "-c", "agent/test-task"}} {
		command := exec.Command("git", args...)
		command.Dir = workspace
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("prepare repository: %v: %s", err, output)
		}
	}
	pipeline := Pipeline{}
	base, err := pipeline.git(context.Background(), workspace, noGitCredential, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	configHash, err := trustedGitConfig(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := pipeline.verifyGitState(context.Background(), workspace, configHash, "agent/test-task", strings.TrimSpace(base)); err != nil {
		t.Fatalf("unchanged Git state was rejected: %v", err)
	}
	configPath := filepath.Join(workspace, ".git", "config")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, append(config, []byte("\n[remote \"origin\"]\n\turl = https://attacker.example/repo.git\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.verifyGitState(context.Background(), workspace, configHash, "agent/test-task", strings.TrimSpace(base)); err == nil {
		t.Fatal("executor-modified remote configuration was accepted")
	}
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.git(context.Background(), workspace, noGitCredential, "switch", "-q", "-c", "agent/other"); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.verifyGitState(context.Background(), workspace, configHash, "agent/test-task", strings.TrimSpace(base)); err == nil {
		t.Fatal("executor-modified HEAD branch was accepted")
	}
}

func TestPipelinePushesExactValidatedCommitInsteadOfMutableBranch(t *testing.T) {
	workspace := t.TempDir()
	remote := filepath.Join(t.TempDir(), "remote.git")
	for _, command := range [][]string{{"git", "init", "--bare", "-q", remote}, {"git", "-C", workspace, "init", "-q"}, {"git", "-C", workspace, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-q", "-m", "validated"}} {
		if output, err := exec.Command(command[0], command[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("prepare Git fixture: %v: %s", err, output)
		}
	}
	pipeline := Pipeline{}
	validated, err := pipeline.git(context.Background(), workspace, noGitCredential, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	validated = strings.TrimSpace(validated)
	if _, err := pipeline.git(context.Background(), workspace, noGitCredential, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-q", "-m", "later-mutable-head"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".git", "hooks", "pre-push"), []byte("#!/bin/sh\nexit 77\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.git(context.Background(), workspace, noGitCredential, "push", "--no-verify", "file://"+remote, validated+":refs/heads/agent/test-task"); err != nil {
		t.Fatalf("push exact validated commit without running repository hook: %v", err)
	}
	actual, err := exec.Command("git", "--git-dir="+remote, "rev-parse", "refs/heads/agent/test-task").Output()
	if err != nil || strings.TrimSpace(string(actual)) != validated {
		t.Fatalf("remote branch escaped validated commit: got=%q want=%q err=%v", actual, validated, err)
	}
}

func TestPipelineEvaluatesNewFilesWithTheSamePatchBudget(t *testing.T) {
	workspace := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-q", "-m", "base"}} {
		command := exec.Command("git", args...)
		command.Dir = workspace
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("initialize temporary repository: %v: %s", err, output)
		}
	}
	if err := os.MkdirAll(filepath.Join(workspace, "internal"), 0o700); err != nil {
		t.Fatal(err)
	}
	newFile := filepath.Join(workspace, "internal", "new_test.go")
	if err := os.WriteFile(newFile, []byte("package internal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{AllowedPaths: []string{"internal/**"}}
	files, size, patchSHA256, err := pipeline.validatePatch(context.Background(), workspace, Submission{})
	if err != nil || len(files) != 1 || files[0] != "internal/new_test.go" || size == 0 || len(patchSHA256) != 64 {
		t.Fatalf("new source file was not evaluated: files=%q size=%d sha=%q err=%v", files, size, patchSHA256, err)
	}
	if _, err := pipeline.git(context.Background(), workspace, noGitCredential, "add", "--", files[0]); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.verifyStagedPatch(context.Background(), workspace, patchSHA256, size); err != nil {
		t.Fatalf("unchanged staged patch was rejected: %v", err)
	}
	if err := os.WriteFile(newFile, []byte("package internal\n// changed after validation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.git(context.Background(), workspace, noGitCredential, "add", "--", files[0]); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.verifyStagedPatch(context.Background(), workspace, patchSHA256, size); err == nil {
		t.Fatal("changed staged patch retained its validation evidence")
	}
	if err := os.WriteFile(newFile, []byte("package internal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.git(context.Background(), workspace, noGitCredential, "add", "--", files[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.git(context.Background(), workspace, noGitCredential, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "validated patch"); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.verifyCommittedPatch(context.Background(), workspace, patchSHA256, size); err != nil {
		t.Fatalf("exact committed patch was rejected: %v", err)
	}
	if err := pipeline.verifyCommittedPatch(context.Background(), workspace, strings.Repeat("0", 64), size); err == nil {
		t.Fatal("changed committed patch digest was accepted")
	}
	if err := os.WriteFile(filepath.Join(workspace, "internal", "credential.pem"), []byte("private material"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := pipeline.validatePatch(context.Background(), workspace, Submission{}); err == nil {
		t.Fatal("disallowed new credential file bypassed patch evaluation")
	}
	if err := os.Remove(filepath.Join(workspace, "internal", "credential.pem")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("package internal\n// AKIA"+strings.Repeat("A", 16)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := pipeline.validatePatch(context.Background(), workspace, Submission{}); err == nil {
		t.Fatal("secret in a new source file bypassed patch evaluation")
	}
	if err := os.WriteFile(newFile, []byte{0, 1, 2, 3, 4}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := pipeline.validatePatch(context.Background(), workspace, Submission{}); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary source patch was accepted: %v", err)
	}
}

func TestAgentPatchSecretScanChecksOnlyAddedHighConfidenceCredentials(t *testing.T) {
	for _, line := range []string{
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"AKIA" + strings.Repeat("A", 16),
		"ghp_" + strings.Repeat("A", 36),
		"github_pat_" + strings.Repeat("A", 50),
		"glpat-" + strings.Repeat("A", 24),
		"sk-proj-" + strings.Repeat("A", 24),
	} {
		if !agentPatchContainsSecret("diff --git a/file b/file\n+++ b/file\n+" + line + "\n") {
			t.Fatalf("added credential format was not rejected: %q", line[:min(len(line), 12)])
		}
		if agentPatchContainsSecret("diff --git a/file b/file\n " + line + "\n-" + line + "\n") {
			t.Fatalf("unchanged or removed credential blocked a repair: %q", line[:min(len(line), 12)])
		}
	}
	if agentPatchContainsSecret("diff --git a/file b/file\n+++ b/file\n+document the sk- prefix without a token\n") {
		t.Fatal("ordinary documentation was treated as a credential")
	}
}

func TestPipelineRejectsUnsafeResultContract(t *testing.T) {
	submission := Submission{}
	submission.Task.BranchName = "agent/task-1"
	if validExecutionResult(submission, ExecutionResult{Summary: "done", BranchName: "other", HeadSHA: "0123456789abcdef0123456789abcdef01234567", PullRequestURL: "https://github.com/acme/widgets/pull/1", PullRequestNumber: 1}, domain.AgentDraftURLPolicy{}) {
		t.Fatal("mismatched branch must be rejected")
	}
}

func TestAdapterResultKeepsDisposableGitLabDraftOnAdmittedOrigin(t *testing.T) {
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = "http://gitlab:8929/api/v4"
	submission.Task.Repository = "team/project"
	submission.Task.BranchName = "agent/task-1"
	result := ExecutionResult{
		Summary: "Draft MR created for human review", BranchName: "agent/task-1",
		HeadSHA:        "0123456789abcdef0123456789abcdef01234567",
		PullRequestURL: "http://gitlab:8929/team/project/-/merge_requests/7", PullRequestNumber: 7,
	}
	if validExecutionResult(submission, result, domain.AgentDraftURLPolicy{}) {
		t.Fatal("plaintext GitLab result passed without development opt-in")
	}
	if !validExecutionResult(submission, result, domain.AgentDraftURLPolicy{AllowGitLabHTTP: true}) {
		t.Fatal("local GitLab result was rejected with development opt-in")
	}
	result.PullRequestURL = "http://other-gitlab:8929/team/project/-/merge_requests/7"
	if validExecutionResult(submission, result, domain.AgentDraftURLPolicy{AllowGitLabHTTP: true}) {
		t.Fatal("cross-origin GitLab result was accepted")
	}
}

func TestAdapterResultRejectsUnrelatedGitHubPRLink(t *testing.T) {
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitHub
	submission.Task.APIBaseURL = "https://api.github.com"
	submission.Task.Repository = "acme/widgets"
	submission.Task.BranchName = "agent/task-1"
	result := ExecutionResult{Summary: "draft ready", BranchName: submission.Task.BranchName,
		HeadSHA: "0123456789abcdef0123456789abcdef01234567", PullRequestURL: "https://github.com/acme/widgets/pull/7", PullRequestNumber: 7}
	if !validExecutionResult(submission, result, domain.AgentDraftURLPolicy{}) {
		t.Fatal("matching GitHub PR was rejected")
	}
	result.PatchSHA256 = "invalid"
	result.ChangedFileCount = 1
	result.DiffBytes = 128
	if validExecutionResult(submission, result, domain.AgentDraftURLPolicy{}) {
		t.Fatal("malformed patch evidence was accepted")
	}
	result.PatchSHA256 = strings.Repeat("a", 64)
	if !validExecutionResult(submission, result, domain.AgentDraftURLPolicy{}) {
		t.Fatal("bounded patch evidence was rejected")
	}
	result.VerificationProfileSHA256 = strings.Repeat("b", 64)
	result.VerificationOutputSHA256 = strings.Repeat("c", 64)
	result.VerificationOutputBytes = 42
	if !validExecutionResult(submission, result, domain.AgentDraftURLPolicy{}) {
		t.Fatal("bounded approved verification evidence was rejected")
	}
	result.VerificationOutputSHA256 = ""
	if validExecutionResult(submission, result, domain.AgentDraftURLPolicy{}) {
		t.Fatal("partial verification evidence was accepted")
	}
	result.VerificationOutputSHA256 = strings.Repeat("c", 64)
	for _, wrong := range []string{
		"https://github.com/acme/other/pull/7",
		"https://github.com/acme/widgets/pull/8",
		"https://attacker.example/acme/widgets/pull/7",
	} {
		result.PullRequestURL = wrong
		if validExecutionResult(submission, result, domain.AgentDraftURLPolicy{}) {
			t.Fatalf("unrelated GitHub PR link accepted: %s", wrong)
		}
	}
}

func TestAdapterResultMapsOwnedGitLabDraftToConfiguredPublicOrigin(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = "http://gitlab:8929/api/v4"
	submission.Task.Repository = "team/project"
	submission.Task.BranchName = "agent/test-task"
	response := providerDraftResponse{
		WebURL: "http://gitlab:8929/team/project/-/merge_requests/7", IID: 7,
		Draft: true, State: "opened", SHA: headSHA, SourceBranch: "agent/test-task",
		SourceProjectID: 4, TargetProjectID: 4, Description: agentDraftDescription(submission, headSHA),
	}
	policy := domain.AgentDraftURLPolicy{AllowGitLabHTTP: true, GitLabPublicBaseURL: "http://127.0.0.1:8929", GitLabPublicForAPIBaseURL: submission.Task.APIBaseURL}
	draft, err := response.validated(submission, headSHA, policy)
	if err != nil || draft.URL != "http://127.0.0.1:8929/team/project/-/merge_requests/7" {
		t.Fatalf("public draft URL = %+v, %v", draft, err)
	}
	if !validExecutionResult(submission, ExecutionResult{Summary: "draft ready", BranchName: submission.Task.BranchName, HeadSHA: headSHA, PullRequestURL: draft.URL, PullRequestNumber: 7}, policy) {
		t.Fatal("public draft result was rejected by adapter callback boundary")
	}
	response.WebURL = "http://127.0.0.1:8929/team/other/-/merge_requests/7"
	if _, err := response.validated(submission, headSHA, policy); err == nil {
		t.Fatal("different repository on public origin was accepted")
	}
}

func TestPipelinePublishesDisposableGitLabDraftOverExplicitLocalHTTP(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.EscapedPath() != "/api/v4/projects/team%2Fproject/merge_requests" || request.Header.Get("Authorization") != "Bearer local-token" {
			http.Error(writer, "unexpected GitLab API request", http.StatusBadRequest)
			return
		}
		if request.Method == http.MethodGet {
			if request.URL.Query().Get("source_branch") != "agent/test-task" || request.URL.Query().Get("state") != "opened" {
				http.Error(writer, "GitLab draft lookup used wrong filter", http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(`[]`))
			return
		}
		if request.Method != http.MethodPost {
			http.Error(writer, "unexpected GitLab method", http.StatusBadRequest)
			return
		}
		var body struct {
			Title        string `json:"title"`
			SourceBranch string `json:"source_branch"`
			TargetBranch string `json:"target_branch"`
			Description  string `json:"description"`
			Draft        *bool  `json:"draft"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || !strings.HasPrefix(body.Title, "[Draft]") || body.SourceBranch != "agent/test-task" || body.TargetBranch != "main" || !strings.Contains(body.Description, testAgentDraftMarker()) || body.Draft != nil {
			http.Error(writer, "GitLab draft creation contract was not satisfied", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"web_url":"` + server.URL + `/team/project/-/merge_requests/7","description":` + strconv.Quote(testAgentDraftMarker()) + `,"iid":7,"draft":true,"state":"opened","sha":"` + headSHA + `","source_branch":"agent/test-task","target_branch":"main","source_project_id":4,"target_project_id":4}`))
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = server.URL + "/api/v4"
	submission.Task.Repository = "team/project"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.SourceBaseRef = "main"
	pipeline := Pipeline{GitLabToken: "local-token", GitLabCloneBaseURL: server.URL, GitLabAllowHTTP: true}
	draft, err := pipeline.createDraftPullRequest(context.Background(), submission, headSHA)
	if err != nil || draft.Number != 7 || draft.URL != server.URL+"/team/project/-/merge_requests/7" {
		t.Fatalf("local GitLab draft=%+v err=%v", draft, err)
	}
	pipeline.GitLabAllowHTTP = false
	if _, err := pipeline.createDraftPullRequest(context.Background(), submission, headSHA); err == nil {
		t.Fatal("local GitLab API was accepted without explicit HTTP opt-in")
	}
}

func TestPipelineCreatesGitHubDraftOnlyForExactPushedRevision(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	var creates atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/acme/project/pulls" || request.Header.Get("Authorization") != "Bearer github-token" {
			http.Error(writer, "unexpected GitHub request", http.StatusBadRequest)
			return
		}
		if request.Method == http.MethodGet {
			if request.URL.Query().Get("head") != "acme:agent/test-task" || request.URL.Query().Get("base") != "main" {
				http.Error(writer, "GitHub draft lookup was not scoped", http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(`[]`))
			return
		}
		creates.Add(1)
		var body struct {
			Draft bool   `json:"draft"`
			Head  string `json:"head"`
			Base  string `json:"base"`
			Body  string `json:"body"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || !body.Draft || body.Head != "agent/test-task" || body.Base != "main" || !strings.Contains(body.Body, testAgentDraftMarker()) {
			http.Error(writer, "GitHub draft creation contract was not satisfied", http.StatusBadRequest)
			return
		}
		_, _ = writer.Write([]byte(`{"html_url":"https://github.com/acme/project/pull/7","body":` + strconv.Quote(testAgentDraftMarker()) + `,"number":7,"draft":true,"state":"open","base":{"ref":"main"},"head":{"ref":"agent/test-task","sha":"` + headSHA + `","repo":{"full_name":"acme/project"}}}`))
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitHub
	submission.Task.APIBaseURL = server.URL
	submission.Task.Repository = "acme/project"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.SourceBaseRef = "main"
	pipeline := Pipeline{GitHubToken: "github-token", HTTPClient: server.Client(), GitHubPublicBaseURL: "https://github.com", GitHubPublicForAPIBaseURL: server.URL}
	draft, err := pipeline.createDraftPullRequest(context.Background(), submission, headSHA)
	if err != nil || draft.Number != 7 || creates.Load() != 1 {
		t.Fatalf("GitHub draft=%+v creates=%d err=%v", draft, creates.Load(), err)
	}
}

func TestDraftValidationRejectsChangedGitHubTarget(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitHub
	submission.Task.APIBaseURL = "https://api.github.com"
	submission.Task.Repository = "acme/project"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.SourceBaseRef = "main"
	var draft providerDraftResponse
	draft.HTMLURL = "https://github.com/acme/project/pull/7"
	draft.Body = agentDraftMarker(submission)
	draft.Number = 7
	draft.Draft = true
	draft.State = "open"
	draft.Head.Ref = submission.Task.BranchName
	draft.Head.SHA = headSHA
	draft.Head.Repo.FullName = submission.Task.Repository
	draft.Base.Ref = "release"
	if _, err := draft.validated(submission, headSHA, (Pipeline{}).DraftURLPolicy()); err == nil {
		t.Fatal("GitHub Draft with a changed target branch was accepted")
	}
	draft.Base.Ref = "main"
	if _, err := draft.validated(submission, headSHA, (Pipeline{}).DraftURLPolicy()); err != nil {
		t.Fatalf("unchanged GitHub Draft target was rejected: %v", err)
	}
}

func TestPipelineReconcilesLostGitLabCreateResponseWithoutAnotherMR(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	var lookups, creates atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			if lookups.Add(1) == 1 {
				_, _ = writer.Write([]byte(`[]`))
				return
			}
			_, _ = writer.Write([]byte(`[{"web_url":"` + server.URL + `/team/project/-/merge_requests/7","description":` + strconv.Quote(testAgentDraftMarker()) + `,"iid":7,"draft":true,"state":"opened","sha":"` + headSHA + `","source_branch":"agent/test-task","target_branch":"main","source_project_id":4,"target_project_id":4}]`))
		case http.MethodPost:
			creates.Add(1)
			connection, _, err := writer.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
		default:
			http.Error(writer, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = server.URL + "/api/v4"
	submission.Task.Repository = "team/project"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.SourceBaseRef = "main"
	pipeline := Pipeline{GitLabToken: "local-token", GitLabAllowHTTP: true}
	draft, err := pipeline.createDraftPullRequest(context.Background(), submission, headSHA)
	if err != nil || draft.Number != 7 || creates.Load() != 1 || lookups.Load() != 2 {
		t.Fatalf("lost-response reconciliation draft=%+v creates=%d lookups=%d err=%v", draft, creates.Load(), lookups.Load(), err)
	}
}

func TestPipelineReconcilesGitLabCreateServerErrorAfterSideEffect(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	var lookups, creates atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			if lookups.Add(1) == 1 {
				_, _ = writer.Write([]byte(`[]`))
				return
			}
			_, _ = writer.Write([]byte(`[{"web_url":"` + server.URL + `/team/project/-/merge_requests/7","description":` + strconv.Quote(testAgentDraftMarker()) + `,"iid":7,"draft":true,"state":"opened","sha":"` + headSHA + `","source_branch":"agent/test-task","target_branch":"main","source_project_id":4,"target_project_id":4}]`))
			return
		}
		creates.Add(1)
		http.Error(writer, "creation committed before the response failed", http.StatusBadGateway)
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = server.URL + "/api/v4"
	submission.Task.Repository = "team/project"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.SourceBaseRef = "main"
	draft, err := (Pipeline{GitLabToken: "local-token", GitLabAllowHTTP: true}).createDraftPullRequest(context.Background(), submission, headSHA)
	if err != nil || draft.Number != 7 || creates.Load() != 1 || lookups.Load() != 2 {
		t.Fatalf("server-error reconciliation draft=%+v creates=%d lookups=%d err=%v", draft, creates.Load(), lookups.Load(), err)
	}
}

func TestPipelineReconcilesMalformedGitLabCreateResponseWithoutAnotherMR(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	var lookups, creates atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			if lookups.Add(1) == 1 {
				_, _ = writer.Write([]byte(`[]`))
				return
			}
			_, _ = writer.Write([]byte(`[{"web_url":"` + server.URL + `/team/project/-/merge_requests/7","description":` + strconv.Quote(testAgentDraftMarker()) + `,"iid":7,"draft":true,"state":"opened","sha":"` + headSHA + `","source_branch":"agent/test-task","target_branch":"main","source_project_id":4,"target_project_id":4}]`))
		case http.MethodPost:
			creates.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"web_url":`))
		default:
			http.Error(writer, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = server.URL + "/api/v4"
	submission.Task.Repository = "team/project"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.SourceBaseRef = "main"
	draft, err := (Pipeline{GitLabToken: "local-token", GitLabAllowHTTP: true}).createDraftPullRequest(context.Background(), submission, headSHA)
	if err != nil || draft.Number != 7 || creates.Load() != 1 || lookups.Load() != 2 {
		t.Fatalf("malformed-response reconciliation draft=%+v creates=%d lookups=%d err=%v", draft, creates.Load(), lookups.Load(), err)
	}
}

func TestPipelineReconcilesMalformedGitHubCreateResponseWithoutAnotherPR(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	var lookups, creates atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/acme/project/pulls" || request.Header.Get("Authorization") != "Bearer github-token" {
			http.Error(writer, "unexpected GitHub request", http.StatusBadRequest)
			return
		}
		switch request.Method {
		case http.MethodGet:
			if request.URL.Query().Get("head") != "acme:agent/test-task" || request.URL.Query().Get("base") != "main" {
				http.Error(writer, "GitHub draft lookup was not scoped", http.StatusBadRequest)
				return
			}
			if lookups.Add(1) == 1 {
				_, _ = writer.Write([]byte(`[]`))
				return
			}
			_, _ = writer.Write([]byte(`[{"html_url":"https://github.com/acme/project/pull/7","body":` + strconv.Quote(testAgentDraftMarker()) + `,"number":7,"draft":true,"state":"open","base":{"ref":"main"},"head":{"ref":"agent/test-task","sha":"` + headSHA + `","repo":{"full_name":"acme/project"}}}]`))
		case http.MethodPost:
			creates.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"html_url":`))
		default:
			http.Error(writer, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitHub
	submission.Task.APIBaseURL = server.URL
	submission.Task.Repository = "acme/project"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.SourceBaseRef = "main"
	pipeline := Pipeline{GitHubToken: "github-token", HTTPClient: server.Client(), GitHubPublicBaseURL: "https://github.com", GitHubPublicForAPIBaseURL: server.URL}
	draft, err := pipeline.createDraftPullRequest(context.Background(), submission, headSHA)
	if err != nil || draft.Number != 7 || creates.Load() != 1 || lookups.Load() != 2 {
		t.Fatalf("malformed-response reconciliation draft=%+v creates=%d lookups=%d err=%v", draft, creates.Load(), lookups.Load(), err)
	}
}

func TestPipelineDoesNotReconcileMalformedResponseToUnownedDraft(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	var lookups, creates atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			if lookups.Add(1) == 1 {
				_, _ = writer.Write([]byte(`[]`))
				return
			}
			_, _ = writer.Write([]byte(`[{"web_url":"` + server.URL + `/team/project/-/merge_requests/7","description":"not this task","iid":7,"draft":true,"state":"opened","sha":"` + headSHA + `","source_branch":"agent/test-task","target_branch":"main","source_project_id":4,"target_project_id":4}]`))
		case http.MethodPost:
			creates.Add(1)
			_, _ = writer.Write([]byte(`{"web_url":`))
		default:
			http.Error(writer, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = server.URL + "/api/v4"
	submission.Task.Repository = "team/project"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.SourceBaseRef = "main"
	pipeline := Pipeline{GitLabToken: "local-token", GitLabAllowHTTP: true}
	if _, err := pipeline.createDraftPullRequest(context.Background(), submission, headSHA); err == nil {
		t.Fatal("unowned Draft MR was accepted after malformed create response")
	}
	if _, err := pipeline.createDraftPullRequest(context.Background(), submission, headSHA); err == nil || creates.Load() != 1 {
		t.Fatalf("unowned Draft MR permitted a duplicate create: creates=%d err=%v", creates.Load(), err)
	}
}

func TestPipelineRejectsExistingDraftAtDifferentRevision(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	var creates atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			creates.Add(1)
		}
		_, _ = writer.Write([]byte(`[{"web_url":"` + server.URL + `/team/project/-/merge_requests/7","description":` + strconv.Quote(testAgentDraftMarker()) + `,"iid":7,"draft":true,"state":"opened","sha":"ffffffffffffffffffffffffffffffffffffffff","source_branch":"agent/test-task","target_branch":"main","source_project_id":4,"target_project_id":4}]`))
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = server.URL + "/api/v4"
	submission.Task.Repository = "team/project"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.SourceBaseRef = "main"
	if _, err := (Pipeline{GitLabToken: "local-token", GitLabAllowHTTP: true}).createDraftPullRequest(context.Background(), submission, headSHA); err == nil || creates.Load() != 0 {
		t.Fatalf("stale draft was reused or a duplicate was posted: creates=%d err=%v", creates.Load(), err)
	}
}

func TestPipelineReconcilesOnlyExactExistingDraftWithoutProviderWrites(t *testing.T) {
	const headSHA = "fedcba9876543210fedcba9876543210fedcba98"
	const title = "Recover retained publication"
	const body = "The adapter may restart after creating a Draft. Acceptance: no repeated push or Draft creation."
	for _, scenario := range []struct {
		name      string
		candidate string
		wantFound bool
	}{
		{name: "matching", candidate: "matching", wantFound: true},
		{name: "absent", candidate: "absent"},
		{name: "stale_head", candidate: "stale"},
		{name: "unowned", candidate: "unowned"},
		{name: "ambiguous", candidate: "ambiguous"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var reads, writes atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet {
					writes.Add(1)
					http.Error(writer, "recovery attempted a provider write", http.StatusMethodNotAllowed)
					return
				}
				if request.Header.Get("Authorization") != "Bearer scoped-token" {
					http.Error(writer, "missing scoped token", http.StatusUnauthorized)
					return
				}
				reads.Add(1)
				switch request.URL.EscapedPath() {
				case "/api/v4/projects/team%2Fproject/issues/19":
					_, _ = writer.Write([]byte(`{"state":"opened","title":` + strconv.Quote(title) + `,"description":` + strconv.Quote(body) + `,"labels":[]}`))
				case "/api/v4/projects/team%2Fproject/merge_requests":
					if request.URL.Query().Get("source_branch") != "agent/test-task" || request.URL.Query().Get("target_branch") != "main" {
						http.Error(writer, "lookup scope is invalid", http.StatusBadRequest)
						return
					}
					if scenario.candidate == "absent" {
						_, _ = writer.Write([]byte("[]"))
						return
					}
					submission := Submission{}
					submission.Task.BranchName = "agent/test-task"
					marker := agentDraftMarker(submission)
					candidateSHA := headSHA
					if scenario.candidate == "stale" {
						candidateSHA = strings.Repeat("f", 40)
					}
					if scenario.candidate == "unowned" {
						marker = "someone else's Draft"
					}
					candidate := `{"web_url":` + strconv.Quote(server.URL+"/team/project/-/merge_requests/7") + `,"description":` + strconv.Quote(marker) + `,"iid":7,"draft":true,"state":"opened","sha":` + strconv.Quote(candidateSHA) + `,"source_branch":"agent/test-task","target_branch":"main","source_project_id":4,"target_project_id":4}`
					if scenario.candidate == "ambiguous" {
						_, _ = writer.Write([]byte("[" + candidate + "," + candidate + "]"))
					} else {
						_, _ = writer.Write([]byte("[" + candidate + "]"))
					}
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()
			submission := Submission{}
			submission.Task.Provider = domain.ProviderGitLab
			submission.Task.APIBaseURL = server.URL + "/api/v4"
			submission.Task.Repository = "team/project"
			submission.Task.OriginKind = "issue"
			submission.Task.OriginNumber = 19
			submission.Task.OriginRevision = domain.AgentIssueRevision(domain.ProviderGitLab, submission.Task.APIBaseURL, submission.Task.Repository, 19, title, body)
			submission.Task.BranchName = "agent/test-task"
			submission.Task.SourceBaseRef = "main"
			submission.Limits.DeadlineAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
			checkpoint := PublicationCheckpoint{HeadSHA: headSHA, PatchSHA256: strings.Repeat("a", 64), ChangedFileCount: 1, DiffBytes: 72}
			pipeline := Pipeline{GitLabToken: "scoped-token", GitLabCloneBaseURL: server.URL, GitLabAllowHTTP: true}
			result, err := pipeline.ReconcilePublication(context.Background(), "adapter-recovery", submission, checkpoint)
			if scenario.wantFound {
				if err != nil || result.HeadSHA != headSHA || result.PullRequestNumber != 7 || result.PatchSHA256 != checkpoint.PatchSHA256 {
					t.Fatalf("matching Draft was not recovered: result=%+v err=%v", result, err)
				}
			} else if err == nil {
				t.Fatalf("unsafe or absent Draft was accepted: %+v", result)
			}
			if writes.Load() != 0 || reads.Load() != 2 {
				t.Fatalf("reconciliation performed provider writes or wrong reads: reads=%d writes=%d", reads.Load(), writes.Load())
			}
		})
	}
}

func TestPipelineReconcilesHostedStyleGitHubDraftReadOnly(t *testing.T) {
	const title = "Recover a reviewed change"
	const body = "The adapter restarted after publication. Acceptance: recover only an exact Draft."
	const headSHA = "fedcba9876543210fedcba9876543210fedcba98"
	var reads, writes atomic.Int32
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writes.Add(1)
			http.Error(writer, "no provider write permitted", http.StatusMethodNotAllowed)
			return
		}
		if request.Header.Get("Authorization") != "Bearer scoped-token" {
			http.Error(writer, "missing token", http.StatusUnauthorized)
			return
		}
		reads.Add(1)
		switch request.URL.Path {
		case "/repos/acme/widgets/issues/19":
			_, _ = writer.Write([]byte(`{"state":"open","title":` + strconv.Quote(title) + `,"body":` + strconv.Quote(body) + `,"labels":[]}`))
		case "/repos/acme/widgets/pulls":
			if request.URL.Query().Get("head") != "acme:agent/recovery" || request.URL.Query().Get("base") != "main" {
				http.Error(writer, "wrong branch lookup", http.StatusBadRequest)
				return
			}
			submission := Submission{}
			submission.Task.BranchName = "agent/recovery"
			_, _ = writer.Write([]byte(`[{"html_url":"https://github.com/acme/widgets/pull/7","body":` + strconv.Quote(agentDraftMarker(submission)) + `,"number":7,"draft":true,"state":"open","base":{"ref":"main"},"head":{"ref":"agent/recovery","sha":"` + headSHA + `","repo":{"full_name":"acme/widgets"}}}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitHub
	submission.Task.APIBaseURL = server.URL
	submission.Task.Repository = "acme/widgets"
	submission.Task.OriginKind = "issue"
	submission.Task.OriginNumber = 19
	submission.Task.OriginRevision = domain.AgentIssueRevision(domain.ProviderGitHub, server.URL, "acme/widgets", 19, title, body)
	submission.Task.BranchName = "agent/recovery"
	submission.Task.SourceBaseRef = "main"
	submission.Limits.DeadlineAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	checkpoint := PublicationCheckpoint{HeadSHA: headSHA, PatchSHA256: strings.Repeat("a", 64), ChangedFileCount: 1, DiffBytes: 55}
	pipeline := Pipeline{GitHubToken: "scoped-token", GitHubCloneBaseURL: server.URL, GitHubPublicBaseURL: "https://github.com", GitHubPublicForAPIBaseURL: server.URL, HTTPClient: server.Client()}
	result, err := pipeline.ReconcilePublication(context.Background(), "adapter-recovery", submission, checkpoint)
	if err != nil || result.PullRequestURL != "https://github.com/acme/widgets/pull/7" || result.HeadSHA != headSHA || reads.Load() != 2 || writes.Load() != 0 {
		t.Fatalf("GitHub recovery was not read-only or exact: result=%+v reads=%d writes=%d err=%v", result, reads.Load(), writes.Load(), err)
	}
}

func TestPipelineReconcilesFeedbackDraftOnlyWithExactReadOnlyEvidence(t *testing.T) {
	const previousSHA = "0123456789abcdef0123456789abcdef01234567"
	const pushedSHA = "fedcba9876543210fedcba9876543210fedcba98"
	const instruction = "Add the focused retry regression without changing the existing Draft."
	for _, provider := range []domain.Provider{domain.ProviderGitHub, domain.ProviderGitLab} {
		for _, scenario := range []struct {
			name   string
			accept bool
		}{
			{name: "exact_draft", accept: true},
			{name: "changed_comment"},
			{name: "stale_head"},
			{name: "changed_target"},
			{name: "wrong_review_number"},
			{name: "unowned_draft"},
		} {
			t.Run(string(provider)+"/"+scenario.name, func(t *testing.T) {
				var reads, writes atomic.Int32
				var server *httptest.Server
				handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if request.Method != http.MethodGet {
						writes.Add(1)
						http.Error(writer, "recovery may not write", http.StatusMethodNotAllowed)
						return
					}
					if request.Header.Get("Authorization") != "Bearer scoped-token" {
						http.Error(writer, "missing scoped token", http.StatusUnauthorized)
						return
					}
					reads.Add(1)
					body := "@openreview revise " + instruction
					if scenario.name == "changed_comment" {
						body += " edited"
					}
					sha := pushedSHA
					if scenario.name == "stale_head" {
						sha = strings.Repeat("f", 40)
					}
					targetBranch := "main"
					if scenario.name == "changed_target" {
						targetBranch = "release"
					}
					number := 7
					if scenario.name == "wrong_review_number" {
						number = 8
					}
					markerSubmission := Submission{}
					markerSubmission.Task.BranchName = "agent/recovery"
					marker := agentDraftMarker(markerSubmission)
					if scenario.name == "unowned_draft" {
						marker = "not this agent"
					}
					switch provider {
					case domain.ProviderGitHub:
						switch request.URL.Path {
						case "/repos/acme/widgets/issues/comments/91":
							_, _ = writer.Write([]byte(`{"id":91,"issue_url":` + strconv.Quote(server.URL+"/repos/acme/widgets/issues/7") + `,"user":{"id":55},"body":` + strconv.Quote(body) + `}`))
						case "/repos/acme/widgets/pulls/7":
							_, _ = writer.Write([]byte(`{"html_url":` + strconv.Quote("https://github.com/acme/widgets/pull/"+strconv.Itoa(number)) + `,"body":` + strconv.Quote(marker) + `,"number":` + strconv.Itoa(number) + `,"draft":true,"state":"open","base":{"ref":` + strconv.Quote(targetBranch) + `},"head":{"ref":"agent/recovery","sha":"` + sha + `","repo":{"full_name":"acme/widgets"}}}`))
						default:
							http.NotFound(writer, request)
						}
					case domain.ProviderGitLab:
						switch request.URL.EscapedPath() {
						case "/api/v4/projects/team%2Fproject/merge_requests/7/notes/91":
							_, _ = writer.Write([]byte(`{"id":91,"noteable_type":"MergeRequest","noteable_iid":7,"author":{"id":55},"body":` + strconv.Quote(body) + `}`))
						case "/api/v4/projects/team%2Fproject/merge_requests/7":
							_, _ = writer.Write([]byte(`{"web_url":` + strconv.Quote(server.URL+"/team/project/-/merge_requests/"+strconv.Itoa(number)) + `,"description":` + strconv.Quote(marker) + `,"iid":` + strconv.Itoa(number) + `,"draft":true,"state":"opened","sha":"` + sha + `","source_branch":"agent/recovery","target_branch":` + strconv.Quote(targetBranch) + `,"source_project_id":4,"target_project_id":4}`))
						default:
							http.NotFound(writer, request)
						}
					}
				})
				if provider == domain.ProviderGitHub {
					server = httptest.NewTLSServer(handler)
				} else {
					server = httptest.NewServer(handler)
				}
				defer server.Close()
				digest := sha256.Sum256([]byte(instruction))
				submission := Submission{}
				submission.Task.Provider = provider
				submission.Task.APIBaseURL = server.URL
				submission.Task.Repository = "acme/widgets"
				if provider == domain.ProviderGitLab {
					submission.Task.APIBaseURL += "/api/v4"
					submission.Task.Repository = "team/project"
				}
				submission.Task.OriginKind, submission.Task.OriginNumber, submission.Task.OriginRevision = "pull_request", 7, previousSHA
				submission.Task.BranchName, submission.Task.SourceBaseRef, submission.Task.SourceBaseSHA = "agent/recovery", "agent/recovery", previousSHA
				submission.Task.Feedback = &domain.AgentTaskFeedbackBinding{CommentExternalID: "91", ActorExternalID: "55", InstructionSHA256: hex.EncodeToString(digest[:]), TargetBranch: "main"}
				submission.Limits.DeadlineAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
				checkpoint := PublicationCheckpoint{HeadSHA: pushedSHA, PatchSHA256: strings.Repeat("a", 64), ChangedFileCount: 1, DiffBytes: 42}
				pipeline := Pipeline{HTTPClient: server.Client()}
				if provider == domain.ProviderGitHub {
					pipeline.GitHubToken, pipeline.GitHubCloneBaseURL = "scoped-token", server.URL
					pipeline.GitHubPublicBaseURL, pipeline.GitHubPublicForAPIBaseURL = "https://github.com", server.URL
				} else {
					pipeline.GitLabToken, pipeline.GitLabCloneBaseURL, pipeline.GitLabAllowHTTP = "scoped-token", server.URL, true
				}
				result, err := pipeline.ReconcilePublication(context.Background(), "adapter-feedback-recovery", submission, checkpoint)
				if scenario.accept && (err != nil || result.HeadSHA != pushedSHA || result.PullRequestNumber != 7) {
					t.Fatalf("exact feedback Draft was not recovered: result=%+v err=%v", result, err)
				}
				if !scenario.accept && err == nil {
					t.Fatalf("unsafe feedback Draft was recovered: %+v", result)
				}
				wantReads := int32(2)
				if scenario.name == "changed_comment" {
					wantReads = 1
				}
				if writes.Load() != 0 || reads.Load() != wantReads {
					t.Fatalf("recovery wrote to provider or skipped a required read: reads=%d writes=%d err=%v", reads.Load(), writes.Load(), err)
				}
			})
		}
	}
}

func TestPipelineRejectsUnownedDraftDespiteMatchingBranchAndRevision(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	submission := Submission{}
	submission.Task.Provider = domain.ProviderGitLab
	submission.Task.APIBaseURL = "http://gitlab:8929/api/v4"
	submission.Task.Repository = "team/project"
	submission.Task.BranchName = "agent/test-task"
	response := providerDraftResponse{
		WebURL: "http://gitlab:8929/team/project/-/merge_requests/7", IID: 7,
		Draft: true, State: "opened", SHA: headSHA, SourceBranch: "agent/test-task",
		SourceProjectID: 4, TargetProjectID: 4, Description: "Someone else's change",
	}
	if _, err := response.validated(submission, headSHA, domain.AgentDraftURLPolicy{AllowGitLabHTTP: true}); err == nil {
		t.Fatal("unowned Draft MR was accepted on branch and SHA alone")
	}
	response.Description = agentDraftDescription(submission, headSHA)
	if _, err := response.validated(submission, headSHA, domain.AgentDraftURLPolicy{AllowGitLabHTTP: true}); err != nil {
		t.Fatalf("owned Draft MR was rejected: %v", err)
	}
}

func TestAgentDraftDescriptionSeparatesEvidenceFromUnverifiedClaims(t *testing.T) {
	submission := Submission{AttemptID: "2f4dfa44-ff53-43a9-92dc-a92b9852f57d"}
	submission.Task.OriginKind = "issue"
	submission.Task.OriginNumber = 42
	submission.Task.SourceBaseSHA = "0123456789abcdef0123456789abcdef01234567"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.ExecutorProfile = "codex"
	submission.Plan.Revision = 3
	submission.Plan.SHA256 = strings.Repeat("a", 64)
	body := agentDraftDescription(submission, "fedcba9876543210fedcba9876543210fedcba98", draftEvidence{
		Files: []string{"src/service.go", "src/<script>.go"}, DiffBytes: 381,
	})
	for _, expected := range []string{
		agentDraftMarker(submission), "## Outcome", "## Scope", "2 changed file(s), 381 diff byte(s)",
		"## Risk", "runtime dependencies and severity were not measured", "## Acceptance mapping",
		"## Invariants", "## Verification", "Build, tests, SAST, performance, UI, and migrations were not attested",
		"## Rollout", "## Rollback", "## Provenance", "plan revision 3", "&lt;script&gt;",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("draft description is missing %q: %s", expected, body)
		}
	}
	if strings.Contains(body, "<script>") || strings.Count(body, agentDraftMarker(submission)) != 1 {
		t.Fatal("draft description rendered unsafe file markup or repeated its ownership marker")
	}
}

func TestAgentDraftDescriptionLinksOnlyExactProviderFiles(t *testing.T) {
	const head = "fedcba9876543210fedcba9876543210fedcba98"
	submission := Submission{AttemptID: "2f4dfa44-ff53-43a9-92dc-a92b9852f57d"}
	submission.Task.Provider = domain.ProviderGitHub
	submission.Task.APIBaseURL = "https://api.github.com"
	submission.Task.Repository = "RainLib/open-review-platform"
	submission.Task.OriginKind = "issue"
	submission.Task.OriginNumber = 42
	submission.Task.SourceBaseSHA = "0123456789abcdef0123456789abcdef01234567"
	submission.Task.BranchName = "agent/test-task"
	submission.Task.ExecutorProfile = "codex"
	submission.Plan.Revision = 1
	submission.Plan.SHA256 = strings.Repeat("a", 64)
	body := (Pipeline{}).draftDescription(submission, head, draftEvidence{Files: []string{"src/review #1.go", "docs/removed.md", "../unsafe"}, DeletedFiles: map[string]bool{"docs/removed.md": true}, DiffBytes: 88})
	if !strings.Contains(body, `<a href="https://github.com/RainLib/open-review-platform/blob/`+head+`/src/review%20%231.go"><code>src/review #1.go</code></a>`) {
		t.Fatalf("exact changed-file link is missing: %s", body)
	}
	if !strings.Contains(body, "- <code>../unsafe</code>") || strings.Contains(body, `href="https://github.com/RainLib/open-review-platform/blob/`+head+`/../unsafe`) {
		t.Fatalf("unsafe path was linked instead of shown as plain evidence: %s", body)
	}
	if !strings.Contains(body, `<a href="https://github.com/RainLib/open-review-platform/commit/`+head+`"><code>docs/removed.md</code></a> (deleted; opens commit diff)`) {
		t.Fatalf("deleted file did not link to its exact commit diff: %s", body)
	}
}

func TestDeletedAgentFilesOnlyMarksAbsentCheckoutPaths(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "changed.go"), []byte("package source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleted := deletedAgentFiles(workspace, []string{"src/changed.go", "src/removed.go", "../outside.go"})
	if deleted["src/changed.go"] || !deleted["src/removed.go"] || !deleted["../outside.go"] {
		t.Fatalf("changed/deleted path classification is unsafe: %+v", deleted)
	}
}

func testAgentDraftMarker() string {
	submission := Submission{}
	submission.Task.BranchName = "agent/test-task"
	return agentDraftMarker(submission)
}

func TestIsolatedExecutorEnvironmentDoesNotLeakAdapterOrProviderSecrets(t *testing.T) {
	environment := isolatedExecutorEnvironment("/workspace/home", []string{
		"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "AGENT_TASK_ADAPTER_SECRET=callback-secret",
		"AGENT_ADAPTER_GITHUB_TOKEN=repository-token", "OPENAI_API_KEY=model-secret",
		"AGENT_ADAPTER_CODEX_MODEL_API_KEY=broker-upstream-secret", "HOME=/root",
	})
	joined := strings.Join(environment, "\n")
	for _, forbidden := range []string{"callback-secret", "repository-token", "model-secret", "broker-upstream-secret", "HOME=/root"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("executor environment leaked %q: %s", forbidden, joined)
		}
	}
	for _, required := range []string{"HOME=/workspace/home", "PATH=/usr/local/bin:/usr/bin:/bin", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("executor environment missing %q: %s", required, joined)
		}
	}
}
