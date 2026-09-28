package notification

import (
	"context"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type DeliveryStore interface {
	PrepareNotificationDeliveries(context.Context, domain.OutboxMessage) ([]domain.NotificationDelivery, error)
	FinishNotificationDelivery(context.Context, uuid.UUID, int, error) error
}

type DeliverySender interface {
	Send(context.Context, domain.NotificationDelivery) (int, error)
}

type Service struct {
	Store  DeliveryStore
	Sender DeliverySender
}

// Handle delivers every matching route. Successful destinations are durable,
// so a retry only re-attempts destinations that have not yet been delivered.
func (s Service) Handle(ctx context.Context, message domain.OutboxMessage) error {
	if s.Store == nil || s.Sender == nil {
		return fmt.Errorf("notification store and sender are required")
	}
	deliveries, err := s.Store.PrepareNotificationDeliveries(ctx, message)
	if err != nil {
		return err
	}
	var firstSendError error
	for _, delivery := range deliveries {
		status, sendErr := s.Sender.Send(ctx, delivery)
		if err := s.Store.FinishNotificationDelivery(ctx, delivery.ID, status, sendErr); err != nil {
			return fmt.Errorf("record notification delivery: %w", err)
		}
		if sendErr != nil && firstSendError == nil {
			firstSendError = sendErr
		}
	}
	return firstSendError
}
