package config

import "testing"

func loadControlAPIConfig() error {
	configuration, err := Load()
	if err != nil {
		return err
	}
	return configuration.ValidateControlAPI()
}

func TestDevelopmentAuthIsRejectedOutsideDevelopment(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("AUTH_MODE", "development")
	if err := loadControlAPIConfig(); err == nil {
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
	if err := loadControlAPIConfig(); err == nil {
		t.Fatal("GitHub webhook secret must be required in production")
	}
}

func TestGitHubWebhookSecretIsOptionalForLocalDevelopment(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")
	if err := loadControlAPIConfig(); err != nil {
		t.Fatalf("development config should be allowed without a GitHub secret: %v", err)
	}
}

func TestProductionControlAPIRequiresProviderAuthorizationReceiptSecret(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("CASDOOR_ISSUER", "https://casdoor.example.com")
	t.Setenv("CASDOOR_AUDIENCE", "open-review-platform")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET", "")
	t.Setenv("OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET", "")
	if err := loadControlAPIConfig(); err == nil {
		t.Fatal("production control API must require the provider authorization receipt secret")
	}

	t.Setenv("OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET", "0123456789abcdef0123456789abcdef")
	if err := loadControlAPIConfig(); err != nil {
		t.Fatalf("production control API with a receipt secret should be valid: %v", err)
	}
}

func TestWorkerConfigDoesNotRequireIngressIdentityOrWebhookSecret(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("CASDOOR_ISSUER", "")
	t.Setenv("CASDOOR_AUDIENCE", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")
	t.Setenv("GITHUB_API_URL", "https://api.github.com")
	t.Setenv("GITLAB_API_URL", "https://gitlab.com/api/v4")
	configuration, err := Load()
	if err != nil {
		t.Fatalf("worker configuration should only need transport-safe settings: %v", err)
	}
	if err := configuration.ValidateControlAPI(); err == nil {
		t.Fatal("public control API must still require OIDC and webhook configuration")
	}
}

func TestProviderAPIURLsMustBeTrustedHTTPSDestinations(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "GitHub HTTP", key: "GITHUB_API_URL", value: "http://github.example.com/api/v3"},
		{name: "GitLab user info", key: "GITLAB_API_URL", value: "https://token@gitlab.example.com/api/v4"},
		{name: "GitHub query", key: "GITHUB_API_URL", value: "https://github.example.com/api/v3?redirect=https://attacker.invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
			t.Setenv("ENVIRONMENT", "development")
			t.Setenv("AUTH_MODE", "development")
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatalf("%s must be rejected", test.key)
			}
		})
	}
}

func TestConsoleURLMustBePublicAndUsesLocalHTTPOnlyInDevelopment(t *testing.T) {
	configure := func(t *testing.T, environment, appURL string) {
		t.Helper()
		t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
		t.Setenv("ENVIRONMENT", environment)
		t.Setenv("AUTH_MODE", "development")
		t.Setenv("OPEN_REVIEW_APP_URL", appURL)
	}

	configure(t, "development", "http://127.0.0.1:3110")
	if configuration, err := Load(); err != nil || configuration.ConsoleURL != "http://127.0.0.1:3110" {
		t.Fatalf("local development Console URL should be retained, config=%#v err=%v", configuration, err)
	}

	configure(t, "production", "http://review.example.com")
	if _, err := Load(); err == nil {
		t.Fatal("production Console URL must require HTTPS")
	}

	configure(t, "development", "https://review.example.com?next=https://attacker.invalid")
	if _, err := Load(); err == nil {
		t.Fatal("Console URL with query must be rejected")
	}
}

func TestGitLabOAuthClientConfigurationMustBeCompleteAndTrusted(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("GITLAB_OAUTH_CLIENT_ID", "client")
	t.Setenv("GITLAB_OAUTH_CLIENT_SECRET", "")
	if _, err := Load(); err == nil {
		t.Fatal("GitLab OAuth client id without secret must be rejected")
	}

	t.Setenv("GITLAB_OAUTH_CLIENT_SECRET", "secret")
	t.Setenv("GITLAB_OAUTH_BASE_URL", "https://token@gitlab.example.com")
	if _, err := Load(); err == nil {
		t.Fatal("GitLab OAuth base URL with credentials must be rejected")
	}

	t.Setenv("GITLAB_OAUTH_BASE_URL", "https://gitlab.example.com/gitlab")
	if _, err := Load(); err != nil {
		t.Fatalf("complete trusted GitLab OAuth configuration should be accepted: %v", err)
	}

	t.Setenv("GITLAB_OAUTH_INTERNAL_BASE_URL", "https://token@gitlab.internal")
	if _, err := Load(); err == nil {
		t.Fatal("GitLab OAuth internal base URL with credentials must be rejected")
	}

	t.Setenv("GITLAB_OAUTH_INTERNAL_BASE_URL", "https://gitlab.internal/gitlab")
	if _, err := Load(); err != nil {
		t.Fatalf("trusted GitLab OAuth internal base URL should be accepted: %v", err)
	}
}

func TestAgentGitLabPublicBaseRequiresBrowserReachableOrigin(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("AGENT_GITLAB_PUBLIC_BASE_URL", "http://127.0.0.1:8929/gitlab")
	configuration, err := Load()
	if err != nil || configuration.AgentTask.GitLabPublicBaseURL != "http://127.0.0.1:8929/gitlab" {
		t.Fatalf("local public GitLab URL rejected: %#v, %v", configuration.AgentTask, err)
	}
	t.Setenv("AGENT_GITLAB_PUBLIC_BASE_URL", "http://gitlab:8929")
	if _, err := Load(); err == nil {
		t.Fatal("internal Docker hostname was accepted as a public browser URL")
	}
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("AGENT_GITLAB_PUBLIC_BASE_URL", "http://127.0.0.1:8929")
	if _, err := Load(); err == nil {
		t.Fatal("plaintext production public GitLab URL was accepted")
	}
	t.Setenv("AGENT_GITLAB_PUBLIC_BASE_URL", "https://gitlab.example.com")
	if _, err := Load(); err != nil {
		t.Fatalf("HTTPS public GitLab URL rejected: %v", err)
	}
}

func TestAgentGitHubPublicBaseRequiresHTTPS(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("AGENT_GITHUB_PUBLIC_BASE_URL", "http://127.0.0.1:8929")
	if _, err := Load(); err == nil {
		t.Fatal("plaintext GitHub PR base was accepted")
	}
	t.Setenv("AGENT_GITHUB_PUBLIC_BASE_URL", "https://user:password@ghe.example")
	if _, err := Load(); err == nil {
		t.Fatal("GitHub PR base with credentials was accepted")
	}
	t.Setenv("AGENT_GITHUB_PUBLIC_BASE_URL", "https://ghe.example")
	if configuration, err := Load(); err != nil || configuration.AgentTask.GitHubPublicBaseURL != "https://ghe.example" {
		t.Fatalf("trusted GitHub PR base rejected: %#v, %v", configuration.AgentTask, err)
	}
}

func TestGitLabHTTPRequiresExplicitDevelopmentOptIn(t *testing.T) {
	configure := func(t *testing.T, environment, allowHTTP string) {
		t.Helper()
		t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
		t.Setenv("ENVIRONMENT", environment)
		t.Setenv("AUTH_MODE", "development")
		t.Setenv("GITLAB_API_URL", "http://gitlab:8929/api/v4")
		t.Setenv("GITLAB_ALLOW_HTTP", allowHTTP)
	}

	configure(t, "development", "false")
	if _, err := Load(); err == nil {
		t.Fatal("development HTTP GitLab must require an explicit opt-in")
	}

	configure(t, "development", "true")
	if configuration, err := Load(); err != nil || !configuration.GitLab.AllowHTTP {
		t.Fatalf("explicit development GitLab HTTP must be retained, config=%#v err=%v", configuration.GitLab, err)
	}

	configure(t, "production", "true")
	if _, err := Load(); err == nil {
		t.Fatal("production GitLab HTTP must be rejected")
	}
}

func TestGitLabDeploymentTokenAvailabilityMustBeExplicitBoolean(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("GITLAB_DEPLOYMENT_TOKEN_CONFIGURED", "not-a-boolean")
	if _, err := Load(); err == nil {
		t.Fatal("GitLab deployment token availability must reject an invalid boolean")
	}

	t.Setenv("GITLAB_DEPLOYMENT_TOKEN_CONFIGURED", "true")
	configuration, err := Load()
	if err != nil {
		t.Fatalf("explicit GitLab deployment token availability should be accepted: %v", err)
	}
	if !configuration.GitLab.DeploymentTokenAvailable {
		t.Fatal("GitLab deployment token availability was not retained")
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

func TestReviewExecutionPolicyMustBeValid(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "unknown effort", key: "OCR_REVIEW_EFFORT", value: "fast"},
		{name: "negative prompt tokens", key: "OCR_MAX_PROMPT_TOKENS", value: "-1"},
		{name: "negative token budget", key: "OCR_MAX_TOKENS_BUDGET", value: "-1"},
		{name: "negative subtask timeout", key: "OCR_SUBTASK_TIMEOUT_MINUTES", value: "-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
			t.Setenv("ENVIRONMENT", "development")
			t.Setenv("AUTH_MODE", "development")
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatalf("%s must be rejected", test.key)
			}
		})
	}
}

func TestReviewExecutionPolicyUsesBoundedDefaultsForOlderDeployments(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("OCR_CONCURRENCY", "")
	t.Setenv("OCR_REVIEW_EFFORT", "")
	t.Setenv("OCR_MAX_PROMPT_TOKENS", "")
	t.Setenv("OCR_MAX_TOKENS_BUDGET", "")
	t.Setenv("OCR_SUBTASK_TIMEOUT_MINUTES", "")
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Runner.OCRConcurrency != 2 || configuration.Runner.OCREffort != "low" || configuration.Runner.OCRMaxTokens != 8000 || configuration.Runner.OCRTokenBudget != 128000 || configuration.Runner.OCRSubtaskTimeout != 5 {
		t.Fatalf("unexpected bounded OCR defaults: %#v", configuration.Runner)
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
