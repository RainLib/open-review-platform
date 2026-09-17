package store

import (
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

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
