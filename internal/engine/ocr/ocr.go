package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type Executor struct {
	Binary      string
	Version     string
	GitBinary   string
	Concurrency int
	Effort      string
	MaxTokens   int
	TokenBudget int
	// SubtaskTimeout is passed to OCR in minutes and also caps the platform's
	// child process. A CLI timeout that does not terminate its wrapper must not
	// leave an in-flight review consuming model capacity beyond this budget.
	SubtaskTimeout int
	Timeout        time.Duration
}

var gitVersionPattern = regexp.MustCompile(`git version (\d+)\.(\d+)`)

func (e Executor) VerifyVersion(ctx context.Context) error {
	output, err := exec.CommandContext(ctx, e.Binary, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("execute OCR version check: %w: %s", err, trimmedOutput(output))
	}
	if !strings.Contains(string(output), e.Version) {
		return fmt.Errorf("OCR version must contain %q, got %q", e.Version, strings.TrimSpace(string(output)))
	}
	return nil
}

// VerifyGitVersion fails fast because OCR's range-review mode requires Git
// 2.41 or later. Older versions can report a false missing merge-base for a
// valid PR range, which otherwise looks like a transient review failure.
func (e Executor) VerifyGitVersion(ctx context.Context) error {
	gitBinary := e.gitBinary()
	output, err := exec.CommandContext(ctx, gitBinary, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("execute Git version check (%s): %w: %s", gitBinary, err, trimmedOutput(output))
	}
	match := gitVersionPattern.FindStringSubmatch(string(output))
	if len(match) != 3 {
		return fmt.Errorf("parse Git version for OCR: %q", strings.TrimSpace(string(output)))
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	if major < 2 || (major == 2 && minor < 41) {
		return fmt.Errorf("OCR range reviews require Git 2.41 or newer, got %q; set GIT_BINARY to a supported binary", strings.TrimSpace(string(output)))
	}
	return nil
}

func (e Executor) Review(ctx context.Context, directory, base, head string) ([]domain.Finding, error) {
	return e.review(ctx, directory, base, head, "", nil)
}

// ReviewWithExclude keeps the platform's risk planner outside the CLI while
// using OCR's native gitignore-style exclusion support.
func (e Executor) ReviewWithExclude(ctx context.Context, directory, base, head string, exclude []string) ([]domain.Finding, error) {
	return e.review(ctx, directory, base, head, "", exclude)
}

// ReviewWithSelectedPaths creates an ephemeral, base-rooted commit containing
// only the selected diff paths. OCR therefore sees an exact review range
// instead of receiving a long list of deferred paths that it must parse and
// skip itself.
func (e Executor) ReviewWithSelectedPaths(ctx context.Context, directory, base, head string, selected []string) ([]domain.Finding, error) {
	return e.reviewSelected(ctx, directory, base, head, "", selected)
}

// ReviewWithRule executes OCR with a runner-owned rule file. The caller passes
// canonical JSON already resolved by the control plane; this method never
// reads a rule file from the untrusted pull-request head.
func (e Executor) ReviewWithRule(ctx context.Context, directory, base, head string, ruleFileJSON []byte) ([]domain.Finding, error) {
	return e.ReviewWithRuleAndExclude(ctx, directory, base, head, ruleFileJSON, nil)
}

// ReviewWithRuleAndExclude combines immutable enterprise rules with a
// run-local risk scope. The dynamic paths are derived only from the checked-out
// diff and are never read from the untrusted PR head as configuration.
func (e Executor) ReviewWithRuleAndExclude(ctx context.Context, directory, base, head string, ruleFileJSON []byte, exclude []string) ([]domain.Finding, error) {
	return e.reviewWithRule(ctx, directory, base, head, ruleFileJSON, exclude, nil)
}

// ReviewWithRuleAndSelectedPaths applies immutable rules to the same exact
// synthetic range used by ReviewWithSelectedPaths.
func (e Executor) ReviewWithRuleAndSelectedPaths(ctx context.Context, directory, base, head string, ruleFileJSON []byte, selected []string) ([]domain.Finding, error) {
	return e.reviewWithRule(ctx, directory, base, head, ruleFileJSON, nil, selected)
}

func (e Executor) reviewWithRule(ctx context.Context, directory, base, head string, ruleFileJSON []byte, exclude, selected []string) ([]domain.Finding, error) {
	if !json.Valid(ruleFileJSON) {
		return nil, fmt.Errorf("OCR rule file is not valid JSON")
	}
	rulePath, cleanup, err := e.trustedRuleFile(directory, ruleFileJSON)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if len(selected) > 0 {
		return e.reviewSelected(ctx, directory, base, head, rulePath, selected)
	}
	return e.review(ctx, directory, base, head, rulePath, exclude)
}

func (e Executor) trustedRuleFile(directory string, ruleFileJSON []byte) (string, func(), error) {
	ruleFile, err := os.CreateTemp(directory, ".open-review-platform-rule-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("create trusted OCR rule file: %w", err)
	}
	rulePath := ruleFile.Name()
	if err := ruleFile.Chmod(0o600); err != nil {
		_ = ruleFile.Close()
		_ = os.Remove(rulePath)
		return "", nil, fmt.Errorf("secure trusted OCR rule file: %w", err)
	}
	if _, err := ruleFile.Write(ruleFileJSON); err != nil {
		_ = ruleFile.Close()
		_ = os.Remove(rulePath)
		return "", nil, fmt.Errorf("write trusted OCR rule file: %w", err)
	}
	if err := ruleFile.Close(); err != nil {
		_ = os.Remove(rulePath)
		return "", nil, fmt.Errorf("close trusted OCR rule file: %w", err)
	}
	return rulePath, func() { _ = os.Remove(rulePath) }, nil
}

func (e Executor) review(ctx context.Context, directory, base, head, rulePath string, exclude []string) ([]domain.Finding, error) {
	timeout := e.executionTimeout()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	output := filepath.Join(directory, "open-review-result.json")
	arguments := e.reviewArguments(base, head, output, rulePath, exclude)
	command := exec.CommandContext(ctx, e.Binary, arguments...)
	command.Dir = directory
	command.Env = withGitBinaryPath(os.Environ(), e.gitBinary())
	configureProcessGroup(command)
	logs, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w after %s", domain.ErrReviewTimedOut, timeout)
		}
		if contextExhausted(logs) {
			return nil, fmt.Errorf("%w: reduce the selected scope or increase the model context", domain.ErrReviewContextExhausted)
		}
		return nil, fmt.Errorf("execute OCR review: %w: %s", err, trimmedOutput(logs))
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		return nil, fmt.Errorf("read OCR result: %w", err)
	}
	findings, err := ParseFindings(raw)
	if err != nil {
		return nil, fmt.Errorf("parse OCR result: %w", err)
	}
	return findings, nil
}

func (e Executor) executionTimeout() time.Duration {
	timeout := e.Timeout
	if e.SubtaskTimeout <= 0 {
		return timeout
	}
	subtaskTimeout := time.Duration(e.SubtaskTimeout) * time.Minute
	if timeout <= 0 || subtaskTimeout < timeout {
		return subtaskTimeout
	}
	return timeout
}

func (e Executor) reviewSelected(ctx context.Context, directory, base, head, rulePath string, selected []string) ([]domain.Finding, error) {
	scopedHead, cleanup, err := e.scopedHead(ctx, directory, base, head, selected)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return e.review(ctx, directory, base, scopedHead, rulePath, nil)
}

// scopedHead keeps the original checkout read-only. It creates an unreferenced
// temporary commit in an attached temporary worktree, so OCR's normal
// --from/--to range machinery still works while the diff contains only paths
// admitted by the risk planner.
func (e Executor) scopedHead(ctx context.Context, directory, base, head string, selected []string) (string, func(), error) {
	if len(selected) == 0 {
		return "", nil, fmt.Errorf("selected review paths are required")
	}
	diffArguments := append([]string{"diff", "--binary", "--no-ext-diff", base, head, "--"}, selected...)
	patch, err := e.gitOutput(ctx, directory, diffArguments...)
	if err != nil {
		return "", nil, fmt.Errorf("build selected review patch: %w", err)
	}
	if len(patch) == 0 {
		return "", nil, fmt.Errorf("selected review paths contain no diff")
	}
	parent, err := os.MkdirTemp("", "open-review-scope-")
	if err != nil {
		return "", nil, fmt.Errorf("create selected review workspace: %w", err)
	}
	scopedDirectory := filepath.Join(parent, "worktree")
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.gitRun(cleanupCtx, directory, nil, "worktree", "remove", "--force", scopedDirectory)
		_ = os.RemoveAll(parent)
	}
	if err := e.gitRun(ctx, directory, nil, "worktree", "add", "--detach", scopedDirectory, base); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create selected review worktree: %w", err)
	}
	if err := e.gitRun(ctx, scopedDirectory, patch, "apply", "--index", "--binary", "-"); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("apply selected review patch: %w", err)
	}
	tree, err := e.gitOutput(ctx, scopedDirectory, "write-tree")
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write selected review tree: %w", err)
	}
	commit, err := e.gitOutput(ctx, scopedDirectory, "-c", "user.name=Open Review", "-c", "user.email=open-review@local.invalid", "commit-tree", strings.TrimSpace(string(tree)), "-p", base)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("commit selected review tree: %w", err)
	}
	scopedHead := strings.TrimSpace(string(commit))
	if scopedHead == "" {
		cleanup()
		return "", nil, fmt.Errorf("commit selected review tree returned no revision")
	}
	return scopedHead, cleanup, nil
}

func (e Executor) gitOutput(ctx context.Context, directory string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, e.gitBinary(), append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, trimmedOutput(output))
	}
	return output, nil
}

func (e Executor) gitRun(ctx context.Context, directory string, input []byte, arguments ...string) error {
	command := exec.CommandContext(ctx, e.gitBinary(), append([]string{"-C", directory}, arguments...)...)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, trimmedOutput(output))
	}
	return nil
}

func contextExhausted(logs []byte) bool {
	message := strings.ToLower(string(logs))
	return strings.Contains(message, "context compression exceeded") ||
		strings.Contains(message, "context length exceeded") ||
		strings.Contains(message, "maximum context length")
}

func (e Executor) reviewArguments(base, head, output, rulePath string, exclude []string) []string {
	arguments := []string{"review", "--from", base, "--to", head, "--format", "json", "--output", output}
	if e.Concurrency > 0 {
		arguments = append(arguments, "--concurrency", strconv.Itoa(e.Concurrency))
	}
	if e.Effort != "" {
		arguments = append(arguments, "--effort", e.Effort)
	}
	if e.MaxTokens > 0 {
		arguments = append(arguments, "--max-tokens", strconv.Itoa(e.MaxTokens))
	}
	if e.TokenBudget > 0 {
		arguments = append(arguments, "--max-tokens-budget", strconv.Itoa(e.TokenBudget))
	}
	if e.SubtaskTimeout > 0 {
		arguments = append(arguments, "--timeout", strconv.Itoa(e.SubtaskTimeout))
	}
	if rulePath != "" {
		arguments = append(arguments, "--rule", rulePath)
	}
	if len(exclude) > 0 {
		arguments = append(arguments, "--exclude", exactExcludePatterns(exclude))
	}
	return arguments
}

// exactExcludePatterns converts changed-file paths into literal gitignore
// patterns. OCR accepts gitignore-style exclusions, where a Next.js path such
// as app/[org]/page.tsx would otherwise interpret [org] as a character class
// and scan a file that the risk planner explicitly deferred.
func exactExcludePatterns(paths []string) string {
	patterns := make([]string, 0, len(paths))
	for _, path := range paths {
		patterns = append(patterns, exactExcludePattern(path))
	}
	return strings.Join(patterns, ",")
}

func exactExcludePattern(path string) string {
	var builder strings.Builder
	for index, character := range path {
		if character == '\\' || character == '*' || character == '?' || character == '[' || character == ']' || (index == 0 && (character == '!' || character == '#')) {
			builder.WriteByte('\\')
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

// configureProcessGroup prevents a wrapper CLI from leaving its native OCR
// child running after a review is cancelled. CommandContext otherwise stops
// only the direct process, while the child can retain its stdout pipe and keep
// a worker blocked indefinitely.
func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return err
		}
		return nil
	}
	command.WaitDelay = 5 * time.Second
}

func (e Executor) gitBinary() string {
	if e.GitBinary != "" {
		return e.GitBinary
	}
	return "git"
}

func withGitBinaryPath(environment []string, gitBinary string) []string {
	if directory := filepath.Dir(gitBinary); directory != "." {
		for index, entry := range environment {
			if strings.HasPrefix(entry, "PATH=") {
				copy := append([]string(nil), environment...)
				copy[index] = "PATH=" + directory + string(os.PathListSeparator) + strings.TrimPrefix(entry, "PATH=")
				return copy
			}
		}
		return append(append([]string(nil), environment...), "PATH="+directory)
	}
	return environment
}

// ParseFindings accepts the current OpenCodeReview JSON envelope and a raw
// comment array. Keeping the parser at this boundary makes CLI upgrades an
// explicit, independently testable compatibility decision.
func ParseFindings(raw []byte) ([]domain.Finding, error) {
	var envelope struct {
		Comments []comment `json:"comments"`
		Findings []comment `json:"findings"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && (envelope.Comments != nil || envelope.Findings != nil) {
		if envelope.Comments != nil {
			return normalize(envelope.Comments), nil
		}
		return normalize(envelope.Findings), nil
	}
	var comments []comment
	if err := json.Unmarshal(raw, &comments); err != nil {
		return nil, err
	}
	return normalize(comments), nil
}

type comment struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	Body           string `json:"body"`
	SuggestionCode string `json:"suggestion_code"`
	Suggestion     string `json:"suggestion"`
	StartLine      int    `json:"start_line"`
	EndLine        int    `json:"end_line"`
	Severity       string `json:"severity"`
	Category       string `json:"category"`
}

func normalize(comments []comment) []domain.Finding {
	findings := make([]domain.Finding, 0, len(comments))
	for _, item := range comments {
		body := strings.TrimSpace(item.Content)
		if body == "" {
			body = strings.TrimSpace(item.Body)
		}
		if body == "" {
			continue
		}
		suggestion := strings.TrimSpace(item.SuggestionCode)
		if suggestion == "" {
			suggestion = strings.TrimSpace(item.Suggestion)
		}
		findings = append(findings, domain.Finding{
			Path:       cleanPath(item.Path),
			StartLine:  max(item.StartLine, 0),
			EndLine:    max(item.EndLine, 0),
			Severity:   severity(item.Severity),
			Category:   category(item.Category),
			Body:       body,
			Suggestion: suggestion,
		})
	}
	return findings
}

func cleanPath(path string) string {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if path == "" || cleaned == "." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return ""
	}
	return cleaned
}

func severity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical", "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "medium"
	}
}

func category(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "bug", "security", "performance", "maintainability", "test", "style", "documentation", "other":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func max(value, floor int) int {
	if value < floor {
		return floor
	}
	return value
}

func trimmedOutput(value []byte) string {
	const maxBytes = 4096
	if len(value) > maxBytes {
		// OCR emits one line per deferred path before reporting a provider or
		// parsing failure. Keeping only the prefix hides the actionable final
		// error and turns retry diagnostics into noise. Preserve both ends while
		// retaining the same bounded error payload.
		head := maxBytes / 2
		tail := maxBytes - head
		return string(value[:head]) + "\n… output truncated …\n" + string(value[len(value)-tail:])
	}
	return string(value)
}
