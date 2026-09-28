package modelroute

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// SecretResolver resolves an opaque model credential immediately before the
// OCR child process starts. Implementations must never log or persist the
// returned value.
type SecretResolver interface {
	ResolveModelCredential(context.Context, string) (string, error)
}

// EnvironmentResolver supports self-hosted deployments that mount model keys
// as dedicated environment secrets. Arbitrary process variables cannot be
// read through a control-plane credential reference.
type EnvironmentResolver struct{}

func (EnvironmentResolver) ResolveModelCredential(_ context.Context, reference string) (string, error) {
	const prefix = "env://OPEN_REVIEW_MODEL_SECRET_"
	if !strings.HasPrefix(reference, prefix) {
		return "", fmt.Errorf("credential reference requires an external secret resolver")
	}
	name := strings.TrimPrefix(reference, "env://")
	if name == "" || strings.ContainsAny(name, "=/\\\x00") {
		return "", fmt.Errorf("model credential reference is invalid")
	}
	value, ok := os.LookupEnv(name)
	// Self-hosted deployments created before Models configuration used the
	// runner-only OCR_LLM_TOKEN variable. Preserve that credential boundary for
	// the canonical PRIMARY slot while operators migrate; arbitrary references
	// never gain access to the legacy value.
	if (!ok || value == "") && name == "OPEN_REVIEW_MODEL_SECRET_PRIMARY" {
		value, ok = os.LookupEnv("OCR_LLM_TOKEN")
	}
	if !ok || value == "" {
		return "", fmt.Errorf("model credential %q is not mounted", name)
	}
	return value, nil
}

type Execution struct {
	Route domain.ModelRouteConfig
	Token string
}

// PromptExecution is immutable, control-plane-authored review guidance. It
// travels only in the in-memory runner context and is rendered into a
// runner-owned OCR rule file; it is never an environment variable, credential,
// or persisted model request transcript.
type PromptExecution struct {
	SystemInstruction           string
	RepositoryContext           string
	MaxPromptTokens             int
	AllowRepositoryInstructions bool
}

type executionContextKey struct{}
type promptExecutionContextKey struct{}

func WithExecution(ctx context.Context, execution Execution) context.Context {
	return context.WithValue(ctx, executionContextKey{}, execution)
}

func FromContext(ctx context.Context) (Execution, bool) {
	execution, ok := ctx.Value(executionContextKey{}).(Execution)
	return execution, ok
}

func WithPromptExecution(ctx context.Context, prompt PromptExecution) context.Context {
	return context.WithValue(ctx, promptExecutionContextKey{}, prompt)
}

func PromptExecutionFromContext(ctx context.Context) (PromptExecution, bool) {
	prompt, ok := ctx.Value(promptExecutionContextKey{}).(PromptExecution)
	return prompt, ok
}

// ApplyEnvironment returns a copy with the selected OCR route replacing any
// deployment default. The token exists only in the child environment.
func ApplyEnvironment(ctx context.Context, environment []string) []string {
	execution, ok := FromContext(ctx)
	if !ok || !execution.Route.Enabled {
		return environment
	}
	values := map[string]string{
		"OCR_LLM_URL":       execution.Route.BaseURL,
		"OCR_LLM_TOKEN":     execution.Token,
		"OCR_LLM_MODEL":     execution.Route.Model,
		"OCR_USE_ANTHROPIC": fmt.Sprintf("%t", execution.Route.Protocol == "anthropic-messages"),
	}
	result := make([]string, 0, len(environment)+len(values))
	for _, item := range environment {
		key, _, _ := strings.Cut(item, "=")
		if _, replace := values[key]; !replace {
			result = append(result, item)
		}
	}
	for _, key := range []string{"OCR_LLM_URL", "OCR_LLM_TOKEN", "OCR_LLM_MODEL", "OCR_USE_ANTHROPIC"} {
		result = append(result, key+"="+values[key])
	}
	return result
}
