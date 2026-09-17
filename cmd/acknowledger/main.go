// acknowledger advances verified review runs from acknowledged to admitted.
// It is intentionally isolated from the long-running OCR runner: a burst of
// slow reviews must never delay the first durable queue hand-off.
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
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

const (
	ackQueue    = "openreview.review.ack.v1"
	ackConsumer = "review-acknowledger-v1"
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

	err = consumer.Consume(ctx, ackQueue, ackConsumer, func(ctx context.Context, body []byte) error {
		message, err := messaging.DecodeOutboxMessage(body)
		if err != nil {
			return err
		}
		if message.Topic != "review.run.acknowledged" {
			return fmt.Errorf("unexpected acknowledgement topic %q", message.Topic)
		}
		return messaging.HandleExactlyOnce(ctx, database, ackConsumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
			runID, err := payloadRunID(message.Payload)
			if err != nil {
				return err
			}
			_, err = database.AdvanceRun(ctx, runID, domain.RunAdmitted)
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			return err
		})
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("review acknowledger stopped", "error", err)
	}
}

func payloadRunID(payload map[string]any) (uuid.UUID, error) {
	raw, ok := payload["run_id"].(string)
	if !ok {
		return uuid.Nil, fmt.Errorf("acknowledgement payload run_id is invalid")
	}
	runID, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse acknowledgement run id: %w", err)
	}
	return runID, nil
}
