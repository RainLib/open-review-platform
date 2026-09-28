package scheduler

import (
	"context"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// Store is deliberately narrow: this worker only admits a due schedule. The
// PostgreSQL transaction owns all creation of review jobs/runs and outbox
// messages, so the polling loop can be restarted without duplicate reviews.
type Store interface {
	AdmitDueReviewSchedule(context.Context, string) (domain.ReviewSchedule, bool, error)
}

type Processor struct {
	Store       Store
	WorkerID    string
	TaskStarted func() func()
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	if p.Store == nil || p.WorkerID == "" {
		return false, fmt.Errorf("scheduled review store and worker id are required")
	}
	done := func() {}
	if p.TaskStarted != nil {
		if release := p.TaskStarted(); release != nil {
			done = release
		}
	}
	defer done()
	_, worked, err := p.Store.AdmitDueReviewSchedule(ctx, p.WorkerID)
	return worked, err
}
