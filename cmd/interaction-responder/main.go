package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

const (
	interactionQueue    = "openreview.interaction.response.v1"
	interactionConsumer = "interaction-responder-v1"
)

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
	resolver, err := credentials.New(cfg)
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
			if err := responses.PublishInteractionResponse(ctx, response); err != nil {
				return err
			}
			if response.ReleaseRunID != nil {
				return database.ReleaseAcknowledgedRun(ctx, *response.ReleaseRunID)
			}
			return nil
		})
	}); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("interaction responder stopped", "error", err)
	}
}

func responseFromPayload(payload map[string]any) (domain.InteractionResponse, error) {
	provider, ok := payload["provider"].(string)
	if !ok || !domain.Provider(provider).Valid() {
		return domain.InteractionResponse{}, fmt.Errorf("interaction response provider is invalid")
	}
	response := domain.InteractionResponse{
		Provider:               domain.Provider(provider),
		APIBaseURL:             stringValue(payload, "api_base_url"),
		InstallationExternalID: stringValue(payload, "installation_external_id"),
		CredentialRef:          stringValue(payload, "credential_ref"),
		Repository:             stringValue(payload, "repository"),
		CommentExternalID:      stringValue(payload, "comment_external_id"),
		Reaction:               domain.InteractionReaction(stringValue(payload, "reaction")),
		Body:                   stringValue(payload, "body"),
		Marker:                 stringValue(payload, "marker"),
	}
	var err error
	response.ReviewNumber, err = intValue(payload, "review_number")
	if releaseRunID := stringValue(payload, "release_run_id"); releaseRunID != "" {
		parsed, parseErr := uuid.Parse(releaseRunID)
		if parseErr != nil {
			return domain.InteractionResponse{}, fmt.Errorf("interaction response release run id is invalid")
		}
		response.ReleaseRunID = &parsed
	}
	if err != nil || response.APIBaseURL == "" || response.InstallationExternalID == "" || response.CredentialRef == "" || response.Repository == "" || response.Body == "" || response.Marker == "" || response.ReviewNumber < 1 || !response.Reaction.Valid() || (response.Reaction != domain.InteractionReactionNone && response.CommentExternalID == "") {
		return domain.InteractionResponse{}, fmt.Errorf("interaction response payload is invalid")
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
