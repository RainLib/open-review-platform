package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

const (
	interactionQueue    = "openreview.interaction.response.v1"
	interactionConsumer = "interaction-responder-v1"
)

type interactionResponsePublisher interface {
	PublishInteractionResponse(context.Context, domain.InteractionResponse) error
	PrepareInteractionResponse(context.Context, domain.InteractionResponse) (func(context.Context) error, error)
}

type interactionResponseStore interface {
	ReleaseInitialInteractionAdmission(context.Context, domain.InteractionAdmission) error
	ReleaseAcknowledgedRun(context.Context, uuid.UUID) error
	WithAgentTaskAcknowledgementFence(context.Context, domain.InteractionResponse, func(context.Context) error) error
	AgentTaskSourceStatusCurrent(context.Context, uuid.UUID, int) (bool, error)
	WithAgentTaskSourcePublicationFence(context.Context, uuid.UUID, int, func(context.Context) error) error
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	database, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	reporter := &health.Reporter{
		Store: database, WorkerID: health.EnvironmentWorkerID("INTERACTION_RESPONDER_WORKER_ID", "interaction-responder"),
		Kind: "interaction-responder", Version: health.EnvironmentBuildVersion(), Capacity: 1,
	}
	go reporter.Run(ctx)
	resolver, err := credentials.New(cfg, database)
	if err != nil {
		log.Fatal(err)
	}
	consumer, err := messaging.OpenAMQPConsumer(cfg.Broker.URL, cfg.Broker.Exchange)
	if err != nil {
		log.Fatal(err)
	}
	defer consumer.Close()
	responses := publisher.NewHTTPWithResolver(resolver)
	if err := consumer.Consume(ctx, interactionQueue, interactionConsumer, func(ctx context.Context, body []byte) error {
		done := reporter.BeginTask()
		defer done()
		message, err := messaging.DecodeOutboxMessage(body)
		if err != nil {
			return err
		}
		if message.Topic != "review.interaction.response" {
			return fmt.Errorf("unexpected interaction response topic %q", message.Topic)
		}
		return messaging.HandleExactlyOnce(ctx, database, interactionConsumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
			response, err := responseFromPayload(message.Payload)
			if err != nil {
				return err
			}
			return publishAndReleaseInteraction(ctx, responses, database, response)
		})
	}); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("interaction responder stopped", "error", err)
	}
}

// publishAndReleaseInteraction is the provider-side ordering barrier. A
// release has no meaning until the visible progress/update comment was
// accepted by the source provider, so every release stays after the publish
// call and inside the same inbox-protected consumer attempt.
func publishAndReleaseInteraction(ctx context.Context, responses interactionResponsePublisher, database interactionResponseStore, response domain.InteractionResponse) error {
	if response.SourceRelease != nil {
		// Resolve credentials only for a still-current task. A stale provider
		// acknowledgement must not be posted after cancellation or retry.
		return database.WithAgentTaskAcknowledgementFence(ctx, response, func(ctx context.Context) error {
			return responses.PublishInteractionResponse(ctx, response)
		})
	}
	if strings.HasPrefix(response.Marker, "open-review-platform:agent-task-source:") {
		taskID, err := uuid.Parse(strings.TrimPrefix(response.Marker, "open-review-platform:agent-task-source:"))
		if err != nil || taskID == uuid.Nil || response.ResourceKind != "issue" || response.Admission != nil || response.ReleaseRunID != nil || response.SourceRelease != nil {
			return fmt.Errorf("agent source status marker or release capability is invalid")
		}
		current, err := database.AgentTaskSourceStatusCurrent(ctx, taskID, response.StatusVersion)
		if err != nil || !current {
			return err
		}
		publish, err := responses.PrepareInteractionResponse(ctx, response)
		if err != nil {
			return err
		}
		return database.WithAgentTaskSourcePublicationFence(ctx, taskID, response.StatusVersion, publish)
	}
	if err := responses.PublishInteractionResponse(ctx, response); err != nil {
		return err
	}
	if response.Admission != nil {
		return database.ReleaseInitialInteractionAdmission(ctx, *response.Admission)
	}
	if response.ReleaseRunID != nil {
		return database.ReleaseAcknowledgedRun(ctx, *response.ReleaseRunID)
	}
	return nil
}

func responseFromPayload(payload map[string]any) (domain.InteractionResponse, error) {
	provider, ok := payload["provider"].(string)
	if !ok || !domain.Provider(provider).Valid() {
		return domain.InteractionResponse{}, fmt.Errorf("interaction response provider is invalid")
	}
	response := domain.InteractionResponse{
		TenantID:               uuid.Nil,
		Provider:               domain.Provider(provider),
		APIBaseURL:             stringValue(payload, "api_base_url"),
		InstallationExternalID: stringValue(payload, "installation_external_id"),
		CredentialRef:          stringValue(payload, "credential_ref"),
		Repository:             stringValue(payload, "repository"),
		ResourceKind:           stringValue(payload, "resource_kind"),
		CommentExternalID:      stringValue(payload, "comment_external_id"),
		Reaction:               domain.InteractionReaction(stringValue(payload, "reaction")),
		ReactionOnly:           payload["reaction_only"] == true,
		Body:                   stringValue(payload, "body"),
		Marker:                 stringValue(payload, "marker"),
	}
	if response.ResourceKind == "" {
		response.ResourceKind = "merge_request"
	}
	if raw, present := payload["reaction_only"]; present {
		if _, ok := raw.(bool); !ok {
			return domain.InteractionResponse{}, fmt.Errorf("interaction response reaction-only flag is invalid")
		}
	}
	if markerSince := stringValue(payload, "marker_since"); markerSince != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, markerSince)
		if parseErr != nil || parsed.IsZero() {
			return domain.InteractionResponse{}, fmt.Errorf("interaction response marker boundary is invalid")
		}
		response.MarkerSince = parsed.UTC()
	}
	if rawTenantID := stringValue(payload, "tenant_id"); rawTenantID != "" {
		parsedTenantID, parseErr := uuid.Parse(rawTenantID)
		if parseErr != nil || parsedTenantID == uuid.Nil {
			return domain.InteractionResponse{}, fmt.Errorf("interaction response tenant id is invalid")
		}
		response.TenantID = parsedTenantID
	}
	var err error
	response.ReviewNumber, err = intValue(payload, "review_number")
	if _, present := payload["status_version"]; present {
		var versionErr error
		response.StatusVersion, versionErr = intValue(payload, "status_version")
		if versionErr != nil || response.StatusVersion < 1 || !strings.HasPrefix(response.Marker, "open-review-platform:agent-task-source:") {
			return domain.InteractionResponse{}, fmt.Errorf("interaction response status version is invalid")
		}
	}
	if releaseRunID := stringValue(payload, "release_run_id"); releaseRunID != "" {
		parsed, parseErr := uuid.Parse(releaseRunID)
		if parseErr != nil {
			return domain.InteractionResponse{}, fmt.Errorf("interaction response release run id is invalid")
		}
		response.ReleaseRunID = &parsed
	}
	if _, hasTask := payload["release_agent_task_source_id"]; hasTask {
		parsed, parseErr := uuid.Parse(stringValue(payload, "release_agent_task_source_id"))
		revision, revisionErr := intValue(payload, "release_agent_task_source_revision")
		if parseErr != nil || parsed == uuid.Nil || revisionErr != nil || revision < 1 {
			return domain.InteractionResponse{}, fmt.Errorf("interaction response agent source release is invalid")
		}
		response.SourceRelease = &domain.AgentTaskSourceRelease{TaskID: parsed, Revision: revision}
	} else if _, hasRevision := payload["release_agent_task_source_revision"]; hasRevision {
		return domain.InteractionResponse{}, fmt.Errorf("interaction response agent source release is incomplete")
	}
	if admissionID := stringValue(payload, "admission_interaction_id"); admissionID != "" {
		parsed, parseErr := uuid.Parse(admissionID)
		if parseErr != nil {
			return domain.InteractionResponse{}, fmt.Errorf("interaction response admission id is invalid")
		}
		mode := domain.ReviewMode(stringValue(payload, "admission_mode"))
		var ruleSetID *uuid.UUID
		if rawRuleSetID := stringValue(payload, "admission_rule_set_id"); rawRuleSetID != "" {
			parsedRuleSetID, ruleSetErr := uuid.Parse(rawRuleSetID)
			if ruleSetErr != nil || parsedRuleSetID == uuid.Nil {
				return domain.InteractionResponse{}, fmt.Errorf("interaction response admission rule set id is invalid")
			}
			ruleSetID = &parsedRuleSetID
		}
		response.Admission = &domain.InteractionAdmission{
			InteractionID: parsed,
			Event: domain.CommentEvent{
				Provider: response.Provider, APIBaseURL: response.APIBaseURL, InstallationExternalID: response.InstallationExternalID,
				Repository: response.Repository, CloneURL: stringValue(payload, "admission_clone_url"), ReviewNumber: response.ReviewNumber,
				CommentExternalID: response.CommentExternalID, ActorExternalID: stringValue(payload, "admission_actor_external_id"),
			},
			Mode: mode, RuleSetID: ruleSetID, ActorSubject: stringValue(payload, "admission_actor_subject"), CredentialRef: response.CredentialRef,
		}
		if response.Admission.InteractionID == uuid.Nil || !response.Admission.Mode.Valid() || response.Admission.Event.CloneURL == "" || response.Admission.Event.ActorExternalID == "" || response.Admission.ActorSubject == "" {
			return domain.InteractionResponse{}, fmt.Errorf("interaction response admission payload is invalid")
		}
	}
	if err != nil || (response.ReleaseRunID != nil && response.Admission != nil) || (response.SourceRelease != nil && (response.ReleaseRunID != nil || response.Admission != nil || response.StatusVersion != 0)) || response.APIBaseURL == "" || response.InstallationExternalID == "" || response.CredentialRef == "" || response.Repository == "" || (!response.ReactionOnly && response.Body == "") || response.Marker == "" || response.ReviewNumber < 1 || (response.ResourceKind != "merge_request" && response.ResourceKind != "issue") || !response.Reaction.Valid() || (response.Reaction != domain.InteractionReactionNone && response.CommentExternalID == "") || (strings.HasPrefix(response.CredentialRef, "secret://provider/gitlab-oauth/") && response.TenantID == uuid.Nil) {
		return domain.InteractionResponse{}, fmt.Errorf("interaction response payload is invalid")
	}
	if response.ReactionOnly {
		interactionID, markerErr := uuid.Parse(strings.TrimPrefix(response.Marker, "open-review-platform:interaction:"))
		if response.ResourceKind != "merge_request" || response.Reaction != domain.InteractionReactionEyes || response.Body != "" || response.CommentExternalID == "" || !strings.HasPrefix(response.Marker, "open-review-platform:interaction:") || markerErr != nil || interactionID == uuid.Nil || response.SourceRelease != nil || response.StatusVersion != 0 {
			return domain.InteractionResponse{}, fmt.Errorf("reaction-only review acknowledgement is invalid")
		}
	}
	if strings.HasPrefix(response.Marker, "open-review-platform:agent-task-source:") {
		taskID, parseErr := uuid.Parse(strings.TrimPrefix(response.Marker, "open-review-platform:agent-task-source:"))
		if parseErr != nil || taskID == uuid.Nil || response.ResourceKind != "issue" || response.Admission != nil || response.ReleaseRunID != nil || response.SourceRelease != nil || response.Reaction != domain.InteractionReactionNone {
			return domain.InteractionResponse{}, fmt.Errorf("agent source status payload is invalid")
		}
	}
	if response.SourceRelease != nil {
		isIssue := false
		if response.ResourceKind == "issue" {
			switch {
			case strings.HasPrefix(response.Marker, "open-review-platform:agent-task:auto:"):
				isIssue = response.Marker == "open-review-platform:agent-task:auto:"+response.SourceRelease.TaskID.String()
			case strings.HasPrefix(response.Marker, "open-review-platform:agent-task:"):
				interactionID, markerErr := uuid.Parse(strings.TrimPrefix(response.Marker, "open-review-platform:agent-task:"))
				isIssue = markerErr == nil && interactionID != uuid.Nil
			}
		}
		feedbackID, feedbackErr := uuid.Parse(strings.TrimPrefix(response.Marker, "open-review-platform:agent-task-feedback:"))
		isFeedback := response.ResourceKind == "merge_request" && strings.HasPrefix(response.Marker, "open-review-platform:agent-task-feedback:") && feedbackErr == nil && feedbackID != uuid.Nil
		if response.TenantID == uuid.Nil || (!isIssue && !isFeedback) {
			return domain.InteractionResponse{}, fmt.Errorf("interaction response agent source marker is invalid")
		}
	}
	return response, nil
}

func stringValue(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func intValue(payload map[string]any, key string) (int, error) {
	switch value := payload[key].(type) {
	case float64:
		parsed := int(value)
		if value != float64(parsed) {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s is not a number", key)
	}
}
