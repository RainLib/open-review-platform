package risk

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseChangedFileMetadataIncludesRenameAndBinary(t *testing.T) {
	changes, err := parseChangedFileStatuses([]byte("M\x00internal/api/server.go\x00R093\x00docs/old.md\x00docs/new.md\x00D\x00docs/gone.md\x00A\x00assets/logo.bin\x00"))
	if err != nil || len(changes) != 4 || changes[1].Path != "docs/new.md" || changes[1].PreviousPath != "docs/old.md" || changes[1].ChangeType != "renamed" || changes[2].ChangeType != "deleted" {
		t.Fatalf("changed-file statuses=%#v err=%v", changes, err)
	}
	stats, err := parseChangedFileStats([]byte("2\t1\tinternal/api/server.go\x001\t1\t\x00docs/old.md\x00docs/new.md\x000\t4\tdocs/gone.md\x00-\t-\tassets/logo.bin\x00"))
	if err != nil || stats["docs/new.md"].Additions != 1 || stats["docs/gone.md"].Deletions != 4 || !stats["assets/logo.bin"].Binary {
		t.Fatalf("changed-file statistics=%#v err=%v", stats, err)
	}
	for _, malformed := range [][]byte{[]byte("R100\x00old\x00"), []byte("M\x00file"), []byte("X\x00file\x00")} {
		if _, err := parseChangedFileStatuses(malformed); err == nil {
			t.Fatalf("malformed status accepted: %q", malformed)
		}
	}
	for _, malformed := range [][]byte{[]byte("-\t1\tfile\x00"), []byte("1\t2\t\x00old\x00"), []byte("1\t2\tfile")} {
		if _, err := parseChangedFileStats(malformed); err == nil {
			t.Fatalf("malformed statistics accepted: %q", malformed)
		}
	}
}

func TestPlannerRetainsActualGitChangeMetadata(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	directory := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = directory
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return string(output)
	}
	write := func(path string, body []byte) {
		t.Helper()
		absolute := filepath.Join(directory, path)
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit("init", "-q")
	write("docs/old.md", []byte("same file\n"))
	write("docs/gone.md", []byte("remove me\n"))
	write("internal/api/server.go", []byte("package api\n"))
	runGit("add", "-A")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "base")
	base := strings.TrimSpace(runGit("rev-parse", "HEAD"))
	if err := os.Rename(filepath.Join(directory, "docs/old.md"), filepath.Join(directory, "docs/new.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "docs/gone.md")); err != nil {
		t.Fatal(err)
	}
	write("internal/api/server.go", []byte("package api\n// review this change\n"))
	write("assets/logo.bin", []byte{0, 1, 2, 3})
	runGit("add", "-A")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "head")
	head := strings.TrimSpace(runGit("rev-parse", "HEAD"))
	plan, err := (Planner{GitBinary: git}).PlanWithMode(context.Background(), directory, base, head, ModeStandard)
	if err != nil {
		t.Fatal(err)
	}
	items := make(map[string]Item)
	for _, item := range append(plan.Selected, plan.Deferred...) {
		items[item.Path] = item
	}
	if item := items["docs/new.md"]; item.ChangeType != "renamed" || item.PreviousPath != "docs/old.md" || !item.StatsKnown {
		t.Fatalf("rename metadata: %#v", item)
	}
	if item := items["docs/gone.md"]; item.ChangeType != "deleted" || !item.StatsKnown || item.Deletions != 1 {
		t.Fatalf("delete metadata: %#v", item)
	}
	if item := items["assets/logo.bin"]; item.ChangeType != "added" || !item.StatsKnown || !item.Binary {
		t.Fatalf("binary metadata: %#v", item)
	}
	if item := items["internal/api/server.go"]; item.ChangeType != "modified" || !item.StatsKnown || item.Additions != 1 {
		t.Fatalf("modified metadata: %#v", item)
	}
	for index := 0; index <= maxDiffStatsFiles; index++ {
		write(fmt.Sprintf("bulk/file-%03d.txt", index), []byte("change\n"))
	}
	runGit("add", "-A")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "large head")
	largeHead := strings.TrimSpace(runGit("rev-parse", "HEAD"))
	large, err := (Planner{GitBinary: git}).PlanWithMode(context.Background(), directory, head, largeHead, ModeCritical)
	if err != nil {
		t.Fatal(err)
	}
	if len(large.Selected)+len(large.Deferred) != maxDiffStatsFiles+1 {
		t.Fatalf("large changed-file count=%d", len(large.Selected)+len(large.Deferred))
	}
	for _, item := range append(large.Selected, large.Deferred...) {
		if item.ChangeType != "added" || item.StatsKnown {
			t.Fatalf("large diff must retain status without a second line-count pass: %#v", item)
		}
	}
}

func TestClassifyPrioritizesSecurityAndStateOverDocumentation(t *testing.T) {
	high := Classify("internal/auth/payment/migration.go")
	low := Classify("docs/runbook.md")
	if high.Score < 75 || low.Score != 10 {
		t.Fatalf("unexpected scores: high=%#v low=%#v", high, low)
	}
	if !include(ModeFocused, high.Score) || include(ModeFocused, low.Score) {
		t.Fatal("focused mode must retain high-risk code and defer docs")
	}
}

func TestClassifyExcludesGeneratedArtifacts(t *testing.T) {
	item := Classify("internal/generated/client.go")
	if item.Score != 0 || include(ModeStandard, item.Score) {
		t.Fatalf("generated code must be excluded, got %#v", item)
	}
}

func TestCriticalModeFallsBackToOneHighSignalBoundary(t *testing.T) {
	plan := Plan{
		Deferred: []Item{
			{Path: "internal/api/server.go", Score: 70, Reasons: []string{"public boundary"}},
			{Path: "internal/webhook/normalize.go", Score: 70, Reasons: []string{"asynchronous workflow"}},
			{Path: "internal/store/postgres.go", Score: 45, Reasons: []string{"application code"}},
		},
	}
	plan = criticalFallback(plan)
	if len(plan.Selected) != 1 || plan.Selected[0].Path != "internal/api/server.go" {
		t.Fatalf("unexpected critical fallback scope: %#v", plan.Selected)
	}
	if len(plan.Deferred) != 2 || plan.Deferred[0].Path != "internal/webhook/normalize.go" || plan.Deferred[1].Path != "internal/store/postgres.go" {
		t.Fatalf("unexpected critical fallback deferred scope: %#v", plan.Deferred)
	}
	if got := plan.Selected[0].Reasons[len(plan.Selected[0].Reasons)-1]; got != "critical-mode fallback: highest available high-signal boundary" {
		t.Fatalf("missing fallback provenance: %#v", plan.Selected[0])
	}
}

func TestCriticalModeDefersExcessHighSignalFiles(t *testing.T) {
	plan := Plan{Selected: []Item{
		{Path: "go.mod", Score: 80},
		{Path: "go.sum", Score: 80},
		{Path: "migrations/000002.sql", Score: 80},
		{Path: "migrations/000003.sql", Score: 80},
		{Path: "migrations/000004.sql", Score: 80},
	}}
	plan = limitSignalDiverseSelection(plan, criticalSelectionLimit, criticalFallbackScore, "critical-mode budget: deferred after the highest-signal file")
	if len(plan.Selected) != criticalSelectionLimit {
		t.Fatalf("selected=%d, want %d", len(plan.Selected), criticalSelectionLimit)
	}
	if plan.Selected[0].Path != "go.mod" || len(plan.Deferred) != 4 {
		t.Fatalf("unexpected deferred paths %#v", plan.Deferred)
	}
	for _, item := range plan.Deferred {
		if got := item.Reasons; len(got) != 1 || got[0] != "critical-mode budget: deferred after the highest-signal file" {
			t.Fatalf("missing deferred provenance %#v", got)
		}
	}
}

func TestFocusedModeCapsScopeAndKeepsDiverseRiskSignals(t *testing.T) {
	plan := Plan{Selected: []Item{
		{Path: "go.mod", Score: 80, Reasons: []string{"dependency or build manifest"}},
		{Path: "go.sum", Score: 80, Reasons: []string{"dependency or build manifest"}},
		{Path: "migrations/001.sql", Score: 80, Reasons: []string{"persistent state or financial side effect"}},
		{Path: "migrations/002.sql", Score: 80, Reasons: []string{"persistent state or financial side effect"}},
		{Path: ".github/workflows/ci.yml", Score: 70, Reasons: []string{"deployment or supply-chain boundary"}},
		{Path: "internal/api/server.go", Score: 70, Reasons: []string{"public boundary or asynchronous workflow"}},
		{Path: "internal/api/auth.go", Score: 100, Reasons: []string{"authentication, authorization, or secret boundary", "public boundary or asynchronous workflow"}},
		{Path: "internal/api/handler.go", Score: 70, Reasons: []string{"public boundary or asynchronous workflow"}},
		{Path: "Dockerfile", Score: 70, Reasons: []string{"deployment or supply-chain boundary"}},
	}}
	plan = limitSignalDiverseSelection(plan, focusedSelectionLimit, 0, "focused-mode budget: deferred after the diverse high-signal files")
	if len(plan.Selected) != focusedSelectionLimit || len(plan.Deferred) != 1 {
		t.Fatalf("unexpected focused scope selected=%#v deferred=%#v", plan.Selected, plan.Deferred)
	}
	for _, signal := range []string{
		"authentication, authorization, or secret boundary",
		"dependency or build manifest",
		"persistent state or financial side effect",
		"deployment or supply-chain boundary",
		"public boundary or asynchronous workflow",
	} {
		found := false
		for _, item := range plan.Selected {
			for _, candidate := range item.Reasons {
				if candidate == signal {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("focused selection lost risk signal %q: %#v", signal, plan.Selected)
		}
	}
	if got := plan.Deferred[0].Reasons[len(plan.Deferred[0].Reasons)-1]; got != "focused-mode budget: deferred after the diverse high-signal files" {
		t.Fatalf("missing focused deferral provenance: %#v", plan.Deferred[0])
	}
}

func TestCriticalModeKeepsOnlyStrongestHighSignalFile(t *testing.T) {
	plan := Plan{
		Selected: []Item{
			{Path: "go.mod", Score: 80, Reasons: []string{"dependency or build manifest"}},
			{Path: "go.sum", Score: 80, Reasons: []string{"dependency or build manifest"}},
			{Path: "migrations/001.sql", Score: 80, Reasons: []string{"persistent state or financial side effect"}},
		},
		Deferred: []Item{
			{Path: ".github/workflows/ci.yml", Score: 70, Reasons: []string{"deployment or supply-chain boundary"}},
			{Path: "internal/api/server.go", Score: 70, Reasons: []string{"public boundary or asynchronous workflow"}},
		},
	}
	plan = limitSignalDiverseSelection(plan, criticalSelectionLimit, criticalFallbackScore, "critical-mode budget: deferred after the highest-signal file")
	if len(plan.Selected) != 1 || len(plan.Deferred) != 4 {
		t.Fatalf("selected=%#v", plan.Selected)
	}
	if plan.Selected[0].Path != "go.mod" {
		t.Fatalf("critical selection must keep the strongest stable candidate: %#v", plan.Selected)
	}
}

func TestCriticalModePromotesStrongerEligibleDeferredFile(t *testing.T) {
	plan := Plan{
		Selected: []Item{
			{Path: "go.mod", Score: 80, Reasons: []string{"dependency or build manifest"}},
		},
		Deferred: []Item{
			{Path: "apps/web/Dockerfile", Score: 85, Reasons: []string{"deployment or supply-chain boundary"}},
		},
	}
	plan = limitSignalDiverseSelection(plan, criticalSelectionLimit, criticalFallbackScore, "critical-mode budget: deferred after the highest-signal file")
	if len(plan.Selected) != 1 || plan.Selected[0].Path != "apps/web/Dockerfile" || len(plan.Deferred) != 1 || plan.Deferred[0].Path != "go.mod" {
		t.Fatalf("eligible deferred file was not promoted: selected=%#v deferred=%#v", plan.Selected, plan.Deferred)
	}
}

func TestClassifyPrioritizesManifestsMigrationsCIAndNestedContainerBuilds(t *testing.T) {
	for _, path := range []string{"go.mod", "migrations/000057_review_config_change_approvals.sql", ".github/workflows/ci.yml", "Dockerfile", "apps/web/Dockerfile"} {
		item := Classify(path)
		if item.Score < 55 || !include(ModeFocused, item.Score) {
			t.Fatalf("%s must be selected in focused mode, got %#v", path, item)
		}
	}
	if got := Classify("apps/web/Dockerfile").Score; got < 75 {
		t.Fatalf("nested container build must remain eligible for critical mode, score=%d", got)
	}
}

func TestStaticImpactSignalsKeepOnlyMeaningfulSelectedReasons(t *testing.T) {
	signals := StaticImpactSignals([]Item{
		{Path: "internal/api/server.go", Reasons: []string{"changed application code", "public boundary or asynchronous workflow"}},
		{Path: "migrations/000057.sql", Reasons: []string{"changed application code", "persistent state or financial side effect"}},
		{Path: "internal/api/another.go", Reasons: []string{"public boundary or asynchronous workflow"}},
		{Path: "internal/api/server_test.go", Reasons: []string{"test-only path"}},
	})
	if got, want := len(signals), 2; got != want {
		t.Fatalf("unexpected static impact signals %#v", signals)
	}
	if signals[0] != "public boundary or asynchronous workflow" || signals[1] != "persistent state or financial side effect" {
		t.Fatalf("unexpected static impact signal order %#v", signals)
	}
}
