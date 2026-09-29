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

const maxAgentSourceDecisionAttempts = 3

// ScheduleAgentTaskSourceRetry creates one delayed, durable successor for a
// transient provider read or model failure. The source worker ACKs the original message
// only after this outbox insert commits. A retry always re-reads the current
// provider Issue and base commit before source admission can continue.
func (s *PostgresStore) ScheduleAgentTaskSourceRetry(ctx context.Context, message domain.OutboxMessage, providerDelay time.Duration) (bool, error) {
	taskID, ok := message.Payload["task_id"].(string)
	if message.ID == uuid.Nil || message.AggregateID == uuid.Nil || message.Topic != "agent.task.source.resolve.requested" || !ok || taskID != message.AggregateID.String() {
		return false, fmt.Errorf("agent source decision retry message is invalid")
	}
	currentAttempt, ok := agentSourceDecisionAttempt(message.Payload)
	if !ok {
		return false, fmt.Errorf("agent source decision attempt is invalid")
	}
	taskRevision, ok := AgentSourceTaskRevision(message.Payload)
	if !ok {
		return false, fmt.Errorf("agent source task revision is invalid")
	}
	nextAttempt := currentAttempt + 1
	if nextAttempt > maxAgentSourceDecisionAttempts {
		return false, nil
	}
	delay := time.Duration(5*(1<<(nextAttempt-2))) * time.Second
	if providerDelay > delay {
		delay = providerDelay
	}
	if delay > 15*time.Minute {
		delay = 15 * time.Minute
	}
	payload := make(map[string]any, len(message.Payload)+2)
	for key, value := range message.Payload {
		payload[key] = value
	}
	payload["source_decision_attempt"] = nextAttempt
	payload["source_decision_retry_of"] = message.ID.String()
	payload["task_revision"] = taskRevision
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode agent source decision retry: %w", err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload,available_at)
		SELECT 'agent_task',t.id,'agent.task.source.resolve.requested',$2,$3::jsonb,now()+$4::interval
		FROM agent_tasks t WHERE t.id=$1 AND t.revision=$5 AND t.state='received' AND t.source_state='pending'
		ON CONFLICT (dedupe_key) DO NOTHING`, message.AggregateID, "agent-task-source-decision-retry:"+message.ID.String(), encoded, delay.String(), taskRevision)
	if err != nil {
		return false, fmt.Errorf("schedule agent source decision retry: %w", err)
	}
	// An existing dedupe row is a successful handoff, and a task that became
	// terminal before this insert no longer needs another evaluation.
	return true, nil
}

// ScheduleAgentTaskSourceDecisionRetry retains the existing call contract for
// delayed Jev messages that were already admitted before provider reads shared
// the same bounded retry sequence.
func (s *PostgresStore) ScheduleAgentTaskSourceDecisionRetry(ctx context.Context, message domain.OutboxMessage, providerDelay time.Duration) (bool, error) {
	return s.ScheduleAgentTaskSourceRetry(ctx, message, providerDelay)
}

func agentSourceDecisionAttempt(payload map[string]any) (int, bool) {
	if payload == nil {
		return 0, false
	}
	value, exists := payload["source_decision_attempt"]
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
		if typed != math.Trunc(typed) || typed > maxAgentSourceDecisionAttempts {
			return 0, false
		}
		attempt = int(typed)
	default:
		return 0, false
	}
	return attempt, attempt >= 1 && attempt <= maxAgentSourceDecisionAttempts
}

// AgentSourceTaskRevision also accepts legacy revision-1 outbox payloads, but
// never lets one of them continue after an explicit source retry increments
// the task revision.
func AgentSourceTaskRevision(payload map[string]any) (int, bool) {
	value, exists := payload["task_revision"]
	if !exists {
		// Messages queued before the revision field was introduced were only
		// emitted for the initial, revision-1 source capture.
		return 1, true
	}
	var revision int
	switch typed := value.(type) {
	case int:
		revision = typed
	case int32:
		revision = int(typed)
	case int64:
		if typed > 2147483647 {
			return 0, false
		}
		revision = int(typed)
	case float64:
		if typed != math.Trunc(typed) || typed > 2147483647 {
			return 0, false
		}
		revision = int(typed)
	default:
		return 0, false
	}
	return revision, revision >= 1
}
