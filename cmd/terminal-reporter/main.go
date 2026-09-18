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

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/RainLib/open-review-platform/internal/terminal"
)

const (
	terminalQueue    = "openreview.review.terminal.v1"
	terminalConsumer = "review-terminal-reporter-v1"
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
	consumer, err := messaging.OpenAMQPConsumer(cfg.Broker.URL, cfg.Broker.Exchange)
	if err != nil {
		log.Fatal(err)
	}
	defer consumer.Close()
	provider := publisher.NewHTTPWithResolver(resolver)
	if err := consumer.Consume(ctx, terminalQueue, terminalConsumer, func(ctx context.Context, body []byte) error {
		message, err := messaging.DecodeOutboxMessage(body)
		if err != nil {
			return err
		}
		switch message.Topic {
		case "review.run.cancelled", "review.run.superseded", "review.run.failed":
		default:
			return fmt.Errorf("unexpected terminal review topic %q", message.Topic)
		}
		return messaging.HandleExactlyOnce(ctx, database, terminalConsumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
			return terminal.Publish(ctx, database, provider, provider, message)
		})
	}); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("terminal review reporter stopped", "error", err)
	}
}
