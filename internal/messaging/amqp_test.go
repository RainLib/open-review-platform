package messaging

import (
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestOutboxMessagePriorityProtectsInteractiveAndSecurityWork(t *testing.T) {
	tests := []struct {
		name    string
		message domain.OutboxMessage
		want    uint8
	}{
		{name: "interaction response", message: domain.OutboxMessage{Topic: "review.interaction.response"}, want: 31},
		{name: "acknowledgement", message: domain.OutboxMessage{Topic: "review.run.acknowledged"}, want: 28},
		{name: "security execution", message: domain.OutboxMessage{Topic: "review.run.admitted", Payload: map[string]any{"review_mode": "security"}}, want: 8},
		{name: "standard execution", message: domain.OutboxMessage{Topic: "review.run.admitted", Payload: map[string]any{"review_mode": "standard"}}, want: 4},
		{name: "approved agent execution", message: domain.OutboxMessage{Topic: "agent.task.execute.requested"}, want: 4},
		{name: "agent cancellation", message: domain.OutboxMessage{Topic: "agent.task.cancel.requested"}, want: 16},
		{name: "ordinary notification", message: domain.OutboxMessage{Topic: "review.run.completed"}, want: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := outboxMessagePriority(test.message); got != test.want {
				t.Fatalf("priority=%d, want %d", got, test.want)
			}
		})
	}
}

func TestTopologyRoutesApprovedAgentTasksToTheirOwnQueue(t *testing.T) {
	for _, binding := range topologyBindings() {
		if binding.name == "openreview.agent-task.execute.v1" && binding.key == "agent.task.execute.requested" {
			return
		}
	}
	t.Fatal("approved Agent tasks have no durable execution queue")
}

func TestTopologyRoutesAgentCancellationToItsOwnQueue(t *testing.T) {
	for _, binding := range topologyBindings() {
		if binding.name == "openreview.agent-task.cancel.v1" && binding.key == "agent.task.cancel.requested" {
			return
		}
	}
	t.Fatal("Agent cancellation has no durable priority queue")
}

func TestTopologyRoutesEveryReviewInterventionTerminalState(t *testing.T) {
	bindings := topologyBindings()
	hasBinding := func(queue, topic string) bool {
		for _, binding := range bindings {
			if binding.name == queue && binding.key == topic {
				return true
			}
		}
		return false
	}
	for _, topic := range []string{
		"review.run.failed",
		"review.run.needs_attention",
	} {
		if !hasBinding("openreview.review.terminal.v1", topic) {
			t.Fatalf("terminal reporter is not bound to %s", topic)
		}
		if !hasBinding("openreview.notification.v1", topic) {
			t.Fatalf("notification worker is not bound to %s", topic)
		}
	}
}
