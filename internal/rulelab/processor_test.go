package rulelab

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/RainLib/open-review-platform/internal/runner"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type testStore struct {
	execution        domain.RuleTestExecution
	completion       domain.RuleTestCompletion
	failed           string
	completedAttempt int
	failedAttempt    int
	renewed          chan int
	renewError       error
	plan             domain.ReviewExecutionPlan
	model            domain.ReviewConfigSnapshot
	configurations   map[domain.ReviewConfigSection]domain.ReviewConfigSnapshot
}

func (s *testStore) ClaimRuleTestRun(context.Context, string, *uuid.UUID) (domain.RuleTestExecution, error) {
	return s.execution, nil
}
func (s *testStore) CompleteRuleTestRun(_ context.Context, _ uuid.UUID, _ string, attempt int, completion domain.RuleTestCompletion) error {
	s.completion = completion
	s.completedAttempt = attempt
	return nil
}
func (s *testStore) FailRuleTestRun(_ context.Context, _ uuid.UUID, _ string, attempt int, message string) error {
	s.failed = message
	s.failedAttempt = attempt
	return nil
}
func (s *testStore) RenewRuleTestRun(_ context.Context, _ uuid.UUID, _ string, attempt int, _ time.Duration) error {
	if s.renewed != nil {
		s.renewed <- attempt
	}
	return s.renewError
}
func (s *testStore) ReviewExecutionPlanForJob(context.Context, uuid.UUID) (domain.ReviewExecutionPlan, error) {
	return s.plan, nil
}
func (s *testStore) ModelRouteForJob(context.Context, uuid.UUID) (domain.ReviewConfigSnapshot, error) {
	return s.model, nil
}
func (s *testStore) ReviewConfigSnapshotForJob(_ context.Context, _ uuid.UUID, section domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, error) {
	snapshot, found := s.configurations[section]
	if !found {
		return domain.ReviewConfigSnapshot{}, store.ErrNotFound
	}
	return snapshot, nil
}

type testCheckout struct{}

func (testCheckout) Prepare(context.Context, domain.ReviewJob) (*runner.Workspace, error) {
	return &runner.Workspace{Path: "/isolated", BaseSHA: "base"}, nil
}

type testExecutor struct {
	selected []string
	rules    []byte
}

type testModelSecret string

func (s testModelSecret) ResolveModelCredential(context.Context, string) (string, error) {
	return string(s), nil
}

func immutableSnapshot(t *testing.T, section domain.ReviewConfigSection, raw string) domain.ReviewConfigSnapshot {
	t.Helper()
	content, hash, valid := domain.CanonicalReviewConfig(section, json.RawMessage(raw))
	if !valid {
		t.Fatalf("invalid %s test snapshot: %s", section, raw)
	}
	return domain.ReviewConfigSnapshot{Section: section, Content: content, ContentSHA256: hash}
}

func (*testExecutor) Review(context.Context, string, string, string) ([]domain.Finding, error) {
	panic("isolated runs must not use the unscoped executor")
}
func (e *testExecutor) ReviewWithRuleAndSelectedPaths(_ context.Context, _, _, _ string, encoded []byte, selected []string) ([]domain.Finding, error) {
	e.selected = append([]string(nil), selected...)
	e.rules = append([]byte(nil), encoded...)
	return []domain.Finding{{Path: selected[0], StartLine: 12, EndLine: 12, Severity: "high", Category: "security", Body: "test finding"}}, nil
}

func TestProcessorUsesExactScopedRuleExecutionWithoutPublisherSurface(t *testing.T) {
	compiled, err := rules.Compile([]rules.Source{{VersionID: uuid.NewString(), Rules: []rules.Rule{{
		Key: "security.boundary", Enforcement: rules.Mandatory, MergeBehavior: rules.Replace,
		Severity: "high", Content: json.RawMessage(`{"prompt":"verify the boundary"}`),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	testRunID := uuid.New()
	store := &testStore{execution: domain.RuleTestExecution{
		Run:      domain.RuleTestRun{ID: testRunID, State: "running", Attempts: 2},
		Job:      domain.ReviewJob{BaseSHA: "base", HeadSHA: "head"},
		Snapshot: domain.RuleSnapshot{ID: uuid.New(), SHA256: compiled.SHA256, CanonicalPayload: compiled.Canonical},
	},
		plan:  domain.ReviewExecutionPlan{Mode: "focused", SelectedPaths: []string{"internal/api/server.go"}, DeferredFiles: 1},
		model: immutableSnapshot(t, domain.ReviewConfigModels, `{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"deepseek-v4-flash","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_PRIMARY","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5}`),
		configurations: map[domain.ReviewConfigSection]domain.ReviewConfigSnapshot{
			domain.ReviewConfigGeneral:    immutableSnapshot(t, domain.ReviewConfigGeneral, `{"review_language":"en"}`),
			domain.ReviewConfigPrompts:    immutableSnapshot(t, domain.ReviewConfigPrompts, `{"system_instruction":"Review the selected changes.","repository_context":"","max_prompt_tokens":12000,"allow_repository_instructions":false}`),
			domain.ReviewConfigCategories: immutableSnapshot(t, domain.ReviewConfigCategories, `{"security":{"enabled":true,"minimum_severity":"high"}}`),
		},
	}
	executor := &testExecutor{}
	processor := Processor{Store: store, Checkout: testCheckout{}, Executor: executor, ModelSecrets: testModelSecret("ephemeral-token"), WorkerID: "test-worker", EngineVersion: "ocr-test"}
	worked, err := processor.Run(context.Background(), testRunID)
	if err != nil {
		t.Fatal(err)
	}
	if !worked || store.failed != "" {
		t.Fatalf("unexpected isolated run result: worked=%v failure=%q", worked, store.failed)
	}
	if store.completedAttempt != 2 {
		t.Fatalf("completion did not retain the execution attempt: %d", store.completedAttempt)
	}
	if len(executor.selected) != 1 || executor.selected[0] != "internal/api/server.go" {
		t.Fatalf("exact selected scope was not preserved: %#v", executor.selected)
	}
	if store.completion.EngineVersion != "ocr-test" || store.completion.SelectedPathCount != 1 || store.completion.DeferredPathCount != 1 || len(store.completion.Findings) != 1 {
		t.Fatalf("unexpected persisted completion: %#v", store.completion)
	}
	var ruleFile rules.OCRRuleFile
	if err := json.Unmarshal(executor.rules, &ruleFile); err != nil || len(ruleFile.Rules) != 1 {
		t.Fatalf("candidate snapshot was not passed to OCR: rules=%#v error=%v", ruleFile, err)
	}
}

type cancelledCheckout struct{}

func (cancelledCheckout) Prepare(ctx context.Context, _ domain.ReviewJob) (*runner.Workspace, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestProcessorCancelsWorkWhenItsAttemptLosesTheLease(t *testing.T) {
	state := &testStore{
		execution: domain.RuleTestExecution{Run: domain.RuleTestRun{ID: uuid.New(), State: "running", Attempts: 7}, Job: domain.ReviewJob{BaseSHA: "base", HeadSHA: "head"}},
		renewed:   make(chan int, 1), renewError: store.ErrJobClaimLost,
		plan:  domain.ReviewExecutionPlan{Mode: "focused", SelectedPaths: []string{"internal/api/server.go"}},
		model: immutableSnapshot(t, domain.ReviewConfigModels, `{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"deepseek-v4-flash","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_PRIMARY","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5}`),
		configurations: map[domain.ReviewConfigSection]domain.ReviewConfigSnapshot{
			domain.ReviewConfigGeneral:    immutableSnapshot(t, domain.ReviewConfigGeneral, `{"review_language":"en"}`),
			domain.ReviewConfigPrompts:    immutableSnapshot(t, domain.ReviewConfigPrompts, `{"system_instruction":"Review the selected changes.","repository_context":"","max_prompt_tokens":12000,"allow_repository_instructions":false}`),
			domain.ReviewConfigCategories: immutableSnapshot(t, domain.ReviewConfigCategories, `{"security":{"enabled":true,"minimum_severity":"high"}}`),
		},
	}
	processor := Processor{
		Store: state, Checkout: cancelledCheckout{}, Executor: &testExecutor{},
		ModelSecrets: testModelSecret("ephemeral-token"), WorkerID: "restarted-worker", LeaseRenewEvery: time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	worked, err := processor.Run(ctx, state.execution.Run.ID)
	if !worked || !errors.Is(err, context.Canceled) || ctx.Err() != nil {
		t.Fatalf("lease loss should cancel execution before the outer deadline: worked=%t error=%v parent=%v", worked, err, ctx.Err())
	}
	select {
	case attempt := <-state.renewed:
		if attempt != 7 {
			t.Fatalf("heartbeat attempt=%d, want 7", attempt)
		}
	default:
		t.Fatal("execution was cancelled without a heartbeat")
	}
	if state.failedAttempt != 7 || state.completedAttempt != 0 {
		t.Fatalf("late error must retain its own attempt: failed=%d completed=%d", state.failedAttempt, state.completedAttempt)
	}
}
