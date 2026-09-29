package store

import (
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
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

func TestInteractionEventMatchesInstallation(t *testing.T) {
	tests := []struct {
		name         string
		installation domain.Installation
		event        domain.CommentEvent
		want         bool
	}{
		{
			name:         "GitHub requires the exact app installation identity",
			installation: domain.Installation{Provider: domain.ProviderGitHub, ExternalID: "github-installation-7"},
			event:        domain.CommentEvent{Provider: domain.ProviderGitHub, InstallationExternalID: "github-installation-7"},
			want:         true,
		},
		{
			name:         "GitHub rejects a different app installation identity",
			installation: domain.Installation{Provider: domain.ProviderGitHub, ExternalID: "github-installation-7"},
			event:        domain.CommentEvent{Provider: domain.ProviderGitHub, InstallationExternalID: "github-installation-8"},
			want:         false,
		},
		{
			name:         "GitLab accepts the project hook identity after scope routing",
			installation: domain.Installation{Provider: domain.ProviderGitLab, ExternalID: "gitlab-scope-opaque"},
			event:        domain.CommentEvent{Provider: domain.ProviderGitLab, InstallationExternalID: "42"},
			want:         true,
		},
		{
			name:         "empty provider identity remains rejected",
			installation: domain.Installation{Provider: domain.ProviderGitLab, ExternalID: "gitlab-scope-opaque"},
			event:        domain.CommentEvent{Provider: domain.ProviderGitLab},
			want:         false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := interactionEventMatchesInstallation(test.installation, test.event); got != test.want {
				t.Fatalf("interactionEventMatchesInstallation() = %t, want %t", got, test.want)
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

func TestOnlyRoutableRunStatesEnterTheOutbox(t *testing.T) {
	for _, state := range []domain.RunState{
		domain.RunAcknowledged,
		domain.RunAdmitted,
		domain.RunCompleted,
		domain.RunFailed,
		domain.RunCancelled,
		domain.RunSuperseded,
		domain.RunNeedsAttention,
	} {
		if !runStateHasBrokerConsumer(state) {
			t.Fatalf("state %s has a consumer but would be omitted from the outbox", state)
		}
	}
	for _, state := range []domain.RunState{
		domain.RunPreparing,
		domain.RunAnalyzing,
		domain.RunNormalizing,
		domain.RunPublishing,
	} {
		if runStateHasBrokerConsumer(state) {
			t.Fatalf("intermediate state %s has no consumer and must stay in the durable event stream", state)
		}
	}
}
