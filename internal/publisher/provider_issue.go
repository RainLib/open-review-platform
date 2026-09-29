package publisher

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// PublishProviderIssueAcknowledgement makes admission visible in two ways: a
// marker-keyed progress comment and an eyes reaction on the source Issue. The
// reaction is an acknowledgement only; the comment remains the authoritative
// state and is updated in place when analysis completes.
func (p *HTTPPublisher) PublishProviderIssueAcknowledgement(ctx context.Context, job domain.ProviderIssueAnalysisJob, body string) error {
	if err := p.PublishProviderIssueAnalysis(ctx, job, body); err != nil {
		return err
	}
	token, err := p.resolve(ctx, job.ProviderJob())
	if err != nil {
		return err
	}
	switch job.Provider {
	case domain.ProviderGitHub:
		endpoint := fmt.Sprintf("%s/repos/%s/issues/%d/reactions", githubAPIBase(job.APIBaseURL), job.Repository, job.IssueNumber)
		if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"content": "eyes"}, nil); err != nil {
			return fmt.Errorf("acknowledge GitHub Issue with reaction: %w", err)
		}
		return nil
	case domain.ProviderGitLab:
		base := strings.TrimSuffix(job.APIBaseURL, "/")
		if base == "" {
			base = "https://gitlab.com/api/v4"
		}
		endpoint := fmt.Sprintf("%s/projects/%s/issues/%d/award_emoji?name=eyes", base, url.PathEscape(job.Repository), job.IssueNumber)
		if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, nil, nil); err != nil {
			if status, ok := err.(*HTTPStatusError); ok && status.StatusCode == http.StatusConflict {
				return nil
			}
			return fmt.Errorf("acknowledge GitLab Issue with reaction: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported provider %q", job.Provider)
	}
}

// PublishProviderIssueAnalysis upserts one marker-keyed comment. The queued
// acknowledgement and terminal analysis intentionally share this method so a
// retry or Issue edit updates the existing conversation instead of adding
// another bot comment.
func (p *HTTPPublisher) PublishProviderIssueAnalysis(ctx context.Context, job domain.ProviderIssueAnalysisJob, body string) error {
	if strings.TrimSpace(body) == "" || strings.TrimSpace(job.StableMarker) == "" {
		return fmt.Errorf("provider issue analysis body and marker are required")
	}
	token, err := p.resolve(ctx, job.ProviderJob())
	if err != nil {
		return err
	}
	body = strings.TrimSpace(body) + "\n\n<!-- " + job.StableMarker + " -->"
	switch job.Provider {
	case domain.ProviderGitHub:
		return p.upsertGitHubIssueAnalysis(ctx, job, token, body)
	case domain.ProviderGitLab:
		return p.upsertGitLabIssueAnalysis(ctx, job, token, body)
	default:
		return fmt.Errorf("unsupported provider %q", job.Provider)
	}
}

func (p *HTTPPublisher) upsertGitHubIssueAnalysis(ctx context.Context, job domain.ProviderIssueAnalysisJob, token, body string) error {
	base := githubAPIBase(job.APIBaseURL)
	endpoint := fmt.Sprintf("%s/repos/%s/issues/%d/comments", base, job.Repository, job.IssueNumber)
	comments, err := p.githubIssueComments(ctx, endpoint, token)
	if err != nil {
		return fmt.Errorf("list GitHub issue triage comments: %w", err)
	}
	for _, comment := range comments {
		if strings.Contains(comment.Body, job.StableMarker) {
			return p.requestJSON(ctx, http.MethodPatch, fmt.Sprintf("%s/repos/%s/issues/comments/%d", base, job.Repository, comment.ID), token, map[string]string{"body": body}, nil)
		}
	}
	return p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil)
}

func (p *HTTPPublisher) upsertGitLabIssueAnalysis(ctx context.Context, job domain.ProviderIssueAnalysisJob, token, body string) error {
	base := strings.TrimSuffix(job.APIBaseURL, "/")
	if base == "" {
		base = "https://gitlab.com/api/v4"
	}
	project := url.PathEscape(job.Repository)
	endpoint := fmt.Sprintf("%s/projects/%s/issues/%d/notes", base, project, job.IssueNumber)
	notes, err := p.gitLabNotes(ctx, endpoint, token)
	if err != nil {
		return fmt.Errorf("list GitLab issue triage notes: %w", err)
	}
	for _, note := range notes {
		if strings.Contains(note.Body, job.StableMarker) {
			return p.requestJSON(ctx, http.MethodPut, fmt.Sprintf("%s/projects/%s/issues/%d/notes/%d", base, project, job.IssueNumber, note.ID), token, map[string]string{"body": body}, nil)
		}
	}
	return p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil)
}
