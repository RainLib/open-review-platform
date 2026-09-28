package publisher

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// ExternalIssuePublisher performs the provider-side effect behind the durable
// external_issue_receipts ledger. The caller persists the result; this type
// never treats a successful HTTP request as durable completion by itself.
type ExternalIssuePublisher interface {
	PublishExternalIssue(context.Context, domain.ExternalIssuePublication) (ExternalIssueResult, error)
	CloseExternalIssue(context.Context, domain.ExternalIssuePublication) error
}

type ExternalIssueResult struct {
	ExternalID  string
	ExternalURL string
}

type providerExternalIssue struct {
	ID          int64  `json:"id"`
	Number      int64  `json:"number"`
	IID         int64  `json:"iid"`
	HTMLURL     string `json:"html_url"`
	WebURL      string `json:"web_url"`
	Body        string `json:"body"`
	Description string `json:"description"`
}

func (p *HTTPPublisher) PublishExternalIssue(ctx context.Context, publication domain.ExternalIssuePublication) (ExternalIssueResult, error) {
	if publication.ID == [16]byte{} || publication.IssueID == [16]byte{} || !publication.Provider.Valid() ||
		strings.TrimSpace(publication.Repository) == "" || strings.TrimSpace(publication.Marker) == "" ||
		strings.TrimSpace(publication.Title) == "" || strings.TrimSpace(publication.Body) == "" {
		return ExternalIssueResult{}, fmt.Errorf("external issue publication is incomplete")
	}
	job := domain.ReviewJob{
		TenantID:               publication.TenantID,
		Provider:               publication.Provider,
		APIBaseURL:             publication.APIBaseURL,
		InstallationExternalID: publication.InstallationExternalID,
		CredentialRef:          publication.CredentialRef,
		Repository:             publication.Repository,
	}
	token, err := p.resolve(ctx, job)
	if err != nil {
		return ExternalIssueResult{}, err
	}
	switch publication.Provider {
	case domain.ProviderGitHub:
		return p.publishGitHubExternalIssue(ctx, publication, token)
	case domain.ProviderGitLab:
		return p.publishGitLabExternalIssue(ctx, publication, token)
	default:
		return ExternalIssueResult{}, fmt.Errorf("unsupported provider %q", publication.Provider)
	}
}

// CloseExternalIssue applies the resolved aggregate state to the exact
// provider Issue recorded in the durable receipt. The provider mutations are
// idempotent: replaying a close for an already-closed ticket is safe.
func (p *HTTPPublisher) CloseExternalIssue(ctx context.Context, publication domain.ExternalIssuePublication) error {
	if publication.ID == [16]byte{} || publication.IssueID == [16]byte{} || !publication.Provider.Valid() ||
		strings.TrimSpace(publication.Repository) == "" || strings.TrimSpace(publication.ExternalID) == "" {
		return fmt.Errorf("external issue closure is incomplete")
	}
	job := domain.ReviewJob{
		TenantID:               publication.TenantID,
		Provider:               publication.Provider,
		APIBaseURL:             publication.APIBaseURL,
		InstallationExternalID: publication.InstallationExternalID,
		CredentialRef:          publication.CredentialRef,
		Repository:             publication.Repository,
	}
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	switch publication.Provider {
	case domain.ProviderGitHub:
		endpoint := fmt.Sprintf("%s/repos/%s/issues/%s", githubAPIBase(publication.APIBaseURL), publication.Repository, url.PathEscape(publication.ExternalID))
		if err := p.requestJSON(ctx, http.MethodPatch, endpoint, token, map[string]any{"state": "closed", "state_reason": "completed"}, nil); err != nil {
			return fmt.Errorf("close GitHub external issue: %w", err)
		}
		return nil
	case domain.ProviderGitLab:
		base := strings.TrimSuffix(strings.TrimSpace(publication.APIBaseURL), "/")
		if base == "" {
			base = "https://gitlab.com/api/v4"
		}
		endpoint := fmt.Sprintf("%s/projects/%s/issues/%s", base, url.PathEscape(publication.Repository), url.PathEscape(publication.ExternalID))
		if err := p.requestJSON(ctx, http.MethodPut, endpoint, token, map[string]any{"state_event": "close"}, nil); err != nil {
			return fmt.Errorf("close GitLab external issue: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported provider %q", publication.Provider)
	}
}

func (p *HTTPPublisher) publishGitHubExternalIssue(ctx context.Context, publication domain.ExternalIssuePublication, token string) (ExternalIssueResult, error) {
	base := githubAPIBase(publication.APIBaseURL)
	endpoint := fmt.Sprintf("%s/repos/%s/issues", base, publication.Repository)
	issues, err := p.githubExternalIssues(ctx, endpoint+"?state=all", token)
	if err != nil {
		return ExternalIssueResult{}, fmt.Errorf("list GitHub external issues: %w", err)
	}
	for _, issue := range issues {
		if strings.Contains(issue.Body, publication.Marker) {
			return externalIssueResult(issue, "github")
		}
	}
	payload := map[string]any{"title": publication.Title, "body": publication.Body}
	if len(publication.Labels) > 0 {
		payload["labels"] = publication.Labels
	}
	if assignee := strings.TrimSpace(publication.AssigneeExternalID); assignee != "" {
		payload["assignees"] = []string{assignee}
	}
	var created providerExternalIssue
	if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, payload, &created); err != nil {
		return ExternalIssueResult{}, fmt.Errorf("create GitHub external issue: %w", err)
	}
	return externalIssueResult(created, "github")
}

func (p *HTTPPublisher) publishGitLabExternalIssue(ctx context.Context, publication domain.ExternalIssuePublication, token string) (ExternalIssueResult, error) {
	base := strings.TrimSuffix(strings.TrimSpace(publication.APIBaseURL), "/")
	if base == "" {
		base = "https://gitlab.com/api/v4"
	}
	endpoint := fmt.Sprintf("%s/projects/%s/issues", base, url.PathEscape(publication.Repository))
	issues, err := p.gitLabExternalIssues(ctx, endpoint+"?state=all", token)
	if err != nil {
		return ExternalIssueResult{}, fmt.Errorf("list GitLab external issues: %w", err)
	}
	for _, issue := range issues {
		if strings.Contains(issue.Description, publication.Marker) {
			return externalIssueResult(issue, "gitlab")
		}
	}
	payload := map[string]any{"title": publication.Title, "description": publication.Body}
	if len(publication.Labels) > 0 {
		payload["labels"] = strings.Join(publication.Labels, ",")
	}
	// GitLab accepts numeric assignee IDs. A cross-provider policy may carry a
	// GitHub login instead, so omit a non-numeric value instead of turning a
	// valid repository publication into an endlessly retried configuration bug.
	if assigneeID, err := strconv.ParseInt(strings.TrimSpace(publication.AssigneeExternalID), 10, 64); err == nil && assigneeID > 0 {
		payload["assignee_ids"] = []int64{assigneeID}
	}
	var created providerExternalIssue
	if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, payload, &created); err != nil {
		return ExternalIssueResult{}, fmt.Errorf("create GitLab external issue: %w", err)
	}
	return externalIssueResult(created, "gitlab")
}

func (p *HTTPPublisher) githubExternalIssues(ctx context.Context, endpoint, token string) ([]providerExternalIssue, error) {
	return collectProviderPages(func(page int) ([]providerExternalIssue, error) {
		pageEndpoint, err := providerListPage(endpoint, page)
		if err != nil {
			return nil, err
		}
		var issues []providerExternalIssue
		if err := p.requestJSON(ctx, http.MethodGet, pageEndpoint, token, nil, &issues); err != nil {
			return nil, err
		}
		return issues, nil
	})
}

func (p *HTTPPublisher) gitLabExternalIssues(ctx context.Context, endpoint, token string) ([]providerExternalIssue, error) {
	return collectProviderPages(func(page int) ([]providerExternalIssue, error) {
		pageEndpoint, err := providerListPage(endpoint, page)
		if err != nil {
			return nil, err
		}
		var issues []providerExternalIssue
		if err := p.requestJSON(ctx, http.MethodGet, pageEndpoint, token, nil, &issues); err != nil {
			return nil, err
		}
		return issues, nil
	})
}

func externalIssueResult(issue providerExternalIssue, provider string) (ExternalIssueResult, error) {
	id := issue.ID
	if provider == "gitlab" && issue.IID > 0 {
		id = issue.IID
	}
	if provider == "github" && issue.Number > 0 {
		id = issue.Number
	}
	url := strings.TrimSpace(issue.HTMLURL)
	if url == "" {
		url = strings.TrimSpace(issue.WebURL)
	}
	if id <= 0 || url == "" {
		return ExternalIssueResult{}, fmt.Errorf("provider external issue response is incomplete")
	}
	return ExternalIssueResult{ExternalID: strconv.FormatInt(id, 10), ExternalURL: url}, nil
}
