package publisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
)

type Publisher interface {
	Publish(context.Context, domain.ReviewJob, ReviewResult) error
}

// LifecycleReporter owns the single evolving PR/MR status comment. Keeping it
// separate from findings publication lets the runner acknowledge work and
// report terminal states even when the review engine never returns findings.
type LifecycleReporter interface {
	PublishStarted(context.Context, domain.ReviewJob) error
	PublishTerminal(context.Context, domain.ReviewJob, LifecycleState) error
}

type HTTPPublisher struct {
	client   *http.Client
	resolver credentials.Resolver
}

func NewHTTP(githubToken, gitlabToken string) *HTTPPublisher {
	return NewHTTPWithResolver(&credentials.ProviderResolver{GitHubToken: githubToken, GitLabToken: gitlabToken})
}

func NewHTTPWithResolver(resolver credentials.Resolver) *HTTPPublisher {
	return &HTTPPublisher{
		client:   &http.Client{Timeout: 30 * time.Second},
		resolver: resolver,
	}
}

func (p *HTTPPublisher) Publish(ctx context.Context, job domain.ReviewJob, result ReviewResult) error {
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	if job.Provider == domain.ProviderGitHub {
		return p.publishGitHub(ctx, job, token, result)
	}
	if job.Provider == domain.ProviderGitLab {
		return p.publishGitLab(ctx, job, token, result)
	}
	return fmt.Errorf("unsupported provider %q", job.Provider)
}

func (p *HTTPPublisher) PublishStarted(ctx context.Context, job domain.ReviewJob) error {
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	marker := "open-review-platform:summary:" + job.ID.String()
	body := StartedReport(job, p.loadReviewContext(ctx, job, token), marker)
	switch job.Provider {
	case domain.ProviderGitHub:
		return p.githubUpsertSummary(ctx, githubAPIBase(job.APIBaseURL), job, token, body)
	case domain.ProviderGitLab:
		base := strings.TrimSuffix(job.APIBaseURL, "/")
		if base == "" {
			base = "https://gitlab.com/api/v4"
		}
		return p.gitLabUpsertSummary(ctx, base, url.PathEscape(job.Repository), job, token, body)
	default:
		return fmt.Errorf("unsupported provider %q", job.Provider)
	}
}

func (p *HTTPPublisher) PublishTerminal(ctx context.Context, job domain.ReviewJob, state LifecycleState) error {
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	marker := "open-review-platform:summary:" + job.ID.String()
	body := TerminalReport(job, state, marker)
	switch job.Provider {
	case domain.ProviderGitHub:
		return p.githubUpsertSummary(ctx, githubAPIBase(job.APIBaseURL), job, token, body)
	case domain.ProviderGitLab:
		base := strings.TrimSuffix(job.APIBaseURL, "/")
		if base == "" {
			base = "https://gitlab.com/api/v4"
		}
		return p.gitLabUpsertSummary(ctx, base, url.PathEscape(job.Repository), job, token, body)
	default:
		return fmt.Errorf("unsupported provider %q", job.Provider)
	}
}

// PublishInteractionResponse writes a single, marker-keyed issue comment after
// an @openreview command is committed. The marker makes a redelivered broker
// message update the original response instead of producing duplicate comments.
func (p *HTTPPublisher) PublishInteractionResponse(ctx context.Context, response domain.InteractionResponse) error {
	if response.Provider == domain.ProviderGitHub && response.Reaction != domain.InteractionReactionNone {
		if _, err := githubInteractionCommentID(response.CommentExternalID); err != nil {
			return err
		}
	}
	job := domain.ReviewJob{
		Provider:               response.Provider,
		APIBaseURL:             response.APIBaseURL,
		InstallationExternalID: response.InstallationExternalID,
		CredentialRef:          response.CredentialRef,
	}
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	if response.Provider == domain.ProviderGitLab {
		return p.publishGitLabInteractionResponse(ctx, response, token)
	}
	if response.Provider != domain.ProviderGitHub {
		return fmt.Errorf("unsupported interaction provider %q", response.Provider)
	}
	if err := p.publishGitHubInteractionResponse(ctx, response, token); err != nil {
		return err
	}
	if response.Reaction == domain.InteractionReactionNone {
		return nil
	}
	return p.publishGitHubInteractionReaction(ctx, response, token)
}

func (p *HTTPPublisher) publishGitHubInteractionResponse(ctx context.Context, response domain.InteractionResponse, token string) error {
	base := githubAPIBase(response.APIBaseURL)
	body := "## Open Review Platform\n\n" + response.Body + "\n\n<!-- " + response.Marker + " -->"
	endpoint := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", base, response.Repository, response.ReviewNumber)
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &comments); err != nil {
		return fmt.Errorf("list GitHub interaction comments: %w", err)
	}
	for _, comment := range comments {
		if strings.Contains(comment.Body, response.Marker) {
			endpoint := fmt.Sprintf("%s/repos/%s/issues/comments/%d", base, response.Repository, comment.ID)
			return p.requestJSON(ctx, http.MethodPatch, endpoint, token, map[string]string{"body": body}, nil)
		}
	}
	return p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil)
}

// publishGitHubInteractionReaction is intentionally performed after the
// marker-keyed reply. If a transient reaction write fails, the inbox retries
// the whole delivery, the reply is updated in place, and GitHub makes the
// same app/content reaction idempotent (200 rather than a second reaction).
func (p *HTTPPublisher) publishGitHubInteractionReaction(ctx context.Context, response domain.InteractionResponse, token string) error {
	commentID, err := githubInteractionCommentID(response.CommentExternalID)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/issues/comments/%d/reactions", githubAPIBase(response.APIBaseURL), response.Repository, commentID)
	if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"content": string(response.Reaction)}, nil); err != nil {
		return fmt.Errorf("publish GitHub interaction reaction: %w", err)
	}
	return nil
}

func githubInteractionCommentID(externalID string) (int64, error) {
	commentID, err := strconv.ParseInt(externalID, 10, 64)
	if err != nil || commentID < 1 {
		return 0, fmt.Errorf("invalid GitHub interaction comment id")
	}
	return commentID, nil
}

func githubAPIBase(apiBaseURL string) string {
	if base := strings.TrimSuffix(apiBaseURL, "/"); base != "" {
		return base
	}
	return "https://api.github.com"
}

func (p *HTTPPublisher) publishGitLabInteractionResponse(ctx context.Context, response domain.InteractionResponse, token string) error {
	base := strings.TrimSuffix(response.APIBaseURL, "/")
	if base == "" {
		base = "https://gitlab.com/api/v4"
	}
	project := url.PathEscape(response.Repository)
	body := "## Open Review Platform\n\n" + response.Body + "\n\n<!-- " + response.Marker + " -->"
	endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes?per_page=100", base, project, response.ReviewNumber)
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &comments); err != nil {
		return fmt.Errorf("list GitLab interaction notes: %w", err)
	}
	for _, comment := range comments {
		if strings.Contains(comment.Body, response.Marker) {
			endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes/%d", base, project, response.ReviewNumber, comment.ID)
			return p.requestJSON(ctx, http.MethodPut, endpoint, token, map[string]string{"body": body}, nil)
		}
	}
	return p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil)
}

func (p *HTTPPublisher) publishGitHub(ctx context.Context, job domain.ReviewJob, token string, result ReviewResult) error {
	base := strings.TrimSuffix(job.APIBaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	findings := result.Findings
	existing, err := p.githubMarkers(ctx, base, job, token)
	if err != nil {
		return err
	}
	var inline []githubInlineComment
	for _, finding := range findings {
		marker := findingMarker(job, finding)
		if existing[marker] {
			continue
		}
		if canInline(job, finding) {
			inline = append(inline, githubInlineComment{Path: finding.Path, Line: finding.EndLine, Side: "RIGHT", Body: FindingReport(job, finding, marker)})
		}
	}
	for start := 0; start < len(inline); start += 25 {
		end := min(start+25, len(inline))
		payload := struct {
			Body     string                `json:"body"`
			CommitID string                `json:"commit_id"`
			Event    string                `json:"event"`
			Comments []githubInlineComment `json:"comments"`
		}{
			Body:     "Open Review Platform findings. <!-- open-review-platform:review:" + job.ID.String() + " -->",
			CommitID: job.HeadSHA,
			Event:    "COMMENT",
			Comments: inline[start:end],
		}
		endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d/reviews", base, job.Repository, job.ReviewNumber)
		if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, payload, nil); err != nil {
			return fmt.Errorf("publish GitHub inline review: %w", err)
		}
	}
	// Inline annotations are easy to miss in GitHub's Files view. Always leave
	// one concise, updatable PR-level result so authors know whether the bot
	// recommends changes or found no actionable risk.
	body := CompletedReport(job, p.loadReviewContext(ctx, job, token), result, "open-review-platform:summary:"+job.ID.String())
	if err := p.githubUpsertSummary(ctx, base, job, token, body); err != nil {
		return err
	}
	return nil
}

func (p *HTTPPublisher) githubMarkers(ctx context.Context, base string, job domain.ReviewJob, token string) (map[string]bool, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d/comments?per_page=100", base, job.Repository, job.ReviewNumber)
	var comments []struct {
		Body string `json:"body"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &comments); err != nil {
		return nil, fmt.Errorf("list GitHub review comments: %w", err)
	}
	return markers(comments), nil
}

func (p *HTTPPublisher) githubUpsertSummary(ctx context.Context, base string, job domain.ReviewJob, token, body string) error {
	endpoint := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", base, job.Repository, job.ReviewNumber)
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &comments); err != nil {
		return fmt.Errorf("list GitHub summary comments: %w", err)
	}
	marker := "open-review-platform:summary:" + job.ID.String()
	for _, comment := range comments {
		if strings.Contains(comment.Body, marker) {
			endpoint := fmt.Sprintf("%s/repos/%s/issues/comments/%d", base, job.Repository, comment.ID)
			return p.requestJSON(ctx, http.MethodPatch, endpoint, token, map[string]string{"body": body}, nil)
		}
	}
	return p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil)
}

func (p *HTTPPublisher) publishGitLab(ctx context.Context, job domain.ReviewJob, token string, result ReviewResult) error {
	base := strings.TrimSuffix(job.APIBaseURL, "/")
	if base == "" {
		base = "https://gitlab.com/api/v4"
	}
	project := url.PathEscape(job.Repository)
	findings := result.Findings
	existing, err := p.gitLabMarkers(ctx, base, project, job, token)
	if err != nil {
		return err
	}
	for _, finding := range findings {
		marker := findingMarker(job, finding)
		if existing[marker] {
			continue
		}
		body := FindingReport(job, finding, marker)
		if canInline(job, finding) && job.BaseSHA != "" {
			payload := struct {
				Body     string `json:"body"`
				Position struct {
					PositionType string `json:"position_type"`
					BaseSHA      string `json:"base_sha"`
					StartSHA     string `json:"start_sha"`
					HeadSHA      string `json:"head_sha"`
					NewPath      string `json:"new_path"`
					NewLine      int    `json:"new_line"`
				} `json:"position"`
			}{Body: body}
			payload.Position.PositionType = "text"
			payload.Position.BaseSHA, payload.Position.StartSHA, payload.Position.HeadSHA = job.BaseSHA, job.BaseSHA, job.HeadSHA
			payload.Position.NewPath, payload.Position.NewLine = finding.Path, finding.EndLine
			endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/discussions", base, project, job.ReviewNumber)
			if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, payload, nil); err != nil {
				return fmt.Errorf("publish GitLab inline discussion: %w", err)
			}
			continue
		}
		endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes", base, project, job.ReviewNumber)
		if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil); err != nil {
			return fmt.Errorf("publish GitLab note: %w", err)
		}
	}
	if err := p.gitLabUpsertSummary(ctx, base, project, job, token, CompletedReport(job, p.loadReviewContext(ctx, job, token), result, "open-review-platform:summary:"+job.ID.String())); err != nil {
		return err
	}
	return nil
}

func (p *HTTPPublisher) gitLabUpsertSummary(ctx context.Context, base, project string, job domain.ReviewJob, token, body string) error {
	endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes?per_page=100", base, project, job.ReviewNumber)
	var notes []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &notes); err != nil {
		return fmt.Errorf("list GitLab summary notes: %w", err)
	}
	marker := "open-review-platform:summary:" + job.ID.String()
	for _, note := range notes {
		if strings.Contains(note.Body, marker) {
			return p.requestJSON(ctx, http.MethodPut, fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes/%d", base, project, job.ReviewNumber, note.ID), token, map[string]string{"body": body}, nil)
		}
	}
	if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil); err != nil {
		return fmt.Errorf("publish GitLab summary: %w", err)
	}
	return nil
}

func (p *HTTPPublisher) gitLabMarkers(ctx context.Context, base, project string, job domain.ReviewJob, token string) (map[string]bool, error) {
	endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/discussions?per_page=100", base, project, job.ReviewNumber)
	var discussions []struct {
		Notes []struct {
			Body string `json:"body"`
		} `json:"notes"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &discussions); err != nil {
		return nil, fmt.Errorf("list GitLab discussions: %w", err)
	}
	found := make(map[string]bool)
	for _, discussion := range discussions {
		for _, note := range discussion.Notes {
			for marker := range markers([]struct {
				Body string `json:"body"`
			}{{Body: note.Body}}) {
				found[marker] = true
			}
			for _, token := range strings.Fields(note.Body) {
				if strings.HasPrefix(token, "open-review-platform:summary:") {
					found[strings.TrimSuffix(token, "-->")] = true
				}
			}
		}
	}
	return found, nil
}

type githubInlineComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

func (p *HTTPPublisher) requestJSON(ctx context.Context, method, endpoint, token string, requestBody, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("marshal provider request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create provider request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("send provider request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("provider returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	if responseBody != nil {
		if err := json.NewDecoder(response.Body).Decode(responseBody); err != nil {
			return fmt.Errorf("decode provider response: %w", err)
		}
	}
	return nil
}

func canInline(job domain.ReviewJob, finding domain.Finding) bool {
	return job.HeadSHA != "" && finding.Path != "" && finding.StartLine > 0 && finding.EndLine >= finding.StartLine
}

func findingMarker(job domain.ReviewJob, finding domain.Finding) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d|%s|%s", job.ID, finding.Path, finding.StartLine, finding.EndLine, finding.Category, finding.Body)))
	return "open-review-platform:finding:" + hex.EncodeToString(sum[:12])
}

// ResultSummary is used both in the PR result comment and the GitHub Check so
// the status surface communicates an actionable verdict instead of a generic
// "completed" message.
func ResultSummary(findings []domain.Finding) string {
	if len(findings) == 0 {
		return "AI analysis completed: no actionable risks were detected."
	}
	counts := make(map[string]int)
	for _, finding := range findings {
		severity := strings.ToLower(strings.TrimSpace(finding.Severity))
		if severity == "" {
			severity = "medium"
		}
		counts[severity]++
	}
	parts := make([]string, 0, len(counts))
	for _, severity := range []string{"critical", "high", "medium", "low"} {
		if count := counts[severity]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, severity))
			delete(counts, severity)
		}
	}
	for severity, count := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", count, severity))
	}
	return fmt.Sprintf("AI analysis completed: changes recommended for %d actionable finding(s) (%s).", len(findings), strings.Join(parts, ", "))
}

func markers(comments []struct {
	Body string `json:"body"`
}) map[string]bool {
	found := make(map[string]bool)
	for _, comment := range comments {
		for _, token := range strings.Fields(comment.Body) {
			if strings.HasPrefix(token, "open-review-platform:finding:") {
				found[strings.TrimSuffix(token, "-->")] = true
			}
		}
	}
	return found
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
