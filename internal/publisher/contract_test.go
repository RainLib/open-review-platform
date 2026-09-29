package publisher

import (
	"strings"
	"testing"
)

func TestParseChangeContractClassifiesContextQuality(t *testing.T) {
	if contract := ParseChangeContract(""); contract.Quality != ContractEmpty {
		t.Fatalf("empty description classified as %s", contract.Quality)
	}
	if contract := ParseChangeContract("Fix the bug."); contract.Quality != ContractMinimal {
		t.Fatalf("unstructured description classified as %s", contract.Quality)
	}
	partial := ParseChangeContract("## Outcome\nStop duplicate jobs.\n\n## Scope\nRunner only.")
	if partial.Quality != ContractPartial || partial.Sections[ContractOutcome] != "Stop duplicate jobs." {
		t.Fatalf("unexpected partial contract: %#v", partial)
	}
	complete := ParseChangeContract(`
## Outcome
Stop duplicate jobs.
## Scope
Runner only.
## Risk
R1, internal source code.
## Acceptance mapping
AC-1 -> runner_test.go
## Invariants
One active run per revision.
## Verification
go test ./...
## Rollout
Internal -> 5% -> 100%.
## Rollback
Disable the feature flag.
`)
	if complete.Quality != ContractComplete || len(complete.Missing) != 0 {
		t.Fatalf("unexpected complete contract: %#v", complete)
	}
}

func TestParseChangeContractSupportsChineseHeadings(t *testing.T) {
	contract := ParseChangeContract("## 目标\n降低重复评论。\n## 验收映射\nAC-1 -> 单元测试")
	if contract.Sections[ContractOutcome] == "" || contract.Sections[ContractAcceptanceMapping] == "" {
		t.Fatalf("Chinese headings were not recognized: %#v", contract)
	}
}

func TestDeclaredEvidenceEscapesUntrustedDescriptionHTML(t *testing.T) {
	contract := ParseChangeContract("## Outcome\n</details><script>alert(1)</script>")
	report := CompletedReport(testReportJob(), ReviewContext{Contract: contract}, ReviewResult{Gate: EvaluateMergeGate(nil, "critical")}, "marker")
	if strings.Contains(report, "<script>") || !strings.Contains(report, "&lt;script&gt;") {
		t.Fatalf("untrusted evidence was not escaped: %s", report)
	}
}
