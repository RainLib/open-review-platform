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
	if p.Checks != nil {
		if checkErr := p.Checks.StartCheck(ctx, *job); checkErr != nil && p.Logger != nil {
			// A status surface must not prevent an otherwise valid review from
			// running. The durable provider publication path still reports the
			// final result, and governance decides whether a later check blocks.
			p.Logger.Warn("could not start review check", "job_id", job.ID, "error", checkErr)
		}
	}
	if err := p.advance(ctx, job.ID, domain.RunAdmitted); err != nil {
		return true, err
	}
	if err := p.advance(ctx, job.ID, domain.RunPreparing); err != nil {
		return true, err
	}
	if err := p.process(ctx, *job); err != nil {
		if failureErr := p.Store.Fail(ctx, job.ID, p.WorkerID, err.Error()); failureErr != nil && !errors.Is(failureErr, store.ErrJobClaimLost) {
			return true, fmt.Errorf("process job %s: %w (record failure: %v)", job.ID, err, failureErr)
		}
		if job.Attempts >= 5 {
			if transitionErr := p.advance(ctx, job.ID, domain.RunFailed); transitionErr != nil && !errors.Is(transitionErr, store.ErrJobClaimLost) {
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
	if err := p.advance(ctx, job.ID, domain.RunAnalyzing); err != nil {
		return err
	}
	findings, err := p.Executor.Review(ctx, workspace.Path, workspace.BaseSHA, job.HeadSHA)
	if err != nil {
		return err
	}
	if err := p.Store.SaveFindings(ctx, job.ID, findings); err != nil {
		return err
	}
	if err := p.advance(ctx, job.ID, domain.RunNormalizing); err != nil {
		return err
	}
	if err := p.advance(ctx, job.ID, domain.RunPublishing); err != nil {
		return err
	}
	if err := p.Publisher.Publish(ctx, job, findings); err != nil {
		return err
	}
	if err := p.advance(ctx, job.ID, domain.RunCompleted); err != nil {
		return err
	}
	return nil
}

func (p Processor) advance(ctx context.Context, jobID uuid.UUID, state domain.RunState) error {
	_, err := p.Store.AdvanceLegacyRun(ctx, jobID, state)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}
