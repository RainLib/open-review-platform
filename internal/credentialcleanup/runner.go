package credentialcleanup

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type Store interface {
	RevokeOrphanedProviderOAuthCredentials(context.Context, time.Time, int) (int, error)
}

type Runner struct {
	Store Store
	Log   *slog.Logger
	Now   func() time.Time

	Interval time.Duration
	MaxAge   time.Duration
	Batch    int
}

func (r Runner) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	maxAge := r.MaxAge
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	batch := r.Batch
	if batch < 1 || batch > 500 {
		batch = 100
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	logger := r.Log
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		revoked, err := r.Store.RevokeOrphanedProviderOAuthCredentials(
			cleanupCtx, now().UTC().Add(-maxAge), batch,
		)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("orphaned GitLab OAuth credential cleanup failed", "error", err)
		} else if revoked > 0 {
			logger.Info("orphaned GitLab OAuth credentials revoked", "count", revoked)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
