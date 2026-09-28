package providerfeedback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
)

const (
	providerPageSize        = 100
	maxProviderPages        = 50
	maxProviderResponseSize = 1 << 20
)

type PollStore interface {
	ClaimProviderIssueFeedbackPoll(context.Context, string, time.Duration) (*domain.ProviderIssueFeedbackPollTarget, error)
	CompleteProviderIssueFeedbackPoll(context.Context, domain.ProviderIssueFeedbackPollTarget, []domain.ProviderIssueFeedbackReaction, time.Time, time.Time) error
	FailProviderIssueFeedbackPoll(context.Context, domain.ProviderIssueFeedbackPollTarget, string, time.Time) error
}

type Client struct {
	Resolver   credentials.Resolver
	HTTPClient *http.Client
}

type Processor struct {
	Store       PollStore
	Client      Client
	WorkerID    string
	Lease       time.Duration
	PollEvery   time.Duration
	TaskStarted func() func()
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	if p.Store == nil || p.Client.Resolver == nil || strings.TrimSpace(p.WorkerID) == "" {
		return false, fmt.Errorf("provider Issue feedback processor is not configured")
	}
	lease := p.Lease
	if lease <= 0 {
		lease = time.Minute
	}
	target, err := p.Store.ClaimProviderIssueFeedbackPoll(ctx, p.WorkerID, lease)
	if errors.Is(err, store.ErrNoProviderIssueFeedbackPoll) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	done := func() {}
	if p.TaskStarted != nil {
		if release := p.TaskStarted(); release != nil {
			done = release
		}
	}
	defer done()
	observedAt := time.Now().UTC()
	reactions, err := p.Client.Poll(ctx, target.Job)
	if err != nil {
		retryAt := observedAt.Add(retryDelay(target.Attempt))
		if failErr := p.Store.FailProviderIssueFeedbackPoll(ctx, *target, err.Error(), retryAt); failErr != nil {
			return true, fmt.Errorf("poll provider Issue feedback: %w; persist failure: %v", err, failErr)
		}
		return true, err
	}
	interval := p.PollEvery
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if err := p.Store.CompleteProviderIssueFeedbackPoll(ctx, *target, reactions, observedAt, observedAt.Add(interval)); err != nil {
		return true, err
	}
	return true, nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Minute << min(attempt-1, 4)
	if delay > 15*time.Minute {
		return 15 * time.Minute
	}
	return delay
}

func (c Client) Poll(ctx context.Context, job domain.ProviderIssueAnalysisJob) ([]domain.ProviderIssueFeedbackReaction, error) {
	if c.Resolver == nil || job.Provider != domain.ProviderGitHub || strings.TrimSpace(job.StableMarker) == "" {
		return nil, fmt.Errorf("GitHub provider Issue feedback target is invalid")
	}
	token, err := c.Resolver.Resolve(ctx, job.ProviderJob())
	if err != nil {
		return nil, fmt.Errorf("resolve GitHub Issue feedback credential: %w", err)
	}
	base, err := githubAPIBase(job.APIBaseURL)
	if err != nil {
		return nil, err
	}
	commentID, err := c.findAnalysisComment(ctx, base, token, job)
	if err != nil {
		return nil, err
	}
	return c.listCommentReactions(ctx, base, token, job.Repository, commentID)
}

type githubComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

type githubReaction struct {
	ID      json.Number `json:"id"`
	Content string      `json:"content"`
	User    struct {
		ID json.Number `json:"id"`
	} `json:"user"`
}

func (c Client) findAnalysisComment(ctx context.Context, base, token string, job domain.ProviderIssueAnalysisJob) (int64, error) {
	for page := 1; page <= maxProviderPages; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=%d&page=%d", base, job.Repository, job.IssueNumber, providerPageSize, page)
		var comments []githubComment
		if err := c.getJSON(ctx, endpoint, token, &comments); err != nil {
			return 0, fmt.Errorf("list GitHub Issue analysis comments: %w", err)
		}
		for _, comment := range comments {
			if comment.ID > 0 && strings.Contains(comment.Body, job.StableMarker) {
				return comment.ID, nil
			}
		}
		if len(comments) < providerPageSize {
			break
		}
	}
	return 0, fmt.Errorf("GitHub Issue analysis comment is unavailable")
}

func (c Client) listCommentReactions(ctx context.Context, base, token, repository string, commentID int64) ([]domain.ProviderIssueFeedbackReaction, error) {
	result := make([]domain.ProviderIssueFeedbackReaction, 0)
	seen := make(map[string]struct{})
	for page := 1; page <= maxProviderPages; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/issues/comments/%d/reactions?per_page=%d&page=%d", base, repository, commentID, providerPageSize, page)
		var reactions []githubReaction
		if err := c.getJSON(ctx, endpoint, token, &reactions); err != nil {
			return nil, fmt.Errorf("list GitHub Issue analysis reactions: %w", err)
		}
		for _, reaction := range reactions {
			kind := ""
			switch reaction.Content {
			case "+1":
				kind = "useful"
			case "-1":
				kind = "not_useful"
			default:
				continue
			}
			externalID, actorID := reaction.ID.String(), reaction.User.ID.String()
			if _, duplicate := seen[externalID]; externalID == "" || actorID == "" || duplicate {
				continue
			}
			seen[externalID] = struct{}{}
			result = append(result, domain.ProviderIssueFeedbackReaction{ExternalID: externalID, ActorExternalID: actorID, Kind: kind})
		}
		if len(reactions) < providerPageSize {
			return result, nil
		}
	}
	return nil, fmt.Errorf("GitHub Issue analysis reactions exceeded %d pages", maxProviderPages)
}

func (c Client) getJSON(ctx context.Context, endpoint, token string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "open-review-provider-feedback-poller")
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub API returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxProviderResponseSize+1))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func githubAPIBase(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "https://api.github.com", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("GitHub API base URL is invalid")
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	if strings.EqualFold(parsed.Host, "github.com") {
		return "https://api.github.com", nil
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return strings.TrimSuffix(parsed.String(), "/"), nil
}
