package providerhealth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/httpguard"
	"github.com/RainLib/open-review-platform/internal/providertransport"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

const (
	maxProbeResponseBytes     = 64 << 10
	maxInventoryResponseBytes = 1 << 20
	probeRepositoryPageSize   = 100
	maxInventoryPages         = 10
	maxInventoryDuration      = 20 * time.Second
)

type ProbeStore interface {
	ClaimProviderHealthProbe(context.Context, string, time.Duration, *uuid.UUID) (*domain.ProviderProbeTarget, error)
	CompleteProviderHealthProbe(context.Context, domain.ProviderProbeTarget, domain.ProviderProbeResult, time.Time) error
}

type Client struct {
	Resolver             credentials.Resolver
	HTTPClient           *http.Client
	AllowPrivateNetworks bool
	AllowInsecureHTTP    bool
}

type githubAppConfigurationResolver interface {
	GitHubAppConfiguration(context.Context) (credentials.GitHubAppConfiguration, error)
}

type Processor struct {
	Store       ProbeStore
	Client      Client
	WorkerID    string
	Lease       time.Duration
	ProbeEvery  time.Duration
	TaskStarted func() func()
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	if p.Store == nil || p.Client.Resolver == nil || strings.TrimSpace(p.WorkerID) == "" {
		return false, fmt.Errorf("provider health processor is not configured")
	}
	lease := p.Lease
	if lease <= 0 {
		lease = time.Minute
	}
	target, err := p.Store.ClaimProviderHealthProbe(ctx, p.WorkerID, lease, nil)
	if errors.Is(err, store.ErrNoProviderHealthProbe) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	done := func() {}
	if p.TaskStarted != nil {
		if release := p.TaskStarted(); release != nil {
			done = release
		}
	}
	defer done()
	// The claim cannot be completed after its lease expires. Bound the whole
	// credential, inventory, and scope probe, leaving time to persist a
	// degraded result instead of repeatedly losing the same claim.
	completionReserve := min(5*time.Second, lease/4)
	probeCtx, cancel := context.WithTimeout(ctx, lease-completionReserve)
	result := p.Client.Probe(probeCtx, *target)
	cancel()
	interval := p.ProbeEvery
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if err := p.Store.CompleteProviderHealthProbe(ctx, *target, result, result.ObservedAt.Add(interval)); err != nil {
		return true, err
	}
	return true, nil
}

func (c Client) Probe(ctx context.Context, target domain.ProviderProbeTarget) domain.ProviderProbeResult {
	started := time.Now()
	result := domain.ProviderProbeResult{
		HealthState: domain.HealthCritical,
		ObservedAt:  started.UTC(),
		Permissions: []string{},
		Receipt: map[string]any{
			"schema":   "open-review.provider-health-probe.v1",
			"provider": target.Installation.Provider,
		},
	}
	finish := func() domain.ProviderProbeResult {
		result.LatencyMS = time.Since(started).Milliseconds()
		return result
	}
	endpoint, capability, err := c.endpoint(target.Installation)
	if err != nil {
		result.ErrorCode = "invalid_probe_endpoint"
		result.ErrorMessage = err.Error()
		return finish()
	}
	job := domain.ReviewJob{
		TenantID: target.Installation.TenantID,
		Provider: target.Installation.Provider, APIBaseURL: target.Installation.APIBaseURL,
		CredentialRef:          target.Installation.CredentialRef,
		InstallationExternalID: target.Installation.ExternalID,
	}
	token, err := c.Resolver.Resolve(ctx, job)
	if err != nil {
		result.ErrorCode = "credential_unavailable"
		result.ErrorMessage = err.Error()
		return finish()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		result.ErrorCode = "request_invalid"
		result.ErrorMessage = err.Error()
		return finish()
	}
	request.Header.Set("Accept", "application/json")
	switch target.Installation.Provider {
	case domain.ProviderGitHub:
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	case domain.ProviderGitLab:
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := c.httpClient()
	response, err := client.Do(request)
	if err != nil {
		result.HealthState = domain.HealthDegraded
		result.ErrorCode = "provider_unreachable"
		result.ErrorMessage = err.Error()
		return finish()
	}
	defer response.Body.Close()
	responseLimit := maxProbeResponseBytes
	if target.Installation.Provider == domain.ProviderGitHub {
		responseLimit = maxInventoryResponseBytes
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(responseLimit+1)))
	if readErr != nil {
		result.HealthState = domain.HealthDegraded
		result.ErrorCode = "provider_response_read_failed"
		result.ErrorMessage = readErr.Error()
		return finish()
	}
	if len(body) > responseLimit {
		result.HealthState = domain.HealthDegraded
		result.ErrorCode = "provider_response_too_large"
		result.ErrorMessage = "provider probe response exceeded the bounded response limit"
		return finish()
	}
	result.RateLimitRemaining = parseInt64Header(response.Header, "X-RateLimit-Remaining")
	result.RateLimitLimit = parseInt64Header(response.Header, "X-RateLimit-Limit")
	if reset := parseInt64Header(response.Header, "X-RateLimit-Reset"); reset != nil && *reset > 0 {
		value := time.Unix(*reset, 0).UTC()
		result.RateLimitResetAt = &value
	}
	result.Receipt["status_code"] = response.StatusCode
	result.Receipt["capability"] = capability
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		result.HealthState = domain.HealthLive
		result.Permissions = []string{capability}
		repositories, inventoryState, inventoryErr := c.repositoryInventory(ctx, target.Installation, token, body)
		result.Receipt["inventory_state"] = inventoryState
		result.Repositories = repositories
		if inventoryErr != nil {
			// An authenticated identity/read probe is still valid even when a
			// provider's repository listing is temporarily unavailable. Keep the
			// installation verified, retain the last durable inventory, and make
			// the inventory condition observable without turning this read-only
			// metadata refresh into an admission outage.
			result.Receipt["inventory_error"] = inventoryErr.Error()
		} else {
			if target.Installation.Provider == domain.ProviderGitLab {
				result.Permissions = append(result.Permissions, "repository_inventory:read")
			}
		}
		// A bounded inventory page proves that the credential is valid, but it
		// does not prove that a later page contains the exact repository the
		// workspace declared. Verify each explicit GitHub repository separately
		// before treating the installation as ready for review admission. Wildcard
		// scopes intentionally remain inventory-backed: expanding them here would
		// turn a health probe into an unbounded crawl.
		if target.Installation.Provider == domain.ProviderGitHub {
			verified, verification, verificationErr := c.verifyGitHubExactRepositoryScope(ctx, target.Installation, token)
			result.Receipt["scope_verification"] = verification.State
			if verificationErr != nil {
				result.HealthState = verification.HealthState
				result.ErrorCode = verification.ErrorCode
				result.ErrorMessage = verificationErr.Error()
				return finish()
			}
			result.Repositories = mergeRepositories(result.Repositories, verified)
			c.observeGitHubIssueTriage(ctx, target.Installation, &result)
		}
		if target.Installation.Provider == domain.ProviderGitLab {
			verified, verification, verificationErr := c.verifyGitLabRepositoryScope(ctx, target.Installation, token)
			result.Receipt["scope_verification"] = verification.State
			if verificationErr != nil {
				result.HealthState = verification.HealthState
				result.ErrorCode = verification.ErrorCode
				result.ErrorMessage = verificationErr.Error()
				return finish()
			}
			result.Repositories = mergeRepositories(result.Repositories, verified)
		}
		if result.RateLimitRemaining != nil && *result.RateLimitRemaining == 0 {
			result.HealthState = domain.HealthDegraded
			result.ErrorCode = "rate_limit_exhausted"
			result.ErrorMessage = "provider rate limit is exhausted"
		}
		return finish()
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		result.HealthState = domain.HealthDegraded
		result.ErrorCode = "provider_temporarily_unavailable"
	} else {
		result.ErrorCode = "provider_auth_or_permission_failed"
	}
	result.ErrorMessage = fmt.Sprintf("provider probe returned HTTP %d", response.StatusCode)
	return finish()
}

func (c Client) observeGitHubIssueTriage(ctx context.Context, installation domain.Installation, result *domain.ProviderProbeResult) {
	if result == nil || installation.CredentialRef != "github-app" {
		return
	}
	resolver, ok := c.Resolver.(githubAppConfigurationResolver)
	if !ok {
		return
	}
	state := "unobserved"
	agentState := "unobserved"
	configuration, err := resolver.GitHubAppConfiguration(ctx)
	if err == nil {
		hasIssuesEvent := false
		for _, event := range configuration.Events {
			if event == "issues" {
				hasIssuesEvent = true
			}
		}
		issuesPermission := strings.ToLower(strings.TrimSpace(configuration.Permissions["issues"]))
		switch {
		case issuesPermission != "write":
			state = "missing_write_permission"
		case !hasIssuesEvent:
			state = "missing_event"
		default:
			state = "ready"
		}
		result.Receipt["github_issues_permission"] = issuesPermission
		result.Receipt["github_issues_event"] = hasIssuesEvent
		switch {
		case configuration.Permissions["contents"] != "write":
			agentState = "missing_contents_write"
		case configuration.Permissions["pull_requests"] != "write":
			agentState = "missing_pull_requests_write"
		case issuesPermission != "read" && issuesPermission != "write":
			agentState = "missing_issues_read"
		default:
			agentState = "app_permissions_declared"
		}
		// GitHub Apps do not expose a selectable `reaction` webhook event.
		// Reaction feedback therefore needs a provider-polling path; never tell
		// operators to configure an event that does not exist in the App UI.
		feedbackState := "polling_required"
		result.Receipt["issue_feedback_state"] = feedbackState
		result.Permissions = append(result.Permissions, "issue_feedback:"+feedbackState)
	} else {
		result.Receipt["issue_triage_error_code"] = "app_configuration_unavailable"
	}
	result.Receipt["issue_triage_state"] = state
	// This is only the App-level prerequisite. It does not assert that a
	// repository-scoped token, isolated executor, or provider write succeeded.
	result.Receipt["agent_coding_app_permission_state"] = agentState
	result.Permissions = append(result.Permissions, "agent_coding:"+agentState)
	result.Permissions = append(result.Permissions, "issue_triage:"+state)
}

type scopeVerification struct {
	State       string
	HealthState domain.HealthState
	ErrorCode   string
}

func (c Client) verifyGitHubExactRepositoryScope(ctx context.Context, installation domain.Installation, token string) ([]domain.ProviderRepository, scopeVerification, error) {
	scopes := exactRepositoryScopes(installation.RepositoryScope)
	if len(scopes) == 0 {
		return nil, scopeVerification{State: "inventory_backed", HealthState: domain.HealthLive}, nil
	}
	base, err := c.validProviderAPIBase(installation.APIBaseURL)
	if err != nil {
		return nil, scopeVerification{State: "invalid", HealthState: domain.HealthCritical, ErrorCode: "invalid_probe_endpoint"}, err
	}
	repositories := make([]domain.ProviderRepository, 0, len(scopes))
	for _, scope := range scopes {
		owner, repositoryName, _ := strings.Cut(scope, "/")
		endpoint := *base
		endpoint.Path = strings.TrimSuffix(base.Path, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repositoryName)
		request, err := providerRequest(ctx, &endpoint, domain.ProviderGitHub, token)
		if err != nil {
			return nil, scopeVerification{State: "unavailable", HealthState: domain.HealthDegraded, ErrorCode: "repository_scope_unavailable"}, err
		}
		response, err := c.httpClient().Do(request)
		if err != nil {
			return nil, scopeVerification{State: "unavailable", HealthState: domain.HealthDegraded, ErrorCode: "repository_scope_unavailable"}, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxProbeResponseBytes+1))
		response.Body.Close()
		if readErr != nil {
			return nil, scopeVerification{State: "unavailable", HealthState: domain.HealthDegraded, ErrorCode: "repository_scope_unavailable"}, readErr
		}
		if len(body) > maxProbeResponseBytes {
			return nil, scopeVerification{State: "unavailable", HealthState: domain.HealthDegraded, ErrorCode: "repository_scope_unavailable"}, fmt.Errorf("repository scope probe response exceeded 64 KiB")
		}
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusForbidden {
			return nil, scopeVerification{State: "not_authorized", HealthState: domain.HealthCritical, ErrorCode: "repository_scope_not_authorized"}, fmt.Errorf("provider does not authorize declared repository scope %q", scope)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, scopeVerification{State: "unavailable", HealthState: domain.HealthDegraded, ErrorCode: "repository_scope_unavailable"}, fmt.Errorf("repository scope probe returned HTTP %d for %q", response.StatusCode, scope)
		}
		verifiedRepository, err := parseGitHubRepository(body)
		if err != nil {
			return nil, scopeVerification{State: "invalid", HealthState: domain.HealthCritical, ErrorCode: "repository_scope_invalid"}, err
		}
		if verifiedRepository.Name != scope {
			return nil, scopeVerification{State: "invalid", HealthState: domain.HealthCritical, ErrorCode: "repository_scope_invalid"}, fmt.Errorf("repository scope probe returned %q for declared scope %q", verifiedRepository.Name, scope)
		}
		repositories = append(repositories, verifiedRepository)
	}
	return repositories, scopeVerification{State: "verified", HealthState: domain.HealthLive}, nil
}

// verifyGitLabRepositoryScope does not infer authorization from the first
// ten projects in the general membership feed. It asks GitLab for each exact
// project or group scope under the same configured API origin. Group probes
// request a bounded page with subgroups and accept only paths inside that
// group; no unbounded inventory crawl is needed to establish one accessible
// project for a wildcard scope.
func (c Client) verifyGitLabRepositoryScope(ctx context.Context, installation domain.Installation, token string) ([]domain.ProviderRepository, scopeVerification, error) {
	base, err := c.validProviderAPIBase(installation.APIBaseURL)
	if err != nil {
		return nil, scopeVerification{State: "invalid", HealthState: domain.HealthCritical, ErrorCode: "invalid_probe_endpoint"}, err
	}
	verified := make([]domain.ProviderRepository, 0)
	for _, raw := range strings.Split(installation.RepositoryScope, ",") {
		scope := strings.Trim(strings.TrimSpace(raw), "/")
		if scope == "" || scope == domain.AllAuthorizedRepositoriesScope || !strings.Contains(scope, "/") {
			return nil, scopeVerification{State: "invalid", HealthState: domain.HealthCritical, ErrorCode: "repository_scope_invalid"}, fmt.Errorf("GitLab repository scope is invalid")
		}
		group := strings.HasSuffix(scope, "/*")
		path := scope
		collection := "projects"
		if group {
			path = strings.TrimSuffix(scope, "/*")
			collection = "groups"
		}
		endpoint := *base
		endpoint.Path = strings.TrimSuffix(base.Path, "/") + "/" + collection + "/" + path
		endpoint.RawPath = strings.TrimSuffix(base.EscapedPath(), "/") + "/" + collection + "/" + url.PathEscape(path)
		if group {
			endpoint.Path += "/projects"
			endpoint.RawPath += "/projects"
			query := endpoint.Query()
			query.Set("include_subgroups", "true")
			query.Set("with_shared", "false")
			query.Set("archived", "false")
			query.Set("per_page", strconv.Itoa(probeRepositoryPageSize))
			query.Set("simple", "true")
			endpoint.RawQuery = query.Encode()
		}
		request, err := providerRequest(ctx, &endpoint, domain.ProviderGitLab, token)
		if err != nil {
			return nil, scopeVerification{State: "unavailable", HealthState: domain.HealthDegraded, ErrorCode: "repository_scope_unavailable"}, err
		}
		response, err := c.httpClient().Do(request)
		if err != nil {
			return nil, scopeVerification{State: "unavailable", HealthState: domain.HealthDegraded, ErrorCode: "repository_scope_unavailable"}, err
		}
		responseLimit := maxProbeResponseBytes
		if group {
			responseLimit = maxInventoryResponseBytes
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(responseLimit+1)))
		response.Body.Close()
		if readErr != nil || len(body) > responseLimit {
			return nil, scopeVerification{State: "unavailable", HealthState: domain.HealthDegraded, ErrorCode: "repository_scope_unavailable"}, fmt.Errorf("GitLab scope response is unavailable or too large")
		}
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
			return nil, scopeVerification{State: "not_authorized", HealthState: domain.HealthCritical, ErrorCode: "repository_scope_not_authorized"}, fmt.Errorf("GitLab does not authorize declared repository scope %q", scope)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, scopeVerification{State: "unavailable", HealthState: domain.HealthDegraded, ErrorCode: "repository_scope_unavailable"}, fmt.Errorf("GitLab scope probe returned HTTP %d", response.StatusCode)
		}
		var repositories []domain.ProviderRepository
		if group {
			repositories, err = parseGitLabRepositories(body)
		} else {
			var project providerRepositoryPayload
			err = decodeProviderJSON(body, &project)
			if err == nil {
				repositories = normalizeRepositories([]providerRepositoryPayload{project})
			}
		}
		if err != nil || len(repositories) == 0 {
			return nil, scopeVerification{State: "invalid", HealthState: domain.HealthCritical, ErrorCode: "repository_scope_invalid"}, fmt.Errorf("GitLab scope %q returned no usable project", scope)
		}
		matched := false
		for _, repository := range repositories {
			if repository.Archived || (!group && repository.Name != scope) || (group && !strings.HasPrefix(repository.Name, path+"/")) {
				return nil, scopeVerification{State: "invalid", HealthState: domain.HealthCritical, ErrorCode: "repository_scope_invalid"}, fmt.Errorf("GitLab scope %q returned an unrelated project", scope)
			}
			matched = true
			verified = append(verified, repository)
		}
		if !matched {
			return nil, scopeVerification{State: "not_authorized", HealthState: domain.HealthCritical, ErrorCode: "repository_scope_not_authorized"}, fmt.Errorf("GitLab scope %q has no accessible project", scope)
		}
	}
	return verified, scopeVerification{State: "verified", HealthState: domain.HealthLive}, nil
}

func exactRepositoryScopes(scope string) []string {
	values := make([]string, 0)
	seen := make(map[string]struct{})
	for _, candidate := range strings.Split(scope, ",") {
		candidate = strings.Trim(strings.TrimSpace(candidate), "/")
		if candidate == "" || candidate == domain.AllAuthorizedRepositoriesScope || strings.HasSuffix(candidate, "/*") {
			return nil
		}
		owner, repository, ok := strings.Cut(candidate, "/")
		if !ok || owner == "" || repository == "" || strings.Contains(repository, "/") {
			return nil
		}
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		values = append(values, candidate)
	}
	return values
}

func parseGitHubRepository(body []byte) (domain.ProviderRepository, error) {
	var payload providerRepositoryPayload
	if err := decodeProviderJSON(body, &payload); err != nil {
		return domain.ProviderRepository{}, fmt.Errorf("decode GitHub repository scope: %w", err)
	}
	repositories := normalizeRepositories([]providerRepositoryPayload{payload})
	if len(repositories) != 1 {
		return domain.ProviderRepository{}, fmt.Errorf("GitHub repository scope response is invalid")
	}
	return repositories[0], nil
}

func mergeRepositories(current, additions []domain.ProviderRepository) []domain.ProviderRepository {
	merged := make([]domain.ProviderRepository, 0, len(current)+len(additions))
	seen := make(map[string]struct{}, len(current)+len(additions))
	for _, repository := range append(current, additions...) {
		if repository.ExternalID == "" {
			continue
		}
		if _, duplicate := seen[repository.ExternalID]; duplicate {
			continue
		}
		seen[repository.ExternalID] = struct{}{}
		merged = append(merged, repository)
	}
	return merged
}

// repositoryInventory normalizes a bounded set of provider pages into safe
// selection metadata. The raw payload and credential never cross the worker
// boundary. A full last page or a later-page error is explicitly partial,
// never presented to onboarding as a complete provider inventory.
func (c Client) repositoryInventory(ctx context.Context, installation domain.Installation, token string, firstResponse []byte) ([]domain.ProviderRepository, string, error) {
	ctx, cancel := context.WithTimeout(ctx, maxInventoryDuration)
	defer cancel()

	var repositories []domain.ProviderRepository
	var pageSize int
	var err error
	switch installation.Provider {
	case domain.ProviderGitHub:
		repositories, pageSize, err = parseGitHubRepositoryPage(firstResponse)
		if err != nil {
			return nil, "invalid", err
		}
	case domain.ProviderGitLab:
		body, fetchErr := c.fetchInventoryPage(ctx, installation, token, 1)
		if fetchErr != nil {
			return nil, "unavailable", fetchErr
		}
		repositories, pageSize, err = parseGitLabRepositoryPage(body)
		if err != nil {
			return nil, "invalid", err
		}
	default:
		return nil, "unavailable", fmt.Errorf("provider %q is unsupported", installation.Provider)
	}
	for page := 2; page <= maxInventoryPages && pageSize == probeRepositoryPageSize; page++ {
		body, fetchErr := c.fetchInventoryPage(ctx, installation, token, page)
		if fetchErr != nil {
			return repositories, "partial", fetchErr
		}
		var next []domain.ProviderRepository
		switch installation.Provider {
		case domain.ProviderGitHub:
			next, pageSize, err = parseGitHubRepositoryPage(body)
		case domain.ProviderGitLab:
			next, pageSize, err = parseGitLabRepositoryPage(body)
		}
		if err != nil {
			return repositories, "partial", err
		}
		merged := mergeRepositories(repositories, next)
		if pageSize > 0 && len(merged) == len(repositories) {
			return repositories, "partial", fmt.Errorf("provider inventory repeated page %d", page)
		}
		repositories = merged
	}
	if pageSize == probeRepositoryPageSize {
		return repositories, "partial", fmt.Errorf("provider inventory capped at %d pages", maxInventoryPages)
	}
	return repositories, "synchronized", nil
}

func (c Client) fetchInventoryPage(ctx context.Context, installation domain.Installation, token string, page int) ([]byte, error) {
	var endpoint *url.URL
	var err error
	if installation.Provider == domain.ProviderGitHub {
		endpoint, _, err = c.endpoint(installation)
	} else {
		endpoint, err = c.gitLabInventoryEndpoint(installation)
	}
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("page", strconv.Itoa(page))
	endpoint.RawQuery = query.Encode()
	request, err := providerRequest(ctx, endpoint, installation.Provider, token)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxInventoryResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxInventoryResponseBytes {
		return nil, fmt.Errorf("provider inventory response exceeded 1 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("provider inventory returned HTTP %d", response.StatusCode)
	}
	return body, nil
}

func providerRequest(ctx context.Context, endpoint *url.URL, provider domain.Provider, token string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	if provider == domain.ProviderGitHub {
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	return request, nil
}

func (c Client) gitLabInventoryEndpoint(installation domain.Installation) (*url.URL, error) {
	base, err := c.validProviderAPIBase(installation.APIBaseURL)
	if err != nil {
		return nil, err
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/projects"
	query := base.Query()
	query.Set("membership", "true")
	query.Set("order_by", "id")
	query.Set("per_page", strconv.Itoa(probeRepositoryPageSize))
	query.Set("simple", "true")
	query.Set("sort", "asc")
	base.RawQuery = query.Encode()
	return base, nil
}

func parseGitHubRepositories(body []byte) ([]domain.ProviderRepository, error) {
	repositories, _, err := parseGitHubRepositoryPage(body)
	return repositories, err
}

func parseGitHubRepositoryPage(body []byte) ([]domain.ProviderRepository, int, error) {
	var payload struct {
		Repositories []providerRepositoryPayload `json:"repositories"`
	}
	if err := decodeProviderJSON(body, &payload); err != nil {
		return nil, 0, fmt.Errorf("decode GitHub repository inventory: %w", err)
	}
	return normalizeRepositories(payload.Repositories), len(payload.Repositories), nil
}

func parseGitLabRepositories(body []byte) ([]domain.ProviderRepository, error) {
	repositories, _, err := parseGitLabRepositoryPage(body)
	return repositories, err
}

func parseGitLabRepositoryPage(body []byte) ([]domain.ProviderRepository, int, error) {
	var payload []providerRepositoryPayload
	if err := decodeProviderJSON(body, &payload); err != nil {
		return nil, 0, fmt.Errorf("decode GitLab repository inventory: %w", err)
	}
	return normalizeRepositories(payload), len(payload), nil
}

type providerRepositoryPayload struct {
	ID                json.RawMessage `json:"id"`
	FullName          string          `json:"full_name"`
	PathWithNamespace string          `json:"path_with_namespace"`
	DefaultBranch     string          `json:"default_branch"`
	Visibility        string          `json:"visibility"`
	Private           bool            `json:"private"`
	Archived          bool            `json:"archived"`
}

func decodeProviderJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func normalizeRepositories(payload []providerRepositoryPayload) []domain.ProviderRepository {
	repositories := make([]domain.ProviderRepository, 0, len(payload))
	seen := make(map[string]struct{}, len(payload))
	for _, candidate := range payload {
		externalID := providerRepositoryID(candidate.ID)
		name := strings.TrimSpace(candidate.FullName)
		if name == "" {
			name = strings.TrimSpace(candidate.PathWithNamespace)
		}
		if externalID == "" || name == "" {
			continue
		}
		if _, duplicate := seen[externalID]; duplicate {
			continue
		}
		seen[externalID] = struct{}{}
		visibility := strings.TrimSpace(candidate.Visibility)
		if visibility == "" && candidate.Private {
			visibility = "private"
		}
		repositories = append(repositories, domain.ProviderRepository{
			ExternalID: externalID, Name: name, DefaultBranch: strings.TrimSpace(candidate.DefaultBranch),
			Visibility: visibility, Archived: candidate.Archived,
		})
	}
	return repositories
}

func providerRepositoryID(raw json.RawMessage) string {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return ""
	}
	if strings.HasPrefix(value, "\"") {
		var decoded string
		if json.Unmarshal(raw, &decoded) != nil {
			return ""
		}
		return strings.TrimSpace(decoded)
	}
	return value
}

func (c Client) endpoint(installation domain.Installation) (*url.URL, string, error) {
	base, err := c.validProviderAPIBase(installation.APIBaseURL)
	if err != nil {
		return nil, "", err
	}
	switch installation.Provider {
	case domain.ProviderGitHub:
		base.Path = strings.TrimSuffix(base.Path, "/") + "/installation/repositories"
		base.RawQuery = "per_page=" + strconv.Itoa(probeRepositoryPageSize)
		return base, "repository_inventory:read", nil
	case domain.ProviderGitLab:
		base.Path = strings.TrimSuffix(base.Path, "/") + "/user"
		return base, "authenticated_identity:read", nil
	default:
		return nil, "", fmt.Errorf("provider %q is unsupported", installation.Provider)
	}
}

func (c Client) validProviderAPIBase(raw string) (*url.URL, error) {
	base, err := validProviderAPIBase(raw)
	if err != nil {
		return nil, err
	}
	if base.Scheme != "https" && !(c.AllowInsecureHTTP && base.Scheme == "http") {
		return nil, fmt.Errorf("provider probe requires HTTPS")
	}
	return base, nil
}

func validProviderAPIBase(raw string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("provider API base URL is invalid")
	}
	return base, nil
}

func (c Client) httpClient() *http.Client {
	client := c.HTTPClient
	if client == nil {
		client = providertransport.NewClient(c.AllowPrivateNetworks)
	}
	return httpguard.NoRedirects(client, 20*time.Second)
}

func parseInt64Header(header http.Header, name string) *int64 {
	value := strings.TrimSpace(header.Get(name))
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return nil
	}
	return &parsed
}
