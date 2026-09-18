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
	Auth        AuthConfig
	GitHub      GitHubConfig
	GitLab      GitLabConfig
	Broker      BrokerConfig
	Runner      RunnerConfig
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
	c := Config{
		DatabaseURL: os.Getenv("CONTROL_DATABASE_URL"),
		HTTPAddress: env("HTTP_ADDRESS", ":8080"),
		Environment: env("ENVIRONMENT", "development"),
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
			Secret: os.Getenv("GITLAB_WEBHOOK_SECRET"),
			APIURL: env("GITLAB_API_URL", "https://gitlab.com/api/v4"),
		},
		Broker: BrokerConfig{
			URL:      env("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
			Exchange: env("RABBITMQ_EXCHANGE", "openreview.events"),
			RelayID:  env("OUTBOX_RELAY_ID", "relay-1"),
		},
		Runner: RunnerConfig{
			ID:                env("RUNNER_ID", "runner-1"),
			GitBinary:         env("GIT_BINARY", "git"),
			OCRBinary:         env("OCR_BINARY", "ocr"),
			OCRVersion:        env("OCR_VERSION", "1.12.4"),
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
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("CONTROL_DATABASE_URL is required")
	}
	if c.Auth.Mode != "oidc" && c.Auth.Mode != "development" {
		return Config{}, fmt.Errorf("AUTH_MODE must be oidc or development")
	}
	if c.Auth.Mode == "development" && c.Environment != "development" {
		return Config{}, fmt.Errorf("AUTH_MODE=development is only allowed when ENVIRONMENT=development")
	}
	if c.Auth.Mode == "oidc" && (c.Auth.Issuer == "" || c.Auth.Audience == "") {
		return Config{}, fmt.Errorf("CASDOOR_ISSUER and CASDOOR_AUDIENCE are required for OIDC")
	}
	if c.Environment != "development" && c.GitHub.Secret == "" {
		return Config{}, fmt.Errorf("GITHUB_WEBHOOK_SECRET is required outside development")
	}
	if !trustedProviderAPIURL(c.GitHub.APIURL) {
		return Config{}, fmt.Errorf("GITHUB_API_URL must be an HTTPS API URL without credentials, query, or fragment")
	}
	if !trustedProviderAPIURL(c.GitLab.APIURL) {
		return Config{}, fmt.Errorf("GITLAB_API_URL must be an HTTPS API URL without credentials, query, or fragment")
	}
	return c, nil
}

func trustedProviderAPIURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
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
