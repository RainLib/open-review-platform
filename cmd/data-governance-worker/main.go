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

	"github.com/RainLib/open-review-platform/internal/governance"
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
	cipher, err := governance.NewArtifactCipher(os.Getenv("DATA_GOVERNANCE_ARTIFACT_KEY"))
	if err != nil {
		log.Fatal("DATA_GOVERNANCE_ARTIFACT_KEY must be a base64-encoded 32-byte key")
	}
	database, err := store.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	var regionMigrator governance.RegionMigrator
	if endpoint := strings.TrimSpace(os.Getenv("DATA_GOVERNANCE_REGION_ORCHESTRATOR_URL")); endpoint != "" {
		regionMigrator, err = governance.NewHTTPRegionOrchestrator(governance.RegionOrchestratorOptions{
			Endpoint: endpoint, Secret: os.Getenv("DATA_GOVERNANCE_REGION_ORCHESTRATOR_SECRET"),
			AllowPrivateNetworks: boolEnv("DATA_GOVERNANCE_REGION_ORCHESTRATOR_ALLOW_PRIVATE"),
			AllowInsecureHTTP:    boolEnv("DATA_GOVERNANCE_REGION_ORCHESTRATOR_ALLOW_HTTP"),
			Timeout:              durationEnv("DATA_GOVERNANCE_REGION_ORCHESTRATOR_TIMEOUT", 20*time.Second),
		})
		if err != nil {
			log.Fatal(err)
		}
	}
	workerID := health.EnvironmentWorkerID("DATA_GOVERNANCE_WORKER_ID", "data-governance-worker")
	reporter := &health.Reporter{
		Store: database, WorkerID: workerID, Kind: "data-governance-worker",
		Version: health.EnvironmentBuildVersion(), Capacity: 1,
	}
	go reporter.Run(ctx)
	processor := governance.Processor{
		Store:              database,
		Cipher:             cipher,
		WorkerID:           workerID,
		Lease:              durationEnv("DATA_GOVERNANCE_LEASE", 2*time.Minute),
		ArtifactTTL:        durationEnv("DATA_GOVERNANCE_ARTIFACT_TTL", 24*time.Hour),
		MaxRecords:         intEnv("DATA_GOVERNANCE_EXPORT_MAX_RECORDS", 10000),
		RegionMigrator:     regionMigrator,
		RegionPollInterval: durationEnv("DATA_GOVERNANCE_REGION_POLL_INTERVAL", 30*time.Second),
		TaskStarted:        reporter.BeginTask,
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		worked, runErr := processor.RunOnce(ctx)
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			slog.Error("data governance job failed", "error", runErr)
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

func boolEnv(name string) bool {
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

func intEnv(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}
