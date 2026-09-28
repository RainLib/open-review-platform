package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/modelroute"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/risk"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type snapshotStore struct {
	snapshot domain.RuleSnapshot
	err      error
	mode     domain.ReviewMode
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
func (s snapshotStore) ReviewModeForJob(context.Context, uuid.UUID) (domain.ReviewMode, error) {
	if s.mode == "" {
		return domain.ReviewModeConfigured, nil
	}
	return s.mode, nil
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

type routedSnapshotStore struct {
	snapshotStore
	model domain.ReviewConfigSnapshot
}

func (s routedSnapshotStore) ModelRouteForJob(context.Context, uuid.UUID) (domain.ReviewConfigSnapshot, error) {
	return s.model, nil
}

type categorySnapshotStore struct {
	snapshotStore
	category domain.ReviewConfigSnapshot
	err      error
}

func (s categorySnapshotStore) ReviewConfigSnapshotForJob(_ context.Context, _ uuid.UUID, section domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, error) {
	if section != domain.ReviewConfigCategories {
		return domain.ReviewConfigSnapshot{}, store.ErrNotFound
	}
	return s.category, s.err
}

type generalSnapshotStore struct {
	snapshotStore
	general domain.ReviewConfigSnapshot
	err     error
}

func (s generalSnapshotStore) ReviewConfigSnapshotForJob(_ context.Context, _ uuid.UUID, section domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, error) {
	if section != domain.ReviewConfigGeneral {
		return domain.ReviewConfigSnapshot{}, store.ErrNotFound
	}
	return s.general, s.err
}

type mergeGateDecisionRecordingStore struct {
	generalSnapshotStore
	inputs []domain.ReviewMergeGateDecisionInput
}

func (s *mergeGateDecisionRecordingStore) SaveReviewMergeGateDecision(_ context.Context, _ uuid.UUID, input domain.ReviewMergeGateDecisionInput) (domain.ReviewMergeGateDecision, error) {
	s.inputs = append(s.inputs, input)
	return domain.ReviewMergeGateDecision{
		Enabled:                    input.Enabled,
		Threshold:                  input.Threshold,
		Conclusion:                 input.Conclusion,
		BlockingFindings:           input.BlockingFindings,
		FindingCount:               input.FindingCount,
		ConfigurationContentSHA256: strings.Repeat("a", 64),
		OriginScopeKind:            "tenant",
		OriginRevision:             7,
		EvaluationVersion:          input.EvaluationVersion,
		DecidedAt:                  time.Now(),
	}, nil
}

type filterSnapshotStore struct {
	snapshotStore
	filters domain.ReviewConfigSnapshot
	err     error
}

type promptSnapshotStore struct {
	snapshotStore
	prompts domain.ReviewConfigSnapshot
	err     error
}

func (s promptSnapshotStore) ReviewConfigSnapshotForJob(_ context.Context, _ uuid.UUID, section domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, error) {
	if section != domain.ReviewConfigPrompts {
		return domain.ReviewConfigSnapshot{}, store.ErrNotFound
	}
	return s.prompts, s.err
}

func (s filterSnapshotStore) ReviewConfigSnapshotForJob(_ context.Context, _ uuid.UUID, section domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, error) {
	if section != domain.ReviewConfigFilters {
		return domain.ReviewConfigSnapshot{}, store.ErrNotFound
	}
	return s.filters, s.err
}

type fixedModelSecret string

func (s fixedModelSecret) ResolveModelCredential(context.Context, string) (string, error) {
	return string(s), nil
}

func TestWithModelRouteResolvesOnlyAtExecution(t *testing.T) {
	content := json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"deepseek-v4-flash","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_PRIMARY","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5}`)
	processor := Processor{
		Store:        routedSnapshotStore{model: domain.ReviewConfigSnapshot{Section: domain.ReviewConfigModels, ContentSHA256: "route-hash", Content: content}},
		ModelSecrets: fixedModelSecret("ephemeral-token"),
	}
	ctx, err := processor.withModelRoute(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	execution, ok := modelroute.FromContext(ctx)
	if !ok || execution.Route.Model != "deepseek-v4-flash" || execution.Token != "ephemeral-token" {
		t.Fatalf("unexpected model execution: %#v ok=%t", execution, ok)
	}
}

func TestApplyCategoryPolicyUsesTheAdmittedSnapshot(t *testing.T) {
	processor := Processor{Store: categorySnapshotStore{category: domain.ReviewConfigSnapshot{
		Section: domain.ReviewConfigCategories,
		Content: json.RawMessage(`{"security":{"enabled":true,"minimum_severity":"high"},"performance":{"enabled":false,"minimum_severity":"medium"}}`),
	}}}
	findings, err := processor.applyCategoryPolicy(context.Background(), uuid.New(), []domain.Finding{
		{Category: "security", Severity: "medium", Body: "below threshold"},
		{Category: "security", Severity: "high", Body: "keep"},
		{Category: "performance", Severity: "critical", Body: "disabled"},
		{Category: "new category", Severity: "low", Body: "unconfigured remains visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Body != "keep" || findings[1].Body != "unconfigured remains visible" {
		t.Fatalf("category snapshot was not applied before publication: %#v", findings)
	}
}

func TestApplyCategoryPolicyRetainsLegacyRunsWithoutSnapshots(t *testing.T) {
	input := []domain.Finding{{Category: "security", Severity: "medium", Body: "legacy finding"}}
	processor := Processor{Store: snapshotStore{}}
	findings, err := processor.applyCategoryPolicy(context.Background(), uuid.New(), input)
	if err != nil || len(findings) != 1 || findings[0].Body != input[0].Body {
		t.Fatalf("legacy run should preserve its findings: %#v err=%v", findings, err)
	}
}

type publicationMinimumStore struct {
	snapshotStore
	minimum string
}

func (s publicationMinimumStore) PublicationMinimumForJob(context.Context, uuid.UUID) (string, error) {
	return s.minimum, nil
}

func TestApplyPublicationMinimumUsesFrozenInstallationFloor(t *testing.T) {
	processor := Processor{Store: publicationMinimumStore{minimum: "high"}}
	findings, err := processor.applyPublicationMinimum(context.Background(), uuid.New(), []domain.Finding{
		{Category: "security", Severity: "critical", Body: "critical"},
		{Category: "new_category", Severity: "medium", Body: "below floor"},
		{Category: "bug", Severity: "high", Body: "high"},
	}, "off")
	if err != nil || len(findings) != 2 || findings[0].Body != "critical" || findings[1].Body != "high" {
		t.Fatalf("frozen publication minimum was not applied: %#v err=%v", findings, err)
	}
	blockers, err := processor.applyPublicationMinimum(context.Background(), uuid.New(), []domain.Finding{{Category: "bug", Severity: "medium", Body: "blocks merge"}}, "medium")
	if err != nil || len(blockers) != 1 || blockers[0].Body != "blocks merge" {
		t.Fatalf("gate-blocking finding must be published below the comment floor: %#v err=%v", blockers, err)
	}
}

func TestReviewResultAndChecksUseTheAdmittedGeneralSnapshot(t *testing.T) {
	backend := generalSnapshotStore{
		snapshotStore: snapshotStore{err: store.ErrNotFound},
		general:       domain.ReviewConfigSnapshot{Section: domain.ReviewConfigGeneral, Content: json.RawMessage(`{"merge_gate_enabled":true,"minimum_blocking_severity":"critical"}`)},
	}
	processor := Processor{Store: backend, MergeGateSeverity: "low"}
	result, err := processor.reviewResult(context.Background(), domain.ReviewJob{ID: uuid.New()}, "focused", risk.Plan{}, []domain.Finding{{Severity: "high", Category: "security"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Gate.Threshold != publisher.MergeGateCritical || result.Gate.Conclusion != publisher.CheckSuccess {
		t.Fatalf("review result ignored immutable general policy: %#v", result.Gate)
	}

	disabled := generalSnapshotStore{
		snapshotStore: snapshotStore{err: store.ErrNotFound},
		general:       domain.ReviewConfigSnapshot{Section: domain.ReviewConfigGeneral, Content: json.RawMessage(`{"merge_gate_enabled":false}`)},
	}
	checks := &receiptRecordingCheckReporter{}
	processor = Processor{Store: disabled, Checks: checks, WorkerID: "worker-1"}
	if _, err := processor.completeSuccessfulReview(context.Background(), domain.ReviewJob{ID: uuid.New()}, []domain.Finding{{Severity: "critical", Category: "security"}}); err != nil {
		t.Fatal(err)
	}
	if checks.completed != 0 {
		t.Fatalf("disabled merge gate must not publish a check, completed=%d", checks.completed)
	}
	result, err = processor.reviewResult(context.Background(), domain.ReviewJob{ID: uuid.New()}, "focused", risk.Plan{}, []domain.Finding{{Severity: "critical", Category: "security"}})
	if err != nil || result.Gate.Threshold != publisher.MergeGateOff || result.Gate.Conclusion != publisher.CheckSuccess {
		t.Fatalf("disabled merge gate was not reflected in the evidence report: result=%#v err=%v", result.Gate, err)
	}
}

func TestMergeGateDecisionUsesTheAdmittedGeneralSnapshot(t *testing.T) {
	backend := &mergeGateDecisionRecordingStore{generalSnapshotStore: generalSnapshotStore{
		snapshotStore: snapshotStore{err: store.ErrNotFound},
		general:       domain.ReviewConfigSnapshot{Section: domain.ReviewConfigGeneral, Content: json.RawMessage(`{"merge_gate_enabled":true,"minimum_blocking_severity":"critical"}`)},
	}}
	processor := Processor{Store: backend, MergeGateSeverity: "low"}
	result, err := processor.reviewResult(context.Background(), domain.ReviewJob{ID: uuid.New()}, "focused", risk.Plan{}, []domain.Finding{{Severity: "high", Category: "security"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.persistMergeGateDecision(context.Background(), uuid.New(), result); err != nil {
		t.Fatal(err)
	}
	if len(backend.inputs) != 1 {
		t.Fatalf("decision store calls=%d, want 1", len(backend.inputs))
	}
	decision := backend.inputs[0]
	if !decision.Enabled || decision.Threshold != "critical" || decision.Conclusion != "success" || decision.BlockingFindings != 0 || decision.FindingCount != 1 {
		t.Fatalf("decision leaked mutable worker configuration: %#v", decision)
	}
}

func TestApplyPathFiltersRestrictsTheImmutableOCRScope(t *testing.T) {
	processor := Processor{Store: filterSnapshotStore{filters: domain.ReviewConfigSnapshot{
		Section: domain.ReviewConfigFilters,
		Content: json.RawMessage(`{"include_paths":["internal/**"],"exclude_paths":["**/fixtures/**"],"skip_generated":true,"skip_vendor":true}`),
	}}}
	plan, err := processor.applyPathFilters(context.Background(), uuid.New(), risk.Plan{
		Selected: []risk.Item{
			{Path: "internal/api/server.go"},
			{Path: "internal/fixtures/request.go"},
			{Path: "apps/web/page.tsx"},
			{Path: "internal/generated/client.go"},
		},
		Deferred: []risk.Item{{Path: "docs/guide.md"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Selected) != 1 || plan.Selected[0].Path != "internal/api/server.go" {
		t.Fatalf("unexpected selected paths after filtering: %#v", plan.Selected)
	}
	if got := strings.Join(plan.Exclude, ","); got != "docs/guide.md,internal/fixtures/request.go,apps/web/page.tsx,internal/generated/client.go" {
		t.Fatalf("filtered paths must become exact deferred exclusions, got %q", got)
	}
}

func TestExecutionPlanRetainsSelectedAndDeferredFileReasons(t *testing.T) {
	plan := executionPlanFromRisk(risk.ModeFocused, risk.Plan{
		Selected:            []risk.Item{{Path: "internal/api/server.go", Score: 70, Reasons: []string{"public boundary or asynchronous workflow"}, ChangeType: "modified", Additions: 4, Deletions: 2, StatsKnown: true}},
		Deferred:            []risk.Item{{Path: "docs/guide.md", Score: 10, Reasons: []string{"documentation-only change"}, ChangeType: "renamed", PreviousPath: "docs/old-guide.md", StatsKnown: true}},
		StaticImpactSignals: []string{"public boundary or asynchronous workflow"},
	})
	normalized, ok := domain.NormalizeReviewExecutionPlan(plan)
	if !ok || len(normalized.FileScopes) != 2 || !normalized.FileScopes[0].Selected || normalized.FileScopes[0].Score != 70 || normalized.FileScopes[0].ChangeType != "modified" || normalized.FileScopes[0].Additions != 4 || !normalized.FileScopes[0].StatsKnown || normalized.FileScopes[1].Selected || normalized.FileScopes[1].Reasons[0] != "documentation-only change" || normalized.FileScopes[1].PreviousPath != "docs/old-guide.md" {
		t.Fatalf("risk scope was not frozen into review file evidence: %#v ok=%t", normalized, ok)
	}
}

func TestExecutionPlanKeepsCountOnlyFallbackForVeryLargeDiff(t *testing.T) {
	plan := executionPlanFromRisk(risk.ModeFocused, risk.Plan{
		Selected: []risk.Item{{Path: "internal/api/server.go", Score: 70}},
		Deferred: make([]risk.Item, 10_000),
	})
	normalized, ok := domain.NormalizeReviewExecutionPlan(plan)
	if !ok || normalized.DeferredFiles != 10_000 || len(normalized.SelectedPaths) != 1 || len(normalized.FileScopes) != 0 {
		t.Fatalf("large diff must retain a valid count-only plan: %#v ok=%t", normalized, ok)
	}
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

func TestReviewPromptsUseTheImmutableSnapshotAndTrustedRuleFile(t *testing.T) {
	executor := &recordingExecutor{}
	processor := Processor{Store: promptSnapshotStore{
		snapshotStore: snapshotStore{err: store.ErrNotFound},
		prompts:       domain.ReviewConfigSnapshot{Section: domain.ReviewConfigPrompts, ContentSHA256: "prompt-snapshot", Content: json.RawMessage(`{"system_instruction":"Prioritize authorization bypasses.","repository_context":"Payments changes require idempotency.","max_prompt_tokens":3200,"allow_repository_instructions":false}`)},
	}, Executor: executor}
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}
	ctx, err := processor.withReviewPrompts(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	prompt, ok := modelroute.PromptExecutionFromContext(ctx)
	if !ok || prompt.MaxPromptTokens != 3200 || prompt.SystemInstruction == "" {
		t.Fatalf("immutable prompt snapshot was not attached to execution: %#v ok=%t", prompt, ok)
	}
	if _, err := processor.reviewWithSnapshot(ctx, job, t.TempDir(), "base", risk.Plan{}); err != nil {
		t.Fatal(err)
	}
	var file rules.OCRRuleFile
	if err := json.Unmarshal(executor.ruleJSON, &file); err != nil {
		t.Fatal(err)
	}
	if executor.defaultCalls != 0 || executor.ruleCalls != 1 || len(file.Rules) != 1 || !strings.Contains(file.Rules[0].Rule, "Prioritize authorization bypasses.") || !strings.Contains(file.Rules[0].Rule, "Payments changes require idempotency.") {
		t.Fatalf("prompt configuration did not produce the trusted OCR rule file: executor=%#v rule=%#v", executor, file)
	}
}

func TestReviewPromptRepositoryInstructionsAreBoundedAndStayInWorkspace(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "AGENTS.md"), []byte("Only report findings with direct code evidence."), 0o600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(external, []byte("this must not enter the prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(directory, "OPENREVIEW.md")); err != nil {
		t.Fatal(err)
	}
	ctx := modelroute.WithPromptExecution(context.Background(), modelroute.PromptExecution{AllowRepositoryInstructions: true})
	rule, err := reviewPromptRule(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rule, "Only report findings with direct code evidence.") || strings.Contains(rule, "this must not enter the prompt") {
		t.Fatalf("repository instruction import crossed the workspace boundary: %q", rule)
	}
}

type terminalStore struct {
	snapshotStore
	run domain.ReviewRun
}

func (s terminalStore) AdvanceLegacyRun(context.Context, uuid.UUID, domain.RunState) (domain.ReviewRun, error) {
	return s.run, nil
}

type staleHeadStore struct {
	snapshotStore
	advanced  []domain.RunState
	cancelled bool
}

type publicationResumeStore struct {
	snapshotStore
	plan     domain.ReviewExecutionPlan
	findings []domain.Finding
	advanced []domain.RunState
	receipts []domain.PublicationReceipt
}

type publishingRunStore struct {
	publicationResumeStore
	succeeded bool
}

func (s *publishingRunStore) AdvanceLegacyRun(_ context.Context, _ uuid.UUID, state domain.RunState) (domain.ReviewRun, error) {
	s.advanced = append(s.advanced, state)
	if state == domain.RunAdmitted {
		return domain.ReviewRun{State: domain.RunPublishing}, nil
	}
	return domain.ReviewRun{State: state}, nil
}

func (s *publishingRunStore) Succeed(context.Context, uuid.UUID, string) error {
	s.succeeded = true
	return nil
}

func (s *publicationResumeStore) SaveReviewExecutionPlan(context.Context, uuid.UUID, domain.ReviewExecutionPlan) (domain.ReviewExecutionPlan, error) {
	return s.plan, nil
}

func (s *publicationResumeStore) ReviewExecutionPlanForJob(context.Context, uuid.UUID) (domain.ReviewExecutionPlan, error) {
	return s.plan, nil
}

func (s *publicationResumeStore) FindingsForJob(context.Context, uuid.UUID) ([]domain.Finding, error) {
	return append([]domain.Finding(nil), s.findings...), nil
}

func (s *publicationResumeStore) RecordPublicationReceipts(_ context.Context, _ uuid.UUID, receipts []domain.PublicationReceipt) error {
	s.receipts = append(s.receipts, receipts...)
	return nil
}

func (s *publicationResumeStore) AdvanceLegacyRun(_ context.Context, _ uuid.UUID, state domain.RunState) (domain.ReviewRun, error) {
	s.advanced = append(s.advanced, state)
	return domain.ReviewRun{State: state}, nil
}

type recordingPublisher struct {
	calls  int
	result publisher.ReviewResult
	err    error
}

type preflightRecordingPublisher struct {
	recordingPublisher
	verifyCalls int
	verifyErr   error
}

func (p *preflightRecordingPublisher) VerifyCurrentReview(context.Context, domain.ReviewJob) error {
	p.verifyCalls++
	return p.verifyErr
}

type preflightStatusRecorder struct {
	startedChecks, completedChecks    int
	startedComments, terminalComments int
	progressComments                  int
}

type failingWorkspacePreparer struct{}

func (failingWorkspacePreparer) Prepare(context.Context, domain.ReviewJob) (*Workspace, error) {
	return nil, errors.New("temporary checkout failure")
}

func TestRunClaimedKeepsTimelineQuietBeforeTerminalResult(t *testing.T) {
	status := &preflightStatusRecorder{}
	processor := Processor{Store: snapshotStore{}, Checkout: failingWorkspacePreparer{}, Lifecycle: status, WorkerID: "worker-1"}
	worked, err := processor.runClaimed(context.Background(), &domain.ReviewJob{ID: uuid.New(), Attempts: 1})
	if err != nil || !worked {
		t.Fatalf("worked=%t err=%v", worked, err)
	}
	if status.startedComments != 0 || status.progressComments != 0 || status.terminalComments != 0 {
		t.Fatalf("pre-terminal review published a timeline comment: %+v", status)
	}
}

func (r *preflightStatusRecorder) StartCheck(context.Context, domain.ReviewJob) error {
	r.startedChecks++
	return nil
}

func (r *preflightStatusRecorder) CompleteCheck(context.Context, domain.ReviewJob, publisher.CheckConclusion, string) error {
	r.completedChecks++
	return nil
}

func (r *preflightStatusRecorder) PublishStarted(context.Context, domain.ReviewJob) error {
	r.startedComments++
	return nil
}

func (r *preflightStatusRecorder) PublishProgress(context.Context, domain.ReviewJob) error {
	r.progressComments++
	return nil
}

func (r *preflightStatusRecorder) PublishTerminal(context.Context, domain.ReviewJob, publisher.LifecycleState) error {
	r.terminalComments++
	return nil
}

func (p *recordingPublisher) Publish(_ context.Context, _ domain.ReviewJob, result publisher.ReviewResult) error {
	p.calls++
	p.result = result
	return p.err
}

type receiptRecordingPublisher struct {
	recordingPublisher
	receipts []domain.PublicationReceipt
}

func (p *receiptRecordingPublisher) PublishWithReceipts(_ context.Context, _ domain.ReviewJob, result publisher.ReviewResult) ([]domain.PublicationReceipt, error) {
	p.calls++
	p.result = result
	return append([]domain.PublicationReceipt(nil), p.receipts...), p.err
}

type receiptRecordingCheckReporter struct {
	started      int
	completed    int
	startErr     error
	completeErr  error
	startReceipt domain.PublicationReceipt
	endReceipt   domain.PublicationReceipt
}

type terminalStateRecordingPublisher struct {
	conclusion publisher.CheckConclusion
	summary    string
	lifecycle  []publisher.LifecycleState
}

func (*terminalStateRecordingPublisher) StartCheck(context.Context, domain.ReviewJob) error {
	return nil
}

func (p *terminalStateRecordingPublisher) CompleteCheck(_ context.Context, _ domain.ReviewJob, conclusion publisher.CheckConclusion, summary string) error {
	p.conclusion, p.summary = conclusion, summary
	return nil
}

func (*terminalStateRecordingPublisher) PublishStarted(context.Context, domain.ReviewJob) error {
	return nil
}

func (p *terminalStateRecordingPublisher) PublishTerminal(_ context.Context, _ domain.ReviewJob, state publisher.LifecycleState) error {
	p.lifecycle = append(p.lifecycle, state)
	return nil
}

func (r *receiptRecordingCheckReporter) StartCheck(context.Context, domain.ReviewJob) error {
	r.started++
	return r.startErr
}

func (r *receiptRecordingCheckReporter) CompleteCheck(context.Context, domain.ReviewJob, publisher.CheckConclusion, string) error {
	r.completed++
	return r.completeErr
}

func (r *receiptRecordingCheckReporter) StartCheckWithReceipt(ctx context.Context, job domain.ReviewJob) (domain.PublicationReceipt, error) {
	if err := r.StartCheck(ctx, job); err != nil {
		return domain.PublicationReceipt{}, err
	}
	return r.startReceipt, nil
}

func (r *receiptRecordingCheckReporter) CompleteCheckWithReceipt(ctx context.Context, job domain.ReviewJob, conclusion publisher.CheckConclusion, summary string) (domain.PublicationReceipt, error) {
	if err := r.CompleteCheck(ctx, job, conclusion, summary); err != nil {
		return domain.PublicationReceipt{}, err
	}
	return r.endReceipt, nil
}

func (s *staleHeadStore) AdvanceLegacyRun(_ context.Context, _ uuid.UUID, state domain.RunState) (domain.ReviewRun, error) {
	s.advanced = append(s.advanced, state)
	return domain.ReviewRun{State: state}, nil
}

func (s *staleHeadStore) Cancel(context.Context, uuid.UUID, string) error {
	s.cancelled = true
	return nil
}

type renewalStore struct {
	snapshotStore
	calls chan struct{}
	err   error
}

type retryScheduleRecordingStore struct {
	snapshotStore
	delay      time.Duration
	retryCalls int
	failCalls  int
}

func (s *retryScheduleRecordingStore) FailWithRetryAfter(_ context.Context, _ uuid.UUID, _ string, _ string, delay time.Duration) error {
	s.delay = delay
	s.retryCalls++
	return nil
}

func (s *retryScheduleRecordingStore) Fail(context.Context, uuid.UUID, string, string) error {
	s.failCalls++
	return nil
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

type modeRecordingPlanner struct {
	mode risk.Mode
}

func (*modeRecordingPlanner) Plan(context.Context, string, string, string) (risk.Plan, error) {
	return risk.Plan{}, nil
}

func (p *modeRecordingPlanner) PlanWithMode(_ context.Context, _, _, _ string, mode risk.Mode) (risk.Plan, error) {
	p.mode = mode
	return risk.Plan{}, nil
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

func TestStopTerminalRunPublishesNeedsAttentionConsistently(t *testing.T) {
	reporter := &terminalStateRecordingPublisher{}
	processor := Processor{Store: snapshotStore{}, WorkerID: "worker-1", Checks: reporter, Lifecycle: reporter}
	stopped, err := processor.stopTerminalRun(context.Background(), domain.ReviewJob{ID: uuid.New()}, domain.ReviewRun{State: domain.RunNeedsAttention})
	if err != nil || !stopped {
		t.Fatalf("stopped=%t err=%v", stopped, err)
	}
	if reporter.conclusion != publisher.CheckFailure || !strings.Contains(reporter.summary, "human attention") {
		t.Fatalf("unexpected needs-attention check: %#v", reporter)
	}
	if len(reporter.lifecycle) != 1 || reporter.lifecycle[0] != publisher.LifecycleNeedsAttention {
		t.Fatalf("unexpected needs-attention lifecycle: %#v", reporter.lifecycle)
	}
}

func TestStopTerminalRunPublishesFailedCheckConsistently(t *testing.T) {
	reporter := &terminalStateRecordingPublisher{}
	processor := Processor{Store: snapshotStore{}, WorkerID: "worker-1", Checks: reporter, Lifecycle: reporter}
	stopped, err := processor.stopTerminalRun(context.Background(), domain.ReviewJob{ID: uuid.New()}, domain.ReviewRun{State: domain.RunFailed})
	if err != nil || !stopped {
		t.Fatalf("stopped=%t err=%v", stopped, err)
	}
	if reporter.conclusion != publisher.CheckFailure || !strings.Contains(reporter.summary, "trustworthy findings") {
		t.Fatalf("unexpected failed check: %#v", reporter)
	}
	if len(reporter.lifecycle) != 1 || reporter.lifecycle[0] != publisher.LifecycleFailed {
		t.Fatalf("unexpected failed lifecycle: %#v", reporter.lifecycle)
	}
}

func TestSupersedeStaleProviderHeadCancelsJobAndPreservesTerminalRun(t *testing.T) {
	backend := &staleHeadStore{}
	processor := Processor{Store: backend, WorkerID: "worker-1"}
	worked, err := processor.supersedeStaleProviderHead(context.Background(), domain.ReviewJob{ID: uuid.New()})
	if err != nil || !worked {
		t.Fatalf("worked=%t err=%v", worked, err)
	}
	if len(backend.advanced) != 1 || backend.advanced[0] != domain.RunSuperseded || !backend.cancelled {
		t.Fatalf("expected superseded run and cancelled job, advanced=%v cancelled=%t", backend.advanced, backend.cancelled)
	}
}

func TestRunClaimedPreflightRejectsStaleReviewBeforeProviderWritesOrModelWork(t *testing.T) {
	for _, preflightErr := range []error{publisher.ErrReviewHeadChanged, publisher.ErrReviewNotOpen} {
		backend := &staleHeadStore{}
		published := &preflightRecordingPublisher{verifyErr: preflightErr}
		status := &preflightStatusRecorder{}
		processor := Processor{Store: backend, Publisher: published, Checks: status, Lifecycle: status, WorkerID: "worker-1"}
		worked, err := processor.runClaimed(context.Background(), &domain.ReviewJob{ID: uuid.New(), HeadSHA: "old-head"})
		if err != nil || !worked {
			t.Fatalf("preflight %v: worked=%t err=%v", preflightErr, worked, err)
		}
		if published.verifyCalls != 1 || published.calls != 0 || status.startedChecks != 0 || status.startedComments != 0 || status.completedChecks != 1 || status.terminalComments != 1 || !backend.cancelled || len(backend.advanced) != 2 || backend.advanced[0] != domain.RunAdmitted || backend.advanced[1] != domain.RunSuperseded {
			t.Fatalf("preflight %v did not stop before publication: calls=%d writes=%d advanced=%v cancelled=%t", preflightErr, published.verifyCalls, published.calls, backend.advanced, backend.cancelled)
		}
	}
}

func TestRunClaimedInvalidRevisionFailsWithoutAnyProviderWrite(t *testing.T) {
	backend := &staleHeadStore{}
	published := &preflightRecordingPublisher{verifyErr: publisher.ErrInvalidReviewRevision}
	status := &preflightStatusRecorder{}
	processor := Processor{Store: backend, Publisher: published, Checks: status, Lifecycle: status, WorkerID: "worker-1"}
	worked, err := processor.runClaimed(context.Background(), &domain.ReviewJob{ID: uuid.New(), BaseSHA: "base-sha", HeadSHA: "head-sha"})
	if err != nil || !worked {
		t.Fatalf("worked=%t err=%v", worked, err)
	}
	if published.verifyCalls != 1 || published.calls != 0 || status.startedChecks != 0 || status.startedComments != 0 || status.completedChecks != 0 || status.terminalComments != 0 || backend.cancelled || len(backend.advanced) != 2 || backend.advanced[0] != domain.RunAdmitted || backend.advanced[1] != domain.RunFailed {
		t.Fatalf("invalid revision crossed provider boundary: publisher=%#v status=%#v advanced=%v", published, status, backend.advanced)
	}
}

func TestResumePublicationReusesPersistedFindingsWithoutAnExecutionEngine(t *testing.T) {
	backend := &publicationResumeStore{
		snapshotStore: snapshotStore{err: store.ErrNotFound},
		plan:          domain.ReviewExecutionPlan{Mode: "critical", SelectedPaths: []string{"internal/api/server.go"}, DeferredFiles: 6},
		findings:      []domain.Finding{{Path: "internal/api/server.go", StartLine: 40, EndLine: 40, Severity: "high", Category: "security", Body: "Validate request input."}},
	}
	published := &recordingPublisher{}
	processor := Processor{Store: backend, Publisher: published, MergeGateSeverity: "high"}
	handled, findings, err := processor.resumePublication(context.Background(), domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}, domain.ReviewRun{State: domain.RunPublishing})
	if err != nil || !handled || len(findings) != 1 {
		t.Fatalf("handled=%t findings=%#v err=%v", handled, findings, err)
	}
	if published.calls != 1 || published.result.Scope.Mode != "critical" || len(published.result.Scope.SelectedPaths) != 1 || published.result.Scope.DeferredFiles != 6 || published.result.Gate.Conclusion != publisher.CheckFailure {
		t.Fatalf("unexpected resumed publication result: %#v calls=%d", published.result, published.calls)
	}
	if len(backend.advanced) != 1 || backend.advanced[0] != domain.RunCompleted {
		t.Fatalf("expected persisted run to complete, advanced=%v", backend.advanced)
	}
}

func TestResumePublicationPersistsPartialProviderReceiptsAndSafeFailure(t *testing.T) {
	backend := &publicationResumeStore{
		snapshotStore: snapshotStore{err: store.ErrNotFound},
		plan:          domain.ReviewExecutionPlan{Mode: "focused", SelectedPaths: []string{"internal/api/server.go"}, DeferredFiles: 1},
		findings:      []domain.Finding{{Path: "internal/api/server.go", StartLine: 12, EndLine: 12, Severity: "medium", Category: "bug", Body: "Preserved finding."}},
	}
	published := &receiptRecordingPublisher{
		recordingPublisher: recordingPublisher{err: errors.New("provider returned HTTP 503: untrusted body must not persist")},
		receipts:           []domain.PublicationReceipt{{ReceiptKind: "inline_finding", StableMarker: "open-review-platform:finding:partial", PayloadHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Published: true}},
	}
	processor := Processor{Store: backend, Publisher: published, MergeGateSeverity: "high"}
	_, _, err := processor.resumePublication(context.Background(), domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}, domain.ReviewRun{State: domain.RunPublishing})
	if err == nil {
		t.Fatal("expected provider publication failure")
	}
	if len(backend.receipts) != 2 || backend.receipts[0].ReceiptKind != "inline_finding" || backend.receipts[1].ReceiptKind != "summary" || backend.receipts[1].Published || strings.Contains(backend.receipts[1].LastError, "untrusted body") {
		t.Fatalf("unexpected persisted provider outcomes: %#v", backend.receipts)
	}
}

func TestRunClaimedResumesPublishingRunWithoutReenteringPreparationOrAnalysis(t *testing.T) {
	backend := &publishingRunStore{publicationResumeStore: publicationResumeStore{
		snapshotStore: snapshotStore{err: store.ErrNotFound},
		plan:          domain.ReviewExecutionPlan{Mode: "focused", SelectedPaths: []string{"internal/api/server.go"}, DeferredFiles: 2},
		findings:      []domain.Finding{{Path: "internal/api/server.go", StartLine: 12, EndLine: 12, Severity: "medium", Category: "bug", Body: "Preserved finding."}},
	}}
	published := &recordingPublisher{}
	processor := Processor{Store: backend, Publisher: published, WorkerID: "worker-1", MergeGateSeverity: "high"}
	worked, err := processor.runClaimed(context.Background(), &domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"})
	if err != nil || !worked || !backend.succeeded || published.calls != 1 {
		t.Fatalf("worked=%t succeeded=%t publish=%d err=%v", worked, backend.succeeded, published.calls, err)
	}
	transitions := make([]string, len(backend.advanced))
	for index, state := range backend.advanced {
		transitions[index] = string(state)
	}
	if got := strings.Join(transitions, ","); got != "admitted,completed" {
		t.Fatalf("publishing retry must not reenter preparation or analysis, transitions=%s", got)
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

func TestProviderRetryAfterBecomesDurableQueueDelay(t *testing.T) {
	backend := &retryScheduleRecordingStore{}
	processor := Processor{Store: backend, WorkerID: "worker-1"}
	worked, err := processor.handleProcessingError(context.Background(), domain.ReviewJob{ID: uuid.New(), Attempts: 1}, &publisher.HTTPStatusError{StatusCode: 429, RetryAfter: 9 * time.Second})
	if err != nil || !worked || backend.retryCalls != 1 || backend.failCalls != 0 || backend.delay != 9*time.Second {
		t.Fatalf("worked=%t err=%v retryCalls=%d failCalls=%d delay=%s", worked, err, backend.retryCalls, backend.failCalls, backend.delay)
	}
	if !isTerminalExecutionFailure(&publisher.HTTPStatusError{StatusCode: 401}) || isTerminalExecutionFailure(&publisher.HTTPStatusError{StatusCode: 503}) {
		t.Fatal("provider terminal errors were classified incorrectly")
	}
}

func TestCheckReceiptsRetainStatusEvidenceWithoutBlockingReview(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "head-sha"}
	store := &publicationResumeStore{}
	reporter := &receiptRecordingCheckReporter{
		startReceipt: domain.PublicationReceipt{ReceiptKind: "status", StableMarker: "open-review-platform:analysis-check:" + job.ID.String(), PayloadHash: strings.Repeat("a", 64), Published: true},
		endReceipt:   domain.PublicationReceipt{ReceiptKind: "status", StableMarker: "open-review-platform:analysis-check:" + job.ID.String(), PayloadHash: strings.Repeat("b", 64), Published: true},
	}
	processor := Processor{Store: store, Checks: reporter}
	if err := processor.startCheck(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := processor.completeCheck(context.Background(), job, publisher.CheckFailure, "blocked"); err != nil {
		t.Fatal(err)
	}
	if reporter.started != 1 || reporter.completed != 1 || len(store.receipts) != 2 {
		t.Fatalf("check calls start=%d completion=%d receipts=%#v", reporter.started, reporter.completed, store.receipts)
	}
	for _, receipt := range store.receipts {
		if receipt.ReceiptKind != "status" || receipt.StableMarker != "open-review-platform:analysis-check:"+job.ID.String() || !receipt.Published {
			t.Fatalf("unexpected persisted status receipt: %#v", receipt)
		}
	}

	reporter.startErr = errors.New("provider response contained sensitive details")
	if err := processor.startCheck(context.Background(), job); err == nil {
		t.Fatal("expected provider status failure")
	}
	failed := store.receipts[len(store.receipts)-1]
	if failed.Published || failed.LastError != "Provider analysis status could not be updated. The review continues and will attempt a final status." || strings.Contains(failed.LastError, "sensitive") {
		t.Fatalf("unsafe or incomplete failed status receipt: %#v", failed)
	}
}

func TestModelContextExhaustionIsTerminal(t *testing.T) {
	if !isTerminalExecutionFailure(domain.ErrReviewContextExhausted) {
		t.Fatal("expected model context exhaustion to bypass retries")
	}
}

func TestSecurityCommandUsesCriticalRiskMode(t *testing.T) {
	planner := &modeRecordingPlanner{}
	processor := Processor{
		Store:          snapshotStore{mode: domain.ReviewModeSecurity},
		RiskPlanner:    planner,
		RiskReviewMode: string(risk.ModeFocused),
	}
	mode, err := processor.riskModeForJob(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if mode != risk.ModeCritical {
		t.Fatalf("security command mode=%q, want critical", mode)
	}
	if _, err := processor.planRisk(context.Background(), "/workspace", "base", "head", mode); err != nil {
		t.Fatal(err)
	}
	if planner.mode != risk.ModeCritical {
		t.Fatalf("planner mode=%q, want critical", planner.mode)
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
