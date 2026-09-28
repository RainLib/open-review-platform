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
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/notification"
	"github.com/RainLib/open-review-platform/internal/store"
)

const (
	notificationQueue    = "openreview.notification.v1"
	notificationConsumer = "review-notifier-v1"
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
	consumer, err := messaging.OpenAMQPConsumer(cfg.Broker.URL, cfg.Broker.Exchange)
	if err != nil {
		log.Fatal(err)
	}
	defer consumer.Close()
	service := notification.Service{
		Store:  database,
		Sender: notification.Sender{Resolver: notification.EnvResolver{}},
	}
	reporter := &health.Reporter{
		Store: database, WorkerID: health.EnvironmentWorkerID("NOTIFIER_WORKER_ID", "notifier"),
		Kind: "notifier", Version: health.EnvironmentBuildVersion(), Capacity: 1,
		Interval: 15 * time.Second,
	}
	go reporter.Run(ctx)
	if err := consumer.Consume(ctx, notificationQueue, notificationConsumer, func(ctx context.Context, body []byte) error {
		done := reporter.BeginTask()
		defer done()
		message, err := messaging.DecodeOutboxMessage(body)
		if err != nil {
			return err
		}
		if !isNotificationTopic(message.Topic) {
			return fmt.Errorf("unexpected notification topic %q", message.Topic)
		}
		return messaging.HandleExactlyOnce(ctx, database, notificationConsumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
			return service.Handle(ctx, message)
		})
	}); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("notification consumer stopped", "error", err)
	}
}

func isNotificationTopic(topic string) bool {
	switch topic {
	case "review.run.completed", "review.run.failed", "review.run.cancelled", "review.run.superseded", "review.run.needs_attention", "notification.destination.test":
		return true
	default:
		return false
	}
}
