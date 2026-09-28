package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/httpguard"
)

// gitLabOAuthRefresher is deliberately worker-local. Its OAuth client secret
// is never exposed to the Console, jobs, broker messages, or audit metadata.
type gitLabOAuthRefresher struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client
	tokenURL     string
}

type gitLabOAuthRefreshResult struct {
	accessToken  string
	expiresAt    time.Time
	refreshToken string
}

func newGitLabOAuthRefresher(cfg config.Config) *gitLabOAuthRefresher {
	clientID := strings.TrimSpace(cfg.GitLab.OAuthClientID)
	clientSecret := strings.TrimSpace(cfg.GitLab.OAuthClientSecret)
	if clientID == "" || clientSecret == "" {
		return nil
	}
	base, ok := gitLabOAuthBaseURL(firstNonEmpty(cfg.GitLab.OAuthInternalBaseURL, cfg.GitLab.OAuthBaseURL), cfg.GitLab.APIURL, cfg.Environment == "development")
	if !ok {
		return nil
	}
	return &gitLabOAuthRefresher{
		clientID: clientID, clientSecret: clientSecret, httpClient: http.DefaultClient,
		tokenURL: strings.TrimRight(base.String(), "/") + "/oauth/token",
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func gitLabOAuthBaseURL(explicit, apiURL string, allowHTTP bool) (*url.URL, bool) {
	base := strings.TrimSpace(explicit)
	if base == "" {
		parsed, err := url.Parse(strings.TrimSpace(apiURL))
		if err != nil || !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/api/v4") {
			return nil, false
		}
		parsed.Path = strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), "/api/v4")
		if parsed.Path == "" {
			parsed.Path = "/"
		}
		base = parsed.String()
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) {
		return nil, false
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, true
}

func (r *gitLabOAuthRefresher) Refresh(ctx context.Context, refreshToken string) (gitLabOAuthRefreshResult, error) {
	if r == nil || strings.TrimSpace(refreshToken) == "" {
		return gitLabOAuthRefreshResult{}, fmt.Errorf("GitLab OAuth refresh is not configured")
	}
	form := url.Values{
		"client_id":     {r.clientID},
		"client_secret": {r.clientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return gitLabOAuthRefreshResult{}, fmt.Errorf("create GitLab OAuth refresh request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := httpguard.NoRedirects(r.httpClient, 20*time.Second).Do(request)
	if err != nil {
		return gitLabOAuthRefreshResult{}, fmt.Errorf("request GitLab OAuth refresh: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return gitLabOAuthRefreshResult{}, fmt.Errorf("GitLab OAuth refresh was rejected")
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return gitLabOAuthRefreshResult{}, fmt.Errorf("decode GitLab OAuth refresh: %w", err)
	}
	if len(payload.AccessToken) < 8 || len(payload.AccessToken) > 16<<10 || payload.ExpiresIn <= 0 || payload.ExpiresIn > 366*24*60*60 || len(payload.RefreshToken) > 16<<10 {
		return gitLabOAuthRefreshResult{}, fmt.Errorf("GitLab OAuth refresh returned an invalid token")
	}
	return gitLabOAuthRefreshResult{
		accessToken: payload.AccessToken, expiresAt: time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second), refreshToken: payload.RefreshToken,
	}, nil
}
