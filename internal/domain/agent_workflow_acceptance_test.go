package domain

import (
	"strings"
	"testing"
)

// passed builds a single passing criterion result for concise table cases.
func passed(criterion string) AgentCriterionResult {
	return AgentCriterionResult{Criterion: criterion, Status: "passed", Evidence: "Signed exact-head verifier evidence for " + criterion}
}

func failed(criterion string) AgentCriterionResult {
	return AgentCriterionResult{Criterion: criterion, Status: "failed", Evidence: "Signed exact-head verifier evidence for " + criterion}
}

// Issue #20: duplicate approved criteria must not permit unrelated evidence
// to count as complete delivery. Verification is a one-to-one mapping.
func TestCriteriaVerifiedRejectsDuplicateApprovedCriteriaWithUnapprovedEvidence(t *testing.T) {
	criteria := []string{"Retry twice", "Retry twice"}
	results := []AgentCriterionResult{passed("Retry twice"), passed("Unapproved replacement")}
	if CriteriaVerified(criteria, results) {
		t.Fatal("duplicate approved criteria allowed unapproved evidence to count as complete delivery")
	}
}

func TestCriteriaVerifiedRejectsDuplicateApprovedCriteriaEntirely(t *testing.T) {
	cases := []struct {
		name     string
		criteria []string
		results  []AgentCriterionResult
	}{
		{"duplicate criteria with duplicate results", []string{"Retry twice", "Retry twice"}, []AgentCriterionResult{passed("Retry twice"), passed("Retry twice")}},
		{"duplicate criteria with single result", []string{"Retry twice", "Retry twice"}, []AgentCriterionResult{passed("Retry twice")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if CriteriaVerified(tc.criteria, tc.results) {
				t.Fatal("duplicate approved criteria accepted")
			}
		})
	}
}

func TestCriteriaVerifiedRejectsUnapprovedEvidence(t *testing.T) {
	criteria := []string{"Retry twice"}
	results := []AgentCriterionResult{passed("Unapproved replacement")}
	if CriteriaVerified(criteria, results) {
		t.Fatal("unapproved evidence accepted for an approved criterion")
	}
	if CriteriaVerified(criteria, []AgentCriterionResult{passed("Retry twice"), passed("Retry twice")}) {
		t.Fatal("more results than approved criteria accepted")
	}
}

// Reject missing, failed, malformed or duplicate verification results.
func TestCriteriaVerifiedRejectsIncompleteInvalidOrDuplicateResults(t *testing.T) {
	criteria := []string{"First criterion", "Second criterion"}
	cases := []struct {
		name    string
		results []AgentCriterionResult
	}{
		{"missing result", []AgentCriterionResult{passed("First criterion")}},
		{"failed result", []AgentCriterionResult{passed("First criterion"), failed("Second criterion")}},
		{"all failed", []AgentCriterionResult{failed("First criterion"), failed("Second criterion")}},
		{"duplicate results", []AgentCriterionResult{passed("First criterion"), passed("First criterion")}},
		{"unknown status", []AgentCriterionResult{{Criterion: "First criterion", Status: "unknown", Evidence: "Some signed verifier evidence"}}},
		{"empty criterion", []AgentCriterionResult{{Criterion: "", Status: "passed", Evidence: "Some signed verifier evidence"}}},
		{"untrimmed criterion", []AgentCriterionResult{{Criterion: " First criterion", Status: "passed", Evidence: "Some signed verifier evidence"}}},
		{"empty evidence", []AgentCriterionResult{{Criterion: "First criterion", Status: "passed", Evidence: ""}}},
		{"short evidence", []AgentCriterionResult{{Criterion: "First criterion", Status: "passed", Evidence: "no"}}},
		{"nul byte in evidence", []AgentCriterionResult{{Criterion: "First criterion", Status: "passed", Evidence: "bad\x00evidence"}}},
		{"oversized criterion", []AgentCriterionResult{{Criterion: strings.Repeat("c", 1001), Status: "passed", Evidence: "Some signed verifier evidence"}}},
		{"oversized evidence", []AgentCriterionResult{{Criterion: "First criterion", Status: "passed", Evidence: strings.Repeat("e", 2001)}}},
	}
	tooMany := make([]AgentCriterionResult, 21)
	for i := range tooMany {
		tooMany[i] = passed("First criterion")
	}
	cases = append(cases, struct {
		name    string
		results []AgentCriterionResult
	}{"too many results", tooMany})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if CriteriaVerified(criteria, tc.results) {
				t.Fatal("invalid verification results accepted")
			}
		})
	}
	if CriteriaVerified(nil, []AgentCriterionResult{passed("First criterion")}) {
		t.Fatal("empty criteria accepted")
	}
	if CriteriaVerified(criteria, nil) {
		t.Fatal("empty results accepted")
	}
}

// Keep valid complete passing evidence accepted.
func TestCriteriaVerifiedAcceptsValidCompletePassingEvidence(t *testing.T) {
	criteria := []string{"First criterion", "Second criterion"}
	results := []AgentCriterionResult{passed("First criterion"), passed("Second criterion")}
	if !ValidCriterionResults(results) {
		t.Fatal("valid criterion results rejected")
	}
	if !CriteriaVerified(criteria, results) {
		t.Fatal("valid complete passing evidence rejected")
	}
}
