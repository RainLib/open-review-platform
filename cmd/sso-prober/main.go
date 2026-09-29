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
	"github.com/RainLib/open-review-platform/internal/sso"
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
	workerID := health.EnvironmentWorkerID("SSO_PROBER_WORKER_ID", "sso-prober")
	reporter := &health.Reporter{
		Store: database, WorkerID: workerID, Kind: "sso-prober",
		Version: health.EnvironmentBuildVersion(), Capacity: 1,
	}
	go reporter.Run(ctx)
	processor := sso.Processor{
		Store:       database,
		Client:      sso.ProbeClient{AllowPrivateNetworks: envBool("SSO_PROBE_ALLOW_PRIVATE_NETWORKS")},
		WorkerID:    workerID,
		TaskStarted: reporter.BeginTask,
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		worked, err := processor.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("SSO probe failed", "error", err)
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
