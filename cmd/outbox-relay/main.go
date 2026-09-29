package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/store"
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
	publisher, err := messaging.OpenAMQPPublisher(cfg.Broker.URL, cfg.Broker.Exchange)
	if err != nil {
		log.Fatal(err)
	}
	defer publisher.Close()
	relay := messaging.Relay{Store: database, Publisher: publisher, RelayID: cfg.Broker.RelayID}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		published, err := relay.Flush(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("outbox relay iteration failed", "error", err)
		}
		if published > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
