package publisher

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// AnalysisCheckName is deliberately stable. GitHub groups checks by name and
// head SHA, which lets retries update one visible review status instead of
// leaving a trail of duplicate checks on the pull request.
const AnalysisCheckName = "Open Review / Analysis"

type CheckConclusion string

const (
	CheckSuccess CheckConclusion = "success"
	CheckFailure CheckConclusion = "failure"
	CheckNeutral CheckConclusion = "neutral"
)

// CheckReporter publishes execution health only. Governance policy is a
// separate future check so a transient LLM/provider outage cannot silently
// become a merge blocker merely because this check is marked required.
type CheckReporter interface {
	StartCheck(context.Context, domain.ReviewJob) error
	CompleteCheck(context.Context, domain.ReviewJob, CheckConclusion, string) error
}

func (p *HTTPPublisher) StartCheck(ctx context.Context, job domain.ReviewJob) error {
	if job.Provider != domain.ProviderGitHub {
		return nil
	}
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	base := githubBase(job.APIBaseURL)
	check, err := p.findGitHubCheck(ctx, base, job, token)
	if err != nil {
		return err
	}
	if check != nil && check.Status != "completed" {
		return p.requestJSON(ctx, http.MethodPatch, fmt.Sprintf("%s/repos/%s/check-runs/%d", base, job.Repository, check.ID), token, map[string]any{
			"status": "in_progress",
			"output": map[string]string{"title": "Open Review in progress", "summary": "Preparing the isolated review workspace and AI analysis."},
		}, nil)
	}
	return p.requestJSON(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/check-runs", base, job.Repository), token, map[string]any{
		"name":       AnalysisCheckName,
		"head_sha":   job.HeadSHA,
		"status":     "in_progress",
		"started_at": job.StartedAt,
		"output": map[string]string{
			"title":   "Open Review in progress",
			"summary": "Preparing the isolated review workspace and AI analysis.",
		},
	}, nil)
}

func (p *HTTPPublisher) CompleteCheck(ctx context.Context, job domain.ReviewJob, conclusion CheckConclusion, summary string) error {
	if job.Provider != domain.ProviderGitHub {
		return nil
	}
	if conclusion != CheckSuccess && conclusion != CheckFailure && conclusion != CheckNeutral {
		return fmt.Errorf("unsupported GitHub check conclusion %q", conclusion)
	}
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	base := githubBase(job.APIBaseURL)
	check, err := p.findGitHubCheck(ctx, base, job, token)
	if err != nil {
		return err
	}
	if check == nil || check.Status == "completed" {
		return nil
	}
	title := "Open Review complete"
	if conclusion == CheckFailure {
		title = "Open Review failed"
	}
	if conclusion == CheckNeutral {
		title = "Open Review completed with warnings"
	}
	return p.requestJSON(ctx, http.MethodPatch, fmt.Sprintf("%s/repos/%s/check-runs/%d", base, job.Repository, check.ID), token, map[string]any{
		"status":     "completed",
		"conclusion": string(conclusion),
		"output":     map[string]string{"title": title, "summary": summary},
	}, nil)
}

type githubCheck struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

func (p *HTTPPublisher) findGitHubCheck(ctx context.Context, base string, job domain.ReviewJob, token string) (*githubCheck, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/commits/%s/check-runs?check_name=%s&filter=latest&per_page=1", base, job.Repository, job.HeadSHA, url.QueryEscape(AnalysisCheckName))
	var response struct {
		CheckRuns []githubCheck `json:"check_runs"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &response); err != nil {
		return nil, fmt.Errorf("list GitHub analysis checks: %w", err)
	}
	if len(response.CheckRuns) == 0 {
		return nil, nil
	}
	return &response.CheckRuns[0], nil
}

func (p *HTTPPublisher) resolve(ctx context.Context, job domain.ReviewJob) (string, error) {
	if p.resolver == nil {
		return "", fmt.Errorf("provider credential resolver is required")
	}
	return p.resolver.Resolve(ctx, job)
}

func githubBase(value string) string {
	base := strings.TrimSuffix(value, "/")
	if base == "" {
		return "https://api.github.com"
	}
	return base
}
