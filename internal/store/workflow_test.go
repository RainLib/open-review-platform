package store

import (
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestInteractionTriggerKind(t *testing.T) {
	tests := []struct {
		command string
		want    string
		wantErr bool
	}{
		{command: "review", want: "comment"},
		{command: "retry", want: "retry"},
		{command: "cancel", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			got, err := interactionTriggerKind(test.command)
			if (err != nil) != test.wantErr {
				t.Fatalf("interactionTriggerKind(%q) error = %v, want error %t", test.command, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("interactionTriggerKind(%q) = %q, want %q", test.command, got, test.want)
			}
		})
	}
}

func TestRunStateRankIsMonotonicForRecoverableStages(t *testing.T) {
	stages := []domain.RunState{
		domain.RunAcknowledged,
		domain.RunAdmitted,
		domain.RunPreparing,
		domain.RunAnalyzing,
		domain.RunNormalizing,
		domain.RunPublishing,
	}
	for index := 1; index < len(stages); index++ {
		if runStateRank(stages[index]) <= runStateRank(stages[index-1]) {
			t.Fatalf("expected %s to rank after %s", stages[index], stages[index-1])
		}
	}
	if runStateRank(domain.RunFailed) != 0 {
		t.Fatal("terminal state must not be treated as a recoverable stage")
	}
}

func TestTerminalInteractionBodyUsesSafeTerminalStatus(t *testing.T) {
	runID := uuid.MustParse("109870c4-f3e3-4a38-9dcd-0a30c4e72cd9")
	for _, test := range []struct {
		state domain.RunState
		want  string
	}{
		{domain.RunCompleted, "has completed"},
		{domain.RunCancelled, "was cancelled"},
		{domain.RunSuperseded, "was superseded"},
		{domain.RunNeedsAttention, "needs attention"},
		{domain.RunFailed, "could not be completed"},
	} {
		body := terminalInteractionBody(runID, test.state)
		if !strings.Contains(body, runID.String()) || !strings.Contains(body, test.want) {
			t.Fatalf("state %s body %q does not contain %q", test.state, body, test.want)
		}
		if strings.Contains(strings.ToLower(body), "token") || strings.Contains(strings.ToLower(body), "endpoint") {
			t.Fatalf("state %s leaked runtime detail: %q", test.state, body)
		}
	}
}
