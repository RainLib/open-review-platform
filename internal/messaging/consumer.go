package messaging

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

// DecodeOutboxMessage validates the provider-neutral envelope emitted by the
// relay before any consumer is allowed to touch the payload.
func DecodeOutboxMessage(body []byte) (domain.OutboxMessage, error) {
	var envelope struct {
		MessageID   string         `json:"message_id"`
		AggregateID string         `json:"aggregate_id"`
		Topic       string         `json:"topic"`
		Payload     map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("decode broker envelope: %w", err)
	}
	messageID, err := uuid.Parse(envelope.MessageID)
	if err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("broker message id: %w", err)
	}
	aggregateID, err := uuid.Parse(envelope.AggregateID)
	if err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("broker aggregate id: %w", err)
	}
	if envelope.Topic == "" || envelope.Payload == nil {
		return domain.OutboxMessage{}, fmt.Errorf("broker topic and payload are required")
	}
	return domain.OutboxMessage{ID: messageID, AggregateID: aggregateID, Topic: envelope.Topic, Payload: envelope.Payload}, nil
}

// HandleExactlyOnce uses the database inbox as the durable idempotency fence.
// A broker redelivery after a publish succeeds will observe the completed
// inbox record; a transient provider failure releases the claim for retry.
func HandleExactlyOnce(ctx context.Context, workflow store.WorkflowStore, consumer string, message domain.OutboxMessage, handler func(context.Context, domain.OutboxMessage) error) error {
	if workflow == nil || consumer == "" || handler == nil {
		return fmt.Errorf("workflow store, consumer, and handler are required")
	}
	claimToken, claimed, err := workflow.ClaimInbox(ctx, consumer, message.ID)
	if err != nil || !claimed {
		return err
	}
	if err := handler(ctx, message); err != nil {
		if releaseErr := workflow.ReleaseInbox(ctx, consumer, message.ID, claimToken, err.Error()); releaseErr != nil {
			return fmt.Errorf("handle message: %w (release: %v)", err, releaseErr)
		}
		return err
	}
	if err := workflow.CompleteInbox(ctx, consumer, message.ID, claimToken); err != nil {
		return fmt.Errorf("complete inbox message: %w", err)
	}
	return nil
}
