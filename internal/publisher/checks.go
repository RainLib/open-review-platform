package publisher

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// AnalysisCheckName is deliberately stable for branch protection. Individual
// review attempts use distinct external IDs, including re-reviews of one SHA.
const AnalysisCheckName = "Open Review / Analysis"

type CheckConclusion string

const (
	CheckSuccess CheckConclusion = "success"
	CheckFailure CheckConclusion = "failure"
	CheckNeutral CheckConclusion = "neutral"
)

// CheckReporter owns one provider status per review attempt. Its
// conclusion reflects the configured merge-gate verdict after durable review
// completion; provider-side branch or pipeline merge policy decides whether
// that status is required for a merge.
type CheckReporter interface {
	StartCheck(context.Context, domain.ReviewJob) error
	CompleteCheck(context.Context, domain.ReviewJob, CheckConclusion, string) error
}

// ReceiptCheckReporter adds durable evidence to the provider status.
// The receipt intentionally contains no provider response body: a check or
// commit-status response can be untrusted and is not needed to retry its
// stable name against the same revision.
type ReceiptCheckReporter interface {
	CheckReporter
	StartCheckWithReceipt(context.Context, domain.ReviewJob) (domain.PublicationReceipt, error)
	CompleteCheckWithReceipt(context.Context, domain.ReviewJob, CheckConclusion, string) (domain.PublicationReceipt, error)
}

func (p *HTTPPublisher) StartCheck(ctx context.Context, job domain.ReviewJob) error {
	_, err := p.startCheck(ctx, job)
	return err
}

func (p *HTTPPublisher) startCheck(ctx context.Context, job domain.ReviewJob) (string, error) {
	token, err := p.resolve(ctx, job)
	if err != nil {
		return "", err
	}
	if job.Provider == domain.ProviderGitLab {
		return "", p.gitLabCommitStatus(ctx, job, token, "running", "Preparing the isolated review workspace and AI analysis.")
	}
	if job.Provider != domain.ProviderGitHub {
		return "", fmt.Errorf("unsupported provider %q", job.Provider)
	}
	base := githubBase(job.APIBaseURL)
	reviewURL, _ := p.consoleLinksForJob(ctx, job)
	check, err := p.findGitHubCheck(ctx, base, job, token)
	if err != nil {
		return "", err
	}
	if check != nil && check.Status == "completed" {
		// A duplicate start for this exact job must not reopen its result.
		return githubCheckExternalID(check), nil
	}
	if check != nil {
		payload := map[string]any{
			"status": "in_progress",
			"output": map[string]string{"title": "Open Review in progress", "summary": "Preparing the isolated review workspace and AI analysis."},
		}
		if reviewURL != "" {
			payload["details_url"] = reviewURL
		}
		err := p.requestJSON(ctx, http.MethodPatch, fmt.Sprintf("%s/repos/%s/check-runs/%d", base, job.Repository, check.ID), token, payload, nil)
		return githubCheckExternalID(check), err
	}
	created := githubCheck{}
	payload := map[string]any{
		"name":        AnalysisCheckName,
		"head_sha":    job.HeadSHA,
		"external_id": analysisCheckMarker(job),
		"status":      "in_progress",
		"started_at":  job.StartedAt,
		"output": map[string]string{
			"title":   "Open Review in progress",
			"summary": "Preparing the isolated review workspace and AI analysis.",
		},
	}
	if reviewURL != "" {
		payload["details_url"] = reviewURL
	}
	if err := p.requestJSON(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/check-runs", base, job.Repository), token, payload, &created); err != nil {
		return "", err
	}
	if created.ID <= 0 {
		return "", fmt.Errorf("GitHub accepted analysis check creation without a reusable check id")
	}
	return githubCheckExternalID(&created), nil
}

func (p *HTTPPublisher) StartCheckWithReceipt(ctx context.Context, job domain.ReviewJob) (domain.PublicationReceipt, error) {
	externalID, err := p.startCheck(ctx, job)
	if err != nil {
		return domain.PublicationReceipt{}, err
	}
	return analysisCheckReceipt(job, "in_progress", "", "Preparing the isolated review workspace and AI analysis.", externalID), nil
}

func (p *HTTPPublisher) CompleteCheck(ctx context.Context, job domain.ReviewJob, conclusion CheckConclusion, summary string) error {
	_, err := p.completeCheck(ctx, job, conclusion, summary)
	return err
}

func (p *HTTPPublisher) completeCheck(ctx context.Context, job domain.ReviewJob, conclusion CheckConclusion, summary string) (string, error) {
	if conclusion != CheckSuccess && conclusion != CheckFailure && conclusion != CheckNeutral {
		return "", fmt.Errorf("unsupported analysis check conclusion %q", conclusion)
	}
	token, err := p.resolve(ctx, job)
	if err != nil {
		return "", err
	}
	if job.Provider == domain.ProviderGitLab {
		state := "success"
		switch conclusion {
		case CheckFailure:
			state = "failed"
		case CheckNeutral:
			state = "skipped"
		}
		return "", p.gitLabCommitStatus(ctx, job, token, state, summary)
	}
	if job.Provider != domain.ProviderGitHub {
		return "", fmt.Errorf("unsupported provider %q", job.Provider)
	}
	base := githubBase(job.APIBaseURL)
	reviewURL, _ := p.consoleLinksForJob(ctx, job)
	check, err := p.findGitHubCheck(ctx, base, job, token)
	if err != nil {
		return "", err
	}
	title := "Open Review complete"
	if conclusion == CheckFailure {
		title = "Open Review failed"
	}
	if conclusion == CheckNeutral {
		title = "Open Review completed with warnings"
	}
	if check == nil {
		// Starting the check is deliberately best-effort so a temporary provider
		// failure cannot stop the review. The terminal status is still required
		// for a configured merge gate, therefore create its stable check when the
		// earlier start never reached GitHub.
		created := githubCheck{}
		payload := map[string]any{
			"name":        AnalysisCheckName,
			"head_sha":    job.HeadSHA,
			"external_id": analysisCheckMarker(job),
			"status":      "completed",
			"conclusion":  string(conclusion),
			"output":      map[string]string{"title": title, "summary": summary},
		}
		if reviewURL != "" {
			payload["details_url"] = reviewURL
		}
		if err := p.requestJSON(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/check-runs", base, job.Repository), token, payload, &created); err != nil {
			return "", err
		}
		if created.ID <= 0 {
			return "", fmt.Errorf("GitHub accepted terminal analysis check creation without a reusable check id")
		}
		return githubCheckExternalID(&created), nil
	}
	// Only this job's Check Run is updated. A delayed terminal retry must never
	// overwrite a newer review of the same commit.
	payload := map[string]any{
		"status":     "completed",
		"conclusion": string(conclusion),
		"output":     map[string]string{"title": title, "summary": summary},
	}
	if reviewURL != "" {
		payload["details_url"] = reviewURL
	}
	err = p.requestJSON(ctx, http.MethodPatch, fmt.Sprintf("%s/repos/%s/check-runs/%d", base, job.Repository, check.ID), token, payload, nil)
	return githubCheckExternalID(check), err
}

func (p *HTTPPublisher) CompleteCheckWithReceipt(ctx context.Context, job domain.ReviewJob, conclusion CheckConclusion, summary string) (domain.PublicationReceipt, error) {
	externalID, err := p.completeCheck(ctx, job, conclusion, summary)
	if err != nil {
		return domain.PublicationReceipt{}, err
	}
	return analysisCheckReceipt(job, "completed", conclusion, summary, externalID), nil
}

func analysisCheckReceipt(job domain.ReviewJob, state string, conclusion CheckConclusion, summary, externalID string) domain.PublicationReceipt {
	marker := analysisCheckMarker(job)
	payload := strings.Join([]string{job.HeadSHA, marker, state, string(conclusion), gitLabStatusDescription(summary)}, "\n")
	digest := sha256.Sum256([]byte(payload))
	return domain.PublicationReceipt{
		ReceiptKind:  "status",
		StableMarker: marker,
		ExternalID:   externalID,
		PayloadHash:  fmt.Sprintf("%x", digest[:]),
		Published:    true,
	}
}

func analysisCheckMarker(job domain.ReviewJob) string {
	return "open-review-platform:analysis-check:" + job.ID.String()
}

// gitLabCommitStatus uses the stable status name consumed by GitLab's merge
// checks. GitLab resolves repeated publications for the same SHA/name as the
// current external status, so retrying a worker does not create a new policy
// identity. The description intentionally carries only the bounded result,
// while the PR/MR comment holds detailed evidence.
func (p *HTTPPublisher) gitLabCommitStatus(ctx context.Context, job domain.ReviewJob, token, state, summary string) error {
	base := strings.TrimSuffix(job.APIBaseURL, "/")
	if base == "" {
		base = "https://gitlab.com/api/v4"
	}
	project := url.PathEscape(job.Repository)
	endpoint := fmt.Sprintf("%s/projects/%s/statuses/%s", base, project, url.PathEscape(job.HeadSHA))
	payload := map[string]string{
		"state":       state,
		"name":        AnalysisCheckName,
		"description": gitLabStatusDescription(summary),
	}
	// GitLab uses commit SHA and ref when selecting a pipeline. Retain the
	// admitted source branch so a pipeline for a different ref is not selected.
	// Duplicate pipelines on the same SHA/ref still require provider-side
	// workflow configuration or an explicit pipeline ID.
	if job.HeadRef != "" {
		payload["ref"] = job.HeadRef
	}
	// GitLab renders target_url as the direct destination from the merge
	// widget. Keep it deployment-owned and derived from the immutable run,
	// matching the evidence-report link without accepting provider input.
	if reviewURL, _ := p.consoleLinksForJob(ctx, job); reviewURL != "" {
		payload["target_url"] = reviewURL
	}
	if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, payload, nil); err != nil {
		return fmt.Errorf("publish GitLab analysis status: %w", err)
	}
	return nil
}

func gitLabStatusDescription(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	const limit = 255
	characters := []rune(value)
	if len(characters) <= limit {
		return value
	}
	return string(characters[:limit-1]) + "…"
}

type githubCheck struct {
	ID         int64  `json:"id"`
	Status     string `json:"status"`
	ExternalID string `json:"external_id"`
}

func githubCheckExternalID(check *githubCheck) string {
	if check == nil || check.ID <= 0 {
		return ""
	}
	return strconv.FormatInt(check.ID, 10)
}

func (p *HTTPPublisher) findGitHubCheck(ctx context.Context, base string, job domain.ReviewJob, token string) (*githubCheck, error) {
	// GitHub's default latest filter collapses runs by name. Fetch all so a
	// retry can find this job even after another attempt on the same SHA.
	// GitHub limits this ref endpoint to the newest 1000 check suites.
	for page := 1; page <= 10; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/commits/%s/check-runs?check_name=%s&filter=all&per_page=100&page=%d", base, job.Repository, job.HeadSHA, url.QueryEscape(AnalysisCheckName), page)
		var response struct {
			CheckRuns []githubCheck `json:"check_runs"`
		}
		if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &response); err != nil {
			return nil, fmt.Errorf("list GitHub analysis checks: %w", err)
		}
		for i := range response.CheckRuns {
			if response.CheckRuns[i].ExternalID == analysisCheckMarker(job) {
				return &response.CheckRuns[i], nil
			}
		}
		if len(response.CheckRuns) < 100 {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("GitHub analysis check lookup exceeded the latest 1000 runs for %s at %s", job.Repository, job.HeadSHA)
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
