package messaging

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type relayStore struct {
	messages   []domain.OutboxMessage
	published  []uuid.UUID
	released   []uuid.UUID
	claimLimit int
}

func (s *relayStore) ClaimOutbox(_ context.Context, _ string, limit int) ([]domain.OutboxMessage, error) {
	s.claimLimit = limit
	return s.messages[:min(len(s.messages), limit)], nil
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
	published, err := (Relay{Store: store, Publisher: relayPublisher{failID: second}, RelayID: "relay-a", BatchSize: 2}).Flush(context.Background())
	if err == nil {
		t.Fatal("failed broker publish must be observable after its outbox row is released")
	}
	if published != 1 || len(store.published) != 1 || store.published[0] != first {
		t.Fatalf("unexpected published messages: %#v", store.published)
	}
	if len(store.released) != 1 || store.released[0] != second {
		t.Fatalf("unexpected released messages: %#v", store.released)
	}
}

func TestRelayPublishesUrgentMessagesBeforeNormalWork(t *testing.T) {
	standard, security, acknowledgement := uuid.New(), uuid.New(), uuid.New()
	store := &relayStore{messages: []domain.OutboxMessage{
		{ID: standard, Topic: "review.run.admitted", Payload: map[string]any{"review_mode": "standard"}},
		{ID: security, Topic: "review.run.admitted", Payload: map[string]any{"review_mode": "security"}},
		{ID: acknowledgement, Topic: "review.run.acknowledged"},
	}}
	published, err := (Relay{Store: store, Publisher: relayPublisher{}, RelayID: "relay-priority", BatchSize: 3}).Flush(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if published != 3 {
		t.Fatalf("published=%d, want 3", published)
	}
	want := []uuid.UUID{acknowledgement, security, standard}
	if len(store.published) != len(want) {
		t.Fatalf("published=%#v, want %#v", store.published, want)
	}
	for index, id := range want {
		if store.published[index] != id {
			t.Fatalf("published[%d]=%s, want %s; priority ordering was lost", index, store.published[index], id)
		}
	}
}

type blockedRelayPublisher struct{}

func (blockedRelayPublisher) Publish(ctx context.Context, _ domain.OutboxMessage) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestRelayBoundsBlockedBrokerConfirmationWithinClaimLease(t *testing.T) {
	messageID := uuid.New()
	store := &relayStore{messages: []domain.OutboxMessage{{ID: messageID, Topic: "review.interaction.response"}}}
	started := time.Now()
	published, err := (Relay{Store: store, Publisher: blockedRelayPublisher{}, RelayID: "relay-bounded", PublishTimeout: 20 * time.Millisecond}).Flush(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || published != 0 || len(store.released) != 1 || store.released[0] != messageID {
		t.Fatalf("blocked broker publish was not durably released: published=%d error=%v released=%v", published, err, store.released)
	}
	if elapsed := time.Since(started); elapsed > time.Second || store.claimLimit != 1 {
		t.Fatalf("default claim was not bounded: duration=%s limit=%d", elapsed, store.claimLimit)
	}
}
