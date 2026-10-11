package domain

import (
	"fmt"
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

// TestCriteriaVerifiedInclusiveBoundaries demonstrates the inclusive valid
// boundaries of the field-length rules: criteria of exactly 3 and exactly
// 1000 bytes and evidence of exactly 3 and exactly 2000 bytes must pass both
// validators with complete passing evidence, including multi-byte UTF-8
// content measured under the existing byte-length rules. Over-limit fields
// must be rejected; for over-limit criterion cases the approved criterion and
// the result criterion are identical at equal cardinality, so the rejection
// can only come from field validation rather than a criterion mismatch.
func TestCriteriaVerifiedInclusiveBoundaries(t *testing.T) {
	asciiMinCriterion := "abc"
	asciiMaxCriterion := strings.Repeat("x", 1000)
	asciiMinEvidence := "efg"
	asciiMaxEvidence := strings.Repeat("y", 2000)
	threeDistinctMaxCriteria := []string{
		strings.Repeat("a", 999) + "0",
		strings.Repeat("a", 999) + "1",
		strings.Repeat("a", 999) + "2",
	}
	threeMaxEvidenceResults := []AgentCriterionResult{
		{Criterion: threeDistinctMaxCriteria[0], Status: "passed", Evidence: asciiMaxEvidence},
		{Criterion: threeDistinctMaxCriteria[1], Status: "passed", Evidence: asciiMaxEvidence},
		{Criterion: threeDistinctMaxCriteria[2], Status: "passed", Evidence: asciiMaxEvidence},
	}
	multibyteMinCriterion := "日" // 3 bytes
	multibyteMaxCriterion := strings.Repeat("é", 500)
	multibyteMinEvidence := "漢" // 3 bytes
	multibyteMaxEvidence := strings.Repeat("é", 1000)
	asciiOverCriterion := strings.Repeat("x", 1001)
	multibyteOverCriterion := strings.Repeat("é", 501) // 1002 bytes
	asciiOverEvidence := strings.Repeat("y", 2001)
	multibyteOverEvidence := strings.Repeat("é", 1001) // 2002 bytes
	cases := []struct {
		name         string
		criteria     []string
		results      []AgentCriterionResult
		wantVerified bool
	}{
		{
			name:     "minimum-size ASCII criterion and evidence at inclusive limits",
			criteria: []string{asciiMinCriterion},
			results: []AgentCriterionResult{
				{Criterion: asciiMinCriterion, Status: "passed", Evidence: asciiMinEvidence},
			},
			wantVerified: true,
		},
		{
			name:     "maximum-size ASCII criterion and evidence at inclusive limits",
			criteria: []string{asciiMaxCriterion},
			results: []AgentCriterionResult{
				{Criterion: asciiMaxCriterion, Status: "passed", Evidence: asciiMaxEvidence},
			},
			wantVerified: true,
		},
		{
			name:         "three 1000-byte criteria with 2000-byte evidence fully verified",
			criteria:     threeDistinctMaxCriteria,
			results:      threeMaxEvidenceResults,
			wantVerified: true,
		},
		{
			name:     "multi-byte UTF-8 criterion and evidence at exact byte limits",
			criteria: []string{multibyteMinCriterion, multibyteMaxCriterion},
			results: []AgentCriterionResult{
				{Criterion: multibyteMinCriterion, Status: "passed", Evidence: multibyteMinEvidence},
				{Criterion: multibyteMaxCriterion, Status: "passed", Evidence: multibyteMaxEvidence},
			},
			wantVerified: true,
		},
		{
			name:     "ASCII criterion over the 1000-byte limit rejected at equal cardinality with identical criterion",
			criteria: []string{asciiOverCriterion},
			results: []AgentCriterionResult{
				{Criterion: asciiOverCriterion, Status: "passed", Evidence: "Valid evidence for the over-limit criterion."},
			},
			wantVerified: false,
		},
		{
			name:     "multi-byte UTF-8 criterion over the 1000-byte limit rejected at equal cardinality with identical criterion",
			criteria: []string{multibyteOverCriterion},
			results: []AgentCriterionResult{
				{Criterion: multibyteOverCriterion, Status: "passed", Evidence: "Valid evidence for the over-limit criterion."},
			},
			wantVerified: false,
		},
		{
			name:     "ASCII evidence over the 2000-byte limit rejected",
			criteria: []string{"Retry twice"},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: asciiOverEvidence},
			},
			wantVerified: false,
		},
		{
			name:     "multi-byte UTF-8 evidence over the 2000-byte limit rejected",
			criteria: []string{"Retry twice"},
			results: []AgentCriterionResult{
				{Criterion: "Retry twice", Status: "passed", Evidence: multibyteOverEvidence},
			},
			wantVerified: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CriteriaVerified(tc.criteria, tc.results); got != tc.wantVerified {
				t.Fatalf("CriteriaVerified = %v, want %v", got, tc.wantVerified)
			}
			if got := ValidCriterionResults(tc.results); got != tc.wantVerified {
				t.Fatalf("ValidCriterionResults = %v, want %v", got, tc.wantVerified)
			}
		})
	}
}

// TestCriteriaVerifiedTwentyDistinctCriteria demonstrates that a full valid
// result set of 20 distinct approved criteria passes verification, including
// when the results are reordered, while 21 criteria/results are rejected.
func TestCriteriaVerifiedTwentyDistinctCriteria(t *testing.T) {
	twentyCriteria := make([]string, 20)
	twentyResults := make([]AgentCriterionResult, 20)
	for i := range twentyCriteria {
		twentyCriteria[i] = fmt.Sprintf("Criterion %02d verified distinctly", i)
		twentyResults[i] = AgentCriterionResult{
			Criterion: twentyCriteria[i],
			Status:    "passed",
			Evidence:  fmt.Sprintf("Evidence for criterion %02d recorded fully.", i),
		}
	}
	reorderedResults := make([]AgentCriterionResult, 20)
	for i, r := range twentyResults {
		reorderedResults[19-i] = r
	}
	twentyOneCriteria := append(append([]string{}, twentyCriteria...), "Criterion 20 extra beyond limit")
	twentyOneResults := append(append([]AgentCriterionResult{}, twentyResults...), AgentCriterionResult{
		Criterion: "Criterion 20 extra beyond limit",
		Status:    "passed",
		Evidence:  "Evidence for the twenty-first criterion recorded fully.",
	})
	cases := []struct {
		name     string
		criteria []string
		results  []AgentCriterionResult
		want     bool
	}{
		{
			name:     "twenty distinct criteria in delivered order",
			criteria: twentyCriteria,
			results:  twentyResults,
			want:     true,
		},
		{
			name:     "twenty distinct criteria with reordered results",
			criteria: twentyCriteria,
			results:  reorderedResults,
			want:     true,
		},
		{
			name:     "twenty-one distinct criteria rejected",
			criteria: twentyOneCriteria,
			results:  twentyOneResults,
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CriteriaVerified(tc.criteria, tc.results); got != tc.want {
				t.Fatalf("CriteriaVerified = %v, want %v", got, tc.want)
			}
			if got := ValidCriterionResults(tc.results); got != tc.want {
				t.Fatalf("ValidCriterionResults = %v, want %v", got, tc.want)
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
