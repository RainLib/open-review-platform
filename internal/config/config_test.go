package config

import "testing"

func TestDevelopmentAuthIsRejectedOutsideDevelopment(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("AUTH_MODE", "development")
	if _, err := Load(); err == nil {
		t.Fatal("development authentication must not be accepted in production")
	}
}

func TestGitHubWebhookSecretIsRequiredOutsideDevelopment(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("CASDOOR_ISSUER", "https://casdoor.example.com")
	t.Setenv("CASDOOR_AUDIENCE", "open-review-platform")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")
	if _, err := Load(); err == nil {
		t.Fatal("GitHub webhook secret must be required in production")
	}
}

func TestGitHubWebhookSecretIsOptionalForLocalDevelopment(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")
	if _, err := Load(); err != nil {
		t.Fatalf("development config should be allowed without a GitHub secret: %v", err)
	}
}

func TestOCRTimeoutMustBePositiveDuration(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("OCR_TIMEOUT", "0s")
	if _, err := Load(); err == nil {
		t.Fatal("OCR_TIMEOUT must reject a non-positive duration")
	}
}

func TestCheckoutTimeoutMustBePositiveDuration(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("CHECKOUT_TIMEOUT", "0s")
	if _, err := Load(); err == nil {
		t.Fatal("CHECKOUT_TIMEOUT must reject a non-positive duration")
	}
}

func TestMergeGateSeverityMustBeKnown(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("MERGE_GATE_MIN_SEVERITY", "urgent")
	if _, err := Load(); err == nil {
		t.Fatal("MERGE_GATE_MIN_SEVERITY must reject unknown severities")
	}
}
