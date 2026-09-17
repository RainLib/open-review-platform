package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type ReviewExecutor interface {
	Review(context.Context, string, string, string) ([]domain.Finding, error)
}

type Processor struct {
	Store     store.Store
	Checkout  Checkout
	Executor  ReviewExecutor
	Publisher publisher.Publisher
	Checks    publisher.CheckReporter
	WorkerID  string
	Logger    *slog.Logger
}

var errTerminalRun = errors.New("review run became terminal")

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
	if err := p.process(ctx, *job); err != nil {
		if errors.Is(err, errTerminalRun) {
			_, stopErr := p.stopTerminalRun(ctx, *job, domain.ReviewRun{State: domain.RunCancelled})
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
		if checkErr := p.Checks.CompleteCheck(ctx, *job, publisher.CheckSuccess, "AI analysis completed. Findings, if any, were published to this pull request."); checkErr != nil && p.Logger != nil {
			p.Logger.Warn("could not finalize successful review check", "job_id", job.ID, "error", checkErr)
		}
	}
	if p.Logger != nil {
		p.Logger.Info("review job succeeded", "job_id", job.ID, "attempt", job.Attempts)
	}
	return true, nil
}

func (p Processor) process(ctx context.Context, job domain.ReviewJob) error {
	workspace, err := p.Checkout.Prepare(ctx, job)
	if err != nil {
		return err
	}
	defer workspace.Close()
	run, err := p.advance(ctx, job.ID, domain.RunAnalyzing)
	if err != nil {
		return err
	}
	if run.State.Terminal() {
		return errTerminalRun
	}
	findings, err := p.Executor.Review(ctx, workspace.Path, workspace.BaseSHA, job.HeadSHA)
	if err != nil {
		return err
	}
	if err := p.Store.SaveFindings(ctx, job.ID, findings); err != nil {
		return err
	}
	run, err = p.advance(ctx, job.ID, domain.RunNormalizing)
	if err != nil {
		return err
	}
	if run.State.Terminal() {
		return errTerminalRun
	}
	run, err = p.advance(ctx, job.ID, domain.RunPublishing)
	if err != nil {
		return err
	}
	if run.State.Terminal() {
		return errTerminalRun
	}
	if err := p.Publisher.Publish(ctx, job, findings); err != nil {
		return err
	}
	run, err = p.advance(ctx, job.ID, domain.RunCompleted)
	if err != nil {
		return err
	}
	if run.State.Terminal() && run.State != domain.RunCompleted {
		return errTerminalRun
	}
	return nil
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
