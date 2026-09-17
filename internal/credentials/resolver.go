package credentials

import (
	"context"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/domain"
)

// Resolver retrieves a short-lived provider credential immediately before a
// provider side effect. Its value must never be persisted in jobs, events, or
// broker messages.
type Resolver interface {
	Resolve(context.Context, domain.ReviewJob) (string, error)
}

type ProviderResolver struct {
	GitHubApp   *GitHubAppBroker
	GitHubToken string
	GitLabToken string
}

func New(cfg config.Config) (*ProviderResolver, error) {
	resolver := &ProviderResolver{GitHubToken: cfg.Runner.GitHubToken, GitLabToken: cfg.Runner.GitLabToken}
	if cfg.GitHub.AppID == "" && cfg.GitHub.PrivateKeyPath == "" {
		return resolver, nil
	}
	if cfg.GitHub.AppID == "" || cfg.GitHub.PrivateKeyPath == "" {
		return nil, fmt.Errorf("GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY_PATH must be configured together")
	}
	broker, err := NewGitHubAppBrokerFromFile(cfg.GitHub.AppID, cfg.GitHub.PrivateKeyPath, cfg.GitHub.APIURL)
	if err != nil {
		return nil, err
	}
	resolver.GitHubApp = broker
	return resolver, nil
}

func (r *ProviderResolver) Resolve(ctx context.Context, job domain.ReviewJob) (string, error) {
	switch job.Provider {
	case domain.ProviderGitHub:
		if job.CredentialRef == "github-app" {
			if r.GitHubApp == nil {
				return "", fmt.Errorf("GitHub App credential resolver is not configured")
			}
			if job.InstallationExternalID == "" {
				return "", fmt.Errorf("GitHub App installation id is missing")
			}
			return r.GitHubApp.InstallationToken(ctx, job.InstallationExternalID)
		}
		if r.GitHubToken == "" {
			return "", fmt.Errorf("GitHub credential resolver returned no token")
		}
		return r.GitHubToken, nil
	case domain.ProviderGitLab:
		if r.GitLabToken == "" {
			return "", fmt.Errorf("GitLab credential resolver returned no token")
		}
		return r.GitLabToken, nil
	default:
		return "", fmt.Errorf("unsupported provider %q", job.Provider)
	}
}
