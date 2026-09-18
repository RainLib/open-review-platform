// Package risk turns a changed-file list into a conservative AI review scope.
// It intentionally starts with deterministic, explainable path signals. A
// later symbol/call-graph provider can enrich the same Plan without changing
// runner or provider contracts.
package risk

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type Mode string

const (
	ModeStandard Mode = "standard"
	ModeFocused  Mode = "focused"
	ModeCritical Mode = "critical"

	criticalFallbackScore = 55
	criticalFallbackLimit = 2
)

func (m Mode) Valid() bool {
	return m == ModeStandard || m == ModeFocused || m == ModeCritical
}

type Item struct {
	Path    string
	Score   int
	Reasons []string
}

type Plan struct {
	Selected []Item
	Deferred []Item
	Exclude  []string
}

type Planner struct {
	GitBinary string
	Mode      Mode
}

func (p Planner) Plan(ctx context.Context, directory, base, head string) (Plan, error) {
	git := p.GitBinary
	if git == "" {
		git = "git"
	}
	command := exec.CommandContext(ctx, git, "diff", "--name-only", base, head)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		return Plan{}, fmt.Errorf("list changed files for risk planning: %w", err)
	}
	mode := p.Mode
	if mode == "" {
		mode = ModeFocused
	}
	if !mode.Valid() {
		return Plan{}, fmt.Errorf("unsupported risk review mode %q", mode)
	}
	plan := Plan{}
	for _, path := range strings.Fields(string(output)) {
		item := Classify(path)
		if include(mode, item.Score) {
			plan.Selected = append(plan.Selected, item)
			continue
		}
		plan.Deferred = append(plan.Deferred, item)
		plan.Exclude = append(plan.Exclude, path)
	}
	sort.Slice(plan.Selected, func(i, j int) bool { return plan.Selected[i].Score > plan.Selected[j].Score })
	sort.Slice(plan.Deferred, func(i, j int) bool { return plan.Deferred[i].Score > plan.Deferred[j].Score })
	if mode == ModeCritical && len(plan.Selected) == 0 {
		plan = criticalFallback(plan)
	}
	plan.Exclude = plan.Exclude[:0]
	for _, item := range plan.Deferred {
		plan.Exclude = append(plan.Exclude, item.Path)
	}
	return plan, nil
}

// criticalFallback prevents a high-priority review from becoming an empty
// review when no path crosses the strict threshold. It keeps the scope small
// and explainable: at most two public or asynchronous workflow boundaries
// (score >= 55), never low-value artifacts or ordinary application files.
func criticalFallback(plan Plan) Plan {
	remaining := make([]Item, 0, len(plan.Deferred))
	for _, item := range plan.Deferred {
		if len(plan.Selected) < criticalFallbackLimit && item.Score >= criticalFallbackScore {
			item.Reasons = append(item.Reasons, "critical-mode fallback: highest available high-signal boundary")
			plan.Selected = append(plan.Selected, item)
			continue
		}
		remaining = append(remaining, item)
	}
	plan.Deferred = remaining
	return plan
}

func include(mode Mode, score int) bool {
	switch mode {
	case ModeStandard:
		return score > 0
	case ModeCritical:
		return score >= 75
	default:
		return score >= 55
	}
}

// Classify is deliberately conservative: it only excludes known low-value
// artifacts in focused modes and elevates paths with security, state, or
// external-impact signals. It never claims to be a call-graph score.
func Classify(path string) Item {
	normalized := strings.ToLower(strings.TrimSpace(path))
	item := Item{Path: path, Score: 45, Reasons: []string{"changed application code"}}
	if normalized == "" {
		return Item{Path: path}
	}
	if contains(normalized, "/vendor/", "/node_modules/", "/generated/", "/testdata/", "/fixtures/", "/coverage/", ".snap") || strings.HasSuffix(normalized, ".lock") {
		return Item{Path: path, Score: 0, Reasons: []string{"generated, vendored, fixture, coverage, or lock artifact"}}
	}
	if strings.HasSuffix(normalized, ".md") || strings.HasPrefix(normalized, "docs/") {
		return Item{Path: path, Score: 10, Reasons: []string{"documentation-only change"}}
	}
	if contains(normalized, "/auth", "/identity", "/permission", "/rbac", "/policy", "/session", "/oauth", "/jwt", "/secret") {
		item.Score += 45
		item.Reasons = append(item.Reasons, "authentication, authorization, or secret boundary")
	}
	if contains(normalized, "/payment", "/billing", "/ledger", "/migration", "/schema", "/database", "/repository", "/sql", "/cache") {
		item.Score += 35
		item.Reasons = append(item.Reasons, "persistent state or financial side effect")
	}
	if contains(normalized, "/api/", "/controller", "/handler", "/router", "/webhook", "/queue", "/worker", "/event") {
		item.Score += 25
		item.Reasons = append(item.Reasons, "public boundary or asynchronous workflow")
	}
	if contains(normalized, "/deploy", "/infra", "/terraform", "/k8s", "/docker", "/.github/workflows/") {
		item.Score += 25
		item.Reasons = append(item.Reasons, "deployment or supply-chain boundary")
	}
	if contains(normalized, "_test.", "/test/", "/tests/") {
		item.Score -= 25
		item.Reasons = append(item.Reasons, "test-only path")
	}
	if item.Score > 100 {
		item.Score = 100
	}
	if item.Score < 0 {
		item.Score = 0
	}
	return item
}

func contains(value string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(value, part) {
			return true
		}
	}
	return false
}
