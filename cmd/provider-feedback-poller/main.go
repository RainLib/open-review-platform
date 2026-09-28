package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/providerfeedback"
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
	resolver, err := credentials.New(cfg, database)
	if err != nil {
		log.Fatal(err)
	}
	workerID := health.EnvironmentWorkerID("PROVIDER_FEEDBACK_POLLER_WORKER_ID", "provider-feedback-poller")
	reporter := &health.Reporter{
		Store: database, WorkerID: workerID, Kind: "provider-feedback-poller",
		Version: health.EnvironmentBuildVersion(), Capacity: 1,
	}
	go reporter.Run(ctx)
	processor := providerfeedback.Processor{
		Store: database, Client: providerfeedback.Client{Resolver: resolver}, WorkerID: workerID,
		Lease:       durationEnv("PROVIDER_FEEDBACK_POLL_LEASE", time.Minute),
		PollEvery:   durationEnv("PROVIDER_FEEDBACK_POLL_INTERVAL", 5*time.Minute),
		TaskStarted: reporter.BeginTask,
	}
	ticker := time.NewTicker(durationEnv("PROVIDER_FEEDBACK_WORKER_INTERVAL", 5*time.Second))
	defer ticker.Stop()
	for {
		worked, runErr := processor.RunOnce(ctx)
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			slog.Error("provider Issue feedback poll failed", "error", runErr)
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

func durationEnv(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
