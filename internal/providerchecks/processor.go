package providerchecks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
)

type ProbeStore interface {
	ClaimProviderCheckProbe(context.Context, string, time.Duration) (*domain.ProviderCheckTarget, error)
	CompleteProviderCheckProbe(context.Context, domain.ProviderCheckTarget, domain.ProviderCheckObservation, time.Time) error
	FailProviderCheckProbe(context.Context, domain.ProviderCheckTarget, string, time.Time) error
}

type Processor struct {
	Store       ProbeStore
	Client      Client
	WorkerID    string
	Lease       time.Duration
	PollEvery   time.Duration
	TaskStarted func() func()
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	if p.Store == nil || p.Client.Resolver == nil || strings.TrimSpace(p.WorkerID) == "" {
		return false, fmt.Errorf("provider checks processor is not configured")
	}
	lease := p.Lease
	if lease <= 0 {
		lease = time.Minute
	}
	target, err := p.Store.ClaimProviderCheckProbe(ctx, p.WorkerID, lease)
	if errors.Is(err, store.ErrNoProviderCheckProbe) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	done := func() {}
	if p.TaskStarted != nil {
		if release := p.TaskStarted(); release != nil {
			done = release
		}
	}
	defer done()
	observation, err := p.Client.Fetch(ctx, target.Job)
	if err != nil {
		// The read model records only a bounded code. Raw provider errors and
		// credential resolver details never become user-visible evidence.
		retryAt := time.Now().UTC().Add(checkRetryDelay(target.Attempt))
		if persistErr := p.Store.FailProviderCheckProbe(ctx, *target, providerReadFailureCode(err), retryAt); persistErr != nil {
			return true, fmt.Errorf("provider checks read failed; persist failure: %w", persistErr)
		}
		return true, fmt.Errorf("provider checks read failed")
	}
	interval := p.PollEvery
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if err := p.Store.CompleteProviderCheckProbe(ctx, *target, observation, observation.ObservedAt.Add(interval)); err != nil {
		return true, err
	}
	return true, nil
}

func checkRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Minute << min(attempt-1, 4)
}
