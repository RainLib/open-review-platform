package providermetadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/httpguard"
	"github.com/RainLib/open-review-platform/internal/providertransport"
)

const maxMergeRequestResponseBytes = 256 << 10

var fullCommitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

var ErrStaleMergeRequest = errors.New("GitLab merge request author or head changed")

// GitLabAuthorClient belongs in a provider-calling worker, never the public
// webhook API. A GitLab MR hook's top-level user is the actor, not necessarily
// the author, so author-name policy cannot be decided from that field.
type GitLabAuthorClient struct {
	Resolver             credentials.Resolver
	HTTPClient           *http.Client
	AllowPrivateNetworks bool
	AllowInsecureHTTP    bool
}

// ResolveAuthor re-reads the exact MR head before returning its author. A
// newer head or changed author ID must be treated as stale admission evidence,
// not as a reason to apply the old webhook to the current revision.
func (c GitLabAuthorClient) ResolveAuthor(ctx context.Context, job domain.ReviewJob, expectedAuthorID string) (string, error) {
	if job.Provider != domain.ProviderGitLab || c.Resolver == nil || job.ReviewNumber <= 0 ||
		!fullCommitSHA.MatchString(job.HeadSHA) || !validAuthorID(expectedAuthorID) {
		return "", fmt.Errorf("GitLab author lookup requires a verified MR identity")
	}
	base, err := c.validBase(job.APIBaseURL)
	if err != nil {
		return "", err
	}
	if !validRepository(job.Repository) {
		return "", fmt.Errorf("GitLab author lookup repository is invalid")
	}
	token, err := c.Resolver.Resolve(ctx, job)
	if err != nil || strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("GitLab author lookup credential is unavailable")
	}
	endpoint := base + "/projects/" + url.PathEscape(job.Repository) + "/merge_requests/" + strconv.Itoa(job.ReviewNumber)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("GitLab author lookup request is invalid: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = providertransport.NewClient(c.AllowPrivateNetworks)
	}
	response, err := httpguard.NoRedirects(client, 20*time.Second).Do(request)
	if err != nil {
		return "", fmt.Errorf("read GitLab merge request author: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitLab author lookup returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMergeRequestResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read GitLab author response: %w", err)
	}
	if len(body) > maxMergeRequestResponseBytes {
		return "", fmt.Errorf("GitLab author response is too large")
	}
	var actual struct {
		IID    int    `json:"iid"`
		SHA    string `json:"sha"`
		Author struct {
			ID       json.Number `json:"id"`
			Username string      `json:"username"`
		} `json:"author"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&actual); err != nil {
		return "", fmt.Errorf("decode GitLab author response: %w", err)
	}
	author := strings.TrimSpace(actual.Author.Username)
	if actual.IID != job.ReviewNumber || !strings.EqualFold(actual.SHA, job.HeadSHA) ||
		actual.Author.ID.String() != expectedAuthorID || author == "" || len(author) > 255 || strings.ContainsAny(author, "\r\n\t") {
		return "", ErrStaleMergeRequest
	}
	return author, nil
}

func (c GitLabAuthorClient) validBase(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", fmt.Errorf("GitLab author lookup API base is invalid")
	}
	if parsed.Scheme != "https" && !(c.AllowInsecureHTTP && parsed.Scheme == "http") {
		return "", fmt.Errorf("GitLab author lookup requires HTTPS")
	}
	if !strings.HasSuffix(strings.TrimSuffix(parsed.EscapedPath(), "/"), "/api/v4") {
		return "", fmt.Errorf("GitLab author lookup API path is invalid")
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func validAuthorID(id string) bool {
	parsed, err := strconv.ParseUint(id, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == id
}

func validRepository(repository string) bool {
	parts := strings.Split(repository, "/")
	if len(parts) < 2 || strings.Trim(repository, "/") != repository {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\r\n") {
			return false
		}
	}
	return true
}
