// issue-publisher writes the provider Issues that were admitted by the
// transactionally persisted auto-create policy. It never receives raw
// provider tokens from RabbitMQ; credentials are resolved immediately before
// the provider call and the durable receipt is the source of truth.
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
	"github.com/RainLib/open-review-platform/internal/issuepublisher"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
)

const (
	externalIssueQueue    = "openreview.external-issue.publish.v1"
	externalIssueConsumer = "external-issue-publisher-v1"
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
	consumer, err := messaging.OpenAMQPConsumer(cfg.Broker.URL, cfg.Broker.Exchange)
	if err != nil {
		log.Fatal(err)
	}
	defer consumer.Close()
	provider := publisher.NewHTTPWithResolver(resolver)
	processor := issuepublisher.Processor{Store: database, Provider: provider}
	if err := consumer.Consume(ctx, externalIssueQueue, externalIssueConsumer, func(ctx context.Context, body []byte) error {
		message, err := messaging.DecodeOutboxMessage(body)
		if err != nil {
			return err
		}
		if message.Topic != "external.issue.create" && message.Topic != "external.issue.close" {
			return fmt.Errorf("unexpected external issue topic %q", message.Topic)
		}
		return messaging.HandleExactlyOnce(ctx, database, externalIssueConsumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
			return processor.Handle(ctx, message)
		})
	}); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("external issue publisher stopped", "error", err)
	}
}
