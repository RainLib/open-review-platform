package messaging

import (
	"context"
	"errors"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type relayStore struct {
	messages  []domain.OutboxMessage
	published []uuid.UUID
	released  []uuid.UUID
}

func (s *relayStore) ClaimOutbox(context.Context, string, int) ([]domain.OutboxMessage, error) {
	return s.messages, nil
}
func (s *relayStore) MarkOutboxPublished(_ context.Context, id uuid.UUID, _ string) error {
	s.published = append(s.published, id)
	return nil
}
func (s *relayStore) ReleaseOutbox(_ context.Context, id uuid.UUID, _ string, _ string) error {
	s.released = append(s.released, id)
	return nil
}
func (*relayStore) ClaimInbox(context.Context, string, uuid.UUID) (uuid.UUID, bool, error) {
	return uuid.Nil, false, nil
}
func (*relayStore) CompleteInbox(context.Context, string, uuid.UUID, uuid.UUID) error { return nil }
func (*relayStore) ReleaseInbox(context.Context, string, uuid.UUID, uuid.UUID, string) error {
	return nil
}

type relayPublisher struct{ failID uuid.UUID }

func (p relayPublisher) Publish(_ context.Context, message domain.OutboxMessage) error {
	if message.ID == p.failID {
		return errors.New("broker unavailable")
	}
	return nil
}

func TestRelayPublishesAndReleasesIndependently(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	store := &relayStore{messages: []domain.OutboxMessage{{ID: first}, {ID: second}}}
	published, err := (Relay{Store: store, Publisher: relayPublisher{failID: second}, RelayID: "relay-a"}).Flush(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if published != 1 || len(store.published) != 1 || store.published[0] != first {
		t.Fatalf("unexpected published messages: %#v", store.published)
	}
	if len(store.released) != 1 || store.released[0] != second {
		t.Fatalf("unexpected released messages: %#v", store.released)
	}
}
