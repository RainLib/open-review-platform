// Package risk turns a changed-file list into a conservative AI review scope.
// It intentionally starts with deterministic, explainable path signals. A
// later symbol/call-graph provider can enrich the same Plan without changing
// runner or provider contracts.
package risk

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type Mode string

const (
	ModeStandard Mode = "standard"
	ModeFocused  Mode = "focused"
	ModeCritical Mode = "critical"

	criticalFallbackScore = 55
	criticalFallbackLimit = 1
	focusedSelectionLimit = 8
	// A metadata view must not turn a large, high-priority review into another
	// full diff pass. Large changes keep their status and explicit unknown stats.
	maxDiffStatsFiles = 200
	// Security mode is the fast incident path: review the single strongest
	// boundary first and retain every other path as explicit deferred evidence.
	// This also prevents a small set of unrelated high-risk files from being
	// bundled into one long-running model conversation.
	criticalSelectionLimit = 1
)

func (m Mode) Valid() bool {
	return m == ModeStandard || m == ModeFocused || m == ModeCritical
}

type Item struct {
	Path         string
	Score        int
	Reasons      []string
	ChangeType   string
	PreviousPath string
	Additions    int
	Deletions    int
	StatsKnown   bool
	Binary       bool
}

type Plan struct {
	Selected            []Item
	Deferred            []Item
	Exclude             []string
	StaticImpactSignals []string
}

type Planner struct {
	GitBinary string
	Mode      Mode
}

func (p Planner) Plan(ctx context.Context, directory, base, head string) (Plan, error) {
	return p.PlanWithMode(ctx, directory, base, head, p.Mode)
}

// PlanWithMode lets an authorized command select a run-local review intensity
// without mutating the deployment-wide default for other tenants or runs.
func (p Planner) PlanWithMode(ctx context.Context, directory, base, head string, mode Mode) (Plan, error) {
	if mode == "" {
		mode = ModeFocused
	}
	if !mode.Valid() {
		return Plan{}, fmt.Errorf("unsupported risk review mode %q", mode)
	}
	git := p.GitBinary
	if git == "" {
		git = "git"
	}
	command := exec.CommandContext(ctx, git, "diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--name-status", "-z", base, head)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		return Plan{}, fmt.Errorf("list changed files for risk planning: %w", err)
	}
	changes, err := parseChangedFileStatuses(output)
	if err != nil {
		return Plan{}, err
	}
	stats := make(map[string]fileStats)
	if len(changes) <= maxDiffStatsFiles {
		statsCommand := exec.CommandContext(ctx, git, "diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--numstat", "-z", base, head)
		statsCommand.Dir = directory
		statsOutput, err := statsCommand.Output()
		if err != nil {
			return Plan{}, fmt.Errorf("read changed file statistics for risk planning: %w", err)
		}
		stats, err = parseChangedFileStats(statsOutput)
		if err != nil {
			return Plan{}, err
		}
	}
	plan := Plan{}
	for _, change := range changes {
		item := Classify(change.Path)
		item.ChangeType, item.PreviousPath = change.ChangeType, change.PreviousPath
		if stat, ok := stats[change.Path]; ok {
			item.Additions, item.Deletions, item.StatsKnown, item.Binary = stat.Additions, stat.Deletions, true, stat.Binary
		}
		if include(mode, item.Score) {
			plan.Selected = append(plan.Selected, item)
			continue
		}
		plan.Deferred = append(plan.Deferred, item)
		plan.Exclude = append(plan.Exclude, change.Path)
	}
	sort.Slice(plan.Selected, func(i, j int) bool {
		if plan.Selected[i].Score == plan.Selected[j].Score {
			return plan.Selected[i].Path < plan.Selected[j].Path
		}
		return plan.Selected[i].Score > plan.Selected[j].Score
	})
	sort.Slice(plan.Deferred, func(i, j int) bool {
		if plan.Deferred[i].Score == plan.Deferred[j].Score {
			return plan.Deferred[i].Path < plan.Deferred[j].Path
		}
		return plan.Deferred[i].Score > plan.Deferred[j].Score
	})
	if mode == ModeCritical && len(plan.Selected) == 0 {
		plan = criticalFallback(plan)
	} else if mode == ModeCritical {
		plan = limitSignalDiverseSelection(plan, criticalSelectionLimit, criticalFallbackScore, "critical-mode budget: deferred after the highest-signal file")
	} else if mode == ModeFocused {
		plan = limitSignalDiverseSelection(plan, focusedSelectionLimit, 0, "focused-mode budget: deferred after the diverse high-signal files")
	}
	plan.Exclude = plan.Exclude[:0]
	for _, item := range plan.Deferred {
		plan.Exclude = append(plan.Exclude, item.Path)
	}
	plan.StaticImpactSignals = StaticImpactSignals(plan.Selected)
	return plan, nil
}

type changedFile struct {
	Path, ChangeType, PreviousPath string
}

type fileStats struct {
	Additions, Deletions int
	Binary               bool
}

func parseChangedFileStatuses(output []byte) ([]changedFile, error) {
	if len(output) == 0 {
		return nil, nil
	}
	if output[len(output)-1] != 0 {
		return nil, fmt.Errorf("changed-file status output is truncated")
	}
	parts := bytes.Split(output[:len(output)-1], []byte{0})
	changes := make([]changedFile, 0, len(parts)/2)
	for index := 0; index < len(parts); {
		if index+1 >= len(parts) || len(parts[index]) == 0 {
			return nil, fmt.Errorf("changed-file status output is malformed")
		}
		status := parts[index][0]
		change := changedFile{Path: string(parts[index+1])}
		index += 2
		switch status {
		case 'A':
			change.ChangeType = "added"
		case 'M':
			change.ChangeType = "modified"
		case 'D':
			change.ChangeType = "deleted"
		case 'T':
			change.ChangeType = "type_changed"
		case 'R', 'C':
			if index >= len(parts) {
				return nil, fmt.Errorf("renamed-file status output is malformed")
			}
			change.PreviousPath, change.Path = change.Path, string(parts[index])
			index++
			if status == 'R' {
				change.ChangeType = "renamed"
			} else {
				change.ChangeType = "copied"
			}
		default:
			return nil, fmt.Errorf("unsupported changed-file status %q", status)
		}
		if change.Path == "" || (change.PreviousPath != "" && change.PreviousPath == change.Path) {
			return nil, fmt.Errorf("changed-file path is invalid")
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func parseChangedFileStats(output []byte) (map[string]fileStats, error) {
	stats := make(map[string]fileStats)
	if len(output) == 0 {
		return stats, nil
	}
	if output[len(output)-1] != 0 {
		return nil, fmt.Errorf("changed-file statistics output is truncated")
	}
	parts := bytes.Split(output[:len(output)-1], []byte{0})
	for index := 0; index < len(parts); {
		fields := bytes.SplitN(parts[index], []byte{'\t'}, 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("changed-file statistics output is malformed")
		}
		additions, deletions := 0, 0
		binary := string(fields[0]) == "-" && string(fields[1]) == "-"
		if !binary {
			var err error
			additions, err = strconv.Atoi(string(fields[0]))
			if err != nil || additions < 0 {
				return nil, fmt.Errorf("changed-file additions are invalid")
			}
			deletions, err = strconv.Atoi(string(fields[1]))
			if err != nil || deletions < 0 {
				return nil, fmt.Errorf("changed-file deletions are invalid")
			}
		}
		path := string(fields[2])
		index++
		if path == "" {
			if index+1 >= len(parts) {
				return nil, fmt.Errorf("renamed-file statistics output is malformed")
			}
			path = string(parts[index+1])
			index += 2
		}
		if path == "" {
			return nil, fmt.Errorf("changed-file statistics path is invalid")
		}
		stats[path] = fileStats{Additions: additions, Deletions: deletions, Binary: binary}
	}
	return stats, nil
}

// criticalFallback prevents a high-priority review from becoming an empty
// review when no path crosses the strict threshold. It keeps the scope small
// and explainable: one public or asynchronous workflow boundary
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

func limitSignalDiverseSelection(plan Plan, limit, deferredMinimumScore int, deferralReason string) Plan {
	if limit <= 0 {
		return plan
	}
	candidates := append([]Item{}, plan.Selected...)
	hasEligibleDeferred := false
	for _, item := range plan.Deferred {
		if deferredMinimumScore > 0 && item.Score >= deferredMinimumScore {
			candidates = append(candidates, item)
			hasEligibleDeferred = true
		}
	}
	if len(candidates) <= limit && !hasEligibleDeferred {
		return plan
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Score > candidates[j].Score
	})

	selected := make([]Item, 0, limit)
	selectedPaths := make(map[string]struct{}, limit)
	seenSignals := make(map[string]struct{})
	for _, item := range candidates {
		if len(selected) == limit {
			break
		}
		signals := riskSignals(item)
		addsSignal := false
		for _, signal := range signals {
			if _, seen := seenSignals[signal]; !seen {
				addsSignal = true
				break
			}
		}
		if !addsSignal {
			continue
		}
		selected = append(selected, item)
		selectedPaths[item.Path] = struct{}{}
		for _, signal := range signals {
			seenSignals[signal] = struct{}{}
		}
	}
	for _, item := range candidates {
		if len(selected) == limit {
			break
		}
		if _, exists := selectedPaths[item.Path]; exists {
			continue
		}
		selected = append(selected, item)
		selectedPaths[item.Path] = struct{}{}
	}

	deferred := make([]Item, 0, len(plan.Selected)+len(plan.Deferred)-len(selected))
	allItems := append(append([]Item{}, plan.Selected...), plan.Deferred...)
	for _, item := range allItems {
		if _, chosen := selectedPaths[item.Path]; chosen {
			continue
		}
		for _, candidate := range candidates {
			if candidate.Path == item.Path {
				item.Reasons = append(item.Reasons, deferralReason)
				break
			}
		}
		deferred = append(deferred, item)
	}
	plan.Selected = selected
	plan.Deferred = deferred
	sort.Slice(plan.Deferred, func(i, j int) bool {
		if plan.Deferred[i].Score == plan.Deferred[j].Score {
			return plan.Deferred[i].Path < plan.Deferred[j].Path
		}
		return plan.Deferred[i].Score > plan.Deferred[j].Score
	})
	return plan
}

func riskSignals(item Item) []string {
	signals := make([]string, 0, len(item.Reasons))
	for _, reason := range item.Reasons {
		switch reason {
		case "authentication, authorization, or secret boundary",
			"persistent state or financial side effect",
			"public boundary or asynchronous workflow",
			"dependency or build manifest",
			"deployment or supply-chain boundary":
			signals = append(signals, reason)
		}
	}
	return signals
}

func include(mode Mode, score int) bool {
	switch mode {
	case ModeStandard:
		return score > 0
	case ModeCritical:
		return score >= 75
	default:
		// A directory name is a prioritization hint, not evidence that ordinary
		// application code is safe. Keep it eligible in focused reviews; the
		// bounded selection still puts stronger boundaries first and defers
		// documentation, tests and generated artifacts.
		return score >= 45
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
	if hasPathSegment(normalized, "vendor", "node_modules", "generated", "testdata", "fixtures", "coverage") || strings.HasSuffix(normalized, ".snap") || strings.HasSuffix(normalized, ".lock") {
		return Item{Path: path, Score: 0, Reasons: []string{"generated, vendored, fixture, coverage, or lock artifact"}}
	}
	if strings.HasSuffix(normalized, ".md") || strings.HasPrefix(normalized, "docs/") {
		return Item{Path: path, Score: 10, Reasons: []string{"documentation-only change"}}
	}
	if hasPathSegment(normalized, "auth", "identity", "permission", "rbac", "policy", "session", "oauth", "jwt", "secret") || contains(normalized, "oauth", "jwt", "secret") {
		item.Score += 45
		item.Reasons = append(item.Reasons, "authentication, authorization, or secret boundary")
	}
	if hasPathSegment(normalized, "payment", "billing", "ledger", "migration", "migrations", "schema", "database", "repository", "sql", "cache") {
		item.Score += 35
		item.Reasons = append(item.Reasons, "persistent state or financial side effect")
	}
	if hasPathSegment(normalized, "api", "controller", "handler", "router", "webhook", "queue", "worker", "event") {
		item.Score += 25
		item.Reasons = append(item.Reasons, "public boundary or asynchronous workflow")
	}
	if isDependencyManifest(normalized) {
		item.Score += 35
		item.Reasons = append(item.Reasons, "dependency or build manifest")
	}
	if isContainerOrBuildFile(normalized) {
		containerScore := 35
		if strings.Contains(strings.Trim(strings.TrimPrefix(normalized, "./"), "/"), "/") {
			// A service-local image is the executable runtime boundary for that
			// component, so prefer it over a repository-wide build wrapper when
			// a latency-oriented security pass can inspect only one file.
			containerScore = 40
		}
		item.Score += containerScore
		item.Reasons = append(item.Reasons, "deployment or supply-chain boundary")
	} else if hasPathSegment(normalized, "deploy", "infra", "terraform", "k8s", "docker") || strings.HasPrefix(normalized, ".github/workflows/") {
		item.Score += 25
		item.Reasons = append(item.Reasons, "deployment or supply-chain boundary")
	}
	if contains(normalized, "_test.") || hasPathSegment(normalized, "test", "tests") {
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

func hasPathSegment(value string, segments ...string) bool {
	path := "/" + strings.Trim(strings.ToLower(value), "/") + "/"
	for _, segment := range segments {
		segment = strings.Trim(strings.ToLower(segment), "/")
		if segment != "" && strings.Contains(path, "/"+segment+"/") {
			return true
		}
	}
	return false
}

func isDependencyManifest(path string) bool {
	switch strings.TrimPrefix(path, "./") {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "composer.json", "composer.lock", "pom.xml", "build.gradle", "build.gradle.kts", "cargo.toml", "cargo.lock", "requirements.txt", "poetry.lock", "pyproject.toml":
		return true
	default:
		return false
	}
}

func isContainerOrBuildFile(path string) bool {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "./"), "/"), "/")
	name := parts[len(parts)-1]
	switch name {
	case "dockerfile", "docker-compose.yml", "docker-compose.yaml", ".gitlab-ci.yml", "makefile":
		return true
	default:
		return false
	}
}

// StaticImpactSignals turns the deterministic reasons behind selected paths
// into concise, durable evidence. They describe only path-level signals from
// this exact diff; callers must not present them as a dependency graph or a
// measurement of runtime reachability.
func StaticImpactSignals(items []Item) []string {
	const maxSignals = 6
	signals := make([]string, 0, maxSignals)
	seen := make(map[string]struct{})
	for _, item := range items {
		for _, reason := range item.Reasons {
			reason = strings.TrimSpace(reason)
			if reason == "" || reason == "changed application code" || reason == "test-only path" {
				continue
			}
			if _, exists := seen[reason]; exists {
				continue
			}
			seen[reason] = struct{}{}
			signals = append(signals, reason)
			if len(signals) == maxSignals {
				return signals
			}
		}
	}
	return signals
}
