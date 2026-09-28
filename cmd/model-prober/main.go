package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/modelprobe"
	"github.com/RainLib/open-review-platform/internal/modelroute"
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
	workerID := health.EnvironmentWorkerID("MODEL_PROBER_WORKER_ID", "model-prober")
	reporter := &health.Reporter{Store: database, WorkerID: workerID, Kind: "model-prober", Version: health.EnvironmentBuildVersion(), Capacity: 1}
	go reporter.Run(ctx)
	processor := modelprobe.Processor{
		Store: database,
		Client: modelprobe.Client{
			Resolver:             modelroute.EnvironmentResolver{},
			AllowPrivateNetworks: envBool("MODEL_PROBE_ALLOW_PRIVATE_NETWORKS"),
		},
		WorkerID: workerID, TaskStarted: reporter.BeginTask,
	}
	ticker := time.NewTicker(durationEnv("MODEL_PROBE_POLL_INTERVAL", 2*time.Second))
	defer ticker.Stop()
	for {
		worked, err := processor.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("model probe failed", "error", err)
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

func envBool(name string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return err == nil && value
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
