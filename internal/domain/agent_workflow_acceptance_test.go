package domain

import (
	"strings"
	"testing"
)

// TestCriteriaVerifiedStrictOneToOneMapping is the regression test for the
// duplicate-criterion defect: approved criteria must map one-to-one onto
// passing verification results, so duplicated approved criteria or evidence
// for unapproved criteria must never be counted as complete verification.
func TestCriteriaVerifiedStrictOneToOneMapping(t *testing.T) {
	cases := []struct {
		name     string
		criteria []string
		results  []AgentCriterionResult
		want     bool
	}{
		{
			name:     "duplicate approved criteria with unapproved replacement evidence",
			criteria: []string{"Retry twice", "Retry twice"},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
				{Criterion: "Unapproved replacement", Status: "passed", Evidence: "Unrelated evidence for an unapproved criterion."},
			},
			want: false,
		},
		{
			name:     "unapproved evidence alongside passing approved criterion",
			criteria: []string{"Retry twice"},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
				{Criterion: "Unapproved replacement", Status: "passed", Evidence: "Unrelated evidence for an unapproved criterion."},
			},
			want: false,
		},
		{
			name:     "duplicate evidence for a single approved criterion",
			criteria: []string{"Retry twice"},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
				{Criterion: "Retry twice", Status: "passed", Evidence: "Repeated evidence for the same criterion."},
			},
			want: false,
		},
		{
			name:     "missing evidence for second approved criterion",
			criteria: []string{"Retry twice", "Show final error"},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
			},
			want: false,
		},
		{
			name:     "failed result for approved criterion",
			criteria: []string{"Retry twice"},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "failed", Evidence: "Retry did not occur twice."},
			},
			want: false,
		},
		{
			name:     "failed second result with passing first",
			criteria: []string{"Retry twice", "Show final error"},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
				{Criterion: "Show final error", Status: "failed", Evidence: "Final error was not shown."},
			},
			want: false,
		},
		{
			name:     "valid complete passing evidence accepted",
			criteria: []string{"Retry twice", "Show final error"},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
				{Criterion: "Show final error", Status: "passed", Evidence: "Final error surfaced in the run log."},
			},
			want: true,
		},
		{
			name:     "empty approved criteria rejected",
			criteria: nil,
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
			},
			want: false,
		},
		{
			name:     "case-sensitive criterion match required",
			criteria: []string{"retry twice"},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
			},
			want: false,
		},
		{
			name:     "untrimmed approved criterion must not match trimmed evidence",
			criteria: []string{" Retry twice "},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CriteriaVerified(tc.criteria, tc.results); got != tc.want {
				t.Fatalf("CriteriaVerified(%q, results) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestCriteriaVerifiedEqualCardinalityMalformedFields ensures malformed
// verification results are rejected even when their cardinality exactly
// matches the approved criteria, so rejection cannot be explained by length
// differences alone.
func TestCriteriaVerifiedEqualCardinalityMalformedFields(t *testing.T) {
	criteria := []string{"Retry twice", "Show final error"}
	passing := func() []AgentCriterionResult {
		return []AgentCriterionResult{
			{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
			{Criterion: "Show final error", Status: "passed", Evidence: "Final error surfaced in the run log."},
		}
	}
	mutate := map[string]func(results []AgentCriterionResult){
		"unknown status": func(rs []AgentCriterionResult) { rs[1].Status = "pending" },
		"empty status": func(rs []AgentCriterionResult) { rs[1].Status = "" },
		"empty criterion": func(rs []AgentCriterionResult) { rs[1].Criterion = "" },
		"untrimmed criterion": func(rs []AgentCriterionResult) { rs[1].Criterion = " Show final error " },
		"short criterion": func(rs []AgentCriterionResult) { rs[1].Criterion = "ab" },
		"oversized criterion": func(rs []AgentCriterionResult) { rs[1].Criterion = strings.Repeat("x", 1001) },
		"criterion containing NUL": func(rs []AgentCriterionResult) { rs[1].Criterion = "Show\x00final error" },
		"empty evidence": func(rs []AgentCriterionResult) { rs[1].Evidence = "" },
		"short evidence": func(rs []AgentCriterionResult) { rs[1].Evidence = "no" },
		"evidence containing NUL": func(rs []AgentCriterionResult) { rs[1].Evidence = "Final error\x00surfaced in the run log." },
		"oversized evidence": func(rs []AgentCriterionResult) { rs[1].Evidence = strings.Repeat("x", 2001) },
	}
	for name, mutateResults := range mutate {
		t.Run(name, func(t *testing.T) {
			results := passing()
			mutateResults(results)
			if len(results) != len(criteria) {
				t.Fatalf("test setup error: results cardinality %d must equal criteria cardinality %d", len(results), len(criteria))
			}
			if got := CriteriaVerified(criteria, results); got {
				t.Fatalf("CriteriaVerified accepted malformed results (%s) at equal cardinality", name)
			}
			if ValidCriterionResults(results) {
				t.Fatalf("ValidCriterionResults accepted malformed results (%s) at equal cardinality", name)
			}
		})
	}
}

// TestValidCriterionResultsMalformedAndDuplicate checks the result-level
// validator directly: malformed fields and duplicate criteria are rejected
// while well-formed distinct results pass.
func TestValidCriterionResultsMalformedAndDuplicate(t *testing.T) {
	if !ValidCriterionResults(nil) {
		t.Fatal("ValidCriterionResults(nil) = false, want true")
	}
	valid := []AgentCriterionResult{
		{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
		{Criterion: "Show final error", Status: "failed", Evidence: "Final error was not shown."},
	}
	if !ValidCriterionResults(valid) {
		t.Fatal("ValidCriterionResults rejected well-formed distinct results")
	}
	duplicate := []AgentCriterionResult{
		{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."},
		{Criterion: "Retry twice", Status: "passed", Evidence: "Repeated evidence for the same criterion."},
	}
	if ValidCriterionResults(duplicate) {
		t.Fatal("ValidCriterionResults accepted duplicate criterion results")
	}
	oversized := make([]AgentCriterionResult, 21)
	for i := range oversized {
		oversized[i] = AgentCriterionResult{Criterion: "Retry twice", Status: "passed", Evidence: "Retried twice and observed the final error."}
	}
	if ValidCriterionResults(oversized) {
		t.Fatal("ValidCriterionResults accepted more than 20 results")
	}
}
