// Package rulelab executes policy experiments in an isolated channel. It has
// no publisher dependency by design, so a Test Lab run cannot create provider
// comments, checks, review decisions, or merge-gate statuses.
package rulelab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/modelroute"
	"github.com/RainLib/open-review-platform/internal/runner"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type Store interface {
	runner.ModelRouteSnapshotReader
	runner.ReviewConfigSnapshotReader
	ReviewExecutionPlanForJob(context.Context, uuid.UUID) (domain.ReviewExecutionPlan, error)
	ClaimRuleTestRun(context.Context, string, *uuid.UUID) (domain.RuleTestExecution, error)
	CompleteRuleTestRun(context.Context, uuid.UUID, string, int, domain.RuleTestCompletion) error
	FailRuleTestRun(context.Context, uuid.UUID, string, int, string) error
	RenewRuleTestRun(context.Context, uuid.UUID, string, int, time.Duration) error
}

type Processor struct {
	Store           Store
	Checkout        runner.WorkspacePreparer
	Executor        runner.ReviewExecutor
	ModelSecrets    modelroute.SecretResolver
	EngineVersion   string
	WorkerID        string
	CheckoutTimeout time.Duration
	LeaseDuration   time.Duration
	LeaseRenewEvery time.Duration
	Logger          *slog.Logger
	TaskStarted     func() func()
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	return p.run(ctx, nil)
}

func (p Processor) Run(ctx context.Context, testRunID uuid.UUID) (bool, error) {
	if testRunID == uuid.Nil {
		return false, fmt.Errorf("rule test run id is required")
	}
	return p.run(ctx, &testRunID)
}

func (p Processor) run(ctx context.Context, testRunID *uuid.UUID) (bool, error) {
	if p.Store == nil || p.Checkout == nil || p.Executor == nil || p.WorkerID == "" {
		return false, fmt.Errorf("rule test processor is not configured")
	}
	execution, err := p.Store.ClaimRuleTestRun(ctx, p.WorkerID, testRunID)
	if errors.Is(err, store.ErrNoQueuedJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	done := func() {}
	if p.TaskStarted != nil {
		if release := p.TaskStarted(); release != nil {
			done = release
		}
	}
	defer done()
	started := time.Now()
	executionCtx, stop := p.keepClaimAlive(ctx, execution.Run.ID, execution.Run.Attempts)
	defer stop()
	completion, err := p.execute(executionCtx, execution)
	completion.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		if failErr := p.Store.FailRuleTestRun(context.WithoutCancel(ctx), execution.Run.ID, p.WorkerID, execution.Run.Attempts, err.Error()); failErr != nil {
			return true, fmt.Errorf("execute isolated rule test: %w (persist failure: %v)", err, failErr)
		}
		return true, err
	}
	if err := p.Store.CompleteRuleTestRun(ctx, execution.Run.ID, p.WorkerID, execution.Run.Attempts, completion); err != nil {
		return true, err
	}
	return true, nil
}

func (p Processor) execute(ctx context.Context, execution domain.RuleTestExecution) (domain.RuleTestCompletion, error) {
	plan, err := p.Store.ReviewExecutionPlanForJob(ctx, execution.Job.ID)
	if err != nil {
		return domain.RuleTestCompletion{}, fmt.Errorf("load source execution plan: %w; run a new source review to retain an exact plan before replay", err)
	}
	normalized, valid := domain.NormalizeReviewExecutionPlan(plan)
	if !valid || normalized.Mode != plan.Mode || !slices.Equal(normalized.SelectedPaths, plan.SelectedPaths) {
		return domain.RuleTestCompletion{}, fmt.Errorf("source execution plan is invalid; no replacement scope will be planned")
	}
	completion := domain.RuleTestCompletion{
		EngineVersion: p.EngineVersion, SelectedPathCount: len(plan.SelectedPaths), DeferredPathCount: plan.DeferredFiles,
	}
	if len(plan.SelectedPaths) == 0 {
		return completion, fmt.Errorf("source execution plan selected no paths; AI replay did not run and cannot provide policy quality evidence")
	}
	policy := runner.ExecutionPolicy{
		ModelRoutes: p.Store, Configurations: p.Store, ModelSecrets: p.ModelSecrets,
		Logger: p.Logger, RequireSnapshots: true,
	}
	ctx, err = policy.WithModelRoute(ctx, execution.Job.ID)
	if err != nil {
		return completion, err
	}
	ctx, err = policy.WithReviewPrompts(ctx, execution.Job.ID)
	if err != nil {
		return completion, err
	}
	categories, err := policy.CategoryPolicy(ctx, execution.Job.ID)
	if err != nil {
		return completion, err
	}
	checkoutCtx := ctx
	cancel := func() {}
	if p.CheckoutTimeout > 0 {
		checkoutCtx, cancel = context.WithTimeout(ctx, p.CheckoutTimeout)
	}
	defer cancel()
	workspace, err := p.Checkout.Prepare(checkoutCtx, execution.Job)
	if err != nil {
		return domain.RuleTestCompletion{}, fmt.Errorf("prepare isolated rule test workspace: %w", err)
	}
	defer workspace.Close()
	if execution.Job.BaseSHA == "" || execution.Job.HeadSHA == "" || workspace.BaseSHA != execution.Job.BaseSHA {
		return completion, fmt.Errorf("isolated checkout does not match the source review's retained revision")
	}
	ruleFile, err := runner.CompileExecutionRuleFile(ctx, workspace.Path, &execution.Snapshot)
	if err != nil {
		return completion, fmt.Errorf("compile isolated OCR rule file: %w", err)
	}
	encoded, err := json.Marshal(ruleFile)
	if err != nil {
		return completion, fmt.Errorf("encode isolated OCR rule file: %w", err)
	}
	executor, ok := p.Executor.(runner.RuleAndSelectedPathReviewExecutor)
	if !ok {
		return completion, fmt.Errorf("review executor must support exact selected paths for isolated tests")
	}
	completion.Findings, err = executor.ReviewWithRuleAndSelectedPaths(ctx, workspace.Path, workspace.BaseSHA, execution.Job.HeadSHA, encoded, append([]string(nil), plan.SelectedPaths...))
	if err != nil {
		return completion, fmt.Errorf("execute isolated OCR rule test: %w", err)
	}
	completion.Findings, _ = categories.FilterFindings(completion.Findings)
	return completion, nil
}

func (p Processor) keepClaimAlive(ctx context.Context, testRunID uuid.UUID, attempt int) (context.Context, context.CancelFunc) {
	executionCtx, cancel := context.WithCancel(ctx)
	lease := p.LeaseDuration
	if lease <= 0 {
		lease = 10 * time.Minute
	}
	interval := p.LeaseRenewEvery
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				if err := p.Store.RenewRuleTestRun(executionCtx, testRunID, p.WorkerID, attempt, lease); err != nil {
					if p.Logger != nil {
						p.Logger.Warn("isolated rule test claim was lost", "test_run_id", testRunID, "error", err)
					}
					cancel()
					return
				}
			}
		}
	}()
	return executionCtx, cancel
}
