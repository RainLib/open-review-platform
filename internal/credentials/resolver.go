package credentials

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/providercredentials"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
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
	GitLabOAuth *gitLabOAuthRefresher
	OAuthStore  OAuthCredentialStore
	OAuthCipher *providercredentials.Cipher
}

// OAuthCredentialStore is a deliberately narrow worker-side read interface.
// It cannot create, list, or reveal secrets to a console request.
type OAuthCredentialStore interface {
	LoadProviderOAuthCredential(context.Context, uuid.UUID, string) (domain.ProviderOAuthCredential, error)
	RefreshProviderOAuthCredential(context.Context, uuid.UUID, string, domain.ProviderOAuthCredentialRefresh) (bool, error)
}

func New(cfg config.Config, oauthStore ...OAuthCredentialStore) (*ProviderResolver, error) {
	resolver := &ProviderResolver{GitHubToken: cfg.Runner.GitHubToken, GitLabToken: cfg.Runner.GitLabToken, GitLabOAuth: newGitLabOAuthRefresher(cfg)}
	if len(oauthStore) > 0 {
		resolver.OAuthStore = oauthStore[0]
	}
	if cfg.ProviderCredentialEncryptionKey != "" {
		cipher, err := providercredentials.New(cfg.ProviderCredentialEncryptionKey)
		if err != nil {
			return nil, err
		}
		resolver.OAuthCipher = &cipher
	}
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
		if strings.HasPrefix(job.CredentialRef, "secret://provider/gitlab-oauth/") {
			return r.gitLabOAuthToken(ctx, job)
		}
		if r.GitLabToken == "" {
			return "", fmt.Errorf("GitLab credential resolver returned no token")
		}
		return r.GitLabToken, nil
	default:
		return "", fmt.Errorf("unsupported provider %q", job.Provider)
	}
}

// GitHubAppConfiguration exposes only the App's public capability declaration
// to worker-side health probes. It never exposes the signing key or a token.
func (r *ProviderResolver) GitHubAppConfiguration(ctx context.Context) (GitHubAppConfiguration, error) {
	if r.GitHubApp == nil {
		return GitHubAppConfiguration{}, fmt.Errorf("GitHub App credential resolver is not configured")
	}
	return r.GitHubApp.Configuration(ctx)
}

func (r *ProviderResolver) gitLabOAuthToken(ctx context.Context, job domain.ReviewJob) (string, error) {
	if r.OAuthStore == nil || r.OAuthCipher == nil {
		return "", fmt.Errorf("GitLab OAuth credential resolver is not configured")
	}
	if job.TenantID == uuid.Nil {
		return "", fmt.Errorf("GitLab OAuth credential tenant is unavailable")
	}
	credential, err := r.OAuthStore.LoadProviderOAuthCredential(ctx, job.TenantID, job.CredentialRef)
	if errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("GitLab OAuth credential is unavailable")
	}
	if err != nil {
		return "", fmt.Errorf("load GitLab OAuth credential: %w", err)
	}
	if credential.Provider != domain.ProviderGitLab || credential.RevokedAt != nil {
		return "", fmt.Errorf("GitLab OAuth credential is unavailable")
	}
	if credential.ExpiresAt != nil && !credential.ExpiresAt.After(time.Now().UTC()) {
		return r.refreshGitLabOAuthToken(ctx, job, credential)
	}
	return r.openGitLabOAuthToken(credential)
}

func (r *ProviderResolver) openGitLabOAuthToken(credential domain.ProviderOAuthCredential) (string, error) {
	token, err := r.OAuthCipher.Open(credential.CredentialRef, credential.AccessTokenCiphertext)
	if err != nil {
		return "", fmt.Errorf("GitLab OAuth credential is unavailable")
	}
	return token, nil
}

func (r *ProviderResolver) refreshGitLabOAuthToken(ctx context.Context, job domain.ReviewJob, credential domain.ProviderOAuthCredential) (string, error) {
	if len(credential.RefreshTokenCiphertext) == 0 || r.GitLabOAuth == nil {
		return "", fmt.Errorf("GitLab OAuth credential has expired; reconnect GitLab")
	}
	refreshToken, err := r.OAuthCipher.Open(credential.CredentialRef, credential.RefreshTokenCiphertext)
	if err != nil {
		return "", fmt.Errorf("GitLab OAuth credential is unavailable")
	}
	refreshed, refreshErr := r.GitLabOAuth.Refresh(ctx, refreshToken)
	if refreshErr != nil {
		return r.concurrentGitLabOAuthWinner(ctx, job, credential, refreshErr)
	}
	accessCiphertext, err := r.OAuthCipher.Seal(credential.CredentialRef, refreshed.accessToken)
	if err != nil {
		return "", fmt.Errorf("protect refreshed GitLab OAuth credential: %w", err)
	}
	var refreshCiphertext []byte
	if refreshed.refreshToken != "" {
		refreshCiphertext, err = r.OAuthCipher.Seal(credential.CredentialRef, refreshed.refreshToken)
		if err != nil {
			return "", fmt.Errorf("protect refreshed GitLab OAuth credential: %w", err)
		}
	}
	updated, err := r.OAuthStore.RefreshProviderOAuthCredential(ctx, job.TenantID, credential.CredentialRef, domain.ProviderOAuthCredentialRefresh{
		ExpectedAccessTokenCiphertext: credential.AccessTokenCiphertext,
		AccessTokenCiphertext:         accessCiphertext,
		RefreshTokenCiphertext:        refreshCiphertext,
		ExpiresAt:                     refreshed.expiresAt,
	})
	if err != nil {
		return "", fmt.Errorf("persist refreshed GitLab OAuth credential: %w", err)
	}
	if !updated {
		return r.concurrentGitLabOAuthWinner(ctx, job, credential, nil)
	}
	return refreshed.accessToken, nil
}

func (r *ProviderResolver) concurrentGitLabOAuthWinner(ctx context.Context, job domain.ReviewJob, previous domain.ProviderOAuthCredential, cause error) (string, error) {
	winner, err := r.OAuthStore.LoadProviderOAuthCredential(ctx, job.TenantID, previous.CredentialRef)
	if err == nil && winner.Provider == domain.ProviderGitLab && winner.RevokedAt == nil && winner.ExpiresAt != nil && winner.ExpiresAt.After(time.Now().UTC()) && string(winner.AccessTokenCiphertext) != string(previous.AccessTokenCiphertext) {
		return r.openGitLabOAuthToken(winner)
	}
	if cause != nil {
		return "", fmt.Errorf("GitLab OAuth credential has expired; reconnect GitLab: %w", cause)
	}
	return "", fmt.Errorf("GitLab OAuth credential refresh did not complete; reconnect GitLab")
}
