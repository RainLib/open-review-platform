package credentials

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RainLib/open-review-platform/internal/httpguard"
)

const tokenRefreshSkew = time.Minute

type transientCredentialError struct {
	cause      error
	retryAfter time.Duration
}

func (err *transientCredentialError) Error() string { return err.cause.Error() }
func (err *transientCredentialError) Unwrap() error { return err.cause }

// TransientDelay reports only credential-service failures that may safely
// retry the same immutable source admission. Authentication and redirect
// failures remain terminal until an operator repairs the installation.
func TransientDelay(err error) (time.Duration, bool) {
	var transient *transientCredentialError
	if errors.As(err, &transient) {
		return transient.retryAfter, true
	}
	return 0, false
}

func credentialRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(min(seconds, 900)) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return min(max(time.Until(deadline), 0), 15*time.Minute)
	}
	return 0
}

type GitHubAppBroker struct {
	appID  int64
	key    *rsa.PrivateKey
	apiURL string
	client *http.Client
	now    func() time.Time
	mu     sync.Mutex
	cache  map[string]installationToken
}

type installationToken struct {
	value     string
	expiresAt time.Time
}

// GitHubAppConfiguration is the non-secret portion of the authenticated App
// registration that affects webhook-driven product capabilities. It never
// contains a private key, webhook secret, or installation token.
type GitHubAppConfiguration struct {
	Events      []string          `json:"events"`
	Permissions map[string]string `json:"permissions"`
}

func NewGitHubAppBrokerFromFile(appID, privateKeyPath, apiURL string) (*GitHubAppBroker, error) {
	privateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read GitHub App private key: %w", err)
	}
	return NewGitHubAppBroker(appID, privateKey, apiURL)
}

func NewGitHubAppBroker(appID string, privateKeyPEM []byte, apiURL string) (*GitHubAppBroker, error) {
	parsedAppID, err := strconv.ParseInt(appID, 10, 64)
	if err != nil || parsedAppID <= 0 {
		return nil, fmt.Errorf("GitHub App id must be a positive integer")
	}
	key, err := parseRSAKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	apiURL = strings.TrimSuffix(strings.TrimSpace(apiURL), "/")
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	return &GitHubAppBroker{
		appID: parsedAppID, key: key, apiURL: apiURL,
		client: &http.Client{Timeout: 20 * time.Second}, now: time.Now, cache: make(map[string]installationToken),
	}, nil
}

func (b *GitHubAppBroker) InstallationToken(ctx context.Context, installationID string) (string, error) {
	if installationID == "" {
		return "", fmt.Errorf("GitHub App installation id is required")
	}
	now := b.now().UTC()
	b.mu.Lock()
	if token, ok := b.cache[installationID]; ok && token.expiresAt.After(now.Add(tokenRefreshSkew)) {
		b.mu.Unlock()
		return token.value, nil
	}
	b.mu.Unlock()

	signed, err := b.signedJWT(now)
	if err != nil {
		return "", err
	}
	endpoint := b.apiURL + "/app/installations/" + installationID + "/access_tokens"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create GitHub installation token request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+signed)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := httpguard.NoRedirects(b.client, 20*time.Second).Do(request)
	if err != nil {
		wrapped := fmt.Errorf("request GitHub installation token: %w", err)
		var networkError net.Error
		if ctx.Err() == nil && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout())) {
			return "", &transientCredentialError{cause: wrapped}
		}
		return "", wrapped
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		wrapped := fmt.Errorf("GitHub installation token returned HTTP %d", response.StatusCode)
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return "", &transientCredentialError{cause: wrapped, retryAfter: credentialRetryAfter(response.Header.Get("Retry-After"))}
		}
		return "", wrapped
	}
	var decoded struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode GitHub installation token: %w", err)
	}
	if decoded.Token == "" || decoded.ExpiresAt.IsZero() {
		return "", fmt.Errorf("GitHub installation token response is incomplete")
	}
	b.mu.Lock()
	b.cache[installationID] = installationToken{value: decoded.Token, expiresAt: decoded.ExpiresAt.UTC()}
	b.mu.Unlock()
	return decoded.Token, nil
}

// RepositoryWriteToken mints a fresh, single-repository installation token
// for the credential-broker boundary. It never uses the broader read-worker
// token cache: a cached installation-wide token must not satisfy a coding
// task's narrower repository and permission contract.
func (b *GitHubAppBroker) RepositoryWriteToken(ctx context.Context, installationID, repository string) (string, error) {
	parsedID, err := strconv.ParseInt(installationID, 10, 64)
	parts := strings.Split(repository, "/")
	if err != nil || parsedID <= 0 || len(parts) != 2 || !validGitHubRepositoryPart(parts[0]) || !validGitHubRepositoryPart(parts[1]) {
		return "", fmt.Errorf("GitHub repository token scope is invalid")
	}
	if err := b.CheckRepositoryWriteReadiness(ctx); err != nil {
		return "", err
	}
	permissions := map[string]string{"contents": "write", "pull_requests": "write", "issues": "read"}
	encoded, err := json.Marshal(map[string]any{"repositories": []string{parts[1]}, "permissions": permissions})
	if err != nil {
		return "", err
	}
	signed, err := b.signedJWT(b.now().UTC())
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiURL+"/app/installations/"+installationID+"/access_tokens", bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("create scoped GitHub token request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+signed)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := httpguard.NoRedirects(b.client, 20*time.Second).Do(request)
	if err != nil {
		wrapped := fmt.Errorf("request scoped GitHub token: %w", err)
		var networkError net.Error
		if ctx.Err() == nil && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout())) {
			return "", &transientCredentialError{cause: wrapped}
		}
		return "", wrapped
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		wrapped := fmt.Errorf("scoped GitHub token returned HTTP %d", response.StatusCode)
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return "", &transientCredentialError{cause: wrapped, retryAfter: credentialRetryAfter(response.Header.Get("Retry-After"))}
		}
		return "", wrapped
	}
	var result struct {
		Token               string            `json:"token"`
		ExpiresAt           time.Time         `json:"expires_at"`
		Permissions         map[string]string `json:"permissions"`
		RepositorySelection string            `json:"repository_selection"`
		Repositories        []struct {
			FullName string `json:"full_name"`
		} `json:"repositories"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode scoped GitHub token: %w", err)
	}
	if result.Token == "" || !result.ExpiresAt.After(b.now().UTC().Add(30*time.Second)) || result.RepositorySelection != "selected" || len(result.Repositories) != 1 || !strings.EqualFold(result.Repositories[0].FullName, repository) {
		return "", fmt.Errorf("scoped GitHub token response has an invalid repository scope")
	}
	for name, level := range permissions {
		if result.Permissions[name] != level {
			return "", fmt.Errorf("scoped GitHub token response has insufficient permissions")
		}
	}
	for name, level := range result.Permissions {
		if name == "metadata" && level == "read" {
			continue // GitHub grants repository metadata read implicitly.
		}
		if permissions[name] != level {
			return "", fmt.Errorf("scoped GitHub token response has unexpected permissions")
		}
	}
	return result.Token, nil
}

// CheckRepositoryWriteReadiness is a read-only App registration gate. A
// verified installation can review Issues with Contents:read yet still be
// unable to push an agent branch; do not discover that after running a CLI.
func (b *GitHubAppBroker) CheckRepositoryWriteReadiness(ctx context.Context) error {
	configuration, err := b.Configuration(ctx)
	if err != nil {
		return fmt.Errorf("check GitHub App coding permissions: %w", err)
	}
	if configuration.Permissions["contents"] != "write" {
		return fmt.Errorf("GitHub App Contents:write permission is required for agent branch publication")
	}
	if configuration.Permissions["pull_requests"] != "write" {
		return fmt.Errorf("GitHub App Pull requests:write permission is required for agent Draft PR publication")
	}
	if permission := configuration.Permissions["issues"]; permission != "read" && permission != "write" {
		return fmt.Errorf("GitHub App Issues:read permission is required for agent source verification")
	}
	return nil
}

func validGitHubRepositoryPart(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 100 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

// Configuration reads the App-level event subscriptions and repository
// permissions. Installation-token probes cannot observe this registration
// state, but webhook-driven features must not infer it from repository access.
func (b *GitHubAppBroker) Configuration(ctx context.Context) (GitHubAppConfiguration, error) {
	signed, err := b.signedJWT(b.now().UTC())
	if err != nil {
		return GitHubAppConfiguration{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, b.apiURL+"/app", nil)
	if err != nil {
		return GitHubAppConfiguration{}, fmt.Errorf("create GitHub App configuration request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+signed)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := httpguard.NoRedirects(b.client, 20*time.Second).Do(request)
	if err != nil {
		return GitHubAppConfiguration{}, fmt.Errorf("request GitHub App configuration: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return GitHubAppConfiguration{}, fmt.Errorf("GitHub App configuration returned HTTP %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, 64<<10)
	var configuration GitHubAppConfiguration
	if err := json.NewDecoder(limited).Decode(&configuration); err != nil {
		return GitHubAppConfiguration{}, fmt.Errorf("decode GitHub App configuration: %w", err)
	}
	if configuration.Permissions == nil {
		configuration.Permissions = map[string]string{}
	}
	return configuration, nil
}

func (b *GitHubAppBroker) signedJWT(now time.Time) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-30 * time.Second).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(b.appID, 10)})
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := encodedHeader + "." + encodedClaims
	sum := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, b.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign GitHub App JWT: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseRSAKey(value []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(value)
	if block == nil {
		return nil, fmt.Errorf("decode GitHub App private key PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub App private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("GitHub App private key must be RSA")
	}
	return rsaKey, nil
}
