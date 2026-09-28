package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

const maxTerminalPublicationAttempts = 5

// ScheduleTerminalPublicationRetry creates a new durable hand-off rather than
// asking RabbitMQ to immediately redeliver the same message. The original
// consumer can ACK only after this row commits. attempt includes the current
// provider call, so a new terminal event has attempt 1 and may schedule 2..5.
func (s *PostgresStore) ScheduleTerminalPublicationRetry(ctx context.Context, message domain.OutboxMessage, providerDelay time.Duration) (bool, error) {
	if message.ID == uuid.Nil || message.AggregateID == uuid.Nil || !terminalPublicationTopic(message.Topic) {
		return false, fmt.Errorf("terminal publication retry message is invalid")
	}
	currentAttempt, ok := terminalPublicationAttempt(message.Payload)
	if !ok {
		return false, fmt.Errorf("terminal publication retry attempt is invalid")
	}
	nextAttempt := currentAttempt + 1
	if nextAttempt > maxTerminalPublicationAttempts {
		return false, nil
	}
	delay := terminalPublicationDelay(nextAttempt, providerDelay)
	payload := make(map[string]any, len(message.Payload)+2)
	for key, value := range message.Payload {
		payload[key] = value
	}
	payload["terminal_publication_attempt"] = nextAttempt
	payload["terminal_publication_retry_of"] = message.ID.String()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode terminal publication retry: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO outbox_messages (
			aggregate_type,aggregate_id,topic,dedupe_key,payload,available_at
		) VALUES ('review_run',$1,$2,$3,$4::jsonb,now()+$5::interval)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		message.AggregateID, message.Topic, "terminal-publication-retry:"+message.ID.String(), encoded, delay.String())
	if err != nil {
		return false, fmt.Errorf("schedule terminal publication retry: %w", err)
	}
	// A duplicate call means the prior transaction already committed the exact
	// successor. It is still safe for the consumer to complete this message.
	return true, nil
}

func terminalPublicationAttempt(payload map[string]any) (int, bool) {
	if payload == nil {
		return 0, false
	}
	value, exists := payload["terminal_publication_attempt"]
	if !exists {
		return 1, true
	}
	var attempt int
	switch typed := value.(type) {
	case int:
		attempt = typed
	case int32:
		attempt = int(typed)
	case int64:
		attempt = int(typed)
	case float64:
		if typed != math.Trunc(typed) {
			return 0, false
		}
		attempt = int(typed)
	default:
		return 0, false
	}
	return attempt, attempt >= 1 && attempt <= maxTerminalPublicationAttempts
}

func terminalPublicationDelay(nextAttempt int, providerDelay time.Duration) time.Duration {
	// attempt 2 starts at 5s, then 10s, 20s and 40s. A provider-directed
	// Retry-After is a minimum, never a reason to shorten local backoff.
	delay := 5 * time.Second * time.Duration(1<<max(nextAttempt-2, 0))
	if providerDelay > delay {
		delay = providerDelay
	}
	if delay > 15*time.Minute {
		return 15 * time.Minute
	}
	return delay
}

func terminalPublicationTopic(topic string) bool {
	switch topic {
	case "review.run.cancelled", "review.run.superseded", "review.run.failed", "review.run.needs_attention":
		return true
	default:
		return false
	}
}
