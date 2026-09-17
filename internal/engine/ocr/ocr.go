package ocr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type Executor struct {
	Binary    string
	Version   string
	GitBinary string
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
	return e.review(ctx, directory, base, head, "")
}

// ReviewWithRule executes OCR with a runner-owned rule file. The caller passes
// canonical JSON already resolved by the control plane; this method never
// reads a rule file from the untrusted pull-request head.
func (e Executor) ReviewWithRule(ctx context.Context, directory, base, head string, ruleFileJSON []byte) ([]domain.Finding, error) {
	if !json.Valid(ruleFileJSON) {
		return nil, fmt.Errorf("OCR rule file is not valid JSON")
	}
	ruleFile, err := os.CreateTemp(directory, ".open-review-platform-rule-*.json")
	if err != nil {
		return nil, fmt.Errorf("create trusted OCR rule file: %w", err)
	}
	rulePath := ruleFile.Name()
	defer os.Remove(rulePath)
	if err := ruleFile.Chmod(0o600); err != nil {
		_ = ruleFile.Close()
		return nil, fmt.Errorf("secure trusted OCR rule file: %w", err)
	}
	if _, err := ruleFile.Write(ruleFileJSON); err != nil {
		_ = ruleFile.Close()
		return nil, fmt.Errorf("write trusted OCR rule file: %w", err)
	}
	if err := ruleFile.Close(); err != nil {
		return nil, fmt.Errorf("close trusted OCR rule file: %w", err)
	}
	return e.review(ctx, directory, base, head, rulePath)
}

func (e Executor) review(ctx context.Context, directory, base, head, rulePath string) ([]domain.Finding, error) {
	output := filepath.Join(directory, "open-review-result.json")
	arguments := []string{"review", "--from", base, "--to", head, "--format", "json", "--output", output}
	if rulePath != "" {
		arguments = append(arguments, "--rule", rulePath)
	}
	command := exec.CommandContext(ctx, e.Binary, arguments...)
	command.Dir = directory
	command.Env = withGitBinaryPath(os.Environ(), e.gitBinary())
	logs, err := command.CombinedOutput()
	if err != nil {
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
		return string(value[:maxBytes]) + "…"
	}
	return string(value)
}
