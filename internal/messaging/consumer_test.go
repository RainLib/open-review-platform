package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type inboxStore struct {
	claimed    bool
	completed  bool
	released   bool
	releaseErr error
}

func (s *inboxStore) ClaimOutbox(context.Context, string, int) ([]domain.OutboxMessage, error) {
	return nil, nil
}
func (s *inboxStore) MarkOutboxPublished(context.Context, uuid.UUID, string) error { return nil }
func (s *inboxStore) ReleaseOutbox(context.Context, uuid.UUID, string, string) error {
	return nil
}
func (s *inboxStore) ClaimInbox(context.Context, string, uuid.UUID) (uuid.UUID, bool, error) {
	return uuid.New(), s.claimed, nil
}
func (s *inboxStore) CompleteInbox(context.Context, string, uuid.UUID, uuid.UUID) error {
	s.completed = true
	return nil
}
func (s *inboxStore) ReleaseInbox(context.Context, string, uuid.UUID, uuid.UUID, string) error {
	s.released = true
	return s.releaseErr
}

func TestDecodeOutboxMessage(t *testing.T) {
	messageID, aggregateID := uuid.New(), uuid.New()
	body, err := json.Marshal(map[string]any{
		"message_id": messageID.String(), "aggregate_id": aggregateID.String(),
		"topic": "review.interaction.response", "payload": map[string]any{"body": "accepted"},
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := DecodeOutboxMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != messageID || message.AggregateID != aggregateID || message.Topic != "review.interaction.response" {
		t.Fatalf("unexpected decoded message: %#v", message)
	}
}

func TestHandleExactlyOnceCompletesOrReleases(t *testing.T) {
	message := domain.OutboxMessage{ID: uuid.New()}
	success := &inboxStore{claimed: true}
	if err := HandleExactlyOnce(context.Background(), success, "consumer", message, func(context.Context, domain.OutboxMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !success.completed || success.released {
		t.Fatalf("unexpected inbox completion state: %#v", success)
	}

	failure := &inboxStore{claimed: true}
	err := HandleExactlyOnce(context.Background(), failure, "consumer", message, func(context.Context, domain.OutboxMessage) error { return errors.New("provider unavailable") })
	if err == nil || !failure.released || failure.completed {
		t.Fatalf("expected released failed message, got err=%v state=%#v", err, failure)
	}
}
