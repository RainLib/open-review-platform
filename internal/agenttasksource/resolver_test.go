package agenttasksource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type staticResolver string

func (s staticResolver) Resolve(context.Context, domain.ReviewJob) (string, error) {
	return string(s), nil
}

func TestResolverDoesNotFollowProviderCredentialRedirect(t *testing.T) {
	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL+"/capture")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	task := domain.AgentTask{Provider: domain.ProviderGitLab, APIBaseURL: source.URL + "/api/v4", Repository: "acme/widgets", OriginKind: "issue", OriginNumber: 7, OriginRevision: "frozen-revision"}
	if err := (Resolver{AllowGitLabHTTP: true}).VerifyOrigin(context.Background(), task, "read-token"); err == nil {
		t.Fatal("redirected provider metadata must fail closed")
	}
	if redirected.Load() {
		t.Fatal("provider credential was forwarded to a redirect destination")
	}
}

func TestProviderSourceRetryClassifiesOnlyTransientFailures(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusTooManyRequests)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.WriteHeader(int(status.Load()))
	}))
	defer server.Close()
	resolver := Resolver{HTTPClient: server.Client()}
	for _, tc := range []struct {
		status    int32
		transient bool
		delay     time.Duration
	}{
		{http.StatusTooManyRequests, true, 12 * time.Second},
		{http.StatusServiceUnavailable, true, 12 * time.Second},
		{http.StatusUnauthorized, false, 0},
		{http.StatusForbidden, false, 0},
		{http.StatusNotFound, false, 0},
		{http.StatusTemporaryRedirect, false, 0},
	} {
		status.Store(tc.status)
		var payload map[string]any
		err := resolver.getJSON(context.Background(), server.URL, "read-token", domain.ProviderGitHub, &payload)
		if err == nil {
			t.Fatalf("HTTP %d unexpectedly passed", tc.status)
		}
		if delay, transient := TransientDelay(err); transient != tc.transient || delay != tc.delay {
			t.Fatalf("HTTP %d retry=%t delay=%s error=%v", tc.status, transient, delay, err)
		}
	}
	if sourceRetryAfter("999999999999999999999999") != 0 || sourceRetryAfter("3600") != 15*time.Minute {
		t.Fatal("provider Retry-After was not bounded")
	}
}

func TestProviderSourceTimeoutIsRetryableOnlyWhileTaskIsLive(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 20 * time.Millisecond
	resolver := Resolver{HTTPClient: client}
	var payload map[string]any
	if err := resolver.getJSON(context.Background(), server.URL, "read-token", domain.ProviderGitHub, &payload); err == nil {
		t.Fatal("provider timeout unexpectedly passed")
	} else if _, transient := TransientDelay(err); !transient {
		t.Fatalf("provider timeout was not retryable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := resolver.getJSON(ctx, server.URL, "read-token", domain.ProviderGitHub, &payload); err == nil {
		t.Fatal("expired task unexpectedly passed")
	} else if _, transient := TransientDelay(err); transient {
		t.Fatalf("expired task scheduled another retry: %v", err)
	}
}

func TestProviderSourceBoundsMetadataAndRetriesTruncatedTransport(t *testing.T) {
	oversized := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, maxSourceMetadataBytes+1))
	}))
	defer oversized.Close()
	var payload map[string]any
	err := (Resolver{HTTPClient: oversized.Client()}).getJSON(context.Background(), oversized.URL, "read-token", domain.ProviderGitHub, &payload)
	if err == nil {
		t.Fatal("oversized provider metadata was accepted")
	}
	if _, transient := TransientDelay(err); transient {
		t.Fatalf("oversized provider metadata should not be retried: %v", err)
	}
	truncated := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "200")
		_, _ = w.Write([]byte(`{"default_branch":"main"}`))
	}))
	defer truncated.Close()
	err = (Resolver{HTTPClient: truncated.Client()}).getJSON(context.Background(), truncated.URL, "read-token", domain.ProviderGitHub, &payload)
	if err == nil {
		t.Fatal("truncated provider metadata was accepted")
	}
	if _, transient := TransientDelay(err); !transient {
		t.Fatalf("truncated provider transport should be retryable: %v", err)
	}
	malformed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"default_branch":`))
	}))
	defer malformed.Close()
	err = (Resolver{HTTPClient: malformed.Client()}).getJSON(context.Background(), malformed.URL, "read-token", domain.ProviderGitHub, &payload)
	if err == nil {
		t.Fatal("complete malformed provider metadata was accepted")
	}
	if _, transient := TransientDelay(err); transient {
		t.Fatalf("complete malformed provider metadata should fail closed: %v", err)
	}
}

func TestResolverRequiresHTTPSExceptExplicitDevelopmentGitLab(t *testing.T) {
	if _, err := trustedAPIBase("http://github.example/api/v3", false); err == nil {
		t.Fatal("GitHub source reread accepted plaintext HTTP")
	}
	if _, err := trustedAPIBase("http://gitlab.example/api/v4", false); err == nil {
		t.Fatal("GitLab source reread accepted plaintext HTTP without development opt-in")
	}
	if base, err := trustedAPIBase("http://gitlab.example/api/v4", true); err != nil || base != "http://gitlab.example/api/v4" {
		t.Fatalf("explicit local GitLab HTTP was rejected: base=%q err=%v", base, err)
	}
}

func TestResolverFreezesGitHubDefaultBranchCommit(t *testing.T) {
	title := "Retry loses state"
	body := "Observed: retry loses state. Expected behavior: one persisted result. Acceptance criteria: a regression test checks recovery."
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer read-token" {
			t.Fatalf("authorization = %q", got)
		}
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/7":
			_, _ = w.Write([]byte(`{"state":"open","title":"` + title + `","body":"` + body + `","labels":[{"name":"bug"}]}`))
		case "/repos/acme/widgets":
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		case "/repos/acme/widgets/commits/main":
			_, _ = w.Write([]byte(`{"sha":"0123456789abcdef0123456789abcdef01234567"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	task := domain.AgentTask{ID: uuid.New(), TenantID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "acme/widgets", OriginKind: "issue", OriginNumber: 7}
	task.OriginRevision = domain.AgentIssueRevision(task.Provider, task.APIBaseURL, task.Repository, task.OriginNumber, title, body)
	result, err := (Resolver{Resolver: staticResolver("read-token"), HTTPClient: server.Client()}).Resolve(context.Background(), domain.AgentTaskSourceTarget{Task: task, InstallationExternalID: "42", CredentialRef: "github-app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.BaseRef != "main" || result.BaseSHA != "0123456789abcdef0123456789abcdef01234567" || result.Issue == nil || result.Issue.Revision != task.OriginRevision {
		t.Fatalf("snapshot = %#v", result)
	}
}

func TestResolverFreezesGitLabDefaultBranchCommit(t *testing.T) {
	title := "Queue stalls"
	body := "Observed: queue stalls. Expected behavior: work resumes. Acceptance criteria: a focused regression test checks the recovery path."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects/acme%2Fwidgets/issues/8", "/projects/acme/widgets/issues/8":
			_, _ = w.Write([]byte(`{"state":"opened","title":"` + title + `","description":"` + body + `","labels":["bug"]}`))
		case "/projects/acme%2Fwidgets", "/projects/acme/widgets":
			_, _ = w.Write([]byte(`{"default_branch":"trunk"}`))
		case "/projects/acme%2Fwidgets/repository/branches/trunk", "/projects/acme/widgets/repository/branches/trunk":
			_, _ = w.Write([]byte(`{"commit":{"id":"abcdef0123456789abcdef0123456789abcdef01"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	task := domain.AgentTask{ID: uuid.New(), TenantID: uuid.New(), Provider: domain.ProviderGitLab, APIBaseURL: server.URL, Repository: "acme/widgets", OriginKind: "issue", OriginNumber: 8}
	task.OriginRevision = domain.AgentIssueRevision(task.Provider, task.APIBaseURL, task.Repository, task.OriginNumber, title, body)
	result, err := (Resolver{Resolver: staticResolver("read-token"), AllowGitLabHTTP: true}).Resolve(context.Background(), domain.AgentTaskSourceTarget{Task: task, InstallationExternalID: "installation", CredentialRef: "secret://provider/gitlab-oauth/test"})
	if err != nil {
		t.Fatal(err)
	}
	if result.BaseRef != "trunk" || result.BaseSHA != "abcdef0123456789abcdef0123456789abcdef01" || result.Issue == nil || result.Issue.Revision != task.OriginRevision {
		t.Fatalf("snapshot = %#v", result)
	}
}

func TestResolverRejectsChangedIssueBeforeReadingSource(t *testing.T) {
	readSource := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/issues/7" {
			readSource = true
			return
		}
		_, _ = w.Write([]byte(`{"state":"open","title":"Changed Issue","body":"The contents changed after the task was admitted."}`))
	}))
	defer server.Close()
	task := domain.AgentTask{ID: uuid.New(), TenantID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "acme/widgets", OriginKind: "issue", OriginNumber: 7}
	task.OriginRevision = domain.AgentIssueRevision(task.Provider, task.APIBaseURL, task.Repository, task.OriginNumber, "Original Issue", "Original body")
	if _, err := (Resolver{Resolver: staticResolver("read-token"), HTTPClient: server.Client()}).Resolve(context.Background(), domain.AgentTaskSourceTarget{Task: task, InstallationExternalID: "42", CredentialRef: "github-app"}); err == nil || readSource {
		t.Fatalf("changed Issue must block source admission: err=%v readSource=%t", err, readSource)
	}
}

func TestResolverRejectsRemovedAutomaticAdmissionLabel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"state":"opened","title":"Fix retry state","description":"Observed retries duplicate work. Expected behavior: one result. Acceptance criteria: focused regression test.","labels":[]}`))
	}))
	defer server.Close()
	task := domain.AgentTask{ID: uuid.New(), TenantID: uuid.New(), Provider: domain.ProviderGitLab, APIBaseURL: server.URL, Repository: "acme/widgets", OriginKind: "issue", OriginNumber: 8, RequestedBy: "policy:auto"}
	task.OriginRevision = domain.AgentAutomaticIssueRevision(task.Provider, task.APIBaseURL, task.Repository, task.OriginNumber, "Fix retry state", "Observed retries duplicate work. Expected behavior: one result. Acceptance criteria: focused regression test.", []string{"openreview:implement"})
	if _, err := (Resolver{Resolver: staticResolver("read-token"), AllowGitLabHTTP: true}).Resolve(context.Background(), domain.AgentTaskSourceTarget{Task: task, InstallationExternalID: "installation", CredentialRef: "secret://provider/gitlab-oauth/test"}); err == nil {
		t.Fatal("removed opt-in label must block the automatic candidate")
	}
}

func TestResolverFreezesOnlyTheUnchangedGitHubDraftFeedbackHead(t *testing.T) {
	head := "0123456789abcdef0123456789abcdef01234567"
	taskID := uuid.New()
	instruction := "Handle the missing retry result without widening the Draft PR scope, then add a focused regression test."
	digest := sha256.Sum256([]byte(instruction))
	var commentBody atomic.Value
	commentBody.Store("@openreview revise " + instruction)
	var serverURL string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/pulls/12":
			_, _ = w.Write([]byte(`{"draft":true,"base":{"ref":"main"},"head":{"ref":"agent/` + taskID.String() + `","sha":"` + head + `"}}`))
		case "/repos/acme/widgets/issues/comments/91":
			_, _ = w.Write([]byte(`{"id":91,"user":{"id":55},"issue_url":"` + serverURL + `/repos/acme/widgets/issues/12","body":"` + commentBody.Load().(string) + `"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL
	task := domain.AgentTask{ID: uuid.New(), TenantID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "acme/widgets", OriginKind: "pull_request", OriginNumber: 12, OriginRevision: head, ExecutionBranch: "agent/" + taskID.String()}
	resolver := Resolver{Resolver: staticResolver("read-token"), HTTPClient: server.Client()}
	target := domain.AgentTaskSourceTarget{Task: task, InstallationExternalID: "42", CredentialRef: "github-app", Feedback: &domain.AgentTaskFeedbackBinding{CommentExternalID: "91", ActorExternalID: "55", InstructionSHA256: hex.EncodeToString(digest[:]), TargetBranch: "main"}}
	result, err := resolver.Resolve(context.Background(), target)
	if err != nil || result.BaseRef != task.ExecutionBranch || result.BaseSHA != head || result.Feedback == nil || result.Feedback.Instruction != instruction {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	wrongTarget := *target.Feedback
	wrongTarget.TargetBranch = "release"
	target.Feedback = &wrongTarget
	if _, err := resolver.Resolve(context.Background(), target); err == nil {
		t.Fatal("Draft PR with a changed target branch was accepted")
	}
	target.Feedback.TargetBranch = "main"
	wrongActor := *target.Feedback
	wrongActor.ActorExternalID = "56"
	target.Feedback = &wrongActor
	if _, err := resolver.Resolve(context.Background(), target); err == nil {
		t.Fatal("Draft PR comment by a different actor was accepted")
	}
	target.Feedback.ActorExternalID = "55"
	commentBody.Store("@openreview revise " + instruction + " Edited after admission.")
	if _, err := resolver.Resolve(context.Background(), target); err == nil {
		t.Fatal("edited Draft PR comment was accepted")
	}
}

func TestResolverRejectsGitLabFeedbackWhenDraftHeadMoves(t *testing.T) {
	head := "abcdef0123456789abcdef0123456789abcdef01"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"draft":true,"source_branch":"agent/` + uuid.NewString() + `","sha":"` + head + `"}`))
	}))
	defer server.Close()
	task := domain.AgentTask{ID: uuid.New(), TenantID: uuid.New(), Provider: domain.ProviderGitLab, APIBaseURL: server.URL, Repository: "acme/widgets", OriginKind: "pull_request", OriginNumber: 4, OriginRevision: head, ExecutionBranch: "agent/" + uuid.NewString()}
	if _, err := (Resolver{Resolver: staticResolver("read-token"), AllowGitLabHTTP: true}).Resolve(context.Background(), domain.AgentTaskSourceTarget{Task: task, InstallationExternalID: "project", CredentialRef: "secret://provider/gitlab-oauth/test"}); err == nil {
		t.Fatal("moved draft head must be rejected")
	}
}

func TestResolverBindsGitLabDraftFeedbackNote(t *testing.T) {
	head := "abcdef0123456789abcdef0123456789abcdef01"
	branch := "agent/" + uuid.NewString()
	instruction := "Preserve the existing Draft MR and add a focused regression test for retry recovery."
	digest := sha256.Sum256([]byte(instruction))
	var noteBody atomic.Value
	noteBody.Store("@openreview revise " + instruction)
	var targetBranch atomic.Value
	targetBranch.Store("main")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects/acme/widgets/merge_requests/4":
			_, _ = w.Write([]byte(`{"draft":true,"source_branch":"` + branch + `","target_branch":"` + targetBranch.Load().(string) + `","sha":"` + head + `"}`))
		case "/projects/acme/widgets/merge_requests/4/notes/88":
			_, _ = w.Write([]byte(`{"id":88,"noteable_type":"MergeRequest","noteable_iid":4,"author":{"id":99},"body":"` + noteBody.Load().(string) + `"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	task := domain.AgentTask{ID: uuid.New(), TenantID: uuid.New(), Provider: domain.ProviderGitLab, APIBaseURL: server.URL, Repository: "acme/widgets", OriginKind: "pull_request", OriginNumber: 4, OriginRevision: head, ExecutionBranch: branch}
	target := domain.AgentTaskSourceTarget{Task: task, InstallationExternalID: "project", CredentialRef: "secret://provider/gitlab-oauth/test", Feedback: &domain.AgentTaskFeedbackBinding{CommentExternalID: "88", ActorExternalID: "99", InstructionSHA256: hex.EncodeToString(digest[:]), TargetBranch: "main"}}
	resolver := Resolver{Resolver: staticResolver("read-token"), AllowGitLabHTTP: true}
	snapshot, err := resolver.Resolve(context.Background(), target)
	if err != nil || snapshot.Feedback == nil || snapshot.Feedback.Instruction != instruction {
		t.Fatalf("verified note=%#v error=%v", snapshot.Feedback, err)
	}
	if err := resolver.VerifyOriginWithFeedback(context.Background(), task, target.Feedback, "read-token"); err != nil {
		t.Fatalf("execution preflight rejected unchanged feedback: %v", err)
	}
	targetBranch.Store("release")
	if _, err := resolver.Resolve(context.Background(), target); err == nil {
		t.Fatal("Draft MR with a changed target branch was accepted at source capture")
	}
	if err := resolver.VerifyOriginWithFeedback(context.Background(), task, target.Feedback, "read-token"); err == nil {
		t.Fatal("Draft MR with a changed target branch was accepted at execution preflight")
	}
	targetBranch.Store("main")
	noteBody.Store("@openreview revise " + instruction + " Edited after admission.")
	if _, err := resolver.Resolve(context.Background(), target); err == nil {
		t.Fatal("edited Draft MR note was accepted")
	}
	if err := resolver.VerifyOriginWithFeedback(context.Background(), task, target.Feedback, "read-token"); err == nil {
		t.Fatal("execution preflight accepted edited Draft MR note")
	}
}

func TestVerifiedFeedbackInstructionRejectsChangedCommand(t *testing.T) {
	instruction := "Handle the missing retry result and add a focused regression test without widening scope."
	digest := sha256.Sum256([]byte(instruction))
	binding := &domain.AgentTaskFeedbackBinding{CommentExternalID: "91", ActorExternalID: "55", InstructionSHA256: hex.EncodeToString(digest[:])}
	if got, err := verifiedFeedbackInstruction("@openreview revise "+instruction, binding); err != nil || got != instruction {
		t.Fatalf("verified feedback=%q error=%v", got, err)
	}
	for _, changed := range []string{"@openreview revise " + instruction + " edited", "@openreview review", "deleted"} {
		if _, err := verifiedFeedbackInstruction(changed, binding); err == nil {
			t.Fatalf("changed feedback %q was accepted", changed)
		}
	}
}
