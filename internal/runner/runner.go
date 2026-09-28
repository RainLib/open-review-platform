package runner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/modelroute"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/risk"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type ReviewExecutor interface {
	Review(context.Context, string, string, string) ([]domain.Finding, error)
}

type RuleAwareReviewExecutor interface {
	ReviewWithRule(context.Context, string, string, string, []byte) ([]domain.Finding, error)
}

type ScopedReviewExecutor interface {
	ReviewWithExclude(context.Context, string, string, string, []string) ([]domain.Finding, error)
}

type RuleAndScopedReviewExecutor interface {
	ReviewWithRuleAndExclude(context.Context, string, string, string, []byte, []string) ([]domain.Finding, error)
}

// SelectedPathReviewExecutor receives the exact risk-admitted paths instead of
// a potentially large deferred-path list. Implementations can materialize a
// minimal review range without making the model parse exclusions.
type SelectedPathReviewExecutor interface {
	ReviewWithSelectedPaths(context.Context, string, string, string, []string) ([]domain.Finding, error)
}

// RuleAndSelectedPathReviewExecutor combines the immutable enterprise-rule
// snapshot with an exact, risk-admitted review range.
type RuleAndSelectedPathReviewExecutor interface {
	ReviewWithRuleAndSelectedPaths(context.Context, string, string, string, []byte, []string) ([]domain.Finding, error)
}

type RiskPlanner interface {
	Plan(context.Context, string, string, string) (risk.Plan, error)
}

// ModeAwareRiskPlanner preserves the legacy planner contract while allowing a
// command-triggered run to choose its persisted review intensity.
type ModeAwareRiskPlanner interface {
	PlanWithMode(context.Context, string, string, string, risk.Mode) (risk.Plan, error)
}

type WorkspacePreparer interface {
	Prepare(context.Context, domain.ReviewJob) (*Workspace, error)
}

// RunnerStore is intentionally narrower than the control-plane Store. It
// documents exactly which durable operations a worker may perform and makes
// execution behavior testable without a live PostgreSQL instance.
type RunnerStore interface {
	Claim(context.Context, string) (*domain.ReviewJob, error)
	ClaimForRun(context.Context, string, uuid.UUID) (*domain.ReviewJob, error)
	AdvanceLegacyRun(context.Context, uuid.UUID, domain.RunState) (domain.ReviewRun, error)
	ReviewModeForJob(context.Context, uuid.UUID) (domain.ReviewMode, error)
	RuleSnapshotForJob(context.Context, uuid.UUID) (domain.RuleSnapshot, error)
	SaveFindings(context.Context, uuid.UUID, []domain.Finding) error
	Succeed(context.Context, uuid.UUID, string) error
	Fail(context.Context, uuid.UUID, string, string) error
	FailTerminal(context.Context, uuid.UUID, string, string) error
	Cancel(context.Context, uuid.UUID, string) error
	RenewClaim(context.Context, uuid.UUID, string, time.Duration) error
}

// executionPlanStore is optional only for compatibility with older recovery
// stores. Postgres implements it, making the published scope and normalized
// findings durable enough to resume a provider-only retry without OCR/LLM.
type executionPlanStore interface {
	SaveReviewExecutionPlan(context.Context, uuid.UUID, domain.ReviewExecutionPlan) (domain.ReviewExecutionPlan, error)
	ReviewExecutionPlanForJob(context.Context, uuid.UUID) (domain.ReviewExecutionPlan, error)
	FindingsForJob(context.Context, uuid.UUID) ([]domain.Finding, error)
}

// publicationReceiptStore is optional for compatibility with older runner
// stores. Postgres records provider publication outcomes against the immutable
// run so Console evidence and recovery runbooks do not rely on worker logs.
type publicationReceiptStore interface {
	RecordPublicationReceipts(context.Context, uuid.UUID, []domain.PublicationReceipt) error
}

// mergeGateDecisionStore is implemented by the authoritative Postgres store.
// It retains a deterministic decision before provider publication so external
// check failure cannot erase the policy result for the reviewed revision.
type mergeGateDecisionStore interface {
	SaveReviewMergeGateDecision(context.Context, uuid.UUID, domain.ReviewMergeGateDecisionInput) (domain.ReviewMergeGateDecision, error)
}

// retryAfterStore lets a provider's bounded Retry-After become durable queue
// scheduling data instead of a worker-local sleep.
type retryAfterStore interface {
	FailWithRetryAfter(context.Context, uuid.UUID, string, string, time.Duration) error
}

// shadowRolloutQueueStore is deliberately optional while old recovery stores
// are retired. The Postgres implementation queues isolated Rule Lab work only;
// it has no provider publisher or merge-gate write path.
type shadowRolloutQueueStore interface {
	QueueShadowRuleTests(context.Context, uuid.UUID) error
}

type Processor struct {
	Store             RunnerStore
	Checkout          WorkspacePreparer
	Executor          ReviewExecutor
	Publisher         publisher.Publisher
	Checks            publisher.CheckReporter
	Lifecycle         publisher.LifecycleReporter
	RiskPlanner       RiskPlanner
	RiskReviewMode    string
	EngineVersion     string
	CheckoutTimeout   time.Duration
	LeaseDuration     time.Duration
	LeaseRenewEvery   time.Duration
	MergeGateSeverity string
	WorkerID          string
	Logger            *slog.Logger
	ModelSecrets      modelroute.SecretResolver
	TaskStarted       func() func()
	// TerminalPollInterval controls how quickly an in-flight review observes a
	// cancellation or supersession. It is configurable for deterministic tests;
	// production callers should leave it unset.
	TerminalPollInterval time.Duration
}

var errTerminalRun = errors.New("review run became terminal")

type terminalRunError struct {
	run domain.ReviewRun
}

func (e terminalRunError) Error() string { return errTerminalRun.Error() }

func (e terminalRunError) Is(target error) bool { return target == errTerminalRun }

// RunOnce is intentionally small: all durable transitions are in Store, while
// all untrusted repository access is scoped to a temporary Workspace.
func (p Processor) RunOnce(ctx context.Context) (worked bool, err error) {
	job, err := p.Store.Claim(ctx, p.WorkerID)
	if errors.Is(err, store.ErrNoQueuedJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return p.runClaimed(ctx, job)
}

// RunForRun is called by the RabbitMQ execution consumer. The broker payload
// names one run, so this method cannot accidentally claim another queued job.
func (p Processor) RunForRun(ctx context.Context, runID uuid.UUID) (worked bool, err error) {
	job, err := p.Store.ClaimForRun(ctx, p.WorkerID, runID)
	if errors.Is(err, store.ErrNoQueuedJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return p.runClaimed(ctx, job)
}

func (p Processor) runClaimed(ctx context.Context, job *domain.ReviewJob) (worked bool, err error) {
	worked = true
	done := func() {}
	if p.TaskStarted != nil {
		if release := p.TaskStarted(); release != nil {
			done = release
		}
	}
	defer done()
	executionCtx, stopLeaseHeartbeat := p.keepClaimAlive(ctx, *job)
	defer stopLeaseHeartbeat()
	run, err := p.advance(executionCtx, job.ID, domain.RunAdmitted)
	if err != nil {
		return true, err
	}
	if validator, ok := p.Publisher.(interface{ ValidateReviewRevision(domain.ReviewJob) error }); ok {
		if revisionErr := validator.ValidateReviewRevision(*job); revisionErr != nil {
			if run.State.Terminal() {
				// Recovery must not publish a terminal status for malformed
				// historical input, even if the run ended before this claim.
				return true, nil
			}
			return p.handleProcessingError(ctx, *job, revisionErr)
		}
	}
	if stopped, stopErr := p.stopTerminalRun(ctx, *job, run); stopErr != nil || stopped {
		return true, stopErr
	}
	if verifier, ok := p.Publisher.(publisher.ReviewRevisionVerifier); ok {
		if verifyErr := verifier.VerifyCurrentReview(executionCtx, *job); verifyErr != nil {
			// A recovery job may already have retained findings. Recheck its
			// revision before that publication path as well as before OCR.
			return p.handleProcessingError(ctx, *job, verifyErr)
		}
	}
	if handled, findings, resumeErr := p.resumePublication(executionCtx, *job, run); handled {
		if resumeErr != nil {
			return p.handleProcessingError(ctx, *job, resumeErr)
		}
		return p.completeSuccessfulReview(ctx, *job, findings)
	}
	general, generalErr := p.generalConfigForJob(executionCtx, job.ID)
	if generalErr != nil {
		return p.handleProcessingError(ctx, *job, generalErr)
	}
	if p.Checks != nil && general.MergeGateEnabled {
		if checkErr := p.startCheck(executionCtx, *job); checkErr != nil && p.Logger != nil {
			// A status surface must not prevent an otherwise valid review from
			// running. The durable provider publication path still reports the
			// final result, and governance decides whether a later check blocks.
			p.Logger.Warn("could not start review check", "job_id", job.ID, "error", checkErr)
		}
	}
	// The Check/commit status carries progress without adding timeline noise.
	// Publish a PR/MR comment only when the review has a final result or a
	// terminal failure, regardless of whether a webhook or comment started it.
	run, err = p.advance(executionCtx, job.ID, domain.RunPreparing)
	if err != nil {
		return true, err
	}
	if stopped, stopErr := p.stopTerminalRun(ctx, *job, run); stopErr != nil || stopped {
		return true, stopErr
	}
	findings, err := p.processUntilTerminal(executionCtx, *job)
	if err != nil {
		return p.handleProcessingError(ctx, *job, err)
	}
	return p.completeSuccessfulReview(ctx, *job, findings)
}

func (p Processor) completeSuccessfulReview(ctx context.Context, job domain.ReviewJob, findings []domain.Finding) (bool, error) {
	general, err := p.generalConfigForJob(ctx, job.ID)
	if err != nil {
		return true, err
	}
	if err := p.Store.Succeed(ctx, job.ID, p.WorkerID); err != nil {
		return true, fmt.Errorf("mark job %s succeeded: %w", job.ID, err)
	}
	if p.Checks != nil && general.MergeGateEnabled {
		gate := publisher.EvaluateMergeGate(findings, general.MinimumBlockingSeverity)
		summary := gate.Summary(findings)
		if plans, ok := p.Store.(executionPlanStore); ok {
			if plan, planErr := plans.ReviewExecutionPlanForJob(ctx, job.ID); planErr == nil {
				summary = (publisher.ReviewResult{Findings: findings, Gate: gate, Scope: publisher.ReviewScope{
					Mode: plan.Mode, SelectedPaths: plan.SelectedPaths, DeferredFiles: plan.DeferredFiles,
				}}).CheckSummary()
			}
		}
		if checkErr := p.completeCheck(ctx, job, gate.Conclusion, summary); checkErr != nil && p.Logger != nil {
			p.Logger.Warn("could not finalize successful review check", "job_id", job.ID, "error", checkErr)
		}
	}
	if p.Logger != nil {
		p.Logger.Info("review job succeeded", "job_id", job.ID, "attempt", job.Attempts)
	}
	return true, nil
}

func (p Processor) handleProcessingError(ctx context.Context, job domain.ReviewJob, err error) (bool, error) {
	var terminal terminalRunError
	if errors.As(err, &terminal) {
		_, stopErr := p.stopTerminalRun(ctx, job, terminal.run)
		return true, stopErr
	}
	if errors.Is(err, publisher.ErrReviewHeadChanged) || errors.Is(err, publisher.ErrReviewNotOpen) {
		return p.supersedeStaleProviderHead(ctx, job)
	}
	invalidRevision := errors.Is(err, publisher.ErrInvalidReviewRevision)
	terminalFailure := invalidRevision || isTerminalExecutionFailure(err)
	var failureErr error
	if terminalFailure {
		failureErr = p.Store.FailTerminal(ctx, job.ID, p.WorkerID, err.Error())
	} else if retryAfter := publisher.RetryAfter(err); retryAfter > 0 {
		if retryStore, ok := p.Store.(retryAfterStore); ok {
			failureErr = retryStore.FailWithRetryAfter(ctx, job.ID, p.WorkerID, err.Error(), retryAfter)
		} else {
			failureErr = p.Store.Fail(ctx, job.ID, p.WorkerID, err.Error())
		}
	} else {
		failureErr = p.Store.Fail(ctx, job.ID, p.WorkerID, err.Error())
	}
	if failureErr != nil && !errors.Is(failureErr, store.ErrJobClaimLost) {
		return true, fmt.Errorf("process job %s: %w (record failure: %v)", job.ID, err, failureErr)
	}
	if terminalFailure || job.Attempts >= 5 {
		if _, transitionErr := p.advance(ctx, job.ID, domain.RunFailed); transitionErr != nil && !errors.Is(transitionErr, store.ErrJobClaimLost) {
			return true, fmt.Errorf("mark review run %s failed: %w", job.ID, transitionErr)
		}
		if invalidRevision {
			// The terminal reporter also suppresses this invalid job. Never
			// create a Check or comment from malformed persisted input.
			return true, nil
		}
		general, generalErr := p.generalConfigForJob(ctx, job.ID)
		if generalErr != nil {
			general = p.defaultGeneralConfig()
			if p.Logger != nil {
				p.Logger.Warn("could not load merge gate policy for failed review", "job_id", job.ID, "error", generalErr)
			}
		}
		if p.Checks != nil && general.MergeGateEnabled {
			summary := "The review could not be completed after retrying. See the task detail for the safe error summary."
			if terminalFailure {
				summary = "The review exceeded its execution budget. No findings were published; adjust the execution budget or retry after the model service recovers."
				if errors.Is(err, domain.ErrReviewContextExhausted) {
					summary = "The review exhausted the model context for its selected scope. No findings were published; narrow the scope or increase the model context before retrying."
				}
			}
			if checkErr := p.completeCheck(ctx, job, publisher.CheckFailure, summary); checkErr != nil && p.Logger != nil {
				p.Logger.Warn("could not finalize failed review check", "job_id", job.ID, "error", checkErr)
			}
		}
		if p.Lifecycle != nil {
			state := publisher.LifecycleFailed
			if errors.Is(err, domain.ErrReviewTimedOut) {
				state = publisher.LifecycleTimedOut
			} else if errors.Is(err, domain.ErrReviewContextExhausted) {
				state = publisher.LifecycleContextExhausted
			}
			if publishErr := p.Lifecycle.PublishTerminal(ctx, job, state); publishErr != nil && p.Logger != nil {
				p.Logger.Warn("could not publish failed review report", "job_id", job.ID, "error", publishErr)
			}
		}
	}
	if p.Logger != nil {
		p.Logger.Error("review job failed; queued for retry or terminal failure", "job_id", job.ID, "attempt", job.Attempts, "error", err)
	}
	return true, nil
}

// supersedeStaleProviderHead records the provider's current revision as the
// final publication authority. The review job becomes cancelled, while the
// durable run and its lifecycle report remain available for audit.
func (p Processor) supersedeStaleProviderHead(ctx context.Context, job domain.ReviewJob) (bool, error) {
	run, err := p.advance(ctx, job.ID, domain.RunSuperseded)
	if err != nil {
		return true, fmt.Errorf("supersede stale provider review %s: %w", job.ID, err)
	}
	if !run.State.Terminal() {
		return true, fmt.Errorf("stale provider review %s did not reach a terminal run state", job.ID)
	}
	_, err = p.stopTerminalRun(ctx, job, run)
	return true, err
}

// resumePublication uses the persisted findings and exact admitted scope when
// a worker was retried after entering the publishing stage. It never invokes
// checkout, risk planning, or the model again. Older runs without this
// checkpoint deliberately fall back to the compatible full execution path.
func (p Processor) resumePublication(ctx context.Context, job domain.ReviewJob, run domain.ReviewRun) (bool, []domain.Finding, error) {
	if run.State != domain.RunPublishing {
		return false, nil, nil
	}
	plans, ok := p.Store.(executionPlanStore)
	if !ok {
		return false, nil, nil
	}
	plan, err := plans.ReviewExecutionPlanForJob(ctx, job.ID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil, nil
	}
	if err != nil {
		return true, nil, err
	}
	findings, err := plans.FindingsForJob(ctx, job.ID)
	if err != nil {
		return true, nil, err
	}
	result, err := p.reviewResult(ctx, job, plan.Mode, riskPlanFromExecutionPlan(plan), findings)
	if err != nil {
		return true, nil, err
	}
	if err := p.persistMergeGateDecision(ctx, job.ID, result); err != nil {
		return true, nil, err
	}
	published, err := p.applyPublicationMinimum(ctx, job.ID, result.Findings, string(result.Gate.Threshold))
	if err != nil {
		return true, nil, err
	}
	result.SuppressedFindings = len(result.Findings) - len(published)
	result.Findings = published
	if err := p.publish(ctx, job, result); err != nil {
		return true, nil, err
	}
	completed, err := p.advance(ctx, job.ID, domain.RunCompleted)
	if err != nil {
		return true, nil, err
	}
	if completed.State.Terminal() && completed.State != domain.RunCompleted {
		return true, nil, terminalRunError{run: completed}
	}
	return true, findings, nil
}

func isTerminalExecutionFailure(err error) bool {
	return errors.Is(err, domain.ErrReviewTimedOut) || errors.Is(err, domain.ErrReviewContextExhausted) || publisher.IsTerminalPublicationError(err)
}

// keepClaimAlive makes the database lease explicit. A restart therefore makes
// work eligible again within two minutes, while a healthy review (including a
// long model invocation) renews before its lease can expire.
func (p Processor) keepClaimAlive(ctx context.Context, job domain.ReviewJob) (context.Context, context.CancelFunc) {
	executionCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	interval := p.leaseRenewInterval()
	lease := p.leaseDuration()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				cancel()
				return
			case <-ticker.C:
				if err := p.Store.RenewClaim(ctx, job.ID, p.WorkerID, lease); err != nil {
					if p.Logger != nil {
						p.Logger.Warn("review job lease could not be renewed; stopping execution", "job_id", job.ID, "error", err)
					}
					cancel()
					return
				}
			}
		}
	}()
	return executionCtx, func() {
		close(done)
		cancel()
	}
}

func (p Processor) leaseDuration() time.Duration {
	if p.LeaseDuration > 0 {
		return p.LeaseDuration
	}
	return 2 * time.Minute
}

func (p Processor) leaseRenewInterval() time.Duration {
	if p.LeaseRenewEvery > 0 {
		return p.LeaseRenewEvery
	}
	return 30 * time.Second
}

// processUntilTerminal covers preparation as well as OCR execution. Provider
// commands can arrive while a clone is still waiting on the network; those
// commands must cancel the checkout instead of waiting for it to complete.
func (p Processor) processUntilTerminal(ctx context.Context, job domain.ReviewJob) ([]domain.Finding, error) {
	executionCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go p.cancelReviewWhenTerminal(ctx, job.ID, cancel, done)
	findings, err := p.process(executionCtx, job)
	close(done)
	cancel()

	run, observeErr := p.advance(ctx, job.ID, domain.RunPreparing)
	if observeErr != nil {
		return findings, err
	}
	// Completed is the successful terminal state produced by process. It must
	// flow back to runClaimed so the legacy job is marked succeeded and the
	// provider Check receives the result. Only cancellation/supersession/failure
	// terminal states stop publication.
	if run.State.Terminal() && run.State != domain.RunCompleted {
		return nil, terminalRunError{run: run}
	}
	return findings, err
}

func (p Processor) process(ctx context.Context, job domain.ReviewJob) ([]domain.Finding, error) {
	checkoutCtx := ctx
	cancelCheckout := func() {}
	if p.CheckoutTimeout > 0 {
		checkoutCtx, cancelCheckout = context.WithTimeout(ctx, p.CheckoutTimeout)
	}
	defer cancelCheckout()
	workspace, err := p.Checkout.Prepare(checkoutCtx, job)
	if err != nil {
		if errors.Is(checkoutCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("prepare review workspace timed out after %s", p.CheckoutTimeout)
		}
		return nil, err
	}
	defer workspace.Close()
	run, err := p.advance(ctx, job.ID, domain.RunAnalyzing)
	if err != nil {
		return nil, err
	}
	if run.State.Terminal() && run.State != domain.RunCompleted {
		return nil, terminalRunError{run: run}
	}
	mode, err := p.riskModeForJob(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	plan, err := p.planRisk(ctx, workspace.Path, workspace.BaseSHA, job.HeadSHA, mode)
	if err != nil {
		return nil, err
	}
	plan, err = p.applyPathFilters(ctx, job.ID, plan)
	if err != nil {
		return nil, err
	}
	if plans, ok := p.Store.(executionPlanStore); ok {
		if _, err := plans.SaveReviewExecutionPlan(ctx, job.ID, executionPlanFromRisk(mode, plan)); err != nil {
			return nil, fmt.Errorf("persist admitted review scope: %w", err)
		}
	}
	findings, err := p.reviewUntilTerminal(ctx, job, workspace.Path, workspace.BaseSHA, plan)
	if err != nil {
		return nil, err
	}
	findings, err = p.applyCategoryPolicy(ctx, job.ID, findings)
	if err != nil {
		return nil, err
	}
	if err := p.Store.SaveFindings(ctx, job.ID, findings); err != nil {
		return nil, err
	}
	if shadow, ok := p.Store.(shadowRolloutQueueStore); ok {
		if err := shadow.QueueShadowRuleTests(ctx, job.ID); err != nil {
			return nil, fmt.Errorf("queue isolated shadow rollout: %w", err)
		}
	}
	run, err = p.advance(ctx, job.ID, domain.RunNormalizing)
	if err != nil {
		return nil, err
	}
	if run.State.Terminal() {
		return nil, terminalRunError{run: run}
	}
	run, err = p.advance(ctx, job.ID, domain.RunPublishing)
	if err != nil {
		return nil, err
	}
	if run.State.Terminal() {
		return nil, terminalRunError{run: run}
	}
	result, err := p.reviewResult(ctx, job, string(mode), plan, findings)
	if err != nil {
		return nil, err
	}
	if err := p.persistMergeGateDecision(ctx, job.ID, result); err != nil {
		return nil, err
	}
	published, err := p.applyPublicationMinimum(ctx, job.ID, result.Findings, string(result.Gate.Threshold))
	if err != nil {
		return nil, err
	}
	result.SuppressedFindings = len(result.Findings) - len(published)
	result.Findings = published
	if err := p.publish(ctx, job, result); err != nil {
		return nil, err
	}
	run, err = p.advance(ctx, job.ID, domain.RunCompleted)
	if err != nil {
		return nil, err
	}
	if run.State.Terminal() && run.State != domain.RunCompleted {
		return nil, terminalRunError{run: run}
	}
	return findings, nil
}

// publish persists every completed provider write before a run is terminal.
// If a provider fault happens after only some comments were accepted, those
// markers are retained and a future attempt updates them in place instead of
// re-running OCR/LLM or leaving the Console with a false all-or-nothing view.
func (p Processor) publish(ctx context.Context, job domain.ReviewJob, result publisher.ReviewResult) error {
	if p.Publisher == nil {
		return fmt.Errorf("review publisher is not configured")
	}
	var receipts []domain.PublicationReceipt
	var publishErr error
	if receiptPublisher, ok := p.Publisher.(publisher.ReceiptPublisher); ok {
		receipts, publishErr = receiptPublisher.PublishWithReceipts(ctx, job, result)
	} else {
		publishErr = p.Publisher.Publish(ctx, job, result)
	}

	if len(receipts) > 0 {
		if err := p.persistPublicationReceipts(ctx, job.ID, receipts); err != nil {
			if publishErr != nil {
				return fmt.Errorf("%w (persist provider publication receipts: %v)", publishErr, err)
			}
			return fmt.Errorf("persist provider publication receipts: %w", err)
		}
	}
	if publishErr == nil || errors.Is(publishErr, publisher.ErrReviewHeadChanged) {
		return publishErr
	}
	if err := p.persistPublicationReceipts(ctx, job.ID, []domain.PublicationReceipt{publicationFailureReceipt(job, result)}); err != nil {
		return fmt.Errorf("%w (persist provider publication failure: %v)", publishErr, err)
	}
	return publishErr
}

func (p Processor) persistPublicationReceipts(ctx context.Context, jobID uuid.UUID, receipts []domain.PublicationReceipt) error {
	store, ok := p.Store.(publicationReceiptStore)
	if !ok || len(receipts) == 0 {
		return nil
	}
	return store.RecordPublicationReceipts(ctx, jobID, receipts)
}

func (p Processor) persistMergeGateDecision(ctx context.Context, jobID uuid.UUID, result publisher.ReviewResult) error {
	decisionStore, ok := p.Store.(mergeGateDecisionStore)
	if !ok {
		return nil
	}
	_, err := decisionStore.SaveReviewMergeGateDecision(ctx, jobID, domain.ReviewMergeGateDecisionInput{
		Enabled:           result.Gate.Threshold != publisher.MergeGateOff,
		Threshold:         string(result.Gate.Threshold),
		Conclusion:        string(result.Gate.Conclusion),
		BlockingFindings:  result.Gate.Blocking,
		FindingCount:      len(result.Findings),
		EvaluationVersion: "v1",
	})
	if err != nil {
		return fmt.Errorf("persist immutable merge gate decision: %w", err)
	}
	return nil
}

// startCheck and completeCheck preserve the existing best-effort status
// behavior, but persist an opaque outcome when the reporter supports it. A
// provider status must never block the review engine; the receipt merely gives
// the Console an honest answer about which check publication last succeeded.
func (p Processor) startCheck(ctx context.Context, job domain.ReviewJob) error {
	if p.Checks == nil {
		return nil
	}
	if reporter, ok := p.Checks.(publisher.ReceiptCheckReporter); ok {
		receipt, err := reporter.StartCheckWithReceipt(ctx, job)
		if err != nil {
			p.recordCheckFailure(ctx, job, "in_progress")
			return err
		}
		return p.persistPublicationReceipts(ctx, job.ID, []domain.PublicationReceipt{receipt})
	}
	return p.Checks.StartCheck(ctx, job)
}

func (p Processor) completeCheck(ctx context.Context, job domain.ReviewJob, conclusion publisher.CheckConclusion, summary string) error {
	if p.Checks == nil {
		return nil
	}
	if reporter, ok := p.Checks.(publisher.ReceiptCheckReporter); ok {
		receipt, err := reporter.CompleteCheckWithReceipt(ctx, job, conclusion, summary)
		if err != nil {
			p.recordCheckFailure(ctx, job, "completed:"+string(conclusion))
			return err
		}
		return p.persistPublicationReceipts(ctx, job.ID, []domain.PublicationReceipt{receipt})
	}
	return p.Checks.CompleteCheck(ctx, job, conclusion, summary)
}

func (p Processor) recordCheckFailure(ctx context.Context, job domain.ReviewJob, phase string) {
	// The original provider error may contain untrusted remote data or resolver
	// details. The durable evidence records only the safe recovery instruction.
	marker := "open-review-platform:analysis-check:" + job.ID.String()
	digest := sha256.Sum256([]byte(job.HeadSHA + "\n" + marker + "\n" + phase))
	if err := p.persistPublicationReceipts(ctx, job.ID, []domain.PublicationReceipt{{
		ReceiptKind:  "status",
		StableMarker: marker,
		PayloadHash:  fmt.Sprintf("%x", digest[:]),
		LastError:    "Provider analysis status could not be updated. The review continues and will attempt a final status.",
	}}); err != nil && p.Logger != nil {
		p.Logger.Warn("could not persist analysis status failure receipt", "job_id", job.ID, "error", err)
	}
}

func publicationFailureReceipt(job domain.ReviewJob, result publisher.ReviewResult) domain.PublicationReceipt {
	marker := "open-review-platform:summary:" + job.ID.String()
	digest := sha256.Sum256([]byte(job.HeadSHA + "\n" + marker + "\n" + string(result.Gate.Conclusion) + "\n" + publisher.ResultSummary(result.Findings)))
	return domain.PublicationReceipt{
		ReceiptKind:  "summary",
		StableMarker: marker,
		PayloadHash:  fmt.Sprintf("%x", digest[:]),
		LastError:    "Provider publication did not complete. The durable job will retry using the retained execution plan and findings.",
	}
}

func (p Processor) reviewResult(ctx context.Context, job domain.ReviewJob, mode string, plan risk.Plan, findings []domain.Finding) (publisher.ReviewResult, error) {
	general, err := p.generalConfigForJob(ctx, job.ID)
	if err != nil {
		return publisher.ReviewResult{}, err
	}
	mergeGateSeverity := general.MinimumBlockingSeverity
	if !general.MergeGateEnabled {
		mergeGateSeverity = string(publisher.MergeGateOff)
	}
	result := publisher.ReviewResult{
		Findings: findings,
		Gate:     publisher.EvaluateMergeGate(findings, mergeGateSeverity),
		Scope: publisher.ReviewScope{
			Mode:                mode,
			DeferredFiles:       len(plan.Deferred),
			StaticImpactSignals: append([]string(nil), plan.StaticImpactSignals...),
		},
		EngineVersion:      p.EngineVersion,
		RuleSnapshotStatus: "none",
	}
	for _, item := range plan.Selected {
		result.Scope.SelectedPaths = append(result.Scope.SelectedPaths, item.Path)
	}
	snapshot, err := p.Store.RuleSnapshotForJob(ctx, job.ID)
	if err == nil {
		result.RuleSnapshotID = snapshot.ID.String()
		result.RuleSnapshotSHA = snapshot.SHA256
		result.CompilerVersion = snapshot.CompilerVersion
		result.RuleSnapshotStatus = "resolved"
	} else if !errors.Is(err, store.ErrNotFound) && p.Logger != nil {
		result.RuleSnapshotStatus = "unavailable"
		p.Logger.Warn("could not attach rule snapshot provenance", "job_id", job.ID, "error", err)
	} else if !errors.Is(err, store.ErrNotFound) {
		result.RuleSnapshotStatus = "unavailable"
	}
	if modelStore, ok := p.Store.(modelRouteSnapshotStore); ok {
		snapshot, routeErr := modelStore.ModelRouteForJob(ctx, job.ID)
		if routeErr == nil {
			route, decodeErr := domain.DecodeModelRoute(snapshot.Content)
			if decodeErr == nil && route.Enabled {
				result.ModelProvider = route.Provider
				result.Model = route.Model
				result.ModelRouteSHA = snapshot.ContentSHA256
				result.ModelRouteOrigin = snapshot.OriginScopeKind
			}
		}
	}
	return result, nil
}

// reviewUntilTerminal gives the executor a cancellable context while a small
// watcher observes durable run state. A GitHub push or an explicit cancel can
// therefore stop an OCR process immediately instead of merely preventing its
// later findings from being published.
func (p Processor) reviewUntilTerminal(ctx context.Context, job domain.ReviewJob, directory, base string, plan risk.Plan) ([]domain.Finding, error) {
	reviewCtx, cancel := context.WithCancel(ctx)
	routedCtx, err := p.withModelRoute(reviewCtx, job.ID)
	if err != nil {
		cancel()
		return nil, err
	}
	routedCtx, err = p.withReviewPrompts(routedCtx, job.ID)
	if err != nil {
		cancel()
		return nil, err
	}
	done := make(chan struct{})
	go p.cancelReviewWhenTerminal(ctx, job.ID, cancel, done)
	findings, reviewErr := p.reviewWithSnapshot(routedCtx, job, directory, base, plan)
	close(done)
	cancel()

	run, observeErr := p.advance(ctx, job.ID, domain.RunAnalyzing)
	if observeErr != nil {
		if reviewErr != nil {
			return nil, reviewErr
		}
		return nil, fmt.Errorf("observe review run after execution: %w", observeErr)
	}
	if run.State.Terminal() {
		return nil, terminalRunError{run: run}
	}
	return findings, reviewErr
}

type modelRouteSnapshotStore = ModelRouteSnapshotReader

// reviewConfigSnapshotStore is deliberately optional for worker compatibility
// with runs admitted before review-configuration snapshots existed. Postgres
// implements it, so every newly admitted run filters categories through the
// immutable policy captured at admission rather than the mutable UI setting.
type reviewConfigSnapshotStore = ReviewConfigSnapshotReader

func (p Processor) executionPolicy() ExecutionPolicy {
	modelStore, _ := p.Store.(ModelRouteSnapshotReader)
	configStore, _ := p.Store.(ReviewConfigSnapshotReader)
	publicationStore, _ := p.Store.(PublicationMinimumReader)
	return ExecutionPolicy{ModelRoutes: modelStore, Configurations: configStore, PublicationMinimum: publicationStore, ModelSecrets: p.ModelSecrets, Logger: p.Logger}
}

func (p Processor) applyCategoryPolicy(ctx context.Context, jobID uuid.UUID, findings []domain.Finding) ([]domain.Finding, error) {
	return p.executionPolicy().ApplyCategoryPolicy(ctx, jobID, findings)
}

func (p Processor) applyPublicationMinimum(ctx context.Context, jobID uuid.UUID, findings []domain.Finding, blockingMinimum string) ([]domain.Finding, error) {
	return p.executionPolicy().ApplyPublicationMinimum(ctx, jobID, findings, blockingMinimum)
}

// applyPathFilters runs after deterministic risk selection and before OCR. A
// filtered selected path becomes deferred evidence, so the persisted execution
// plan and provider report remain honest about why it was not analyzed.
func (p Processor) applyPathFilters(ctx context.Context, jobID uuid.UUID, plan risk.Plan) (risk.Plan, error) {
	configStore, ok := p.Store.(reviewConfigSnapshotStore)
	if !ok {
		return plan, nil
	}
	snapshot, err := configStore.ReviewConfigSnapshotForJob(ctx, jobID, domain.ReviewConfigFilters)
	if errors.Is(err, store.ErrNotFound) {
		return plan, nil
	}
	if err != nil {
		return risk.Plan{}, fmt.Errorf("load review filters for review: %w", err)
	}
	filters, err := domain.DecodeReviewFiltersConfig(snapshot.Content)
	if err != nil {
		return risk.Plan{}, fmt.Errorf("decode review filters snapshot: %w", err)
	}
	if len(plan.Selected) == 0 && len(plan.Deferred) == 0 {
		// A legacy executor can run without a risk planner, but it has no
		// trustworthy changed-file inventory for path filters. Do not claim a
		// filter was applied when there is no scope to transform.
		if p.Logger != nil && (len(filters.IncludePaths) > 0 || len(filters.ExcludePaths) > 0 || filters.SkipGenerated || filters.SkipVendor) {
			p.Logger.Warn("review path filters require a risk-planned file scope; preserving legacy full review", "job_id", jobID, "filter_config_sha256", snapshot.ContentSHA256)
		}
		return plan, nil
	}
	filtered := risk.Plan{Selected: make([]risk.Item, 0, len(plan.Selected)), Deferred: append([]risk.Item(nil), plan.Deferred...)}
	for _, item := range plan.Selected {
		if filters.AllowsPath(item.Path) {
			filtered.Selected = append(filtered.Selected, item)
			continue
		}
		item.Reasons = append(item.Reasons, "excluded by immutable review filter")
		filtered.Deferred = append(filtered.Deferred, item)
	}
	filtered.Exclude = make([]string, 0, len(filtered.Deferred))
	seen := make(map[string]struct{}, len(filtered.Deferred))
	for _, item := range filtered.Deferred {
		if item.Path == "" {
			continue
		}
		if _, duplicate := seen[item.Path]; duplicate {
			continue
		}
		seen[item.Path] = struct{}{}
		filtered.Exclude = append(filtered.Exclude, item.Path)
	}
	filtered.StaticImpactSignals = risk.StaticImpactSignals(filtered.Selected)
	if len(filtered.Selected) != len(plan.Selected) && p.Logger != nil {
		p.Logger.Info("applied immutable review path filters", "job_id", jobID, "filter_config_sha256", snapshot.ContentSHA256, "selected_paths", len(filtered.Selected), "deferred_paths", len(filtered.Deferred))
	}
	return filtered, nil
}

func (p Processor) defaultGeneralConfig() domain.ReviewGeneralConfig {
	config := domain.DefaultReviewGeneralConfig()
	if p.MergeGateSeverity == string(publisher.MergeGateOff) {
		config.MergeGateEnabled = false
	} else if publisher.ValidMergeGateSeverity(p.MergeGateSeverity) {
		config.MinimumBlockingSeverity = p.MergeGateSeverity
	}
	return config
}

func (p Processor) generalConfigForJob(ctx context.Context, jobID uuid.UUID) (domain.ReviewGeneralConfig, error) {
	config := p.defaultGeneralConfig()
	configStore, ok := p.Store.(reviewConfigSnapshotStore)
	if !ok {
		return config, nil
	}
	snapshot, err := configStore.ReviewConfigSnapshotForJob(ctx, jobID, domain.ReviewConfigGeneral)
	if errors.Is(err, store.ErrNotFound) {
		return config, nil
	}
	if err != nil {
		return domain.ReviewGeneralConfig{}, fmt.Errorf("load general policy for review: %w", err)
	}
	config, err = domain.DecodeReviewGeneralConfig(snapshot.Content)
	if err != nil {
		return domain.ReviewGeneralConfig{}, fmt.Errorf("decode general policy snapshot: %w", err)
	}
	return config, nil
}

func (p Processor) withReviewPrompts(ctx context.Context, jobID uuid.UUID) (context.Context, error) {
	return p.executionPolicy().WithReviewPrompts(ctx, jobID)
}

func (p Processor) withModelRoute(ctx context.Context, jobID uuid.UUID) (context.Context, error) {
	return p.executionPolicy().WithModelRoute(ctx, jobID)
}

func (p Processor) cancelReviewWhenTerminal(ctx context.Context, jobID uuid.UUID, cancel context.CancelFunc, done <-chan struct{}) {
	ticker := time.NewTicker(p.terminalPollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			cancel()
			return
		case <-ticker.C:
			run, err := p.advance(ctx, jobID, domain.RunAnalyzing)
			if err != nil {
				if p.Logger != nil {
					p.Logger.Warn("could not observe review run while executing", "job_id", jobID, "error", err)
				}
				continue
			}
			if run.State.Terminal() {
				cancel()
				return
			}
		}
	}
}

func (p Processor) terminalPollInterval() time.Duration {
	if p.TerminalPollInterval > 0 {
		return p.TerminalPollInterval
	}
	return time.Second
}

func (p Processor) reviewWithSnapshot(ctx context.Context, job domain.ReviewJob, directory, base string, plan risk.Plan) ([]domain.Finding, error) {
	if deferredOnlyScope(plan) {
		if p.Logger != nil {
			p.Logger.Info("risk planner deferred every changed path; skipping model review", "deferred_files", len(plan.Deferred))
		}
		return []domain.Finding{}, nil
	}
	snapshot, err := p.Store.RuleSnapshotForJob(ctx, job.ID)
	var retained *domain.RuleSnapshot
	if errors.Is(err, store.ErrNotFound) {
		// Legacy runs can lack enterprise rules but still have prompt policy.
	} else if err != nil {
		return nil, fmt.Errorf("load rule snapshot for review: %w", err)
	} else {
		retained = &snapshot
	}
	ruleFile, err := CompileExecutionRuleFile(ctx, directory, retained)
	if err != nil {
		return nil, err
	}
	if len(ruleFile.Rules) == 0 {
		return p.reviewWithScope(ctx, directory, base, job.HeadSHA, plan)
	}
	if p.Logger != nil {
		p.Logger.Info("executing review with immutable rule snapshot", "job_id", job.ID, "rule_snapshot_id", snapshot.ID, "rule_snapshot_sha256", snapshot.SHA256, "ocr_rule_entries", len(ruleFile.Rules))
	}
	return p.reviewWithRuleFile(ctx, directory, base, job.HeadSHA, plan, ruleFile)
}

func (p Processor) reviewWithRuleFile(ctx context.Context, directory, base, head string, plan risk.Plan, ruleFile rules.OCRRuleFile) ([]domain.Finding, error) {
	encoded, err := json.Marshal(ruleFile)
	if err != nil {
		return nil, fmt.Errorf("encode OCR rule file: %w", err)
	}
	if selected := selectedPaths(plan); len(selected) > 0 {
		if executor, ok := p.Executor.(RuleAndSelectedPathReviewExecutor); ok {
			return executor.ReviewWithRuleAndSelectedPaths(ctx, directory, base, head, encoded, selected)
		}
	}
	if len(plan.Exclude) > 0 {
		if executor, ok := p.Executor.(RuleAndScopedReviewExecutor); ok {
			return executor.ReviewWithRuleAndExclude(ctx, directory, base, head, encoded, plan.Exclude)
		}
	}
	executor, ok := p.Executor.(RuleAwareReviewExecutor)
	if !ok {
		return nil, fmt.Errorf("review executor does not support trusted OCR rule files")
	}
	return executor.ReviewWithRule(ctx, directory, base, head, encoded)
}

func reviewPromptRule(ctx context.Context, directory string) (string, error) {
	prompt, ok := modelroute.PromptExecutionFromContext(ctx)
	if !ok {
		return "", nil
	}
	sections := make([]string, 0, 3)
	if instruction := strings.TrimSpace(prompt.SystemInstruction); instruction != "" {
		sections = append(sections, "## Control-plane system instruction\n\n"+instruction)
	}
	if repositoryContext := strings.TrimSpace(prompt.RepositoryContext); repositoryContext != "" {
		sections = append(sections, "## Control-plane repository context\n\n"+repositoryContext)
	}
	if prompt.AllowRepositoryInstructions {
		instructions, err := readRepositoryInstructions(directory)
		if err != nil {
			return "", err
		}
		if instructions != "" {
			sections = append(sections, "## Repository instructions (explicitly enabled)\n\n"+instructions)
		}
	}
	if len(sections) == 0 {
		return "", nil
	}
	return "# Open Review prompt policy\n\n" + strings.Join(sections, "\n\n---\n\n"), nil
}

// readRepositoryInstructions imports only a small fixed set of regular files
// when an administrator opted in. It never follows a link outside the
// checkout, and it bounds each file and the aggregate so a pull request cannot
// turn prompt configuration into an unbounded read.
func readRepositoryInstructions(directory string) (string, error) {
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", fmt.Errorf("resolve review workspace for repository instructions: %w", err)
	}
	const perFileLimit = 6 << 10
	const totalLimit = 16 << 10
	var builder strings.Builder
	for _, name := range []string{"AGENTS.md", "OPENREVIEW.md", ".openreview.md", ".openreview/instructions.md", ".github/openreview.md"} {
		if builder.Len() >= totalLimit {
			break
		}
		candidate := filepath.Join(directory, filepath.FromSlash(name))
		resolved, err := filepath.EvalSymlinks(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("resolve repository instruction %s: %w", name, err)
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(resolved)
		if err != nil {
			return "", fmt.Errorf("open repository instruction %s: %w", name, err)
		}
		contents, readErr := io.ReadAll(io.LimitReader(file, perFileLimit+1))
		closeErr := file.Close()
		if readErr != nil {
			return "", fmt.Errorf("read repository instruction %s: %w", name, readErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close repository instruction %s: %w", name, closeErr)
		}
		if len(contents) > perFileLimit {
			contents = append(contents[:perFileLimit], []byte("\n[truncated by Open Review instruction limit]\n")...)
		}
		prefix := "### " + name + "\n\n"
		if builder.Len() > 0 {
			prefix = "\n\n" + prefix
		}
		remaining := totalLimit - builder.Len() - len(prefix)
		if remaining <= 0 {
			break
		}
		if len(contents) > remaining {
			contents = contents[:remaining]
		}
		if len(contents) == 0 {
			continue
		}
		builder.WriteString(prefix)
		builder.Write(contents)
	}
	return builder.String(), nil
}

func (p Processor) riskModeForJob(ctx context.Context, jobID uuid.UUID) (risk.Mode, error) {
	configured := p.RiskReviewMode
	if configured == "" {
		configured = string(risk.ModeFocused)
	}
	mode := domain.ReviewModeConfigured
	if p.Store != nil {
		resolved, err := p.Store.ReviewModeForJob(ctx, jobID)
		if err != nil {
			return "", fmt.Errorf("load review mode for job: %w", err)
		}
		mode = resolved
	}
	effective := risk.Mode(mode.RiskMode(configured))
	if !effective.Valid() {
		return "", fmt.Errorf("unsupported effective risk review mode %q", effective)
	}
	return effective, nil
}

func (p Processor) planRisk(ctx context.Context, directory, base, head string, mode risk.Mode) (risk.Plan, error) {
	if p.RiskPlanner == nil {
		return risk.Plan{}, nil
	}
	var (
		plan risk.Plan
		err  error
	)
	if planner, ok := p.RiskPlanner.(ModeAwareRiskPlanner); ok {
		plan, err = planner.PlanWithMode(ctx, directory, base, head, mode)
	} else {
		plan, err = p.RiskPlanner.Plan(ctx, directory, base, head)
	}
	if err != nil {
		return risk.Plan{}, fmt.Errorf("plan risk-based review scope: %w", err)
	}
	if p.Logger != nil {
		p.Logger.Info("planned risk-based review scope", "mode", mode, "selected_files", len(plan.Selected), "deferred_files", len(plan.Deferred))
	}
	return plan, nil
}

func (p Processor) reviewWithScope(ctx context.Context, directory, base, head string, plan risk.Plan) ([]domain.Finding, error) {
	if selected := selectedPaths(plan); len(selected) > 0 {
		if executor, ok := p.Executor.(SelectedPathReviewExecutor); ok {
			return executor.ReviewWithSelectedPaths(ctx, directory, base, head, selected)
		}
	}
	if len(plan.Exclude) > 0 {
		if executor, ok := p.Executor.(ScopedReviewExecutor); ok {
			return executor.ReviewWithExclude(ctx, directory, base, head, plan.Exclude)
		}
		if p.Logger != nil {
			p.Logger.Warn("review executor does not support risk scope; reviewing all files")
		}
	}
	return p.Executor.Review(ctx, directory, base, head)
}

func selectedPaths(plan risk.Plan) []string {
	paths := make([]string, 0, len(plan.Selected))
	for _, item := range plan.Selected {
		if item.Path != "" {
			paths = append(paths, item.Path)
		}
	}
	return paths
}

func executionPlanFromRisk(mode risk.Mode, plan risk.Plan) domain.ReviewExecutionPlan {
	// Preserve the existing count-only execution plan for unusually large diffs.
	// File scopes are evidence enrichment, never a reason to fail a review.
	var files []domain.ReviewFileScope
	if len(plan.Selected)+len(plan.Deferred) <= 10_000 {
		files = make([]domain.ReviewFileScope, 0, len(plan.Selected)+len(plan.Deferred))
		for _, item := range plan.Selected {
			files = append(files, domain.ReviewFileScope{Path: item.Path, Selected: true, Score: item.Score, Reasons: append([]string(nil), item.Reasons...), ChangeType: item.ChangeType, PreviousPath: item.PreviousPath, Additions: item.Additions, Deletions: item.Deletions, StatsKnown: item.StatsKnown, Binary: item.Binary})
		}
		for _, item := range plan.Deferred {
			files = append(files, domain.ReviewFileScope{Path: item.Path, Score: item.Score, Reasons: append([]string(nil), item.Reasons...), ChangeType: item.ChangeType, PreviousPath: item.PreviousPath, Additions: item.Additions, Deletions: item.Deletions, StatsKnown: item.StatsKnown, Binary: item.Binary})
		}
	}
	return domain.ReviewExecutionPlan{
		Mode:                string(mode),
		SelectedPaths:       selectedPaths(plan),
		DeferredFiles:       len(plan.Deferred),
		StaticImpactSignals: append([]string(nil), plan.StaticImpactSignals...),
		FileScopes:          files,
	}
}

func riskPlanFromExecutionPlan(plan domain.ReviewExecutionPlan) risk.Plan {
	riskPlan := risk.Plan{Selected: make([]risk.Item, 0, len(plan.SelectedPaths)), Deferred: make([]risk.Item, plan.DeferredFiles), StaticImpactSignals: append([]string(nil), plan.StaticImpactSignals...)}
	for _, path := range plan.SelectedPaths {
		riskPlan.Selected = append(riskPlan.Selected, risk.Item{Path: path})
	}
	return riskPlan
}

// deferredOnlyScope is an explicit focused/critical decision to skip model
// execution. An entirely empty plan means no risk planner is configured and
// preserves the full-review default.
func deferredOnlyScope(plan risk.Plan) bool {
	return len(plan.Selected) == 0 && len(plan.Deferred) > 0
}

func (p Processor) advance(ctx context.Context, jobID uuid.UUID, state domain.RunState) (domain.ReviewRun, error) {
	run, err := p.Store.AdvanceLegacyRun(ctx, jobID, state)
	if errors.Is(err, store.ErrNotFound) {
		return domain.ReviewRun{}, nil
	}
	return run, err
}

// stopTerminalRun turns a cancellation/supersession observed at a durable
// stage boundary into a terminal legacy job. This prevents an already-claimed
// worker from publishing stale findings after a user cancelled the task or a
// newer PR head superseded it.
func (p Processor) stopTerminalRun(ctx context.Context, job domain.ReviewJob, run domain.ReviewRun) (bool, error) {
	if !run.State.Terminal() || run.State == domain.RunCompleted {
		return false, nil
	}
	if err := p.Store.Cancel(ctx, job.ID, p.WorkerID); err != nil && !errors.Is(err, store.ErrJobClaimLost) {
		return true, fmt.Errorf("cancel terminal review job %s: %w", job.ID, err)
	}
	general, generalErr := p.generalConfigForJob(ctx, job.ID)
	if generalErr != nil {
		general = p.defaultGeneralConfig()
		if p.Logger != nil {
			p.Logger.Warn("could not load merge gate policy for stopped review", "job_id", job.ID, "error", generalErr)
		}
	}
	if p.Checks != nil && general.MergeGateEnabled {
		summary := "The review was stopped before findings could be published."
		conclusion := publisher.CheckNeutral
		if run.State == domain.RunFailed {
			conclusion = publisher.CheckFailure
			summary = "The review ended before trustworthy findings could be published. See the task detail for the safe error summary."
		}
		if run.State == domain.RunSuperseded {
			summary = "The review was superseded by a newer pull request revision before findings could be published."
		}
		if run.State == domain.RunNeedsAttention {
			conclusion = publisher.CheckFailure
			summary = "The review requires human attention before trustworthy findings or a merge conclusion can be published. Resolve the recorded intervention, then retry the review."
		}
		if checkErr := p.completeCheck(ctx, job, conclusion, summary); checkErr != nil && p.Logger != nil {
			p.Logger.Warn("could not finalize stopped review check", "job_id", job.ID, "error", checkErr)
		}
	}
	if p.Lifecycle != nil {
		state := publisher.LifecycleFailed
		switch run.State {
		case domain.RunCancelled:
			state = publisher.LifecycleCancelled
		case domain.RunSuperseded:
			state = publisher.LifecycleSuperseded
		case domain.RunNeedsAttention:
			state = publisher.LifecycleNeedsAttention
		}
		if publishErr := p.Lifecycle.PublishTerminal(ctx, job, state); publishErr != nil && p.Logger != nil {
			p.Logger.Warn("could not publish stopped review report", "job_id", job.ID, "error", publishErr)
		}
	}
	return true, nil
}
