// Package interaction contains provider-neutral command parsing and the
// bounded provider read used to turn an explicit first-review command into an
// exact-revision admission request.
package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

// AdmissionRequest is intentionally the minimum durable command context. It
// holds identifiers and deployment-owned credential reference metadata, never
// a provider token or raw provider response.
type AdmissionRequest struct {
	InteractionID uuid.UUID
	TenantID      uuid.UUID
	Event         domain.CommentEvent
	Mode          domain.ReviewMode
	RuleSetID     *uuid.UUID
	ActorSubject  string
	CredentialRef string
}

// Admitter reads the current immutable provider revision immediately before a
// first command-triggered run is admitted. The webhook path itself remains a
// fast, durable acknowledgement path and never performs this network call.
type Admitter struct {
	Resolver   credentials.Resolver
	HTTPClient *http.Client
	Now        func() time.Time
}

func (a Admitter) Resolve(ctx context.Context, request AdmissionRequest) (domain.InboundEvent, error) {
	if request.InteractionID == uuid.Nil || !request.Event.Provider.Valid() || strings.TrimSpace(request.Event.APIBaseURL) == "" || strings.TrimSpace(request.Event.InstallationExternalID) == "" || strings.TrimSpace(request.Event.Repository) == "" || strings.TrimSpace(request.Event.CloneURL) == "" || request.Event.ReviewNumber < 1 || strings.TrimSpace(request.ActorSubject) == "" || strings.TrimSpace(request.CredentialRef) == "" || !request.Mode.Valid() {
		return domain.InboundEvent{}, fmt.Errorf("interaction admission request is invalid")
	}
	token, err := a.Resolver.Resolve(ctx, domain.ReviewJob{
		TenantID:               request.TenantID,
		Provider:               request.Event.Provider,
		APIBaseURL:             request.Event.APIBaseURL,
		InstallationExternalID: request.Event.InstallationExternalID,
		CredentialRef:          request.CredentialRef,
		Repository:             request.Event.Repository,
		ReviewNumber:           request.Event.ReviewNumber,
	})
	if err != nil {
		return domain.InboundEvent{}, fmt.Errorf("resolve interaction provider credential: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return domain.InboundEvent{}, fmt.Errorf("interaction provider credential is empty")
	}

	var event domain.InboundEvent
	switch request.Event.Provider {
	case domain.ProviderGitHub:
		event, err = a.githubReview(ctx, request, token)
	case domain.ProviderGitLab:
		event, err = a.gitLabReview(ctx, request, token)
	default:
		err = fmt.Errorf("interaction provider %q is unsupported", request.Event.Provider)
	}
	if err != nil {
		return domain.InboundEvent{}, err
	}
	event.DeliveryID = "interaction-admission:" + request.InteractionID.String()
	event.EventName = "interaction_admission"
	event.InstallationExternalID = request.Event.InstallationExternalID
	event.Repository = request.Event.Repository
	event.CloneURL = request.Event.CloneURL
	event.ReviewNumber = request.Event.ReviewNumber
	event.TriggerKind = "comment"
	event.ActorKind = "user"
	event.ActorSubject = request.ActorSubject
	event.ReviewMode = request.Mode
	event.RuleSetID = request.RuleSetID
	event.Action = "comment"
	// The interaction responder must publish the progress/update comment before
	// an acknowledger can hand this run to the executor. This preserves the
	// visible "received, then executing" contract for first-review commands.
	event.DeferAcknowledgement = true
	event.ReceivedAt = a.now().UTC()
	event.Payload = interactionAdmissionPayload(request, event)
	return event, nil
}

func (a Admitter) githubReview(ctx context.Context, request AdmissionRequest, token string) (domain.InboundEvent, error) {
	endpoint, err := githubPullEndpoint(request.Event.APIBaseURL, request.Event.Repository, request.Event.ReviewNumber)
	if err != nil {
		return domain.InboundEvent{}, err
	}
	var payload struct {
		Title string `json:"title"`
		Draft bool   `json:"draft"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Base struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := a.getJSON(ctx, endpoint, token, domain.ProviderGitHub, &payload); err != nil {
		return domain.InboundEvent{}, fmt.Errorf("read GitHub pull request revision: %w", err)
	}
	if strings.TrimSpace(payload.Base.Ref) == "" || strings.TrimSpace(payload.Base.SHA) == "" || strings.TrimSpace(payload.Head.Ref) == "" || strings.TrimSpace(payload.Head.SHA) == "" {
		return domain.InboundEvent{}, fmt.Errorf("GitHub pull request revision is incomplete")
	}
	labels := make([]string, 0, len(payload.Labels))
	for _, label := range payload.Labels {
		if value := strings.TrimSpace(label.Name); value != "" {
			labels = append(labels, value)
		}
	}
	return domain.InboundEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: request.Event.APIBaseURL,
		BaseRef: payload.Base.Ref, BaseSHA: payload.Base.SHA, HeadRef: payload.Head.Ref, HeadSHA: payload.Head.SHA,
		IsDraft: payload.Draft, Title: strings.TrimSpace(payload.Title), Author: strings.TrimSpace(payload.User.Login), Labels: labels,
	}, nil
}

func (a Admitter) gitLabReview(ctx context.Context, request AdmissionRequest, token string) (domain.InboundEvent, error) {
	endpoint, err := gitLabMergeRequestEndpoint(request.Event.APIBaseURL, request.Event.Repository, request.Event.ReviewNumber)
	if err != nil {
		return domain.InboundEvent{}, err
	}
	var payload struct {
		Title          string `json:"title"`
		Draft          bool   `json:"draft"`
		WorkInProgress bool   `json:"work_in_progress"`
		Author         struct {
			Username string `json:"username"`
		} `json:"author"`
		Labels       []string `json:"labels"`
		TargetBranch string   `json:"target_branch"`
		SourceBranch string   `json:"source_branch"`
		SHA          string   `json:"sha"`
		DiffRefs     struct {
			BaseSHA string `json:"base_sha"`
			HeadSHA string `json:"head_sha"`
		} `json:"diff_refs"`
	}
	if err := a.getJSON(ctx, endpoint, token, domain.ProviderGitLab, &payload); err != nil {
		return domain.InboundEvent{}, fmt.Errorf("read GitLab merge request revision: %w", err)
	}
	headSHA := strings.TrimSpace(payload.SHA)
	if headSHA == "" {
		headSHA = strings.TrimSpace(payload.DiffRefs.HeadSHA)
	}
	if strings.TrimSpace(payload.TargetBranch) == "" || strings.TrimSpace(payload.SourceBranch) == "" || strings.TrimSpace(payload.DiffRefs.BaseSHA) == "" || headSHA == "" {
		return domain.InboundEvent{}, fmt.Errorf("GitLab merge request revision is incomplete")
	}
	labels := make([]string, 0, len(payload.Labels))
	for _, label := range payload.Labels {
		if value := strings.TrimSpace(label); value != "" {
			labels = append(labels, value)
		}
	}
	return domain.InboundEvent{
		Provider: domain.ProviderGitLab, APIBaseURL: request.Event.APIBaseURL,
		BaseRef: payload.TargetBranch, BaseSHA: payload.DiffRefs.BaseSHA, HeadRef: payload.SourceBranch, HeadSHA: headSHA,
		IsDraft: payload.Draft || payload.WorkInProgress, Title: strings.TrimSpace(payload.Title), Author: strings.TrimSpace(payload.Author.Username), Labels: labels,
	}, nil
}

func (a Admitter) getJSON(ctx context.Context, endpoint, token string, provider domain.Provider, output any) error {
	httpClient := a.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create provider metadata request: %w", err)
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	if provider == domain.ProviderGitHub {
		httpRequest.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	response, err := httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("request provider metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("provider metadata returned HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode provider metadata: %w", err)
	}
	return nil
}

func (a Admitter) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func githubPullEndpoint(apiBaseURL, repository string, number int) (string, error) {
	parts := strings.Split(strings.Trim(repository, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || number < 1 {
		return "", fmt.Errorf("GitHub repository or pull request number is invalid")
	}
	base, err := trustedAPIBase(apiBaseURL)
	if err != nil {
		return "", err
	}
	return base + "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/pulls/" + strconv.Itoa(number), nil
}

func gitLabMergeRequestEndpoint(apiBaseURL, repository string, number int) (string, error) {
	if strings.Trim(strings.TrimSpace(repository), "/") == "" || number < 1 {
		return "", fmt.Errorf("GitLab repository or merge request number is invalid")
	}
	base, err := trustedAPIBase(apiBaseURL)
	if err != nil {
		return "", err
	}
	return base + "/projects/" + url.PathEscape(strings.Trim(repository, "/")) + "/merge_requests/" + strconv.Itoa(number), nil
}

func trustedAPIBase(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("provider API base URL is invalid")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("provider API base URL scheme is invalid")
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func interactionAdmissionPayload(request AdmissionRequest, event domain.InboundEvent) json.RawMessage {
	payload, err := json.Marshal(map[string]any{
		"interaction_id": request.InteractionID.String(), "provider": request.Event.Provider,
		"repository": request.Event.Repository, "review_number": request.Event.ReviewNumber,
		"base_ref": event.BaseRef, "base_sha": event.BaseSHA, "head_ref": event.HeadRef, "head_sha": event.HeadSHA,
	})
	if err != nil {
		return nil
	}
	return payload
}

// AdmissionRequestFromPayload reconstructs the intentionally small command
// context emitted by the webhook transaction. It accepts the JSON-decoded
// number representations used by the AMQP outbox without accepting fractional
// or untrusted/missing identifiers.
func AdmissionRequestFromPayload(payload map[string]any) (AdmissionRequest, error) {
	interactionID, err := uuid.Parse(stringPayloadValue(payload, "interaction_id"))
	if err != nil || interactionID == uuid.Nil {
		return AdmissionRequest{}, fmt.Errorf("interaction admission id is invalid")
	}
	provider := domain.Provider(stringPayloadValue(payload, "provider"))
	mode := domain.ReviewMode(stringPayloadValue(payload, "mode"))
	reviewNumber, err := integerPayloadValue(payload, "review_number")
	if err != nil || !provider.Valid() || !mode.Valid() || reviewNumber < 1 {
		return AdmissionRequest{}, fmt.Errorf("interaction admission provider, mode, or review number is invalid")
	}
	event := domain.CommentEvent{
		Provider:               provider,
		APIBaseURL:             stringPayloadValue(payload, "api_base_url"),
		InstallationExternalID: stringPayloadValue(payload, "installation_external_id"),
		Repository:             stringPayloadValue(payload, "repository"),
		CloneURL:               stringPayloadValue(payload, "clone_url"),
		ReviewNumber:           reviewNumber,
		CommentExternalID:      stringPayloadValue(payload, "comment_external_id"),
		ActorExternalID:        stringPayloadValue(payload, "actor_external_id"),
	}
	request := AdmissionRequest{
		InteractionID: interactionID,
		Event:         event,
		Mode:          mode,
		ActorSubject:  stringPayloadValue(payload, "actor_subject"),
		CredentialRef: stringPayloadValue(payload, "credential_ref"),
	}
	if rawTenantID := stringPayloadValue(payload, "tenant_id"); rawTenantID != "" {
		parsedTenantID, tenantErr := uuid.Parse(rawTenantID)
		if tenantErr != nil || parsedTenantID == uuid.Nil {
			return AdmissionRequest{}, fmt.Errorf("interaction admission tenant id is invalid")
		}
		request.TenantID = parsedTenantID
	}
	if rawRuleSetID := stringPayloadValue(payload, "rule_set_id"); rawRuleSetID != "" {
		parsedRuleSetID, ruleSetErr := uuid.Parse(rawRuleSetID)
		if ruleSetErr != nil || parsedRuleSetID == uuid.Nil {
			return AdmissionRequest{}, fmt.Errorf("interaction admission rule set id is invalid")
		}
		request.RuleSetID = &parsedRuleSetID
	}
	if strings.TrimSpace(request.Event.APIBaseURL) == "" || strings.TrimSpace(request.Event.InstallationExternalID) == "" || strings.TrimSpace(request.Event.Repository) == "" || strings.TrimSpace(request.Event.CloneURL) == "" || strings.TrimSpace(request.Event.CommentExternalID) == "" || strings.TrimSpace(request.ActorSubject) == "" || strings.TrimSpace(request.CredentialRef) == "" {
		return AdmissionRequest{}, fmt.Errorf("interaction admission payload is incomplete")
	}
	return request, nil
}

func stringPayloadValue(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func integerPayloadValue(payload map[string]any, key string) (int, error) {
	switch value := payload[key].(type) {
	case float64:
		parsed := int(value)
		if value != float64(parsed) {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return parsed, nil
	case int:
		return value, nil
	case int64:
		return int(value), nil
	default:
		return 0, fmt.Errorf("%s is not a number", key)
	}
}
