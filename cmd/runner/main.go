package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/engine/ocr"
	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/modelroute"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/risk"
	"github.com/RainLib/open-review-platform/internal/rulelab"
	"github.com/RainLib/open-review-platform/internal/runner"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	database, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	resolver, err := credentials.New(cfg, database)
	if err != nil {
		log.Fatal(err)
	}
	executor := ocr.Executor{
		Binary:         cfg.Runner.OCRBinary,
		Version:        cfg.Runner.OCRVersion,
		GitBinary:      cfg.Runner.GitBinary,
		Concurrency:    cfg.Runner.OCRConcurrency,
		Effort:         cfg.Runner.OCREffort,
		MaxTokens:      cfg.Runner.OCRMaxTokens,
		TokenBudget:    cfg.Runner.OCRTokenBudget,
		SubtaskTimeout: cfg.Runner.OCRSubtaskTimeout,
		Timeout:        cfg.Runner.OCRTimeout,
	}
	if err := executor.VerifyVersion(ctx); err != nil {
		log.Fatal(err)
	}
	if err := executor.VerifyGitVersion(ctx); err != nil {
		log.Fatal(err)
	}
	reviewPublisher := publisher.NewHTTPWithResolverAndSnapshotsAndConsoleURL(resolver, database, cfg.ConsoleURL)
	reporter := &health.Reporter{
		Store: database, WorkerID: cfg.Runner.ID, Kind: "review-runner",
		Version: health.EnvironmentBuildVersion(), Capacity: max(1, cfg.Runner.OCRConcurrency),
	}
	go reporter.Run(ctx)
	processor := runner.Processor{
		Store:             database,
		Checkout:          runner.Checkout{Resolver: resolver, GitBinary: cfg.Runner.GitBinary},
		Executor:          executor,
		Publisher:         reviewPublisher,
		Checks:            reviewPublisher,
		Lifecycle:         reviewPublisher,
		RiskPlanner:       risk.Planner{GitBinary: cfg.Runner.GitBinary, Mode: risk.Mode(cfg.Runner.RiskReviewMode)},
		RiskReviewMode:    cfg.Runner.RiskReviewMode,
		EngineVersion:     cfg.Runner.OCRVersion,
		CheckoutTimeout:   cfg.Runner.CheckoutTimeout,
		LeaseDuration:     cfg.Runner.LeaseDuration,
		LeaseRenewEvery:   cfg.Runner.LeaseRenewEvery,
		MergeGateSeverity: cfg.Runner.MergeGateSeverity,
		WorkerID:          cfg.Runner.ID,
		Logger:            slog.Default(),
		ModelSecrets:      modelroute.EnvironmentResolver{},
		TaskStarted:       reporter.BeginTask,
	}
	testProcessor := rulelab.Processor{
		Store: database, Checkout: runner.Checkout{Resolver: resolver, GitBinary: cfg.Runner.GitBinary},
		Executor: executor, ModelSecrets: modelroute.EnvironmentResolver{},
		EngineVersion: cfg.Runner.OCRVersion, WorkerID: cfg.Runner.ID + "-rule-test",
		CheckoutTimeout: cfg.Runner.CheckoutTimeout, LeaseDuration: cfg.Runner.LeaseDuration,
		LeaseRenewEvery: cfg.Runner.LeaseRenewEvery, Logger: slog.Default(),
		TaskStarted: reporter.BeginTask,
	}
	go func() {
		consumeQueue(ctx, cfg.Broker.URL, cfg.Broker.Exchange, "openreview.review.execute.v1", "review-runner-v1", func(ctx context.Context, body []byte) error {
			message, err := messaging.DecodeOutboxMessage(body)
			if err != nil {
				return err
			}
			if message.Topic != "review.run.admitted" {
				return fmt.Errorf("unexpected execution topic %q", message.Topic)
			}
			return messaging.HandleExactlyOnce(ctx, database, "review-runner-v1", message, func(ctx context.Context, message domain.OutboxMessage) error {
				runID, err := runIDFromPayload(message.Payload)
				if err != nil {
					return err
				}
				_, err = processor.RunForRun(ctx, runID)
				return err
			})
		}, openRunnerConsumer, time.Second)
	}()
	go func() {
		consumeQueue(ctx, cfg.Broker.URL, cfg.Broker.Exchange, "openreview.rule-test.execute.v1", "rule-test-runner-v1", func(ctx context.Context, body []byte) error {
			message, err := messaging.DecodeOutboxMessage(body)
			if err != nil {
				return err
			}
			if message.Topic != "rule.test.requested" {
				return fmt.Errorf("unexpected rule test topic %q", message.Topic)
			}
			return messaging.HandleExactlyOnce(ctx, database, "rule-test-runner-v1", message, func(ctx context.Context, message domain.OutboxMessage) error {
				testRunID, err := testRunIDFromPayload(message.Payload)
				if err != nil {
					return err
				}
				_, err = testProcessor.Run(ctx, testRunID)
				return err
			})
		}, openRunnerConsumer, time.Second)
	}()
	ticker := time.NewTicker(cfg.Runner.PollInterval)
	defer ticker.Stop()
	for {
		worked, runErr := testProcessor.RunOnce(ctx)
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			slog.Error("rule test runner iteration failed", "error", runErr)
		} else if worked {
			continue
		}
		worked, err := processor.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("runner iteration failed", "error", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type queueConsumer interface {
	Consume(context.Context, string, string, func(context.Context, []byte) error) error
	Close() error
}

func openRunnerConsumer(url, exchange string) (queueConsumer, error) {
	return messaging.OpenAMQPConsumer(url, exchange)
}

// The database poller alone cannot keep RabbitMQ delivery live. In
// particular, broker replacement closes Consume's delivery channel while the
// runner process and its heartbeat can remain healthy. Give each queue its
// own reconnecting connection so one failed consumer cannot strand the other.
func consumeQueue(ctx context.Context, url, exchange, queue, consumerID string,
	handler func(context.Context, []byte) error,
	open func(string, string) (queueConsumer, error), initialBackoff time.Duration,
) {
	backoff := initialBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	for ctx.Err() == nil {
		consumer, err := open(url, exchange)
		if err == nil {
			err = consumer.Consume(ctx, queue, consumerID, handler)
			_ = consumer.Close()
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = fmt.Errorf("consumer returned without cancellation")
		}
		slog.Warn("review queue consumer reconnecting", "queue", queue, "error", err, "backoff", backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func testRunIDFromPayload(payload map[string]any) (uuid.UUID, error) {
	raw, ok := payload["test_run_id"].(string)
	if !ok {
		return uuid.Nil, fmt.Errorf("rule test payload test_run_id is invalid")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse rule test run id: %w", err)
	}
	return id, nil
}

func runIDFromPayload(payload map[string]any) (uuid.UUID, error) {
	raw, ok := payload["run_id"].(string)
	if !ok {
		return uuid.Nil, fmt.Errorf("execution payload run_id is invalid")
	}
	runID, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse execution run id: %w", err)
	}
	return runID, nil
}
