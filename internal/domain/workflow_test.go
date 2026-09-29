package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReviewModeRiskMode(t *testing.T) {
	tests := []struct {
		name       string
		mode       ReviewMode
		configured string
		want       string
	}{
		{name: "configured keeps deployment default", mode: ReviewModeConfigured, configured: "critical", want: "critical"},
		{name: "standard is balanced", mode: ReviewModeStandard, configured: "critical", want: "focused"},
		{name: "deep covers source delta", mode: ReviewModeDeep, configured: "focused", want: "standard"},
		{name: "security is high signal", mode: ReviewModeSecurity, configured: "standard", want: "critical"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.mode.RiskMode(test.configured); got != test.want {
				t.Fatalf("RiskMode(%q, %q) = %q, want %q", test.mode, test.configured, got, test.want)
			}
		})
	}
}

func TestRunStateTransitions(t *testing.T) {
	valid := [][2]RunState{
		{RunAcknowledged, RunAdmitted},
		{RunAdmitted, RunPreparing},
		{RunPreparing, RunAnalyzing},
		{RunAnalyzing, RunNormalizing},
		{RunNormalizing, RunPublishing},
		{RunPublishing, RunCompleted},
		{RunPublishing, RunNeedsAttention},
		{RunAnalyzing, RunSuperseded},
		{RunPreparing, RunCancelled},
	}
	for _, transition := range valid {
		if err := ValidateRunTransition(transition[0], transition[1]); err != nil {
			t.Fatalf("expected %s -> %s to be valid: %v", transition[0], transition[1], err)
		}
	}
}

func TestRunStateRejectsStaleAndTerminalTransitions(t *testing.T) {
	invalid := [][2]RunState{
		{RunAcknowledged, RunAnalyzing},
		{RunPublishing, RunAnalyzing},
		{RunCompleted, RunPublishing},
		{RunSuperseded, RunCompleted},
		{RunCancelled, RunFailed},
	}
	for _, transition := range invalid {
		if err := ValidateRunTransition(transition[0], transition[1]); err == nil {
			t.Fatalf("expected %s -> %s to be rejected", transition[0], transition[1])
		}
	}
}

func TestNormalizeRunRetryInput(t *testing.T) {
	valid, ok := NormalizeRunRetryInput(RunRetryInput{ExpectedRevision: 2, IdempotencyKey: " console-retry-0001 "})
	if !ok || valid.IdempotencyKey != "console-retry-0001" {
		t.Fatalf("valid retry input=%#v ok=%v", valid, ok)
	}
	for _, input := range []RunRetryInput{
		{ExpectedRevision: 0, IdempotencyKey: "console-retry-0001"},
		{ExpectedRevision: 1, IdempotencyKey: "short"},
		{ExpectedRevision: 1, IdempotencyKey: "retry\nunsafe-key"},
	} {
		if _, ok := NormalizeRunRetryInput(input); ok {
			t.Fatalf("invalid retry input accepted: %#v", input)
		}
	}
}

func TestNormalizeReviewExecutionPlanPreservesAnExactSafeScope(t *testing.T) {
	plan, ok := NormalizeReviewExecutionPlan(ReviewExecutionPlan{
		Mode:                " FOCUSED ",
		SelectedPaths:       []string{"internal/api/server.go", "internal/api/server.go", "apps/web/page.tsx"},
		DeferredFiles:       4,
		StaticImpactSignals: []string{" public boundary ", "public boundary", "persistent state"},
	})
	if !ok || plan.Mode != "focused" || len(plan.SelectedPaths) != 2 || plan.DeferredFiles != 4 || len(plan.StaticImpactSignals) != 2 || plan.StaticImpactSignals[0] != "public boundary" {
		t.Fatalf("normalized plan=%#v ok=%t", plan, ok)
	}
	for _, input := range []ReviewExecutionPlan{
		{Mode: "invalid", SelectedPaths: []string{"api.go"}},
		{Mode: "focused", SelectedPaths: []string{""}},
		{Mode: "critical", DeferredFiles: -1},
		{Mode: "critical", StaticImpactSignals: []string{""}},
	} {
		if _, ok := NormalizeReviewExecutionPlan(input); ok {
			t.Fatalf("invalid execution plan accepted: %#v", input)
		}
	}
}

func TestNormalizeReviewExecutionPlanBindsFileScopesToSelectedAndDeferredCounts(t *testing.T) {
	input := ReviewExecutionPlan{
		Mode: "focused", SelectedPaths: []string{"internal/api/server.go"}, DeferredFiles: 1,
		FileScopes: []ReviewFileScope{
			{Path: "internal/api/server.go", Selected: true, Score: 70, Reasons: []string{"public boundary or asynchronous workflow"}, ChangeType: "modified", Additions: 3, Deletions: 1, StatsKnown: true},
			{Path: "docs/guide.md", Score: 10, Reasons: []string{"documentation-only change"}, ChangeType: "renamed", PreviousPath: "docs/old-guide.md", StatsKnown: true},
		},
	}
	plan, ok := NormalizeReviewExecutionPlan(input)
	if !ok || len(plan.FileScopes) != 2 || plan.FileScopes[0].Path != input.SelectedPaths[0] || plan.FileScopes[1].Selected {
		t.Fatalf("valid file scopes=%#v ok=%t", plan.FileScopes, ok)
	}
	invalid := []ReviewExecutionPlan{
		{Mode: "focused", SelectedPaths: input.SelectedPaths, DeferredFiles: 1, FileScopes: input.FileScopes[:1]},
		{Mode: "focused", SelectedPaths: input.SelectedPaths, DeferredFiles: 0, FileScopes: input.FileScopes},
		{Mode: "focused", SelectedPaths: input.SelectedPaths, DeferredFiles: 1, FileScopes: []ReviewFileScope{{Path: input.SelectedPaths[0], Score: 70}, input.FileScopes[1]}},
		{Mode: "focused", SelectedPaths: input.SelectedPaths, DeferredFiles: 1, FileScopes: []ReviewFileScope{input.FileScopes[0], input.FileScopes[0]}},
		{Mode: "focused", SelectedPaths: input.SelectedPaths, DeferredFiles: 1, FileScopes: []ReviewFileScope{{Path: input.SelectedPaths[0], Selected: true, Score: 101}, input.FileScopes[1]}},
		{Mode: "focused", SelectedPaths: input.SelectedPaths, DeferredFiles: 1, FileScopes: []ReviewFileScope{{Path: input.SelectedPaths[0], Selected: true, Score: 70, ChangeType: "renamed"}, input.FileScopes[1]}},
		{Mode: "focused", SelectedPaths: input.SelectedPaths, DeferredFiles: 1, FileScopes: []ReviewFileScope{{Path: input.SelectedPaths[0], Selected: true, Score: 70, ChangeType: "modified", Additions: 3}, input.FileScopes[1]}},
		{Mode: "focused", SelectedPaths: input.SelectedPaths, DeferredFiles: 1, FileScopes: []ReviewFileScope{{Path: input.SelectedPaths[0], Selected: true, Score: 70, ChangeType: "unknown"}, input.FileScopes[1]}},
	}
	for _, candidate := range invalid {
		if got, valid := NormalizeReviewExecutionPlan(candidate); valid {
			t.Fatalf("inconsistent file scope accepted: %#v", got)
		}
	}
	legacy, ok := NormalizeReviewExecutionPlan(ReviewExecutionPlan{Mode: "focused", SelectedPaths: input.SelectedPaths, DeferredFiles: 1})
	if !ok || len(legacy.FileScopes) != 0 {
		t.Fatalf("legacy plan should remain readable: %#v ok=%t", legacy, ok)
	}
}

func TestWorkQueueCursorBindsItsViewAndFilters(t *testing.T) {
	createdAt := time.Date(2026, time.September, 19, 8, 0, 0, 0, time.UTC)
	filter := WorkQueueFilter{View: WorkQueueNeedsAttention, Repository: "RainLib/open-review-platform", Query: "timeout", Limit: 25}
	run := ReviewRunSummary{ReviewRun: ReviewRun{ID: uuid.New(), CreatedAt: createdAt}}
	cursor := EncodeWorkQueueCursor(filter, run)
	gotAt, gotID, ok, err := DecodeWorkQueueCursor(WorkQueueFilter{View: WorkQueueNeedsAttention, Repository: "RainLib/open-review-platform", Query: "timeout", Cursor: cursor, Limit: 25})
	if err != nil || !ok || gotID != run.ID || !gotAt.Equal(createdAt) {
		t.Fatalf("cursor decode at=%s id=%s ok=%t err=%v", gotAt, gotID, ok, err)
	}
	if _, _, _, err := DecodeWorkQueueCursor(WorkQueueFilter{View: WorkQueueRunning, Repository: filter.Repository, Query: filter.Query, Cursor: cursor, Limit: 25}); err == nil {
		t.Fatal("cursor scope should reject a changed queue view")
	}
	if (WorkQueueFilter{View: WorkQueueRunning, CursorDirection: WorkQueueCursorBefore, Limit: 25}).Valid() {
		t.Fatal("before cursor direction without a cursor should be invalid")
	}
}

func TestPullRequestCursorBindsCurrentReviewFilter(t *testing.T) {
	createdAt := time.Date(2026, time.September, 19, 10, 0, 0, 0, time.UTC)
	filter := PullRequestFilter{View: PullRequestCompleted, Repository: "RainLib/open-review-platform", Query: "42", Limit: 25}
	run := ReviewRunSummary{ReviewRun: ReviewRun{ID: uuid.New(), CreatedAt: createdAt}}
	cursor := EncodePullRequestCursor(filter, run)
	gotAt, gotID, ok, err := DecodePullRequestCursor(PullRequestFilter{View: PullRequestCompleted, Repository: filter.Repository, Query: filter.Query, Cursor: cursor, Limit: 25})
	if err != nil || !ok || gotID != run.ID || !gotAt.Equal(createdAt) {
		t.Fatalf("cursor decode at=%s id=%s ok=%t err=%v", gotAt, gotID, ok, err)
	}
	if _, _, _, err := DecodePullRequestCursor(PullRequestFilter{View: PullRequestAttention, Repository: filter.Repository, Query: filter.Query, Cursor: cursor, Limit: 25}); err == nil {
		t.Fatal("cursor scope should reject a changed pull request view")
	}
}
