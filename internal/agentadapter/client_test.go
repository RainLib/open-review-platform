package agentadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestClientSubmitsOnlyImmutableHandoff(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	deadline := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Second)
	installationID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/open-review/tasks" || !Verify(secret, r.Header.Get(HeaderTimestamp), r.Header.Get(HeaderSignature), body, time.Now(), time.Minute) {
			t.Fatal("adapter request did not carry a valid signature")
		}
		var submission Submission
		if err := json.Unmarshal(body, &submission); err != nil || submission.Limits.MaxAttempts != 1 || submission.Limits.MaxExecutionSeconds != 1200 || submission.Limits.DeadlineAt != deadline.Format(time.RFC3339Nano) || submission.Task.ExecutorProfile != "codex" || submission.Task.SourceBaseRef != "main" || submission.Task.SourceBaseSHA != "0123456789abcdef0123456789abcdef01234567" || submission.Task.InstallationID != installationID.String() || (submission.Task.OriginKind == "pull_request" && (submission.Task.Feedback == nil || submission.Task.Feedback.CommentExternalID != "91")) {
			t.Fatalf("adapter execution envelope=%#v error=%v", submission.Limits, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":"adapter-job-1"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, secret, server.URL+"/v1/agent-adapter/events", time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	taskID := uuid.New()
	target := domain.AgentTaskAttemptTarget{Attempt: domain.AgentTaskAttempt{ID: uuid.New(), DeadlineAt: &deadline}, Task: domain.AgentTask{ID: taskID, InstallationID: installationID, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "owner/repository", OriginKind: "issue", OriginNumber: 7, OriginRevision: "issue-revision", ExecutorProfile: "codex", SourceState: "ready", SourceBaseRef: "main", SourceBaseSHA: "0123456789abcdef0123456789abcdef01234567", MaxAttempts: 1, MaxExecutionSeconds: 1200}, Plan: domain.AgentTaskPlan{ID: uuid.New(), Revision: 1, PlanSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Summary: "Use the smallest bounded change and focused regression coverage."}}
	jobID, err := client.Submit(context.Background(), target)
	if err != nil || jobID != "adapter-job-1" {
		t.Fatalf("jobID=%q err=%v", jobID, err)
	}
	target.Task.InstallationID = uuid.Nil
	if _, err := client.Submit(context.Background(), target); err == nil {
		t.Fatal("an unbound installation was submitted to the adapter")
	}
	target.Task.InstallationID = installationID
	target.Task.OriginKind = "pull_request"
	target.Task.OriginRevision = "0123456789abcdef0123456789abcdef01234567"
	if _, err := client.Submit(context.Background(), target); err == nil {
		t.Fatal("Draft feedback was submitted without its frozen comment binding")
	}
	digest := sha256.Sum256([]byte("Address the verified Draft feedback without widening scope."))
	target.Feedback = &domain.AgentTaskFeedbackBinding{CommentExternalID: "91", ActorExternalID: "55", InstructionSHA256: hex.EncodeToString(digest[:]), TargetBranch: "main"}
	if _, err := client.Submit(context.Background(), target); err != nil {
		t.Fatalf("signed Draft feedback handoff: %v", err)
	}
}

func TestClientProbesOnlySignedAdapterReachability(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	callback := "https://control.example.invalid/v1/agent-adapter/events"
	service, err := NewService(ServiceConfig{Secret: secret, CallbackURL: callback, Executor: fakeExecution{}, ReceiptDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	client, err := NewClient(server.URL, secret, callback, time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Probe(context.Background()); err != nil {
		t.Fatalf("signed read-only probe: %v", err)
	}
	wrongSecret, err := NewClient(server.URL, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", callback, time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := wrongSecret.Probe(context.Background()); err == nil {
		t.Fatal("adapter accepted a probe signed by another secret")
	}
	unsigned, err := http.Post(server.URL+"/v1/open-review/health", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	unsigned.Body.Close()
	if unsigned.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned probe status=%d, want 401", unsigned.StatusCode)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Probe(context.Background()); err == nil {
		t.Fatal("closing adapter still reported reachable")
	}
}

func TestClientCancelsOnlyTheExactAttemptAndAdapterJob(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	attemptID, taskID := uuid.New(), uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		wantPath := "/v1/open-review/tasks/adapter-job-1/cancel"
		if r.Method != http.MethodPost || r.URL.Path != wantPath || !Verify(secret, r.Header.Get(HeaderTimestamp), r.Header.Get(HeaderSignature), body, time.Now(), time.Minute) || string(body) != `{"attempt_id":"`+attemptID.String()+`"}` {
			t.Fatalf("unexpected cancellation method=%s path=%s body=%s", r.Method, r.URL.Path, body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, secret, server.URL+"/v1/agent-adapter/events", time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Cancel(context.Background(), domain.AgentTaskCancellationRequest{TaskID: taskID, AttemptID: attemptID, AdapterJobID: "adapter-job-1"}); err != nil {
		t.Fatal(err)
	}
}

func TestClientStartsOnlyTheAttachedAttemptAndAdapterJob(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	attemptID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/open-review/tasks/adapter-job-1/start" || !Verify(secret, r.Header.Get(HeaderTimestamp), r.Header.Get(HeaderSignature), body, time.Now(), time.Minute) || string(body) != `{"attempt_id":"`+attemptID.String()+`"}` {
			t.Fatalf("unexpected start method=%s path=%s body=%s", r.Method, r.URL.Path, body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, secret, server.URL+"/v1/agent-adapter/events", time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background(), attemptID, "adapter-job-1"); err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background(), uuid.Nil, "adapter-job-1"); err == nil {
		t.Fatal("nil attempt must not start a job")
	}
}

func TestClientDoesNotForwardSignedStartAcrossRedirect(t *testing.T) {
	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL+"/capture")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client, err := NewClient(source.URL, "0123456789abcdef0123456789abcdef", source.URL+"/v1/agent-adapter/events", time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background(), uuid.New(), "adapter-job-1"); err == nil {
		t.Fatal("redirected signed start should fail closed")
	}
	if redirected.Load() {
		t.Fatal("signed start request was forwarded to another endpoint")
	}
}
