package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/risk"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type snapshotStore struct {
	snapshot domain.RuleSnapshot
	err      error
}

func (s snapshotStore) Claim(context.Context, string) (*domain.ReviewJob, error) {
	return nil, store.ErrNoQueuedJob
}
func (s snapshotStore) ClaimForRun(context.Context, string, uuid.UUID) (*domain.ReviewJob, error) {
	return nil, store.ErrNoQueuedJob
}
func (s snapshotStore) AdvanceLegacyRun(context.Context, uuid.UUID, domain.RunState) (domain.ReviewRun, error) {
	return domain.ReviewRun{}, nil
}
func (s snapshotStore) RuleSnapshotForJob(context.Context, uuid.UUID) (domain.RuleSnapshot, error) {
	return s.snapshot, s.err
}
func (snapshotStore) SaveFindings(context.Context, uuid.UUID, []domain.Finding) error { return nil }
func (snapshotStore) Succeed(context.Context, uuid.UUID, string) error                { return nil }
func (snapshotStore) Fail(context.Context, uuid.UUID, string, string) error           { return nil }
func (snapshotStore) FailTerminal(context.Context, uuid.UUID, string, string) error   { return nil }
func (snapshotStore) Cancel(context.Context, uuid.UUID, string) error                 { return nil }
func (snapshotStore) RenewClaim(context.Context, uuid.UUID, string, time.Duration) error {
	return nil
}

type recordingExecutor struct {
	defaultCalls int
	ruleCalls    int
	ruleJSON     []byte
}

func (e *recordingExecutor) Review(context.Context, string, string, string) ([]domain.Finding, error) {
	e.defaultCalls++
	return nil, nil
}

func (e *recordingExecutor) ReviewWithRule(_ context.Context, _ string, _ string, _ string, value []byte) ([]domain.Finding, error) {
	e.ruleCalls++
	e.ruleJSON = append([]byte(nil), value...)
	return nil, nil
}

func TestReviewWithSnapshotPassesTrustedOCRRuleFile(t *testing.T) {
	canonical, err := json.Marshal(rules.Snapshot{Engine: "ocr", MergeSystemRule: true, Include: []string{"services/payments/**"}, Exclude: []string{"**/fixtures/**"}, Rules: []rules.EffectiveRule{{
		Key: "payments.idempotency", SourceVersion: "version-1", Enforcement: rules.Mandatory, Severity: "critical", Content: json.RawMessage(`{"prompt":"Require idempotent payment retries."}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	processor := Processor{Store: snapshotStore{snapshot: domain.RuleSnapshot{ID: uuid.New(), CanonicalPayload: canonical}}, Executor: executor}
	if _, err := processor.reviewWithSnapshot(context.Background(), domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}, "/workspace", "base", risk.Plan{}); err != nil {
		t.Fatal(err)
	}
	if executor.defaultCalls != 0 || executor.ruleCalls != 1 {
		t.Fatalf("expected one rule-aware review, defaults=%d rules=%d", executor.defaultCalls, executor.ruleCalls)
	}
	var file rules.OCRRuleFile
	if err := json.Unmarshal(executor.ruleJSON, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Rules) != 1 || file.Rules[0].Path != "**/*" || !file.Rules[0].MergeSystemRule {
		t.Fatalf("unexpected OCR file: %#v", file)
	}
	if len(file.Include) != 1 || file.Include[0] != "services/payments/**" || len(file.Exclude) != 1 || file.Exclude[0] != "**/fixtures/**" {
		t.Fatalf("OCR file lost immutable path filters: %#v", file)
	}
}

func TestReviewWithSnapshotFallsBackOnlyWhenNoSnapshotExists(t *testing.T) {
	executor := &recordingExecutor{}
	processor := Processor{Store: snapshotStore{err: store.ErrNotFound}, Executor: executor}
	if _, err := processor.reviewWithSnapshot(context.Background(), domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}, "/workspace", "base", risk.Plan{}); err != nil {
		t.Fatal(err)
	}
	if executor.defaultCalls != 1 || executor.ruleCalls != 0 {
		t.Fatalf("expected standard review fallback, defaults=%d rules=%d", executor.defaultCalls, executor.ruleCalls)
	}
}

type terminalStore struct {
	snapshotStore
	run domain.ReviewRun
}

func (s terminalStore) AdvanceLegacyRun(context.Context, uuid.UUID, domain.RunState) (domain.ReviewRun, error) {
	return s.run, nil
}

type renewalStore struct {
	snapshotStore
	calls chan struct{}
	err   error
}

func (s renewalStore) RenewClaim(context.Context, uuid.UUID, string, time.Duration) error {
	select {
	case s.calls <- struct{}{}:
	default:
	}
	return s.err
}

type blockingExecutor struct {
	stopped chan struct{}
}

type selectedPathExecutor struct {
	recordingExecutor
	selected []string
}

func (e *selectedPathExecutor) ReviewWithSelectedPaths(_ context.Context, _ string, _ string, _ string, selected []string) ([]domain.Finding, error) {
	e.selected = append([]string(nil), selected...)
	return nil, nil
}

type blockingCheckout struct {
	stopped chan struct{}
}

func (c blockingCheckout) Prepare(ctx context.Context, _ domain.ReviewJob) (*Workspace, error) {
	<-ctx.Done()
	close(c.stopped)
	return nil, ctx.Err()
}

func (e blockingExecutor) Review(ctx context.Context, _, _, _ string) ([]domain.Finding, error) {
	<-ctx.Done()
	close(e.stopped)
	return nil, ctx.Err()
}

func TestProcessUntilTerminalCancelsCheckout(t *testing.T) {
	stopped := make(chan struct{})
	processor := Processor{
		Store:                terminalStore{run: domain.ReviewRun{State: domain.RunCancelled}},
		Checkout:             blockingCheckout{stopped: stopped},
		TerminalPollInterval: time.Millisecond,
	}
	_, err := processor.processUntilTerminal(context.Background(), domain.ReviewJob{ID: uuid.New()})
	var terminal terminalRunError
	if !errors.As(err, &terminal) || terminal.run.State != domain.RunCancelled {
		t.Fatalf("expected cancelled terminal error, got %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("checkout did not receive cancellation")
	}
}

func TestProcessBoundsStalledCheckout(t *testing.T) {
	stopped := make(chan struct{})
	processor := Processor{
		Checkout:        blockingCheckout{stopped: stopped},
		CheckoutTimeout: 20 * time.Millisecond,
	}
	_, err := processor.process(context.Background(), domain.ReviewJob{ID: uuid.New()})
	if err == nil || !strings.Contains(err.Error(), "timed out after 20ms") {
		t.Fatalf("expected checkout timeout, got %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("checkout did not receive its deadline cancellation")
	}
}

func TestClaimHeartbeatRenewsAndStopsOnLostLease(t *testing.T) {
	leaseLost := errors.New("lease lost")
	store := renewalStore{calls: make(chan struct{}, 1), err: leaseLost}
	processor := Processor{
		Store:           store,
		WorkerID:        "worker-1",
		LeaseDuration:   time.Second,
		LeaseRenewEvery: time.Millisecond,
	}
	executionCtx, stop := processor.keepClaimAlive(context.Background(), domain.ReviewJob{ID: uuid.New()})
	defer stop()
	select {
	case <-store.calls:
	case <-time.After(time.Second):
		t.Fatal("expected claim renewal")
	}
	select {
	case <-executionCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("execution continued after claim renewal failed")
	}
}

func TestReviewUntilTerminalCancelsInFlightExecutor(t *testing.T) {
	stopped := make(chan struct{})
	processor := Processor{
		Store:                terminalStore{snapshotStore: snapshotStore{err: store.ErrNotFound}, run: domain.ReviewRun{State: domain.RunSuperseded}},
		Executor:             blockingExecutor{stopped: stopped},
		TerminalPollInterval: time.Millisecond,
	}
	_, err := processor.reviewUntilTerminal(context.Background(), domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}, "/workspace", "base", risk.Plan{})
	var terminal terminalRunError
	if !errors.As(err, &terminal) || terminal.run.State != domain.RunSuperseded {
		t.Fatalf("expected superseded terminal error, got %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("executor did not receive cancellation")
	}
}

func TestStopTerminalRunDoesNotCancelCompletedReview(t *testing.T) {
	processor := Processor{Store: snapshotStore{}, WorkerID: "worker-1"}
	stopped, err := processor.stopTerminalRun(context.Background(), domain.ReviewJob{ID: uuid.New()}, domain.ReviewRun{State: domain.RunCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if stopped {
		t.Fatal("completed review must be allowed to mark its job succeeded")
	}
}

func TestExecutionBudgetTimeoutIsTerminal(t *testing.T) {
	if !isTerminalExecutionFailure(domain.ErrReviewTimedOut) {
		t.Fatal("expected execution budget timeout to bypass retries")
	}
	if isTerminalExecutionFailure(errors.New("temporary provider unavailable")) {
		t.Fatal("temporary provider errors must remain retryable")
	}
}

func TestModelContextExhaustionIsTerminal(t *testing.T) {
	if !isTerminalExecutionFailure(domain.ErrReviewContextExhausted) {
		t.Fatal("expected model context exhaustion to bypass retries")
	}
}

func TestDeferredOnlyScopeSkipsModelExecution(t *testing.T) {
	executor := &recordingExecutor{}
	processor := Processor{Store: snapshotStore{err: store.ErrNotFound}, Executor: executor}
	findings, err := processor.reviewWithSnapshot(context.Background(), domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}, "/workspace", "base", risk.Plan{Deferred: []risk.Item{{Path: "docs/guide.md"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 || executor.defaultCalls != 0 || executor.ruleCalls != 0 {
		t.Fatalf("deferred-only plan must skip model execution: findings=%d defaults=%d rules=%d", len(findings), executor.defaultCalls, executor.ruleCalls)
	}
}

func TestReviewWithScopePrefersSelectedPaths(t *testing.T) {
	executor := &selectedPathExecutor{}
	processor := Processor{Executor: executor}
	plan := risk.Plan{
		Selected: []risk.Item{{Path: "internal/api/server.go"}, {Path: "apps/web/app/page.tsx"}},
		Exclude:  []string{"docs/readme.md", "package-lock.json"},
	}
	if _, err := processor.reviewWithScope(context.Background(), "/workspace", "base", "head", plan); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(executor.selected, ","), "internal/api/server.go,apps/web/app/page.tsx"; got != want {
		t.Fatalf("expected exact selected scope, got %q", got)
	}
	if executor.defaultCalls != 0 {
		t.Fatalf("selected-path executor unexpectedly fell back to full review: %d calls", executor.defaultCalls)
	}
}
