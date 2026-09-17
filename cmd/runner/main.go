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
	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/engine/ocr"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/runner"
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
	resolver, err := credentials.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	executor := ocr.Executor{Binary: cfg.Runner.OCRBinary, Version: cfg.Runner.OCRVersion}
	if err := executor.VerifyVersion(ctx); err != nil {
		log.Fatal(err)
	}
	processor := runner.Processor{
		Store:     database,
		Checkout:  runner.Checkout{Resolver: resolver},
		Executor:  executor,
		Publisher: publisher.NewHTTPWithResolver(resolver),
		WorkerID:  cfg.Runner.ID,
		Logger:    slog.Default(),
	}
	ticker := time.NewTicker(cfg.Runner.PollInterval)
	defer ticker.Stop()
	for {
		worked, err := processor.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("runner iteration failed", "error", err)
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
