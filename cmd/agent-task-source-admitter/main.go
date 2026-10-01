// agent-task-source-admitter resolves the exact repository base before a
// human can approve an Agent plan. It is provider-read-only and has no coding
// CLI, repository writer, or adapter credentials.
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

	"github.com/RainLib/open-review-platform/internal/agentdecision"
	"github.com/RainLib/open-review-platform/internal/agentplan"
	"github.com/RainLib/open-review-platform/internal/agenttasksource"
	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

const (
	sourceQueue    = "openreview.agent-task.source.v1"
	sourceConsumer = "agent-task-source-admitter-v1"
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
	reporter := &health.Reporter{Store: database, WorkerID: health.EnvironmentWorkerID("AGENT_TASK_SOURCE_ADMITTER_WORKER_ID", "agent-task-source-admitter"), Kind: "agent-task-source-admitter", Version: health.EnvironmentBuildVersion(), DecisionConfigured: agentdecision.JevConfigured(), Capacity: 1}
	go reporter.Run(ctx)
	credentialResolver, err := credentials.New(cfg, database)
	if err != nil {
		log.Fatal(err)
	}
	consumer, err := messaging.OpenAMQPConsumer(cfg.Broker.URL, cfg.Broker.Exchange)
	if err != nil {
		log.Fatal(err)
	}
	defer consumer.Close()
	resolver := agenttasksource.Resolver{Resolver: credentialResolver, AllowGitLabHTTP: cfg.Environment == "development" && cfg.GitLab.AllowHTTP}
	err = consumer.Consume(ctx, sourceQueue, sourceConsumer, func(ctx context.Context, body []byte) error {
		done := reporter.BeginTask()
		defer done()
		message, err := messaging.DecodeOutboxMessage(body)
		if err != nil {
			return err
		}
		if message.Topic != "agent.task.source.resolve.requested" {
			return fmt.Errorf("unexpected agent task source topic %q", message.Topic)
		}
		return messaging.HandleExactlyOnce(ctx, database, sourceConsumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
			taskID, err := sourceTaskID(message.Payload)
			if err != nil {
				return err
			}
			if taskID != message.AggregateID {
				return fmt.Errorf("agent task source payload does not match its aggregate")
			}
			taskRevision, ok := store.AgentSourceTaskRevision(message.Payload)
			if !ok {
				return fmt.Errorf("agent task source revision is invalid")
			}
			target, err := database.LoadAgentTaskSourceTarget(ctx, taskID)
			if errors.Is(err, store.ErrNoQueuedAgentTask) {
				return nil
			}
			if err != nil {
				return err
			}
			if target.Task.Revision != taskRevision {
				// An old delayed retry cannot evaluate a task reopened by an
				// explicit source retry or superseded by a newer decision.
				return nil
			}
			snapshot, err := resolver.Resolve(ctx, target)
			if err != nil {
				delay, transient := agenttasksource.TransientDelay(err)
				if !transient {
					delay, transient = credentials.TransientDelay(err)
				}
				if transient {
					scheduled, scheduleErr := database.ScheduleAgentTaskSourceRetry(ctx, message, delay)
					if scheduleErr != nil {
						return scheduleErr
					}
					if scheduled {
						slog.Warn("agent task provider read delayed for bounded retry", "task_id", taskID, "provider", target.Task.Provider)
						return nil
					}
				}
				slog.Warn("agent task source capture unavailable", "task_id", taskID, "provider", target.Task.Provider, "repository", target.Task.Repository, "failure", "provider_source_unavailable")
				return database.FailAgentTaskSourceSnapshot(ctx, taskID, "agent_source_snapshot_unavailable", "Open Review could not verify the current Issue revision and repository base commit. Restore the connection and retry source verification in Agent Work; if the Issue changed, submit a new command for its current revision.")
			}
			snapshot.DecisionSignal, err = agentdecision.EvaluateSnapshot(ctx, target.Task, snapshot)
			if err != nil {
				if delay, transient := agentdecision.TransientDelay(err); transient {
					scheduled, scheduleErr := database.ScheduleAgentTaskSourceRetry(ctx, message, delay)
					if scheduleErr != nil {
						return scheduleErr
					}
					if scheduled {
						slog.Warn("agent task decision delayed for bounded retry", "task_id", taskID, "backend", target.Task.DecisionBackend)
						return nil
					}
				}
				slog.Warn("agent task decision unavailable", "task_id", taskID, "backend", target.Task.DecisionBackend, "failure", "decision_backend_unavailable")
				return database.FailAgentTaskSourceSnapshot(ctx, taskID, "agent_decision_unavailable", "Open Review could not obtain a valid decision from the repository's configured backend. No Agent was started. Check the decision service and retry source verification in Agent Work.")
			}
			if target.Task.Workflow.Enabled {
				plan, planErr := agentplan.FromEnvironment().Generate(ctx, target.Task, snapshot)
				if planErr != nil {
					return database.FailAgentTaskSourceSnapshot(ctx, taskID, "agent_plan_unavailable", "Automatic planning could not produce a bounded plan. Check the planner configuration and retry source verification.")
				}
				snapshot.GeneratedPlan = &plan
			}
			_, err = database.RecordAgentTaskSourceSnapshot(ctx, taskID, snapshot)
			return err
		})
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("agent task source admitter stopped", "error", err)
	}
}

func sourceTaskID(payload map[string]any) (uuid.UUID, error) {
	raw, ok := payload["task_id"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return uuid.Nil, fmt.Errorf("agent task source payload task_id is required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse agent task source task_id: %w", err)
	}
	return id, nil
}
