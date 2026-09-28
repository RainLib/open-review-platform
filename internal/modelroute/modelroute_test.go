package modelroute

import (
	"context"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestApplyEnvironmentReplacesDeploymentRouteWithoutPersistingElsewhere(t *testing.T) {
	route := domain.ModelRouteConfig{Enabled: true, Provider: "openai-compatible", Protocol: "openai-chat", BaseURL: "https://models.example/v1/chat/completions", Model: "deepseek-v4-flash", CredentialRef: "env://OPEN_REVIEW_MODEL_SECRET_PRIMARY", Effort: "low", MaxPromptTokens: 8000, TokenBudget: 128000, SubtaskTimeoutMinutes: 5, MaxConcurrentRuns: 2}
	ctx := WithExecution(context.Background(), Execution{Route: route, Token: "ephemeral-token"})
	environment := ApplyEnvironment(ctx, []string{"PATH=/usr/bin", "OCR_LLM_TOKEN=old", "OCR_LLM_MODEL=old"})
	joined := strings.Join(environment, "\n")
	for _, expected := range []string{"OCR_LLM_TOKEN=ephemeral-token", "OCR_LLM_MODEL=deepseek-v4-flash", "OCR_USE_ANTHROPIC=false", "PATH=/usr/bin"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("environment missing %q: %s", expected, joined)
		}
	}
	if strings.Contains(joined, "OCR_LLM_TOKEN=old") || strings.Contains(joined, "OCR_LLM_MODEL=old") {
		t.Fatalf("deployment route was not replaced: %s", joined)
	}
}

func TestEnvironmentResolverRestrictsReferences(t *testing.T) {
	resolver := EnvironmentResolver{}
	if _, err := resolver.ResolveModelCredential(context.Background(), "env://HOME"); err == nil {
		t.Fatal("arbitrary environment reference must be rejected")
	}
	t.Setenv("OPEN_REVIEW_MODEL_SECRET_TEST", "secret")
	value, err := resolver.ResolveModelCredential(context.Background(), "env://OPEN_REVIEW_MODEL_SECRET_TEST")
	if err != nil || value != "secret" {
		t.Fatalf("value=%q error=%v", value, err)
	}
}

func TestEnvironmentResolverFallsBackToLegacyPrimarySecretOnly(t *testing.T) {
	t.Setenv("OPEN_REVIEW_MODEL_SECRET_PRIMARY", "")
	t.Setenv("OCR_LLM_TOKEN", "legacy-primary")
	resolver := EnvironmentResolver{}

	value, err := resolver.ResolveModelCredential(context.Background(), "env://OPEN_REVIEW_MODEL_SECRET_PRIMARY")
	if err != nil || value != "legacy-primary" {
		t.Fatalf("value=%q error=%v", value, err)
	}
	if _, err := resolver.ResolveModelCredential(context.Background(), "env://OPEN_REVIEW_MODEL_SECRET_SECONDARY"); err == nil {
		t.Fatal("legacy token must not satisfy a non-primary credential reference")
	}
}
