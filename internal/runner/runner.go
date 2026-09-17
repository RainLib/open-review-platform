package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
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

type RiskPlanner interface {
	Plan(context.Context, string, string, string) (risk.Plan, error)
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
	RuleSnapshotForJob(context.Context, uuid.UUID) (domain.RuleSnapshot, error)
	SaveFindings(context.Context, uuid.UUID, []domain.Finding) error
	Succeed(context.Context, uuid.UUID, string) error
	Fail(context.Context, uuid.UUID, string, string) error
	Cancel(context.Context, uuid.UUID, string) error
}

type Processor struct {
	Store           RunnerStore
	Checkout        WorkspacePreparer
	Executor        ReviewExecutor
	Publisher       publisher.Publisher
	Checks          publisher.CheckReporter
	RiskPlanner     RiskPlanner
	CheckoutTimeout time.Duration
	WorkerID        string
	Logger          *slog.Logger
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
	run, err := p.advance(ctx, job.ID, domain.RunAdmitted)
	if err != nil {
		return true, err
	}
	if stopped, stopErr := p.stopTerminalRun(ctx, *job, run); stopErr != nil || stopped {
		return true, stopErr
	}
	if p.Checks != nil {
		if checkErr := p.Checks.StartCheck(ctx, *job); checkErr != nil && p.Logger != nil {
			// A status surface must not prevent an otherwise valid review from
			// running. The durable provider publication path still reports the
			// final result, and governance decides whether a later check blocks.
			p.Logger.Warn("could not start review check", "job_id", job.ID, "error", checkErr)
		}
	}
	run, err = p.advance(ctx, job.ID, domain.RunPreparing)
	if err != nil {
		return true, err
	}
	if stopped, stopErr := p.stopTerminalRun(ctx, *job, run); stopErr != nil || stopped {
		return true, stopErr
	}
	findings, err := p.processUntilTerminal(ctx, *job)
	if err != nil {
		var terminal terminalRunError
		if errors.As(err, &terminal) {
			_, stopErr := p.stopTerminalRun(ctx, *job, terminal.run)
			return true, stopErr
		}
		if failureErr := p.Store.Fail(ctx, job.ID, p.WorkerID, err.Error()); failureErr != nil && !errors.Is(failureErr, store.ErrJobClaimLost) {
			return true, fmt.Errorf("process job %s: %w (record failure: %v)", job.ID, err, failureErr)
		}
		if job.Attempts >= 5 {
			if _, transitionErr := p.advance(ctx, job.ID, domain.RunFailed); transitionErr != nil && !errors.Is(transitionErr, store.ErrJobClaimLost) {
				return true, fmt.Errorf("mark review run %s failed: %w", job.ID, transitionErr)
			}
			if p.Checks != nil {
				if checkErr := p.Checks.CompleteCheck(ctx, *job, publisher.CheckFailure, "The review could not be completed after retrying. See the task detail for the safe error summary."); checkErr != nil && p.Logger != nil {
					p.Logger.Warn("could not finalize failed review check", "job_id", job.ID, "error", checkErr)
				}
			}
		}
		if p.Logger != nil {
			p.Logger.Error("review job failed; queued for retry or terminal failure", "job_id", job.ID, "attempt", job.Attempts, "error", err)
		}
		return true, nil
	}
	if err := p.Store.Succeed(ctx, job.ID, p.WorkerID); err != nil {
		return true, fmt.Errorf("mark job %s succeeded: %w", job.ID, err)
	}
	if p.Checks != nil {
		if checkErr := p.Checks.CompleteCheck(ctx, *job, publisher.CheckSuccess, publisher.ResultSummary(findings)); checkErr != nil && p.Logger != nil {
			p.Logger.Warn("could not finalize successful review check", "job_id", job.ID, "error", checkErr)
		}
	}
	if p.Logger != nil {
		p.Logger.Info("review job succeeded", "job_id", job.ID, "attempt", job.Attempts)
	}
	return true, nil
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
	if run.State.Terminal() {
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
	if run.State.Terminal() {
		return nil, terminalRunError{run: run}
	}
	plan, err := p.planRisk(ctx, workspace.Path, workspace.BaseSHA, job.HeadSHA)
	if err != nil {
		return nil, err
	}
	findings, err := p.reviewUntilTerminal(ctx, job, workspace.Path, workspace.BaseSHA, plan.Exclude)
	if err != nil {
		return nil, err
	}
	if err := p.Store.SaveFindings(ctx, job.ID, findings); err != nil {
		return nil, err
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
	if err := p.Publisher.Publish(ctx, job, findings); err != nil {
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

// reviewUntilTerminal gives the executor a cancellable context while a small
// watcher observes durable run state. A GitHub push or an explicit cancel can
// therefore stop an OCR process immediately instead of merely preventing its
// later findings from being published.
func (p Processor) reviewUntilTerminal(ctx context.Context, job domain.ReviewJob, directory, base string, exclude []string) ([]domain.Finding, error) {
	reviewCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go p.cancelReviewWhenTerminal(ctx, job.ID, cancel, done)
	findings, reviewErr := p.reviewWithSnapshot(reviewCtx, job, directory, base, exclude)
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

func (p Processor) reviewWithSnapshot(ctx context.Context, job domain.ReviewJob, directory, base string, exclude []string) ([]domain.Finding, error) {
	snapshot, err := p.Store.RuleSnapshotForJob(ctx, job.ID)
	if errors.Is(err, store.ErrNotFound) {
		return p.reviewWithScope(ctx, directory, base, job.HeadSHA, exclude)
	}
	if err != nil {
		return nil, fmt.Errorf("load rule snapshot for review: %w", err)
	}
	var compiled rules.Snapshot
	if err := json.Unmarshal(snapshot.CanonicalPayload, &compiled); err != nil {
		return nil, fmt.Errorf("decode rule snapshot %s: %w", snapshot.ID, err)
	}
	ruleFile, err := rules.OCRRuleFileForSnapshot(compiled)
	if err != nil {
		return nil, fmt.Errorf("compile OCR rule file from snapshot %s: %w", snapshot.ID, err)
	}
	if len(ruleFile.Rules) == 0 {
		return p.reviewWithScope(ctx, directory, base, job.HeadSHA, exclude)
	}
	if p.Logger != nil {
		p.Logger.Info("executing review with immutable rule snapshot", "job_id", job.ID, "rule_snapshot_id", snapshot.ID, "rule_snapshot_sha256", snapshot.SHA256, "ocr_rule_entries", len(ruleFile.Rules))
	}
	encoded, err := json.Marshal(ruleFile)
	if err != nil {
		return nil, fmt.Errorf("encode OCR rule file from snapshot %s: %w", snapshot.ID, err)
	}
	if executor, ok := p.Executor.(RuleAndScopedReviewExecutor); ok {
		return executor.ReviewWithRuleAndExclude(ctx, directory, base, job.HeadSHA, encoded, exclude)
	}
	executor, ok := p.Executor.(RuleAwareReviewExecutor)
	if !ok {
		return nil, fmt.Errorf("review executor does not support enterprise rule snapshots")
	}
	return executor.ReviewWithRule(ctx, directory, base, job.HeadSHA, encoded)
}

func (p Processor) planRisk(ctx context.Context, directory, base, head string) (risk.Plan, error) {
	if p.RiskPlanner == nil {
		return risk.Plan{}, nil
	}
	plan, err := p.RiskPlanner.Plan(ctx, directory, base, head)
	if err != nil {
		return risk.Plan{}, fmt.Errorf("plan risk-based review scope: %w", err)
	}
	if p.Logger != nil {
		p.Logger.Info("planned risk-based review scope", "selected_files", len(plan.Selected), "deferred_files", len(plan.Deferred))
	}
	return plan, nil
}

func (p Processor) reviewWithScope(ctx context.Context, directory, base, head string, exclude []string) ([]domain.Finding, error) {
	if len(exclude) > 0 {
		if executor, ok := p.Executor.(ScopedReviewExecutor); ok {
			return executor.ReviewWithExclude(ctx, directory, base, head, exclude)
		}
		if p.Logger != nil {
			p.Logger.Warn("review executor does not support risk scope; reviewing all files")
		}
	}
	return p.Executor.Review(ctx, directory, base, head)
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
	if !run.State.Terminal() {
		return false, nil
	}
	if err := p.Store.Cancel(ctx, job.ID, p.WorkerID); err != nil && !errors.Is(err, store.ErrJobClaimLost) {
		return true, fmt.Errorf("cancel terminal review job %s: %w", job.ID, err)
	}
	if p.Checks != nil {
		summary := "The review was stopped before findings could be published."
		if run.State == domain.RunSuperseded {
			summary = "The review was superseded by a newer pull request revision before findings could be published."
		}
		if checkErr := p.Checks.CompleteCheck(ctx, job, publisher.CheckNeutral, summary); checkErr != nil && p.Logger != nil {
			p.Logger.Warn("could not finalize stopped review check", "job_id", job.ID, "error", checkErr)
		}
	}
	return true, nil
}
