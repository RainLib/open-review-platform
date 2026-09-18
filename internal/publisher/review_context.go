package publisher

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// loadReviewContext keeps provider API shapes out of report rendering. Failure
// to load descriptive metadata never changes the code-review result; callers
// render the missing evidence explicitly and continue with the exact SHAs.
func (p *HTTPPublisher) loadReviewContext(ctx context.Context, job domain.ReviewJob, token string) ReviewContext {
	review := ReviewContext{Contract: ParseChangeContract("")}
	var err error
	switch job.Provider {
	case domain.ProviderGitHub:
		review, err = p.loadGitHubReviewContext(ctx, job, token)
	case domain.ProviderGitLab:
		review, err = p.loadGitLabReviewContext(ctx, job, token)
	default:
		err = fmt.Errorf("unsupported provider %q", job.Provider)
	}
	if err != nil {
		review.Contract = normalizedContract(review.Contract)
		review.Warning = "Pull-request scope metadata could not be loaded; commit provenance is still exact."
	}
	return review
}

func (p *HTTPPublisher) loadGitHubReviewContext(ctx context.Context, job domain.ReviewJob, token string) (ReviewContext, error) {
	base := githubAPIBase(job.APIBaseURL)
	var pull struct {
		Title        string `json:"title"`
		Body         string `json:"body"`
		HTMLURL      string `json:"html_url"`
		ChangedFiles int    `json:"changed_files"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
	}
	endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d", base, job.Repository, job.ReviewNumber)
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &pull); err != nil {
		return ReviewContext{}, fmt.Errorf("load GitHub pull request: %w", err)
	}
	var files []struct {
		Filename  string `json:"filename"`
		BlobURL   string `json:"blob_url"`
		Status    string `json:"status"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
		Changes   int    `json:"changes"`
	}
	endpoint = fmt.Sprintf("%s/repos/%s/pulls/%d/files?per_page=100&page=1", base, job.Repository, job.ReviewNumber)
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &files); err != nil {
		return ReviewContext{}, fmt.Errorf("load GitHub pull request files: %w", err)
	}
	review := ReviewContext{
		Title: pull.Title, URL: pull.HTMLURL, TotalFiles: pull.ChangedFiles,
		TotalAdditions: pull.Additions, TotalDeletions: pull.Deletions,
		Truncated: pull.ChangedFiles > len(files), Contract: ParseChangeContract(pull.Body),
	}
	for _, file := range files {
		review.ChangedFiles = append(review.ChangedFiles, ChangedFile{
			Path: file.Filename, URL: file.BlobURL, Status: file.Status, Additions: file.Additions,
			Deletions: file.Deletions, Changes: file.Changes,
		})
	}
	return review, nil
}

func (p *HTTPPublisher) loadGitLabReviewContext(ctx context.Context, job domain.ReviewJob, token string) (ReviewContext, error) {
	base := strings.TrimSuffix(job.APIBaseURL, "/")
	if base == "" {
		base = "https://gitlab.com/api/v4"
	}
	project := url.PathEscape(job.Repository)
	var mergeRequest struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		WebURL       string `json:"web_url"`
		ChangesCount string `json:"changes_count"`
	}
	endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d", base, project, job.ReviewNumber)
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &mergeRequest); err != nil {
		return ReviewContext{}, fmt.Errorf("load GitLab merge request: %w", err)
	}
	var diffs []struct {
		OldPath     string `json:"old_path"`
		NewPath     string `json:"new_path"`
		Diff        string `json:"diff"`
		NewFile     bool   `json:"new_file"`
		RenamedFile bool   `json:"renamed_file"`
		DeletedFile bool   `json:"deleted_file"`
		TooLarge    bool   `json:"too_large"`
	}
	endpoint = fmt.Sprintf("%s/projects/%s/merge_requests/%d/diffs?per_page=100&page=1", base, project, job.ReviewNumber)
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &diffs); err != nil {
		return ReviewContext{}, fmt.Errorf("load GitLab merge request diffs: %w", err)
	}
	review := ReviewContext{Title: mergeRequest.Title, URL: mergeRequest.WebURL, TotalFiles: len(diffs), Contract: ParseChangeContract(mergeRequest.Description)}
	for _, diff := range diffs {
		status := "modified"
		switch {
		case diff.NewFile:
			status = "added"
		case diff.DeletedFile:
			status = "removed"
		case diff.RenamedFile:
			status = "renamed"
		}
		path := diff.NewPath
		if path == "" {
			path = diff.OldPath
		}
		additions, deletions := countUnifiedDiff(diff.Diff)
		review.TotalAdditions += additions
		review.TotalDeletions += deletions
		review.ChangedFiles = append(review.ChangedFiles, ChangedFile{
			Path: path, URL: strings.TrimSuffix(mergeRequest.WebURL, "/") + "/diffs", Status: status, Additions: additions, Deletions: deletions,
			Changes: additions + deletions,
		})
		if diff.TooLarge {
			review.Truncated = true
		}
	}
	if mergeRequest.ChangesCount != "" {
		var total int
		if _, err := fmt.Sscanf(mergeRequest.ChangesCount, "%d", &total); err == nil && total > 0 {
			review.TotalFiles = total
			review.Truncated = review.Truncated || total > len(diffs)
		}
	}
	return review, nil
}

func countUnifiedDiff(diff string) (additions, deletions int) {
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}
