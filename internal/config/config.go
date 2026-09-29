package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL string
	HTTPAddress string
	Environment string
	// ConsoleURL is the public Console origin used in provider comments. It is
	// optional so a self-hosted deployment without a public Console never emits
	// an unusable or internal link.
	ConsoleURL                       string
	Auth                             AuthConfig
	GitHub                           GitHubConfig
	GitLab                           GitLabConfig
	ProviderAuthorizationSecret      string
	GitLabInstallationIdentitySecret string
	ProviderCredentialEncryptionKey  string
	Broker                           BrokerConfig
	Runner                           RunnerConfig
	AgentTask                        AgentTaskConfig
}

type AuthConfig struct {
	Mode     string
	Issuer   string
	Audience string
}

type GitHubConfig struct {
	Secret         string
	AppID          string
	PrivateKeyPath string
	APIURL         string
}

type GitLabConfig struct {
	Secret string
	APIURL string
	// AllowHTTP exists only for an isolated development GitLab. Production
	// provider traffic remains HTTPS-only, including self-managed instances.
	AllowHTTP                bool
	OAuthBaseURL             string
	OAuthInternalBaseURL     string
	OAuthClientID            string
	OAuthClientSecret        string
	DeploymentTokenAvailable bool
}

type BrokerConfig struct {
	URL      string
	Exchange string
	RelayID  string
}

type RunnerConfig struct {
	ID                string
	GitBinary         string
	OCRBinary         string
	OCRVersion        string
	OCRConcurrency    int
	OCREffort         string
	OCRMaxTokens      int
	OCRTokenBudget    int
	OCRSubtaskTimeout int
	RiskReviewMode    string
	MergeGateSeverity string
	CheckoutTimeout   time.Duration
	OCRTimeout        time.Duration
	LeaseDuration     time.Duration
	LeaseRenewEvery   time.Duration
	PollInterval      time.Duration
	GitHubToken       string
	GitLabToken       string
}

// AgentTaskConfig contains only the authenticated callback boundary. Provider
// credentials, local worktree paths and coding CLI configuration do not belong
// in the public control API configuration.
type AgentTaskConfig struct {
	AdapterCallbackSecret string
	LeaseDuration         time.Duration
	GitHubPublicBaseURL   string
	GitLabPublicBaseURL   string
}

func Load() (Config, error) {
	pollInterval := env("RUNNER_POLL_INTERVAL", "5s")
	poll, err := time.ParseDuration(pollInterval)
	if err != nil || poll <= 0 {
		return Config{}, fmt.Errorf("RUNNER_POLL_INTERVAL must be a positive duration")
	}
	// Keep the runtime defaults aligned with .env.example. An older deployment
	// may not have newly introduced keys; treating absence as unlimited model
	// work turns a harmless upgrade into an unbounded-cost review.
	ocrConcurrency, err := envInt("OCR_CONCURRENCY", 2)
	if err != nil || ocrConcurrency < 0 {
		return Config{}, fmt.Errorf("OCR_CONCURRENCY must be a non-negative integer")
	}
	ocrEffort := env("OCR_REVIEW_EFFORT", "low")
	if ocrEffort != "" && ocrEffort != "low" && ocrEffort != "medium" && ocrEffort != "high" {
		return Config{}, fmt.Errorf("OCR_REVIEW_EFFORT must be low, medium, high, or empty")
	}
	ocrMaxTokens, err := envInt("OCR_MAX_PROMPT_TOKENS", 8000)
	if err != nil || ocrMaxTokens < 0 {
		return Config{}, fmt.Errorf("OCR_MAX_PROMPT_TOKENS must be a non-negative integer")
	}
	ocrTokenBudget, err := envInt("OCR_MAX_TOKENS_BUDGET", 128000)
	if err != nil || ocrTokenBudget < 0 {
		return Config{}, fmt.Errorf("OCR_MAX_TOKENS_BUDGET must be a non-negative integer")
	}
	ocrSubtaskTimeout, err := envInt("OCR_SUBTASK_TIMEOUT_MINUTES", 5)
	if err != nil || ocrSubtaskTimeout < 0 {
		return Config{}, fmt.Errorf("OCR_SUBTASK_TIMEOUT_MINUTES must be a non-negative integer")
	}
	riskReviewMode := env("RISK_REVIEW_MODE", "focused")
	if riskReviewMode != "standard" && riskReviewMode != "focused" && riskReviewMode != "critical" {
		return Config{}, fmt.Errorf("RISK_REVIEW_MODE must be standard, focused, or critical")
	}
	mergeGateSeverity := strings.ToLower(env("MERGE_GATE_MIN_SEVERITY", "critical"))
	if mergeGateSeverity != "off" && mergeGateSeverity != "critical" && mergeGateSeverity != "high" && mergeGateSeverity != "medium" && mergeGateSeverity != "low" {
		return Config{}, fmt.Errorf("MERGE_GATE_MIN_SEVERITY must be off, critical, high, medium, or low")
	}
	ocrTimeout, err := time.ParseDuration(env("OCR_TIMEOUT", "15m"))
	if err != nil || ocrTimeout <= 0 {
		return Config{}, fmt.Errorf("OCR_TIMEOUT must be a positive duration")
	}
	checkoutTimeout, err := time.ParseDuration(env("CHECKOUT_TIMEOUT", "2m"))
	if err != nil || checkoutTimeout <= 0 {
		return Config{}, fmt.Errorf("CHECKOUT_TIMEOUT must be a positive duration")
	}
	leaseDuration, err := time.ParseDuration(env("RUNNER_LEASE_DURATION", "2m"))
	if err != nil || leaseDuration <= 0 {
		return Config{}, fmt.Errorf("RUNNER_LEASE_DURATION must be a positive duration")
	}
	leaseRenewEvery, err := time.ParseDuration(env("RUNNER_LEASE_RENEW_INTERVAL", "30s"))
	if err != nil || leaseRenewEvery <= 0 || leaseRenewEvery >= leaseDuration {
		return Config{}, fmt.Errorf("RUNNER_LEASE_RENEW_INTERVAL must be positive and shorter than RUNNER_LEASE_DURATION")
	}
	agentTaskLeaseDuration, err := time.ParseDuration(env("AGENT_TASK_LEASE_DURATION", "2m"))
	if err != nil || agentTaskLeaseDuration < 15*time.Second {
		return Config{}, fmt.Errorf("AGENT_TASK_LEASE_DURATION must be at least 15 seconds")
	}
	gitLabDeploymentTokenAvailable, err := envBool("GITLAB_DEPLOYMENT_TOKEN_CONFIGURED", false)
	if err != nil {
		return Config{}, fmt.Errorf("GITLAB_DEPLOYMENT_TOKEN_CONFIGURED must be a boolean")
	}
	gitLabAllowHTTP, err := envBool("GITLAB_ALLOW_HTTP", false)
	if err != nil {
		return Config{}, fmt.Errorf("GITLAB_ALLOW_HTTP must be a boolean")
	}
	c := Config{
		DatabaseURL: os.Getenv("CONTROL_DATABASE_URL"),
		HTTPAddress: env("HTTP_ADDRESS", ":8080"),
		Environment: env("ENVIRONMENT", "development"),
		ConsoleURL:  strings.TrimSuffix(strings.TrimSpace(os.Getenv("OPEN_REVIEW_APP_URL")), "/"),
		Auth: AuthConfig{
			Mode:     env("AUTH_MODE", "oidc"),
			Issuer:   strings.TrimSuffix(os.Getenv("CASDOOR_ISSUER"), "/"),
			Audience: os.Getenv("CASDOOR_AUDIENCE"),
		},
		GitHub: GitHubConfig{
			Secret:         os.Getenv("GITHUB_WEBHOOK_SECRET"),
			AppID:          os.Getenv("GITHUB_APP_ID"),
			PrivateKeyPath: os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"),
			APIURL:         env("GITHUB_API_URL", "https://api.github.com"),
		},
		GitLab: GitLabConfig{
			Secret:                   os.Getenv("GITLAB_WEBHOOK_SECRET"),
			APIURL:                   env("GITLAB_API_URL", "https://gitlab.com/api/v4"),
			AllowHTTP:                gitLabAllowHTTP,
			OAuthBaseURL:             strings.TrimSpace(os.Getenv("GITLAB_OAUTH_BASE_URL")),
			OAuthInternalBaseURL:     strings.TrimSpace(os.Getenv("GITLAB_OAUTH_INTERNAL_BASE_URL")),
			OAuthClientID:            strings.TrimSpace(os.Getenv("GITLAB_OAUTH_CLIENT_ID")),
			OAuthClientSecret:        strings.TrimSpace(os.Getenv("GITLAB_OAUTH_CLIENT_SECRET")),
			DeploymentTokenAvailable: gitLabDeploymentTokenAvailable,
		},
		ProviderAuthorizationSecret: strings.TrimSpace(firstSet(
			os.Getenv("OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET"),
			os.Getenv("OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET"),
		)),
		GitLabInstallationIdentitySecret: strings.TrimSpace(firstSet(
			os.Getenv("OPEN_REVIEW_GITLAB_INSTALLATION_IDENTITY_SECRET"),
			os.Getenv("OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET"),
			os.Getenv("OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET"),
		)),
		ProviderCredentialEncryptionKey: strings.TrimSpace(os.Getenv("PROVIDER_CREDENTIAL_ENCRYPTION_KEY")),
		Broker: BrokerConfig{
			URL:      env("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
			Exchange: env("RABBITMQ_EXCHANGE", "openreview.events"),
			RelayID:  env("OUTBOX_RELAY_ID", "relay-1"),
		},
		Runner: RunnerConfig{
			ID:                env("RUNNER_ID", "runner-1"),
			GitBinary:         env("GIT_BINARY", "git"),
			OCRBinary:         env("OCR_BINARY", "ocr"),
			OCRVersion:        env("OCR_VERSION", "1.12.5"),
			OCRConcurrency:    ocrConcurrency,
			OCREffort:         ocrEffort,
			OCRMaxTokens:      ocrMaxTokens,
			OCRTokenBudget:    ocrTokenBudget,
			OCRSubtaskTimeout: ocrSubtaskTimeout,
			RiskReviewMode:    riskReviewMode,
			MergeGateSeverity: mergeGateSeverity,
			CheckoutTimeout:   checkoutTimeout,
			OCRTimeout:        ocrTimeout,
			LeaseDuration:     leaseDuration,
			LeaseRenewEvery:   leaseRenewEvery,
			PollInterval:      poll,
			GitHubToken:       os.Getenv("GITHUB_TOKEN"),
			GitLabToken:       os.Getenv("GITLAB_TOKEN"),
		},
		AgentTask: AgentTaskConfig{
			AdapterCallbackSecret: strings.TrimSpace(os.Getenv("AGENT_TASK_ADAPTER_SECRET")),
			LeaseDuration:         agentTaskLeaseDuration,
			GitHubPublicBaseURL:   strings.TrimSuffix(strings.TrimSpace(os.Getenv("AGENT_GITHUB_PUBLIC_BASE_URL")), "/"),
			GitLabPublicBaseURL:   strings.TrimSuffix(strings.TrimSpace(os.Getenv("AGENT_GITLAB_PUBLIC_BASE_URL")), "/"),
		},
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("CONTROL_DATABASE_URL is required")
	}
	if !trustedProviderAPIURL(c.GitHub.APIURL) {
		return Config{}, fmt.Errorf("GITHUB_API_URL must be an HTTPS API URL without credentials, query, or fragment")
	}
	if c.GitLab.AllowHTTP && c.Environment != "development" {
		return Config{}, fmt.Errorf("GITLAB_ALLOW_HTTP is only allowed in development")
	}
	if !trustedGitLabAPIURL(c.GitLab.APIURL, c.GitLab.AllowHTTP) {
		return Config{}, fmt.Errorf("GITLAB_API_URL must be an HTTPS API URL without credentials, query, or fragment (development HTTP requires GITLAB_ALLOW_HTTP=true)")
	}
	if c.ConsoleURL != "" && !trustedConsoleURL(c.ConsoleURL, c.Environment) {
		return Config{}, fmt.Errorf("OPEN_REVIEW_APP_URL must be a public HTTPS URL without credentials, query, or fragment (HTTP is allowed only for local development)")
	}
	if c.AgentTask.GitLabPublicBaseURL != "" && !trustedConsoleURL(c.AgentTask.GitLabPublicBaseURL, c.Environment) {
		return Config{}, fmt.Errorf("AGENT_GITLAB_PUBLIC_BASE_URL must be a public HTTPS URL (local development may use loopback HTTP)")
	}
	if c.AgentTask.GitHubPublicBaseURL != "" && !trustedProviderAPIURL(c.AgentTask.GitHubPublicBaseURL) {
		return Config{}, fmt.Errorf("AGENT_GITHUB_PUBLIC_BASE_URL must be an HTTPS URL without credentials, query, or fragment")
	}
	if (c.GitLab.OAuthClientID == "") != (c.GitLab.OAuthClientSecret == "") {
		return Config{}, fmt.Errorf("GITLAB_OAUTH_CLIENT_ID and GITLAB_OAUTH_CLIENT_SECRET must be configured together")
	}
	if c.GitLab.OAuthBaseURL != "" && !trustedGitLabAPIURL(c.GitLab.OAuthBaseURL, c.GitLab.AllowHTTP) {
		return Config{}, fmt.Errorf("GITLAB_OAUTH_BASE_URL must be an HTTPS URL without credentials, query, or fragment (development HTTP requires GITLAB_ALLOW_HTTP=true)")
	}
	if c.GitLab.OAuthInternalBaseURL != "" && !trustedGitLabAPIURL(c.GitLab.OAuthInternalBaseURL, c.GitLab.AllowHTTP) {
		return Config{}, fmt.Errorf("GITLAB_OAUTH_INTERNAL_BASE_URL must be an HTTPS URL without credentials, query, or fragment (development HTTP requires GITLAB_ALLOW_HTTP=true)")
	}
	return c, nil
}

// ValidateControlAPI applies the credentials and identity requirements that
// belong to the public ingress only. Background workers deliberately load the
// same transport defaults without inheriting a Casdoor client or webhook
// secrets they never read.
func (c Config) ValidateControlAPI() error {
	if c.Auth.Mode != "oidc" && c.Auth.Mode != "development" {
		return fmt.Errorf("AUTH_MODE must be oidc or development")
	}
	if c.Auth.Mode == "development" && c.Environment != "development" {
		return fmt.Errorf("AUTH_MODE=development is only allowed when ENVIRONMENT=development")
	}
	if c.Auth.Mode == "oidc" && (c.Auth.Issuer == "" || c.Auth.Audience == "") {
		return fmt.Errorf("CASDOOR_ISSUER and CASDOOR_AUDIENCE are required for OIDC")
	}
	if c.Environment != "development" && c.GitHub.Secret == "" {
		return fmt.Errorf("GITHUB_WEBHOOK_SECRET is required outside development")
	}
	if c.Environment != "development" && len(c.ProviderAuthorizationSecret) < 32 {
		return fmt.Errorf("OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET (or OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET) must be at least 32 characters outside development")
	}
	if c.Environment != "development" && len(c.GitLabInstallationIdentitySecret) < 32 {
		return fmt.Errorf("OPEN_REVIEW_GITLAB_INSTALLATION_IDENTITY_SECRET must be at least 32 characters when GitLab onboarding is enabled outside development")
	}
	if c.AgentTask.AdapterCallbackSecret != "" && len(c.AgentTask.AdapterCallbackSecret) < 32 {
		return fmt.Errorf("AGENT_TASK_ADAPTER_SECRET must be at least 32 characters when configured")
	}
	return nil
}

func trustedProviderAPIURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func trustedGitLabAPIURL(value string, allowHTTP bool) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme == "https" || (allowHTTP && parsed.Scheme == "http")
}

func trustedConsoleURL(value, environment string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" || environment != "development" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func firstSet(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func envBool(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseBool(value)
}
