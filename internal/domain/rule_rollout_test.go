package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestRuleRolloutCohortIsStableAndBounded(t *testing.T) {
	tenant, salt := uuid.New(), uuid.New()
	first, valid := CohortBucket(tenant, ProviderGitLab, "https://git.example/api/v4/", "team/repository/", 42, salt)
	if !valid || first < 0 || first >= 10_000 {
		t.Fatalf("unexpected bucket %d valid=%t", first, valid)
	}
	second, valid := CohortBucket(tenant, ProviderGitLab, "https://git.example/api/v4", "team/repository", 42, salt)
	if !valid || first != second {
		t.Fatalf("retry changed cohort: first=%d second=%d valid=%t", first, second, valid)
	}
	rollout := RuleRollout{Mode: "canary", State: "active", CanaryBasisPoints: 10_000, CohortSalt: salt}
	if !rollout.IncludesCohort(tenant, ProviderGitLab, "https://git.example/api/v4", "team/repository", 42) {
		t.Fatal("100% canary must include a valid stable cohort")
	}
	rollout.CanaryBasisPoints = 0
	if rollout.IncludesCohort(tenant, ProviderGitLab, "https://git.example/api/v4", "team/repository", 42) {
		t.Fatal("zero-basis canary must not include a cohort")
	}
}

func TestRuleRolloutInputsRejectAmbiguousLifecycle(t *testing.T) {
	baseline, candidate := uuid.New(), uuid.New()
	if !(RuleRolloutInput{BaselineBindingID: baseline, CandidateBindingID: candidate, Mode: "shadow"}).Valid() {
		t.Fatal("shadow input should be valid without a percentage")
	}
	for _, input := range []RuleRolloutInput{
		{BaselineBindingID: baseline, CandidateBindingID: baseline, Mode: "shadow"},
		{BaselineBindingID: baseline, CandidateBindingID: candidate, Mode: "shadow", CanaryBasisPoints: 1},
		{BaselineBindingID: baseline, CandidateBindingID: candidate, Mode: "canary", CanaryBasisPoints: 0},
		{BaselineBindingID: baseline, CandidateBindingID: candidate, Mode: "canary", CanaryBasisPoints: 10_001},
		{BaselineBindingID: baseline, CandidateBindingID: candidate, Mode: "canary", CanaryBasisPoints: 100, AutoRollbackFailedRuns: 11},
		{BaselineBindingID: baseline, CandidateBindingID: candidate, Mode: "canary", CanaryBasisPoints: 100, AutoRollbackWindowMinutes: 4},
	} {
		if input.Valid() {
			t.Fatalf("invalid rollout input accepted: %#v", input)
		}
	}
	if !(RuleRolloutUpdateInput{State: "rolled_back", Revision: 2}).Valid() {
		t.Fatal("revisioned rollback should be valid")
	}
	if !(RuleRolloutUpdateInput{State: "active", CanaryBasisPoints: 2500, Revision: 2}).Valid() {
		t.Fatal("revisioned Canary stage advance should be valid")
	}
	for _, input := range []RuleRolloutUpdateInput{
		{State: "paused", CanaryBasisPoints: 2500, Revision: 2},
		{State: "promoted", CanaryBasisPoints: 10000, Revision: 2},
		{State: "active", CanaryBasisPoints: 7500, Revision: 2},
	} {
		if input.Valid() {
			t.Fatalf("ambiguous rollout update accepted: %#v", input)
		}
	}
}
