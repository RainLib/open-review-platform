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
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type Publisher interface {
	Publish(context.Context, domain.ReviewJob, []domain.Finding) error
}

type HTTPPublisher struct {
	client      *http.Client
	githubToken string
	gitlabToken string
}

func NewHTTP(githubToken, gitlabToken string) *HTTPPublisher {
	return &HTTPPublisher{
		client:      &http.Client{Timeout: 30 * time.Second},
		githubToken: githubToken,
		gitlabToken: gitlabToken,
	}
}

func (p *HTTPPublisher) Publish(ctx context.Context, job domain.ReviewJob, findings []domain.Finding) error {
	switch job.Provider {
	case domain.ProviderGitHub:
		if p.githubToken == "" {
			return fmt.Errorf("GitHub credential resolver returned no token")
		}
		return p.publishGitHub(ctx, job, findings)
	case domain.ProviderGitLab:
		if p.gitlabToken == "" {
			return fmt.Errorf("GitLab credential resolver returned no token")
		}
		return p.publishGitLab(ctx, job, findings)
	default:
		return fmt.Errorf("unsupported provider %q", job.Provider)
	}
}

func (p *HTTPPublisher) publishGitHub(ctx context.Context, job domain.ReviewJob, findings []domain.Finding) error {
	base := strings.TrimSuffix(job.APIBaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	existing, err := p.githubMarkers(ctx, base, job)
	if err != nil {
		return err
	}
	var inline []githubInlineComment
	var summary []domain.Finding
	for _, finding := range findings {
		marker := findingMarker(job, finding)
		if existing[marker] {
			continue
		}
		if canInline(job, finding) {
			inline = append(inline, githubInlineComment{Path: finding.Path, Line: finding.EndLine, Side: "RIGHT", Body: renderFinding(finding, marker)})
		} else {
			summary = append(summary, finding)
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
		if err := p.requestJSON(ctx, http.MethodPost, endpoint, p.githubToken, payload, nil); err != nil {
			return fmt.Errorf("publish GitHub inline review: %w", err)
		}
	}
	if len(summary) > 0 || len(findings) == 0 {
		body := renderSummary(job, summary, "open-review-platform:summary:"+job.ID.String())
		if err := p.githubUpsertSummary(ctx, base, job, body); err != nil {
			return err
		}
	}
	return nil
}

func (p *HTTPPublisher) githubMarkers(ctx context.Context, base string, job domain.ReviewJob) (map[string]bool, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d/comments?per_page=100", base, job.Repository, job.ReviewNumber)
	var comments []struct {
		Body string `json:"body"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, p.githubToken, nil, &comments); err != nil {
		return nil, fmt.Errorf("list GitHub review comments: %w", err)
	}
	return markers(comments), nil
}

func (p *HTTPPublisher) githubUpsertSummary(ctx context.Context, base string, job domain.ReviewJob, body string) error {
	endpoint := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", base, job.Repository, job.ReviewNumber)
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, p.githubToken, nil, &comments); err != nil {
		return fmt.Errorf("list GitHub summary comments: %w", err)
	}
	marker := "open-review-platform:summary:" + job.ID.String()
	for _, comment := range comments {
		if strings.Contains(comment.Body, marker) {
			endpoint := fmt.Sprintf("%s/repos/%s/issues/comments/%d", base, job.Repository, comment.ID)
			return p.requestJSON(ctx, http.MethodPatch, endpoint, p.githubToken, map[string]string{"body": body}, nil)
		}
	}
	return p.requestJSON(ctx, http.MethodPost, endpoint, p.githubToken, map[string]string{"body": body}, nil)
}

func (p *HTTPPublisher) publishGitLab(ctx context.Context, job domain.ReviewJob, findings []domain.Finding) error {
	base := strings.TrimSuffix(job.APIBaseURL, "/")
	if base == "" {
		base = "https://gitlab.com/api/v4"
	}
	project := url.PathEscape(job.Repository)
	existing, err := p.gitLabMarkers(ctx, base, project, job)
	if err != nil {
		return err
	}
	posted := 0
	for _, finding := range findings {
		marker := findingMarker(job, finding)
		if existing[marker] {
			continue
		}
		body := renderFinding(finding, marker)
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
			if err := p.requestJSON(ctx, http.MethodPost, endpoint, p.gitlabToken, payload, nil); err != nil {
				return fmt.Errorf("publish GitLab inline discussion: %w", err)
			}
			posted++
			continue
		}
		endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes", base, project, job.ReviewNumber)
		if err := p.requestJSON(ctx, http.MethodPost, endpoint, p.gitlabToken, map[string]string{"body": body}, nil); err != nil {
			return fmt.Errorf("publish GitLab note: %w", err)
		}
		posted++
	}
	if len(findings) == 0 && posted == 0 {
		endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes", base, project, job.ReviewNumber)
		marker := "open-review-platform:summary:" + job.ID.String()
		if !existing[marker] {
			if err := p.requestJSON(ctx, http.MethodPost, endpoint, p.gitlabToken, map[string]string{"body": renderSummary(job, nil, marker)}, nil); err != nil {
				return fmt.Errorf("publish GitLab empty summary: %w", err)
			}
		}
	}
	return nil
}

func (p *HTTPPublisher) gitLabMarkers(ctx context.Context, base, project string, job domain.ReviewJob) (map[string]bool, error) {
	endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/discussions?per_page=100", base, project, job.ReviewNumber)
	var discussions []struct {
		Notes []struct {
			Body string `json:"body"`
		} `json:"notes"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, endpoint, p.gitlabToken, nil, &discussions); err != nil {
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

func renderFinding(finding domain.Finding, marker string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "**%s · %s**\n\n%s", strings.ToUpper(finding.Severity), finding.Category, finding.Body)
	if finding.Suggestion != "" {
		fmt.Fprintf(&builder, "\n\n```suggestion\n%s\n```", finding.Suggestion)
	}
	fmt.Fprintf(&builder, "\n\n<!-- %s -->", marker)
	return builder.String()
}

func renderSummary(job domain.ReviewJob, findings []domain.Finding, marker string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "## Open Review Platform\n\nReview job `%s` completed.", job.ID)
	if len(findings) == 0 {
		builder.WriteString("\n\nNo unpositioned findings were produced.")
	} else {
		builder.WriteString("\n\n### Findings requiring summary placement\n")
		for _, finding := range findings {
			fmt.Fprintf(&builder, "\n- **%s · %s** %s", strings.ToUpper(finding.Severity), finding.Category, finding.Body)
		}
	}
	fmt.Fprintf(&builder, "\n\n<!-- %s -->", marker)
	return builder.String()
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
