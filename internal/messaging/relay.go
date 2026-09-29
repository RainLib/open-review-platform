package messaging

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
)

// Publisher is deliberately small so the database hand-off can be verified
// without a live broker. AMQPPublisher is the production implementation.
type Publisher interface {
	Publish(context.Context, domain.OutboxMessage) error
}

type Relay struct {
	Store          store.WorkflowStore
	Publisher      Publisher
	RelayID        string
	BatchSize      int
	PublishTimeout time.Duration
}

func (r Relay) Flush(ctx context.Context) (int, error) {
	if r.Store == nil || r.Publisher == nil || r.RelayID == "" {
		return 0, fmt.Errorf("relay store, publisher, and relay id are required")
	}
	batchSize := r.BatchSize
	if batchSize == 0 {
		// A claim has a 30-second lease. One broker confirmation at a time
		// leaves enough room to release a blocked publish before that lease
		// expires, even when RabbitMQ is under disk backpressure.
		batchSize = 1
	}
	publishTimeout := r.PublishTimeout
	if publishTimeout == 0 {
		publishTimeout = 15 * time.Second
	}
	if publishTimeout < 0 || publishTimeout >= 25*time.Second {
		return 0, fmt.Errorf("outbox publish timeout must be positive and shorter than the claim lease")
	}
	messages, err := r.Store.ClaimOutbox(ctx, r.RelayID, batchSize)
	if err != nil {
		return 0, err
	}
	// SQL claims the same priority classes, but UPDATE ... RETURNING has no
	// ordering guarantee. Sort the leased batch before publishing so recovery
	// cannot accidentally reverse the ordering selected by the store.
	sort.SliceStable(messages, func(left, right int) bool {
		return outboxMessagePriority(messages[left]) > outboxMessagePriority(messages[right])
	})
	published := 0
	var publishErrors []error
	for _, message := range messages {
		publishCtx, cancel := context.WithTimeout(ctx, publishTimeout)
		publishErr := r.Publisher.Publish(publishCtx, message)
		cancel()
		if publishErr != nil {
			if releaseErr := r.Store.ReleaseOutbox(ctx, message.ID, r.RelayID, publishErr.Error()); releaseErr != nil {
				return published, fmt.Errorf("publish %s: %w (release: %v)", message.ID, publishErr, releaseErr)
			}
			publishErrors = append(publishErrors, fmt.Errorf("publish %s: %w", message.ID, publishErr))
			continue
		}
		if err := r.Store.MarkOutboxPublished(ctx, message.ID, r.RelayID); err != nil {
			return published, fmt.Errorf("mark %s published: %w", message.ID, err)
		}
		published++
	}
	return published, errors.Join(publishErrors...)
}
