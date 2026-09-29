package health

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type HeartbeatStore interface {
	UpsertWorkerHeartbeat(context.Context, domain.WorkerHeartbeat) error
}

func EnvironmentWorkerID(variable, kind string) string {
	if configured := strings.TrimSpace(os.Getenv(variable)); configured != "" {
		return configured
	}
	hostname, _ := os.Hostname()
	if hostname = strings.TrimSpace(hostname); hostname != "" {
		return hostname + ":" + kind
	}
	return kind
}

func EnvironmentBuildVersion() string {
	return strings.TrimSpace(os.Getenv("OPEN_REVIEW_BUILD_VERSION"))
}

type Reporter struct {
	Store              HeartbeatStore
	WorkerID           string
	Kind               string
	Version            string
	AdapterConfigured  bool
	AdapterProbe       func(context.Context) error
	DecisionConfigured bool
	Capacity           int
	Interval           time.Duration
	TTL                time.Duration
	Logger             *slog.Logger

	startedAt time.Time
	busy      atomic.Int64
}

func (r *Reporter) Run(ctx context.Context) {
	if r.Store == nil || r.WorkerID == "" || r.Kind == "" {
		return
	}
	if r.Capacity < 1 {
		r.Capacity = 1
	}
	if r.Interval <= 0 {
		r.Interval = 15 * time.Second
	}
	if r.TTL <= r.Interval {
		r.TTL = 3 * r.Interval
	}
	if r.Logger == nil {
		r.Logger = slog.Default()
	}
	r.startedAt = time.Now().UTC()
	r.write(ctx)
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.write(ctx)
		}
	}
}

func (r *Reporter) BeginTask() func() {
	r.busy.Add(1)
	return func() { r.busy.Add(-1) }
}

func (r *Reporter) write(ctx context.Context) {
	var probeAt *time.Time
	var reachable *bool
	if r.AdapterConfigured && r.AdapterProbe != nil {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := r.AdapterProbe(probeCtx)
		cancel()
		observedAt := time.Now().UTC()
		result := err == nil
		probeAt, reachable = &observedAt, &result
	}
	now := time.Now().UTC()
	busy := int(r.busy.Load())
	if busy < 0 {
		busy = 0
	}
	if busy > r.Capacity {
		busy = r.Capacity
	}
	if err := r.Store.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: r.WorkerID, Kind: r.Kind, Version: r.Version,
		AdapterConfigured:  r.AdapterConfigured,
		AdapterProbeAt:     probeAt,
		AdapterReachable:   reachable,
		DecisionConfigured: r.DecisionConfigured,
		Capacity:           r.Capacity, Busy: busy, StartedAt: r.startedAt,
		HeartbeatAt: now, ExpiresAt: now.Add(r.TTL),
	}); err != nil && ctx.Err() == nil {
		r.Logger.Warn("worker heartbeat failed", "kind", r.Kind, "error", err)
	}
}
