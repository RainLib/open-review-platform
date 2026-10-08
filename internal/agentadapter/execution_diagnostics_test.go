package agentadapter

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCodingBudgetDiagnosticsPreserveErrorWithoutLeakingChildData(t *testing.T) {
	secretError := errors.New("untrusted child output with synthetic-secret")
	broker := &modelBroker{}
	broker.requests.Store(9)
	broker.outputTokens.Store(modelBrokerTotalOutputTokens + 1)
	err := fmt.Errorf("run fixed coding-agent profile: %w", codingModelFailure(secretError, broker))
	code, summary := executionFailureResult(err)
	if code != "agent_adapter_model_budget_exhausted" || !strings.Contains(summary, "requests 9") || !strings.Contains(summary, "new approved plan") || strings.Contains(summary, "synthetic-secret") || !errors.Is(err, secretError) {
		t.Fatalf("unsafe diagnostic code=%s summary=%s", code, summary)
	}
	broker.outputTokens.Store(1)
	if codingModelFailure(secretError, broker) != secretError {
		t.Fatal("normal CLI failure mislabeled as model budget")
	}
	code, summary = executionFailureResult(fmt.Errorf("run fixed coding-agent profile: %w", secretError))
	if code != "agent_adapter_execution_failed" || !strings.Contains(summary, "coding_executor") || strings.Contains(summary, "synthetic-secret") {
		t.Fatalf("unsafe fallback %s %s", code, summary)
	}
}

func TestGitStateDiagnosticsUseOnlyFixedMetadataCategories(t *testing.T) {
	for _, tc := range []struct {
		kind gitStateFailureKind
		text string
	}{
		{gitConfigurationChanged, "configuration"}, {gitBranchChanged, "branch"}, {gitBaseRevisionChanged, "base revision"},
	} {
		err := fmt.Errorf("untrusted-output-with-synthetic-secret: %w", &gitStateFailure{kind: tc.kind})
		code, summary := executionFailureResult(err)
		if code != "agent_adapter_git_state_rejected" || !strings.Contains(summary, tc.text) || strings.Contains(summary, "synthetic-secret") {
			t.Fatalf("unsafe metadata diagnostic %s %s", code, summary)
		}
	}
}

func TestModelUpstreamDiagnosticsRetainOnlyTrustedStatus(t *testing.T) {
	secret := errors.New("synthetic-secret-child-and-provider-body")
	for _, status := range []int32{-1, 400, 401, 429, 503} {
		broker := &modelBroker{}
		broker.upstreamStatus.Store(status)
		err := codingModelFailure(secret, broker)
		code, summary := executionFailureResult(err)
		if code != "agent_adapter_model_upstream_failed" || strings.Contains(summary, "synthetic-secret") || !errors.Is(err, secret) {
			t.Fatalf("unsafe upstream diagnostic %s %s", code, summary)
		}
		if status > 0 && !strings.Contains(summary, fmt.Sprintf("HTTP %d", status)) {
			t.Fatal("trusted HTTP status missing")
		}
		broker.upstreamStatus.Store(200)
		if codingModelFailure(secret, broker) != secret || codingModelFailure(nil, broker) != nil {
			t.Fatal("successful upstream or executor mislabeled")
		}
		broker.upstreamStatus.Store(status)
		broker.requests.Store(modelBrokerRequestLimit + 1)
		code, _ = executionFailureResult(codingModelFailure(secret, broker))
		if code != "agent_adapter_model_budget_exhausted" {
			t.Fatal("local budget must take precedence")
		}
	}
}
