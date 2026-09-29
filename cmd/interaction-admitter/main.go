// interaction-admitter converts a previously acknowledged first-review
// command into an exact-revision review run. Provider reads happen here, not
// inside the webhook transaction, so GitHub/GitLab receive a prompt response.
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
	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/interaction"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/store"
)

const (
	interactionAdmissionQueue    = "openreview.interaction.admission.v1"
	interactionAdmissionConsumer = "interaction-admitter-v1"
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
	reporter := &health.Reporter{
		Store: database, WorkerID: health.EnvironmentWorkerID("INTERACTION_ADMITTER_WORKER_ID", "interaction-admitter"),
		Kind: "interaction-admitter", Version: health.EnvironmentBuildVersion(), Capacity: 1,
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
	admitter := interaction.Admitter{Resolver: resolver}

	err = consumer.Consume(ctx, interactionAdmissionQueue, interactionAdmissionConsumer, func(ctx context.Context, body []byte) error {
		done := reporter.BeginTask()
		defer done()
		message, err := messaging.DecodeOutboxMessage(body)
		if err != nil {
			return err
		}
		if message.Topic != "review.interaction.admission" {
			return fmt.Errorf("unexpected interaction admission topic %q", message.Topic)
		}
		return messaging.HandleExactlyOnce(ctx, database, interactionAdmissionConsumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
			request, err := interaction.AdmissionRequestFromPayload(message.Payload)
			if err != nil {
				return err
			}
			event, err := admitter.Resolve(ctx, request)
			if err != nil {
				// Resolver and provider SDK errors are intentionally not logged verbatim:
				// implementations may include request headers or endpoint details.
				slog.Warn("interaction admission could not read provider revision", "interaction_id", request.InteractionID, "provider", request.Event.Provider, "repository", request.Event.Repository, "failure", "provider_revision_unavailable")
				return database.FailInitialInteractionAdmission(ctx, request.InteractionID, request.Event, "Open Review could not read the current pull request revision. Check the connection and try `@openreview review` again.")
			}
			if _, _, err := database.AdmitInitialInteractionReview(ctx, request.InteractionID, request.Event, event); err != nil {
				if errors.Is(err, store.ErrWorkspaceSetupIncomplete) {
					return database.FailInitialInteractionAdmission(ctx, request.InteractionID, request.Event, "workspace setup is incomplete; complete setup in Open Review before requesting a review")
				}
				if errors.Is(err, store.ErrSelectedRuleSetUnavailable) {
					return database.FailInitialInteractionAdmission(ctx, request.InteractionID, request.Event, "the selected rule set is not published and active for this repository and target branch")
				}
				slog.Error("interaction admission could not create review run", "interaction_id", request.InteractionID, "provider", request.Event.Provider, "repository", request.Event.Repository, "failure", "admission_persistence_failed")
				return database.FailInitialInteractionAdmission(ctx, request.InteractionID, request.Event, "Open Review could not queue this review safely. Check the connection and try `@openreview review` again.")
			}
			return nil
		})
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("interaction admitter stopped", "error", err)
	}
}
