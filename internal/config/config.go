package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL string
	HTTPAddress string
	Environment string
	Auth        AuthConfig
	GitHub      WebhookConfig
	GitLab      WebhookConfig
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

type RunnerConfig struct {
	ID           string
	OCRBinary    string
	OCRVersion   string
	PollInterval time.Duration
	GitHubToken  string
	GitLabToken  string
}

func Load() (Config, error) {
	pollInterval := env("RUNNER_POLL_INTERVAL", "5s")
	poll, err := time.ParseDuration(pollInterval)
	if err != nil || poll <= 0 {
		return Config{}, fmt.Errorf("RUNNER_POLL_INTERVAL must be a positive duration")
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
		GitHub: WebhookConfig{Secret: os.Getenv("GITHUB_WEBHOOK_SECRET")},
		GitLab: WebhookConfig{Secret: os.Getenv("GITLAB_WEBHOOK_SECRET")},
		Runner: RunnerConfig{
			ID:           env("RUNNER_ID", "runner-1"),
			OCRBinary:    env("OCR_BINARY", "ocr"),
			OCRVersion:   env("OCR_VERSION", "1.12.4"),
			PollInterval: poll,
			GitHubToken:  os.Getenv("GITHUB_TOKEN"),
			GitLabToken:  os.Getenv("GITLAB_TOKEN"),
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
	return c, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
