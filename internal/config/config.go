package config

import (
	"fmt"
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
	GitLab      WebhookConfig
	Broker      BrokerConfig
	Runner      RunnerConfig
}

type AuthConfig struct {
	Mode     string
	Issuer   string
	Audience string
}

type WebhookConfig struct {
	Secret string
}

type GitHubConfig struct {
	Secret         string
	AppID          string
	PrivateKeyPath string
	APIURL         string
}

type BrokerConfig struct {
	URL      string
	Exchange string
	RelayID  string
}

type RunnerConfig struct {
	ID              string
	GitBinary       string
	OCRBinary       string
	OCRVersion      string
	OCRConcurrency  int
	RiskReviewMode  string
	CheckoutTimeout time.Duration
	OCRTimeout      time.Duration
	PollInterval    time.Duration
	GitHubToken     string
	GitLabToken     string
}

func Load() (Config, error) {
	pollInterval := env("RUNNER_POLL_INTERVAL", "5s")
	poll, err := time.ParseDuration(pollInterval)
	if err != nil || poll <= 0 {
		return Config{}, fmt.Errorf("RUNNER_POLL_INTERVAL must be a positive duration")
	}
	ocrConcurrency, err := envInt("OCR_CONCURRENCY", 0)
	if err != nil || ocrConcurrency < 0 {
		return Config{}, fmt.Errorf("OCR_CONCURRENCY must be a non-negative integer")
	}
	riskReviewMode := env("RISK_REVIEW_MODE", "focused")
	if riskReviewMode != "standard" && riskReviewMode != "focused" && riskReviewMode != "critical" {
		return Config{}, fmt.Errorf("RISK_REVIEW_MODE must be standard, focused, or critical")
	}
	ocrTimeout, err := time.ParseDuration(env("OCR_TIMEOUT", "15m"))
	if err != nil || ocrTimeout <= 0 {
		return Config{}, fmt.Errorf("OCR_TIMEOUT must be a positive duration")
	}
	checkoutTimeout, err := time.ParseDuration(env("CHECKOUT_TIMEOUT", "2m"))
	if err != nil || checkoutTimeout <= 0 {
		return Config{}, fmt.Errorf("CHECKOUT_TIMEOUT must be a positive duration")
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
		GitLab: WebhookConfig{Secret: os.Getenv("GITLAB_WEBHOOK_SECRET")},
		Broker: BrokerConfig{
			URL:      env("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
			Exchange: env("RABBITMQ_EXCHANGE", "openreview.events"),
			RelayID:  env("OUTBOX_RELAY_ID", "relay-1"),
		},
		Runner: RunnerConfig{
			ID:              env("RUNNER_ID", "runner-1"),
			GitBinary:       env("GIT_BINARY", "git"),
			OCRBinary:       env("OCR_BINARY", "ocr"),
			OCRVersion:      env("OCR_VERSION", "1.12.4"),
			OCRConcurrency:  ocrConcurrency,
			RiskReviewMode:  riskReviewMode,
			CheckoutTimeout: checkoutTimeout,
			OCRTimeout:      ocrTimeout,
			PollInterval:    poll,
			GitHubToken:     os.Getenv("GITHUB_TOKEN"),
			GitLabToken:     os.Getenv("GITLAB_TOKEN"),
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
	return c, nil
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
