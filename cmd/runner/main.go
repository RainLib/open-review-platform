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
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/publisher"
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
	resolver, err := credentials.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	executor := ocr.Executor{Binary: cfg.Runner.OCRBinary, Version: cfg.Runner.OCRVersion, GitBinary: cfg.Runner.GitBinary, Concurrency: cfg.Runner.OCRConcurrency}
	if err := executor.VerifyVersion(ctx); err != nil {
		log.Fatal(err)
	}
	if err := executor.VerifyGitVersion(ctx); err != nil {
		log.Fatal(err)
	}
	reviewPublisher := publisher.NewHTTPWithResolver(resolver)
	processor := runner.Processor{
		Store:     database,
		Checkout:  runner.Checkout{Resolver: resolver, GitBinary: cfg.Runner.GitBinary},
		Executor:  executor,
		Publisher: reviewPublisher,
		Checks:    reviewPublisher,
		WorkerID:  cfg.Runner.ID,
		Logger:    slog.Default(),
	}
	consumer, err := messaging.OpenAMQPConsumer(cfg.Broker.URL, cfg.Broker.Exchange)
	if err != nil {
		log.Fatal(err)
	}
	defer consumer.Close()
	go func() {
		err := consumer.Consume(ctx, "openreview.review.execute.v1", "review-runner-v1", func(ctx context.Context, body []byte) error {
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
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("queue execution consumer stopped", "error", err)
		}
	}()
	ticker := time.NewTicker(cfg.Runner.PollInterval)
	defer ticker.Stop()
	for {
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
