package domain

import "testing"

func TestWorkspaceSetupRequiresOrderedTransitions(t *testing.T) {
	steps := []WorkspaceSetupStep{WorkspaceSetupConnect, WorkspaceSetupReviewScope, WorkspaceSetupLearning, WorkspaceSetupSeverity, WorkspaceSetupRules, WorkspaceSetupComplete}
	for index, current := range steps {
		for nextIndex, next := range steps {
			want := index < len(steps)-1 && nextIndex == index+1
			if got := CanAdvanceWorkspaceSetup(current, next); got != want {
				t.Fatalf("CanAdvanceWorkspaceSetup(%q, %q)=%v, want %v", current, next, got, want)
			}
		}
	}
}

func TestWorkspaceLearningBoundaryNormalizesOnlyExplicitModes(t *testing.T) {
	boundary, valid := (WorkspaceLearningBoundary{
		Mode:               WorkspaceLearningGovernedPolicy,
		ReviewerExclusions: []string{" reviewer-b ", "reviewer-a", "reviewer-a"},
	}).Normalize()
	if !valid || boundary.Mode != WorkspaceLearningGovernedPolicy || len(boundary.ReviewerExclusions) != 2 || boundary.ReviewerExclusions[0] != "reviewer-a" || boundary.ReviewerExclusions[1] != "reviewer-b" {
		t.Fatalf("normalized boundary=%#v valid=%t", boundary, valid)
	}
	if _, valid := (WorkspaceLearningBoundary{Mode: "implicit"}).Normalize(); valid {
		t.Fatal("implicit learning mode must be rejected")
	}
	if _, valid := (WorkspaceLearningBoundary{Mode: WorkspaceLearningSkipped, ReviewerExclusions: []string{"reviewer-a"}}).Normalize(); valid {
		t.Fatal("skipped learning mode must not retain reviewer exclusions")
	}
}
