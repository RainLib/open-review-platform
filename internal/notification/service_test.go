package notification

import (
	"context"
	"errors"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type fakeDeliveryStore struct {
	deliveries []domain.NotificationDelivery
	finished   []error
}

func (s *fakeDeliveryStore) PrepareNotificationDeliveries(context.Context, domain.OutboxMessage) ([]domain.NotificationDelivery, error) {
	return s.deliveries, nil
}

func (s *fakeDeliveryStore) FinishNotificationDelivery(_ context.Context, _ uuid.UUID, _ int, err error) error {
	s.finished = append(s.finished, err)
	return nil
}

type fakeDeliverySender struct{ calls int }

func (s *fakeDeliverySender) Send(context.Context, domain.NotificationDelivery) (int, error) {
	s.calls++
	if s.calls == 2 {
		return 503, errors.New("provider unavailable")
	}
	return 200, nil
}

func TestServiceRecordsAllDestinationsAndReturnsRetryableFailure(t *testing.T) {
	store := &fakeDeliveryStore{deliveries: []domain.NotificationDelivery{{ID: uuid.New()}, {ID: uuid.New()}, {ID: uuid.New()}}}
	sender := &fakeDeliverySender{}
	err := (Service{Store: store, Sender: sender}).Handle(context.Background(), domain.OutboxMessage{})
	if err == nil || err.Error() != "provider unavailable" {
		t.Fatalf("Handle() error = %v", err)
	}
	if sender.calls != 3 || len(store.finished) != 3 {
		t.Fatalf("calls = %d, finished = %d", sender.calls, len(store.finished))
	}
	if store.finished[0] != nil || store.finished[1] == nil || store.finished[2] != nil {
		t.Fatalf("finished errors = %#v", store.finished)
	}
}
