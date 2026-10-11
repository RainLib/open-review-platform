package agentadapter

import (
	"errors"
	"fmt"
)

// Keep broker and provider error bodies out of diagnostics while preserving
// the phase that needs operator recovery before another execution attempt.
type repositoryCredentialFailure struct{ cause error }

type campaignDeploymentScopeFailure struct{}

func (*campaignDeploymentScopeFailure) Error() string {
	return "approved campaign paths are outside the deployment write allowlist"
}

func (e *repositoryCredentialFailure) Error() string {
	return "repository credential issuance unavailable"
}
func (e *repositoryCredentialFailure) Unwrap() error { return e.cause }

// Model budget diagnostics contain only adapter-owned counters. Child output,
// upstream response bodies, prompts and credentials never enter a callback.
type modelBudgetExhaustionError struct {
	cause                        error
	requests                     int32
	reservedTokens, requestBytes int64
}

// Only a transport-owned status is retained; no provider body or child output.
type modelUpstreamFailure struct {
	cause     error
	status    int32
	errorType string
}

func (e *modelUpstreamFailure) Error() string { return "coding model upstream failed" }
func (e *modelUpstreamFailure) Unwrap() error { return e.cause }

type gitStateFailureKind uint8

const (
	gitConfigurationChanged gitStateFailureKind = iota + 1
	gitBranchChanged
	gitBaseRevisionChanged
)

type gitStateFailure struct{ kind gitStateFailureKind }

func (e *gitStateFailure) Error() string {
	switch e.kind {
	case gitConfigurationChanged:
		return "repository configuration changed or unavailable"
	case gitBranchChanged:
		return "repository branch changed or unavailable"
	case gitBaseRevisionChanged:
		return "repository base revision changed or unavailable"
	default:
		return "repository metadata changed or unavailable"
	}
}

func (e *modelBudgetExhaustionError) Error() string { return "coding model job budget exhausted" }
func (e *modelBudgetExhaustionError) Unwrap() error { return e.cause }

func codingModelFailure(err error, broker *modelBroker) error {
	if broker == nil {
		return err
	}
	if err == nil {
		if broker.blockedUpstream().status == 0 {
			return nil
		}
		// A CLI exiting successfully cannot turn a terminal broker failure into
		// a delivery. Publication still requires a completed coding phase.
		err = errors.New("coding stopped by terminal model broker failure")
	}
	requests, tokens, bytes := broker.requests.Load(), broker.outputTokens.Load(), broker.requestBytes.Load()
	if requests <= modelBrokerRequestLimit && tokens <= modelBrokerTotalOutputTokens && bytes <= modelBrokerTotalRequestBytes {
		observation := broker.lastUpstream()
		status := int32(observation.status)
		if status == 0 {
			status = broker.upstreamStatus.Load()
		}
		if status == -1 || status >= 300 && status <= 599 {
			return &modelUpstreamFailure{cause: err, status: status, errorType: observation.errorType}
		}
		return err
	}
	return &modelBudgetExhaustionError{cause: err, requests: requests, reservedTokens: tokens, requestBytes: bytes}
}

func executionFailureResult(err error) (string, string) {
	var scope *campaignDeploymentScopeFailure
	if errors.As(err, &scope) {
		return "agent_campaign_deployment_scope_unavailable", "The approved campaign write paths are outside the deployment-owned path allowlist. Configure the approved paths before approving a retry plan. Coding and provider publication were not started."
	}
	var credential *repositoryCredentialFailure
	if errors.As(err, &credential) {
		return "agent_repository_credential_unavailable", "The repository coding credential could not be issued or refreshed. Check the deployment-owned coding installation mapping, repository authorization and current task lease before approving a retry plan. No completed delivery receipt was produced."
	}
	var budget *modelBudgetExhaustionError
	if errors.As(err, &budget) {
		return "agent_adapter_model_budget_exhausted", fmt.Sprintf("Coding stopped at the fixed model budget (requests %d, output reservation %d tokens, input %d bytes). No completed delivery receipt was produced. Retry requires a new approved plan.", budget.requests, budget.reservedTokens, budget.requestBytes)
	}
	var gitState *gitStateFailure
	if errors.As(err, &gitState) {
		return "agent_adapter_git_state_rejected", "The isolated result was rejected: " + gitState.Error() + ". The coding executor must leave Git configuration, branch and base commit unchanged; the trusted adapter owns commits and publication. No completed delivery receipt was produced. Retry requires a new approved plan."
	}
	var upstream *modelUpstreamFailure
	if errors.As(err, &upstream) {
		status := "a transport failure"
		if upstream.status >= 300 && upstream.status <= 599 {
			status = fmt.Sprintf("HTTP %d", upstream.status)
		}
		if upstream.errorType != "" {
			status += " (" + upstream.errorType + ")"
		}
		next := "Check the provider before approving a new retry plan."
		if upstream.status == 401 || upstream.status == 403 {
			next = "Correct the model gateway credential, access policy or compatible request route; repeating the same plan cannot resolve this rejection."
		}
		return "agent_adapter_model_upstream_failed", "Coding stopped after the model broker observed " + status + " from its fixed upstream. No completed delivery receipt was produced. " + next
	}
	return "agent_adapter_execution_failed", fmt.Sprintf("The adapter stopped at stage %s without a completed delivery receipt. Review adapter health and the provider before approving a new retry plan.", executionFailureStage(err))
}
