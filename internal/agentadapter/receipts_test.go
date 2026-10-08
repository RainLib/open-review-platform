package agentadapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// A killed adapter cannot run Close. Exercise the on-disk start claim through
// an actual process death, then prove a replacement reports interruption
// without replaying the coding executor or consuming another start claim.
func TestAdapterAbruptProcessCrashRecovery(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	if os.Getenv("OPENREVIEW_ADAPTER_CRASH_CHILD") == "1" {
		callbackURL := os.Getenv("OPENREVIEW_ADAPTER_CRASH_CALLBACK_URL")
		executor := &blockingExecution{started: make(chan struct{})}
		service, err := NewService(ServiceConfig{
			Secret: secret, CallbackURL: callbackURL + "/events", StartGateURL: callbackURL + "/starts",
			AllowHTTP: true, ReceiptDir: os.Getenv("OPENREVIEW_ADAPTER_CRASH_RECEIPT_DIR"),
			Executor: executor, HeartbeatEvery: time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(service.Handler())
		attemptID := uuid.New()
		jobID := submitAdapterRequest(t, secret, server.URL+"/v1/open-review/tasks", testSubmission(attemptID, uuid.New(), callbackURL+"/events"))
		startAdapterRequest(t, secret, server.URL, jobID, attemptID)
		select {
		case <-executor.started:
		case <-time.After(5 * time.Second):
			t.Fatal("coding executor did not start before the crash")
		}
		fmt.Fprintf(os.Stdout, "READY %s %s\n", attemptID, jobID)
		time.Sleep(time.Hour)
		return
	}

	var startClaims atomic.Int32
	events := make(chan domain.AgentTaskAdapterEvent, 2)
	callback := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/starts" {
			startClaims.Add(1)
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		var event domain.AgentTaskAdapterEvent
		if err := json.NewDecoder(request.Body).Decode(&event); err == nil && event.Kind != "heartbeat" {
			events <- event
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()
	storeDir := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestAdapterAbruptProcessCrashRecovery$")
	command.Env = append(os.Environ(),
		"OPENREVIEW_ADAPTER_CRASH_CHILD=1",
		"OPENREVIEW_ADAPTER_CRASH_CALLBACK_URL="+callback.URL,
		"OPENREVIEW_ADAPTER_CRASH_RECEIPT_DIR="+storeDir,
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- line
	}()
	var fields []string
	select {
	case line := <-ready:
		fields = strings.Fields(line)
	case <-time.After(10 * time.Second):
		t.Fatalf("adapter subprocess did not reach execution: %s", stderr.String())
	}
	if len(fields) != 3 || fields[0] != "READY" {
		t.Fatalf("adapter subprocess failed before execution: %q %s", fields, stderr.String())
	}
	attemptID, err := uuid.Parse(fields[1])
	if err != nil {
		t.Fatal(err)
	}
	jobID := fields[2]
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("adapter subprocess was not abruptly terminated")
	}

	replacementExecutor := &blockingExecution{started: make(chan struct{})}
	service, err := NewService(ServiceConfig{
		Secret: secret, CallbackURL: callback.URL + "/events", StartGateURL: callback.URL + "/starts",
		AllowHTTP: true, ReceiptDir: storeDir, Executor: replacementExecutor, HeartbeatEvery: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.Recover(context.Background())
	select {
	case event := <-events:
		if event.AttemptID != attemptID || event.AdapterJobID != jobID || event.Kind != "needs_attention" || event.ErrorCode != "agent_adapter_interrupted" {
			t.Fatalf("unexpected crash recovery event: %+v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("crashed adapter start was not reported to the control plane")
	}
	if startClaims.Load() != 1 {
		t.Fatalf("recovery consumed another start claim: %d", startClaims.Load())
	}
	adapter := httptest.NewServer(service.Handler())
	defer adapter.Close()
	controlBody, _ := json.Marshal(map[string]string{"attempt_id": attemptID.String()})
	request, err := http.NewRequest(http.MethodPost, adapter.URL+"/v1/open-review/tasks/"+jobID+"/start", bytes.NewReader(controlBody))
	if err != nil {
		t.Fatal(err)
	}
	timestamp := strconvUnix(time.Now())
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderSignature, Sign(secret, timestamp, controlBody))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict || startClaims.Load() != 1 {
		t.Fatalf("recovered start was not fenced: HTTP %d, claims %d", response.StatusCode, startClaims.Load())
	}
	job := service.byJob[jobID]
	if job == nil || !job.delivered || job.status != "terminal" {
		t.Fatalf("recovered receipt was not durably completed: %+v", job)
	}
	select {
	case <-replacementExecutor.started:
		t.Fatal("replacement adapter relaunched a possibly completed coding executor")
	default:
	}
}

func TestCancelledRecoveryRetainsEveryInterruptedJobForRetry(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	events := make(chan domain.AgentTaskAdapterEvent, 2)
	callback := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var event domain.AgentTaskAdapterEvent
		if err := json.NewDecoder(request.Body).Decode(&event); err == nil {
			events <- event
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()
	config := ServiceConfig{
		Secret: secret, CallbackURL: callback.URL + "/events", StartGateURL: callback.URL + "/starts",
		AllowHTTP: true, ReceiptDir: t.TempDir(), Executor: fakeExecution{},
	}
	first, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	adapter := httptest.NewServer(first.Handler())
	jobIDs := make([]string, 0, 2)
	for range 2 {
		attemptID := uuid.New()
		jobID := submitAdapterRequest(t, secret, adapter.URL+"/v1/open-review/tasks", testSubmission(attemptID, uuid.New(), callback.URL+"/events"))
		job := first.byJob[jobID]
		job.stateMu.Lock()
		job.status = "started"
		err := first.persistJob(job)
		job.stateMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		jobIDs = append(jobIDs, jobID)
	}
	adapter.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	restarted.Recover(cancelled)
	for _, jobID := range jobIDs {
		job := restarted.byJob[jobID]
		if job == nil || job.status != "terminal" || job.terminal == nil || job.terminal.DeliveryID != "adapter:"+jobID+":interrupted" || job.delivered {
			t.Fatalf("cancelled recovery stranded interrupted job %s: %+v", jobID, job)
		}
	}
	restarted.RetryPending(context.Background())
	seen := make(map[string]bool, len(jobIDs))
	for range jobIDs {
		select {
		case event := <-events:
			if event.Kind != "needs_attention" || event.ErrorCode != "agent_adapter_interrupted" || seen[event.AdapterJobID] {
				t.Fatalf("unexpected recovered callback: %+v", event)
			}
			seen[event.AdapterJobID] = true
		case <-time.After(time.Second):
			t.Fatal("interrupted callback was not retried")
		}
	}
	for _, jobID := range jobIDs {
		if !seen[jobID] || !restarted.byJob[jobID].delivered {
			t.Fatalf("interrupted job %s was not durably delivered", jobID)
		}
	}
}

func TestReservedAdapterJobKeepsItsIdentityAfterRestart(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	callback := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()
	dir := t.TempDir()
	config := ServiceConfig{Secret: secret, CallbackURL: callback.URL, StartGateURL: callback.URL + "/starts", ReceiptDir: dir, Executor: fakeExecution{wait: true}, HeartbeatEvery: time.Hour}
	first, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	adapter := httptest.NewServer(first.Handler())
	attemptID := uuid.New()
	submission := testSubmission(attemptID, uuid.New(), callback.URL)
	jobID := submitAdapterRequest(t, secret, adapter.URL+"/v1/open-review/tasks", submission)
	adapter.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	second.callbackClient = callback.Client()
	adapter = httptest.NewServer(second.Handler())
	defer adapter.Close()
	if replayID := submitAdapterRequest(t, secret, adapter.URL+"/v1/open-review/tasks", submission); replayID != jobID {
		t.Fatalf("reserved job changed identity after restart: %q != %q", replayID, jobID)
	}
	startAdapterRequest(t, secret, adapter.URL, jobID, attemptID)
	if second.byJob[jobID].status != "started" {
		t.Fatal("recovered reservation did not consume its original start gate")
	}
}

func TestStartedAdapterJobNeverReexecutesAfterRestart(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	events := make(chan domain.AgentTaskAdapterEvent, 2)
	callback := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/starts" {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		var event domain.AgentTaskAdapterEvent
		if err := json.NewDecoder(request.Body).Decode(&event); err == nil && event.Kind != "heartbeat" {
			events <- event
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()
	dir := t.TempDir()
	executor := &blockingExecution{started: make(chan struct{}), done: make(chan struct{})}
	config := ServiceConfig{Secret: secret, CallbackURL: callback.URL, StartGateURL: callback.URL + "/starts", ReceiptDir: dir, Executor: executor, HeartbeatEvery: time.Hour}
	first, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	first.callbackClient = callback.Client()
	adapter := httptest.NewServer(first.Handler())
	attemptID := uuid.New()
	jobID := submitAdapterRequest(t, secret, adapter.URL+"/v1/open-review/tasks", testSubmission(attemptID, uuid.New(), callback.URL))
	startAdapterRequest(t, secret, adapter.URL, jobID, attemptID)
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("first executor did not start")
	}
	adapter.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.done:
	case <-time.After(time.Second):
		t.Fatal("first executor did not stop on adapter shutdown")
	}
	secondExecutor := &blockingExecution{started: make(chan struct{})}
	config.Executor = secondExecutor
	second, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	second.callbackClient = callback.Client()
	second.Recover(context.Background())
	select {
	case event := <-events:
		if event.Kind != "needs_attention" || event.ErrorCode != "agent_adapter_interrupted" || event.AdapterJobID != jobID {
			t.Fatalf("unexpected restart event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupted adapter job was not reported")
	}
	adapter = httptest.NewServer(second.Handler())
	defer adapter.Close()
	controlBody, _ := json.Marshal(map[string]string{"attempt_id": attemptID.String()})
	request, _ := http.NewRequest(http.MethodPost, adapter.URL+"/v1/open-review/tasks/"+jobID+"/start", bytes.NewReader(controlBody))
	timestamp := strconvUnix(time.Now())
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderSignature, Sign(secret, timestamp, controlBody))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("recovered started job status=%d, want 409", response.StatusCode)
	}
	select {
	case <-secondExecutor.started:
		t.Fatal("recovered started job relaunched a coding executor")
	default:
	}
}

type fixturePublicationReconciler struct {
	checkpoint   *PublicationCheckpoint
	result       ExecutionResult
	err          error
	reconcileRun atomic.Int32
	executeRun   atomic.Int32
}

func (fixture *fixturePublicationReconciler) Execute(ctx context.Context, _ string, _ Submission) (ExecutionResult, error) {
	fixture.executeRun.Add(1)
	if fixture.checkpoint != nil {
		if err := retainPublicationCheckpoint(ctx, *fixture.checkpoint); err != nil {
			return ExecutionResult{}, err
		}
		return ExecutionResult{}, fmt.Errorf("provider draft confirmation unavailable")
	}
	return ExecutionResult{}, fmt.Errorf("recovery must not run a coding executor")
}

func TestPublicationConfirmationFailureReadsOnceWithoutExecutingAgain(t *testing.T) {
	for _, scenario := range []string{"exact", "changed head", "changed criteria", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			terminal := make(chan domain.AgentTaskAdapterEvent, 1)
			callback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var event domain.AgentTaskAdapterEvent
				if json.NewDecoder(r.Body).Decode(&event) == nil && (event.Kind == "completed" || event.Kind == "needs_attention") {
					terminal <- event
				}
				w.WriteHeader(204)
			}))
			defer callback.Close()
			checkpoint := PublicationCheckpoint{HeadSHA: strings.Repeat("a", 40), PatchSHA256: strings.Repeat("b", 64), ChangedFileCount: 1, DiffBytes: 100,
				VerificationProfileSHA256: strings.Repeat("c", 64), VerificationOutputSHA256: strings.Repeat("d", 64), VerificationOutputBytes: 42,
				VerificationCriteria: []domain.AgentCriterionResult{{Criterion: "Keep original evidence", Status: "passed", Evidence: "Independent check passed"}}}
			result := ExecutionResult{Summary: "Confirmed existing Draft", HeadSHA: checkpoint.HeadSHA, PullRequestURL: "https://github.com/acme/widgets/pull/7", PullRequestNumber: 7,
				PatchSHA256: checkpoint.PatchSHA256, ChangedFileCount: checkpoint.ChangedFileCount, DiffBytes: checkpoint.DiffBytes,
				VerificationProfileSHA256: checkpoint.VerificationProfileSHA256, VerificationOutputSHA256: checkpoint.VerificationOutputSHA256,
				VerificationOutputBytes: checkpoint.VerificationOutputBytes, VerificationCriteria: checkpoint.VerificationCriteria}
			fixture := &fixturePublicationReconciler{checkpoint: &checkpoint, result: result}
			if scenario == "changed head" {
				fixture.result.HeadSHA = strings.Repeat("e", 40)
			}
			if scenario == "changed criteria" {
				fixture.result.VerificationCriteria = []domain.AgentCriterionResult{{Criterion: "Unapproved replacement", Status: "passed", Evidence: "Other check passed"}}
			}
			if scenario == "unavailable" {
				fixture.err = fmt.Errorf("provider unavailable")
			}
			config := ServiceConfig{Secret: "0123456789abcdef0123456789abcdef", CallbackURL: callback.URL + "/events", StartGateURL: callback.URL + "/starts", ReceiptDir: t.TempDir(), Executor: fixture, HeartbeatEvery: time.Hour}
			service, err := NewService(config)
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			service.callbackClient = callback.Client()
			adapter := httptest.NewServer(service.Handler())
			defer adapter.Close()
			submission := testSubmission(uuid.New(), uuid.New(), config.CallbackURL)
			fixture.result.BranchName = submission.Task.BranchName
			jobID := submitAdapterRequest(t, config.Secret, adapter.URL+"/v1/open-review/tasks", submission)
			job := service.byJob[jobID]
			job.stateMu.Lock()
			job.status = "started"
			job.stateMu.Unlock()
			service.active.Add(1)
			service.run(context.Background(), job)
			select {
			case event := <-terminal:
				want := "needs_attention"
				if scenario == "exact" {
					want = "completed"
				}
				if event.Kind != want {
					t.Fatalf("unsafe confirmation: %+v", event)
				}
			case <-time.After(time.Second):
				t.Fatal("publication confirmation never completed")
			}
			if fixture.executeRun.Load() != 1 || fixture.reconcileRun.Load() != 1 {
				t.Fatal("coding replayed or unbounded provider confirmation")
			}
		})
	}
}

func (fixture *fixturePublicationReconciler) ReconcilePublication(context.Context, string, Submission, PublicationCheckpoint) (ExecutionResult, error) {
	fixture.reconcileRun.Add(1)
	return fixture.result, fixture.err
}

func TestRecoveredPublicationCompletesOnlyWithExactEvidence(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	for _, scenario := range []struct {
		name       string
		feedback   bool
		changedSHA bool
		probeError bool
		wantKind   string
	}{
		{name: "matching_draft", wantKind: "completed"},
		{name: "matching_feedback_draft", feedback: true, wantKind: "completed"},
		{name: "changed_head", changedSHA: true, wantKind: "needs_attention"},
		{name: "provider_unavailable", probeError: true, wantKind: "needs_attention"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			events := make(chan domain.AgentTaskAdapterEvent, 2)
			callback := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var event domain.AgentTaskAdapterEvent
				if request.URL.Path == "/events" && json.NewDecoder(request.Body).Decode(&event) == nil {
					events <- event
				}
				writer.WriteHeader(http.StatusNoContent)
			}))
			defer callback.Close()
			config := ServiceConfig{Secret: secret, CallbackURL: callback.URL + "/events", StartGateURL: callback.URL + "/starts", ReceiptDir: t.TempDir(), Executor: fakeExecution{}}
			first, err := NewService(config)
			if err != nil {
				t.Fatal(err)
			}
			adapter := httptest.NewServer(first.Handler())
			attemptID := uuid.New()
			submission := testSubmission(attemptID, uuid.New(), config.CallbackURL)
			if scenario.feedback {
				submission.Task.OriginKind = "pull_request"
				submission.Task.OriginRevision = submission.Task.SourceBaseSHA
				submission.Task.SourceBaseRef = submission.Task.BranchName
				submission.Task.Feedback = &domain.AgentTaskFeedbackBinding{CommentExternalID: "91", ActorExternalID: "55", InstructionSHA256: strings.Repeat("a", 64), TargetBranch: "main"}
			}
			jobID := submitAdapterRequest(t, secret, adapter.URL+"/v1/open-review/tasks", submission)
			adapter.Close()
			checkpoint := PublicationCheckpoint{HeadSHA: "fedcba9876543210fedcba9876543210fedcba98", PatchSHA256: strings.Repeat("a", 64), ChangedFileCount: 2, DiffBytes: 128, VerificationProfileSHA256: strings.Repeat("b", 64), VerificationOutputSHA256: strings.Repeat("c", 64), VerificationOutputBytes: 42}
			job := first.byJob[jobID]
			job.stateMu.Lock()
			job.status, job.publication = "started", &checkpoint
			err = first.persistJob(job)
			job.stateMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			result := ExecutionResult{
				Summary: "Recovered a matching provider Draft.", BranchName: submission.Task.BranchName,
				HeadSHA: checkpoint.HeadSHA, PullRequestURL: "https://github.com/acme/widgets/pull/7", PullRequestNumber: 7,
				PatchSHA256: checkpoint.PatchSHA256, ChangedFileCount: checkpoint.ChangedFileCount, DiffBytes: checkpoint.DiffBytes,
				VerificationProfileSHA256: checkpoint.VerificationProfileSHA256, VerificationOutputSHA256: checkpoint.VerificationOutputSHA256, VerificationOutputBytes: checkpoint.VerificationOutputBytes,
			}
			if scenario.changedSHA {
				result.HeadSHA = strings.Repeat("b", 40)
			}
			reconciler := &fixturePublicationReconciler{result: result}
			if scenario.probeError {
				reconciler.err = fmt.Errorf("provider is unavailable")
			}
			config.Executor = reconciler
			second, err := NewService(config)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			second.callbackClient = callback.Client()
			second.Recover(context.Background())
			select {
			case event := <-events:
				if event.Kind != scenario.wantKind || event.AdapterJobID != jobID || event.AttemptID != attemptID {
					t.Fatalf("unexpected recovery event: %+v", event)
				}
				if scenario.wantKind == "completed" && (event.HeadSHA != checkpoint.HeadSHA || event.DeliveryID != "adapter:"+jobID+":reconciled" || event.VerificationProfileSHA256 != checkpoint.VerificationProfileSHA256 || event.VerificationOutputSHA256 != checkpoint.VerificationOutputSHA256 || event.VerificationOutputBytes != checkpoint.VerificationOutputBytes) {
					t.Fatalf("recovered completion lost exact evidence: %+v", event)
				}
				if scenario.wantKind == "needs_attention" && (event.ErrorCode != "agent_publication_unverified" || event.HeadSHA != "" || event.PullRequestURL != "") {
					t.Fatalf("unverified publication leaked a success result: %+v", event)
				}
			case <-time.After(time.Second):
				t.Fatal("recovered publication was not reported")
			}
			if reconciler.reconcileRun.Load() != 1 || reconciler.executeRun.Load() != 0 {
				t.Fatalf("recovery executed code or skipped provider read: reconcile=%d execute=%d", reconciler.reconcileRun.Load(), reconciler.executeRun.Load())
			}
		})
	}
}

func TestPendingTerminalReceiptReplaysOnceAfterRestart(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	var deliveries atomic.Int32
	callback := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		deliveries.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()
	dir := t.TempDir()
	config := ServiceConfig{Secret: secret, CallbackURL: callback.URL, StartGateURL: callback.URL + "/starts", ReceiptDir: dir, Executor: fakeExecution{}}
	first, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	adapter := httptest.NewServer(first.Handler())
	attemptID := uuid.New()
	jobID := submitAdapterRequest(t, secret, adapter.URL+"/v1/open-review/tasks", testSubmission(attemptID, uuid.New(), callback.URL))
	adapter.Close()
	job := first.byJob[jobID]
	job.stateMu.Lock()
	job.status = "terminal"
	job.terminal = &domain.AgentTaskAdapterEvent{AttemptID: attemptID, AdapterJobID: jobID, DeliveryID: "adapter:" + jobID + ":failed", Kind: "needs_attention", ErrorCode: "fixture_interrupted", Summary: "A retained terminal event needs delivery."}
	err = first.persistJob(job)
	job.stateMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	second.callbackClient = callback.Client()
	second.Recover(context.Background())
	if deliveries.Load() != 1 {
		t.Fatalf("terminal event replay count=%d, want 1", deliveries.Load())
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	third.callbackClient = callback.Client()
	third.Recover(context.Background())
	if deliveries.Load() != 1 {
		t.Fatalf("delivered terminal event replayed again: count=%d", deliveries.Load())
	}
}

func TestPendingTerminalReceiptRetriesWithoutRestart(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	var deliveries atomic.Int32
	callback := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		deliveries.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()
	config := ServiceConfig{Secret: secret, CallbackURL: callback.URL, StartGateURL: callback.URL + "/starts", ReceiptDir: t.TempDir(), Executor: fakeExecution{}}
	service, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.callbackClient = callback.Client()
	adapter := httptest.NewServer(service.Handler())
	defer adapter.Close()
	attemptID := uuid.New()
	jobID := submitAdapterRequest(t, secret, adapter.URL+"/v1/open-review/tasks", testSubmission(attemptID, uuid.New(), callback.URL))
	job := service.byJob[jobID]
	event := domain.AgentTaskAdapterEvent{AttemptID: attemptID, AdapterJobID: jobID, DeliveryID: "adapter:" + jobID + ":failed", Kind: "needs_attention", ErrorCode: "fixture_failure", Summary: "The terminal callback must retry."}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	service.deliverTerminal(cancelled, job, event)
	if job.terminal == nil || job.delivered || deliveries.Load() != 0 {
		t.Fatal("failed terminal callback was not retained for a later in-process retry")
	}
	service.RetryPending(context.Background())
	if !job.delivered || deliveries.Load() != 1 {
		t.Fatalf("pending callback was not delivered once: delivered=%t count=%d", job.delivered, deliveries.Load())
	}
	service.RetryPending(context.Background())
	if deliveries.Load() != 1 {
		t.Fatalf("delivered callback was sent again: count=%d", deliveries.Load())
	}
}

func TestExpiredLeaseTerminalCallbackIsNotRetried(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	var calls atomic.Int32
	callback := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusConflict)
	}))
	defer callback.Close()
	config := ServiceConfig{Secret: secret, CallbackURL: callback.URL, StartGateURL: callback.URL + "/starts", ReceiptDir: t.TempDir(), Executor: fakeExecution{}}
	service, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.callbackClient = callback.Client()
	adapter := httptest.NewServer(service.Handler())
	defer adapter.Close()
	attemptID := uuid.New()
	jobID := submitAdapterRequest(t, secret, adapter.URL+"/v1/open-review/tasks", testSubmission(attemptID, uuid.New(), callback.URL))
	job := service.byJob[jobID]
	service.deliverTerminal(context.Background(), job, domain.AgentTaskAdapterEvent{AttemptID: attemptID, AdapterJobID: jobID, DeliveryID: "adapter:" + jobID + ":failed", Kind: "needs_attention", ErrorCode: "fixture_failure", Summary: "The control-plane lease is gone."})
	if !job.rejected || calls.Load() != 1 {
		t.Fatalf("explicit lease rejection was not retained: rejected=%t calls=%d", job.rejected, calls.Load())
	}
	service.RetryPending(context.Background())
	if calls.Load() != 1 {
		t.Fatalf("stale callback was retried after a durable 409: calls=%d", calls.Load())
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.callbackClient = callback.Client()
	restarted.Recover(context.Background())
	restarted.RetryPending(context.Background())
	if calls.Load() != 1 {
		t.Fatalf("stale callback was retried after restart: calls=%d", calls.Load())
	}
}

func TestReceiptStoreFailsClosedOnCorruptionAndConcurrentOwner(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	dir := t.TempDir()
	config := ServiceConfig{Secret: secret, CallbackURL: "https://control.example.test/v1/agent-adapter/events", ReceiptDir: dir, Executor: fakeExecution{}}
	first, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(config); err == nil {
		t.Fatal("a second adapter replica acquired the same receipt directory")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, uuid.NewString()+".json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(config); err == nil {
		t.Fatal("adapter started with an unreadable durable receipt")
	}
}
