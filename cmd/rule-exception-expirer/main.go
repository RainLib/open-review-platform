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

	"github.com/RainLib/open-review-platform/internal/exceptionexpiry"
	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/store"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	databaseURL := strings.TrimSpace(os.Getenv("CONTROL_DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("CONTROL_DATABASE_URL is required")
	}
	database, err := store.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	workerID := health.EnvironmentWorkerID("RULE_EXCEPTION_EXPIRER_WORKER_ID", "rule-exception-expirer")
	reporter := &health.Reporter{Store: database, WorkerID: workerID, Kind: "rule-exception-expirer", Version: health.EnvironmentBuildVersion(), Capacity: 1}
	go reporter.Run(ctx)
	processor := exceptionexpiry.Processor{
		Store: database, TenantLimit: exceptionexpiry.ParseTenantLimit(os.Getenv("RULE_EXCEPTION_EXPIRER_TENANT_LIMIT"), 100), TaskStarted: reporter.BeginTask,
	}
	ticker := time.NewTicker(durationEnv("RULE_EXCEPTION_EXPIRER_POLL_INTERVAL", 30*time.Second))
	defer ticker.Stop()
	for {
		worked, runErr := processor.RunOnce(ctx)
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			slog.Error("rule exception expiry reconciliation failed", "error", runErr)
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
