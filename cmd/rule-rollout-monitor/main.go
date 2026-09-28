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
	workerID := health.EnvironmentWorkerID("RULE_ROLLOUT_MONITOR_WORKER_ID", "rule-rollout-monitor")
	reporter := &health.Reporter{Store: database, WorkerID: workerID, Kind: "rule-rollout-monitor", Version: health.EnvironmentBuildVersion(), Capacity: 1}
	go reporter.Run(ctx)
	interval := 30 * time.Second
	if value, err := time.ParseDuration(strings.TrimSpace(os.Getenv("RULE_ROLLOUT_MONITOR_POLL_INTERVAL"))); err == nil && value >= 5*time.Second {
		interval = value
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		endTask := reporter.BeginTask()
		rolledBack, runErr := database.ReconcileCanaryFailures(ctx, 100)
		endTask()
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			slog.Error("canary failure reconciliation failed", "error", runErr)
		}
		if rolledBack > 0 {
			slog.Warn("canary rollouts automatically rolled back", "count", rolledBack)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
