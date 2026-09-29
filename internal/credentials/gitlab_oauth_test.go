package credentials

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RainLib/open-review-platform/internal/config"
)

func TestGitLabOAuthRefreshKeepsSelfManagedURLPrefix(t *testing.T) {
	refresher := newGitLabOAuthRefresher(config.Config{GitLab: config.GitLabConfig{
		APIURL: "https://gitlab.example/gitlab/api/v4", OAuthClientID: "client", OAuthClientSecret: "secret",
	}})
	if refresher == nil || refresher.tokenURL != "https://gitlab.example/gitlab/oauth/token" {
		t.Fatalf("relative-URL GitLab refresh endpoint=%#v", refresher)
	}
	if got := newGitLabOAuthRefresher(config.Config{GitLab: config.GitLabConfig{
		APIURL: "https://gitlab.example/gitlab/projects", OAuthClientID: "client", OAuthClientSecret: "secret",
	}}); got != nil {
		t.Fatal("an API base without /api/v4 must not be used for OAuth refresh")
	}
}

func TestGitLabOAuthRefreshNeverFollowsRedirectWithCredentials(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var redirected atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				redirected.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost {
					t.Errorf("refresh method=%s, want POST", request.Method)
				}
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
			}))
			defer source.Close()
			refresher := &gitLabOAuthRefresher{
				clientID: "local-client", clientSecret: "private-client-secret",
				httpClient: source.Client(), tokenURL: source.URL,
			}
			_, err := refresher.Refresh(context.Background(), "private-refresh-token")
			if err == nil || redirected.Load() != 0 {
				t.Fatalf("redirected OAuth refresh: error=%v target_requests=%d", err, redirected.Load())
			}
			if strings.Contains(err.Error(), "private-client-secret") || strings.Contains(err.Error(), "private-refresh-token") {
				t.Fatal("OAuth refresh error exposed a credential")
			}
		})
	}
}
