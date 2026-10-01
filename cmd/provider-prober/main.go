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

	"github.com/RainLib/open-review-platform/internal/agentworkflow"
	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/providerchecks"
	"github.com/RainLib/open-review-platform/internal/providerhealth"
	"github.com/RainLib/open-review-platform/internal/providermetadata"
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
	workerID := health.EnvironmentWorkerID("PROVIDER_PROBER_WORKER_ID", "provider-prober")
	reporter := &health.Reporter{
		Store: database, WorkerID: workerID, Kind: "provider-prober",
		Version: health.EnvironmentBuildVersion(), Capacity: 1,
	}
	go reporter.Run(ctx)
	processor := providerhealth.Processor{
		Store: database,
		Client: providerhealth.Client{
			Resolver:             resolver,
			AllowPrivateNetworks: envBool("PROVIDER_PROBE_ALLOW_PRIVATE_NETWORKS"),
			AllowInsecureHTTP:    envBool("PROVIDER_PROBE_ALLOW_HTTP"),
		},
		WorkerID:    workerID,
		Lease:       durationEnv("PROVIDER_PROBE_LEASE", time.Minute),
		ProbeEvery:  durationEnv("PROVIDER_PROBE_INTERVAL", 5*time.Minute),
		TaskStarted: reporter.BeginTask,
	}
	checksWorkerID := health.EnvironmentWorkerID("PROVIDER_CHECKS_WORKER_ID", "provider-checks-worker")
	checksReporter := &health.Reporter{
		Store: database, WorkerID: checksWorkerID, Kind: "provider-checks-worker",
		Version: health.EnvironmentBuildVersion(), Capacity: 1,
	}
	go checksReporter.Run(ctx)
	checksProcessor := providerchecks.Processor{
		Store: database,
		Client: providerchecks.Client{
			Resolver:             resolver,
			AllowPrivateNetworks: envBool("PROVIDER_CHECKS_ALLOW_PRIVATE_NETWORKS"),
			AllowInsecureHTTP:    envBool("PROVIDER_CHECKS_ALLOW_HTTP"),
			OwnGitHubAppID:       positiveInt64Env("GITHUB_APP_ID"),
		},
		WorkerID:    checksWorkerID,
		Lease:       durationEnv("PROVIDER_CHECKS_LEASE", time.Minute),
		PollEvery:   durationEnv("PROVIDER_CHECKS_POLL_INTERVAL", 5*time.Minute),
		TaskStarted: checksReporter.BeginTask,
	}
	go func() {
		ticker := time.NewTicker(durationEnv("PROVIDER_CHECKS_WORKER_INTERVAL", 5*time.Second))
		defer ticker.Stop()
		for {
			worked, runErr := checksProcessor.RunOnce(ctx)
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				slog.Error("provider check observation failed", "error", runErr)
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
	}()
	workflowReporter := &health.Reporter{Store: database, WorkerID: health.EnvironmentWorkerID("AGENT_DELIVERY_WORKER_ID", "agent-delivery-monitor"), Kind: "agent-delivery-monitor", Version: health.EnvironmentBuildVersion(), Capacity: 1}
	go workflowReporter.Run(ctx)
	workflowProcessor := agentworkflow.Processor{Store: database, Resolver: resolver, WorkerID: workflowReporter.WorkerID, AllowHTTP: cfg.Environment == "development" && envBool("PROVIDER_CHECKS_ALLOW_HTTP"), AllowPrivateNetworks: envBool("PROVIDER_CHECKS_ALLOW_PRIVATE_NETWORKS")}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			release := workflowReporter.BeginTask()
			worked, err := workflowProcessor.RunOnce(ctx)
			release()
			if err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("Agent delivery observation failed", "error", err)
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
	}()
	authorWorkerID := health.EnvironmentWorkerID("GITLAB_AUTHOR_WORKER_ID", "gitlab-author-admitter")
	authorReporter := &health.Reporter{
		Store: database, WorkerID: authorWorkerID, Kind: "gitlab-author-admitter",
		Version: health.EnvironmentBuildVersion(), Capacity: 1,
	}
	go authorReporter.Run(ctx)
	authorProcessor := providermetadata.Processor{
		Store: database,
		Client: providermetadata.GitLabAuthorClient{
			Resolver:             resolver,
			AllowPrivateNetworks: envBool("PROVIDER_PROBE_ALLOW_PRIVATE_NETWORKS"),
			AllowInsecureHTTP:    envBool("PROVIDER_PROBE_ALLOW_HTTP"),
		},
		WorkerID: authorWorkerID, Lease: time.Minute, TaskStarted: authorReporter.BeginTask,
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			worked, runErr := authorProcessor.RunOnce(ctx)
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				slog.Error("GitLab author admission failed", "error", runErr)
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
	}()
	poll := time.NewTicker(durationEnv("PROVIDER_PROBE_POLL_INTERVAL", 5*time.Second))
	defer poll.Stop()
	for {
		worked, runErr := processor.RunOnce(ctx)
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			slog.Error("provider health probe failed", "error", runErr)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
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

func positiveInt64Env(name string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value < 1 {
		return 0
	}
	return value
}
