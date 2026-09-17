package credentials

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const tokenRefreshSkew = time.Minute

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
	response, err := b.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request GitHub installation token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("GitHub installation token returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
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
