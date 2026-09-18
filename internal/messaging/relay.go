package messaging

import (
	"context"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
)

// Publisher is deliberately small so the database hand-off can be verified
// without a live broker. AMQPPublisher is the production implementation.
type Publisher interface {
	Publish(context.Context, domain.OutboxMessage) error
}

type Relay struct {
	Store     store.WorkflowStore
	Publisher Publisher
	RelayID   string
	BatchSize int
}

func (r Relay) Flush(ctx context.Context) (int, error) {
	if r.Store == nil || r.Publisher == nil || r.RelayID == "" {
		return 0, fmt.Errorf("relay store, publisher, and relay id are required")
	}
	batchSize := r.BatchSize
	if batchSize == 0 {
		batchSize = 25
	}
	messages, err := r.Store.ClaimOutbox(ctx, r.RelayID, batchSize)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, message := range messages {
		if err := r.Publisher.Publish(ctx, message); err != nil {
			if releaseErr := r.Store.ReleaseOutbox(ctx, message.ID, r.RelayID, err.Error()); releaseErr != nil {
				return published, fmt.Errorf("publish %s: %w (release: %v)", message.ID, err, releaseErr)
			}
			continue
		}
		if err := r.Store.MarkOutboxPublished(ctx, message.ID, r.RelayID); err != nil {
			return published, fmt.Errorf("mark %s published: %w", message.ID, err)
		}
		published++
	}
	return published, nil
}
