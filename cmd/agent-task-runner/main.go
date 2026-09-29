// agent-task-runner owns the durable admission-to-sandbox boundary. It never
// shells out to a coding CLI on the host: an approved task first receives a
// leased attempt, and a separately configured sandbox adapter must consume
// that attempt before any repository write can occur.
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

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

const (
	agentTaskQueue          = "openreview.agent-task.execute.v1"
	agentTaskCancelQueue    = "openreview.agent-task.cancel.v1"
	agentTaskConsumer       = "agent-task-runner-v1"
	agentTaskCancelConsumer = "agent-task-canceller-v1"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	databaseURL := strings.TrimSpace(os.Getenv("CONTROL_DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("CONTROL_DATABASE_URL is required")
	}
	database, err := store.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	workerID := health.EnvironmentWorkerID("AGENT_TASK_RUNNER_ID", "agent-task-runner")
	reporter := &health.Reporter{Store: database, WorkerID: workerID, Kind: "agent-task-runner", Version: health.EnvironmentBuildVersion(), Capacity: 1}
	consumer, err := messaging.OpenAMQPConsumer(env("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"), env("RABBITMQ_EXCHANGE", "openreview.events"))
	if err != nil {
		log.Fatal(err)
	}
	defer consumer.Close()
	cancellationConsumer, err := messaging.OpenAMQPConsumer(env("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"), env("RABBITMQ_EXCHANGE", "openreview.events"))
	if err != nil {
		log.Fatal(err)
	}
	defer cancellationConsumer.Close()
	lease := durationEnv("AGENT_TASK_LEASE_DURATION", 2*time.Minute)
	if lease < 15*time.Second {
		log.Fatal("AGENT_TASK_LEASE_DURATION must be at least 15s")
	}
	var adapter *agentadapter.Client
	if endpoint := strings.TrimSpace(os.Getenv("AGENT_TASK_ADAPTER_URL")); endpoint != "" {
		allowHTTP := boolEnv("AGENT_TASK_ADAPTER_ALLOW_HTTP")
		if allowHTTP && !strings.EqualFold(strings.TrimSpace(os.Getenv("ENVIRONMENT")), "development") {
			log.Fatal("AGENT_TASK_ADAPTER_ALLOW_HTTP is allowed only when ENVIRONMENT=development")
		}
		adapter, err = agentadapter.NewClient(endpoint, os.Getenv("AGENT_TASK_ADAPTER_SECRET"), os.Getenv("AGENT_TASK_CALLBACK_URL"), durationEnv("AGENT_TASK_ADAPTER_TIMEOUT", 15*time.Second), allowHTTP)
		if err != nil {
			log.Fatalf("configure agent task adapter: %v", err)
		}
	}
	// Report only validated local configuration, never adapter reachability.
	// An invalid adapter configuration must not publish a live runner heartbeat.
	reporter.AdapterConfigured = adapter != nil
	if adapter != nil {
		reporter.AdapterProbe = adapter.Probe
	}
	go reporter.Run(ctx)
	// A lease expiry is not permission to retry code execution. Reap it into a
	// visible terminal state and send a separate remote-stop request instead.
	go reapExpiredAgentAttempts(ctx, database, reporter, durationEnv("AGENT_TASK_REAPER_INTERVAL", 15*time.Second))
	go func() {
		if err := cancellationConsumer.Consume(ctx, agentTaskCancelQueue, agentTaskCancelConsumer, func(ctx context.Context, body []byte) error {
			message, err := messaging.DecodeOutboxMessage(body)
			if err != nil {
				return err
			}
			if message.Topic != "agent.task.cancel.requested" {
				return fmt.Errorf("unexpected agent task cancellation topic %q", message.Topic)
			}
			return messaging.HandleExactlyOnce(ctx, database, agentTaskCancelConsumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
				request, err := cancellationRequestFromPayload(message.Payload)
				if err != nil {
					return err
				}
				// The control plane revokes the lease before publishing this
				// request. Without an adapter there is no remote sandbox to
				// reclaim, so the durable control-plane cancellation remains
				// complete and the event can be acknowledged.
				if adapter == nil {
					return nil
				}
				return adapter.Cancel(ctx, request)
			})
		}); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("agent task cancellation consumer stopped", "error", err)
		}
	}()
	if err := consumer.Consume(ctx, agentTaskQueue, agentTaskConsumer, func(ctx context.Context, body []byte) error {
		message, err := messaging.DecodeOutboxMessage(body)
		if err != nil {
			return err
		}
		if message.Topic != "agent.task.execute.requested" {
			return fmt.Errorf("unexpected agent task topic %q", message.Topic)
		}
		return messaging.HandleExactlyOnce(ctx, database, agentTaskConsumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
			request, err := executionRequestFromPayload(message.Payload)
			if err != nil {
				return err
			}
			attempt, err := database.ClaimAgentTaskAttempt(ctx, workerID, request, lease)
			if errors.Is(err, store.ErrNoQueuedAgentTask) {
				if adapter == nil {
					return nil
				}
				// A prior delivery may have attached this exact job and lost the
				// Start response. Retry only while its original lease is live and
				// the one-use control-plane start gate remains unclaimed.
				return retryPendingAdapterStart(ctx, database, adapter, workerID, request)
			}
			if err != nil {
				return err
			}
			done := reporter.BeginTask()
			defer done()
			if adapter == nil {
				if err := database.MarkAgentTaskAttemptNeedsAttention(ctx, attempt.ID, workerID, "agent_executor_not_configured", "No isolated coding-agent adapter is deployed. Configure a sandbox adapter before retrying this approved plan."); err != nil && !errors.Is(err, store.ErrAgentTaskClaimLost) {
					return err
				}
				return nil
			}
			target, err := database.LoadAgentTaskAttemptTarget(ctx, attempt.ID, workerID)
			if err != nil {
				return err
			}
			return dispatchApprovedAgentTask(ctx, database, adapter, workerID, attempt.ID, target)
		})
	}); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("agent task runner stopped", "error", err)
	}
}

type agentTaskDispatchStore interface {
	MarkAgentTaskAttemptNeedsAttention(context.Context, uuid.UUID, string, string, string) error
	AttachAgentTaskAdapterJob(context.Context, uuid.UUID, string, string) error
}

type agentTaskStartRecoveryStore interface {
	PendingAgentTaskAdapterStart(context.Context, string, domain.AgentTaskExecutionRequest) (*domain.AgentTaskAttempt, error)
}

type agentTaskDispatchAdapter interface {
	Submit(context.Context, domain.AgentTaskAttemptTarget) (string, error)
	Start(context.Context, uuid.UUID, string) error
	Cancel(context.Context, domain.AgentTaskCancellationRequest) error
}

func retryPendingAdapterStart(ctx context.Context, database agentTaskStartRecoveryStore, adapter agentTaskDispatchAdapter, workerID string, request domain.AgentTaskExecutionRequest) error {
	attempt, err := database.PendingAgentTaskAdapterStart(ctx, workerID, request)
	if errors.Is(err, store.ErrNoQueuedAgentTask) {
		return nil
	}
	if err != nil {
		return err
	}
	return adapter.Start(ctx, attempt.ID, attempt.AdapterJobID)
}

func dispatchApprovedAgentTask(ctx context.Context, database agentTaskDispatchStore, adapter agentTaskDispatchAdapter, workerID string, attemptID uuid.UUID, target domain.AgentTaskAttemptTarget) error {
	jobID, err := adapter.Submit(ctx, target)
	if err != nil {
		if attentionErr := database.MarkAgentTaskAttemptNeedsAttention(ctx, attemptID, workerID, "agent_adapter_dispatch_failed", "The isolated adapter did not accept the approved plan. No local CLI or provider write was attempted."); attentionErr != nil && !errors.Is(attentionErr, store.ErrAgentTaskClaimLost) {
			return attentionErr
		}
		return nil
	}
	if err := database.AttachAgentTaskAdapterJob(ctx, attemptID, workerID, jobID); err != nil {
		// Submit only reserves the job. If the authoritative attachment fails,
		// revoke the reservation before a retry can leave an orphan behind.
		// Start has not been called, so this is safe even if the database write
		// committed but its response was lost.
		// The database error may have cancelled ctx, so give cleanup its own
		// short deadline while preserving the request's tracing values.
		cleanupCtx, cleanupDone := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		cancelErr := adapter.Cancel(cleanupCtx, domain.AgentTaskCancellationRequest{TaskID: target.Task.ID, AttemptID: attemptID, AdapterJobID: jobID})
		cleanupDone()
		if cancelErr != nil {
			slog.Warn("could not revoke reserved adapter job after attachment failure", "error", cancelErr)
		}
		if errors.Is(err, store.ErrAgentTaskClaimLost) {
			return nil
		}
		return err
	}
	// The adapter cannot run before this durable identity is visible to
	// callback validation. An ambiguous start is retried/reaped; it must not
	// be reported as a safe dispatch failure.
	return adapter.Start(ctx, attemptID, jobID)
}

func reapExpiredAgentAttempts(ctx context.Context, database *store.PostgresStore, reporter *health.Reporter, interval time.Duration) {
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		worked, err := database.ExpireAgentTaskAttempts(ctx, 25)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("expire stale agent task attempts", "error", err)
		}
		unacknowledged, ackErr := database.ExpireUnacknowledgedAgentTasks(ctx, 25, 30*time.Minute)
		if ackErr != nil && !errors.Is(ackErr, context.Canceled) {
			slog.Error("expire unacknowledged agent tasks", "error", ackErr)
		}
		if worked > 0 || unacknowledged > 0 {
			done := reporter.BeginTask()
			done()
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func cancellationRequestFromPayload(payload map[string]any) (domain.AgentTaskCancellationRequest, error) {
	parseID := func(key string) (uuid.UUID, error) {
		raw, ok := payload[key].(string)
		if !ok {
			return uuid.Nil, fmt.Errorf("agent task cancellation payload %s is invalid", key)
		}
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return uuid.Nil, fmt.Errorf("parse agent task cancellation payload %s: %w", key, err)
		}
		return id, nil
	}
	taskID, err := parseID("task_id")
	if err != nil {
		return domain.AgentTaskCancellationRequest{}, err
	}
	attemptID, err := parseID("attempt_id")
	if err != nil {
		return domain.AgentTaskCancellationRequest{}, err
	}
	request := domain.AgentTaskCancellationRequest{TaskID: taskID, AttemptID: attemptID}
	request.AdapterJobID, _ = payload["adapter_job_id"].(string)
	if !request.Valid() {
		return domain.AgentTaskCancellationRequest{}, errors.New("agent task cancellation payload adapter_job_id is invalid")
	}
	return request, nil
}

func executionRequestFromPayload(payload map[string]any) (domain.AgentTaskExecutionRequest, error) {
	parseID := func(key string) (uuid.UUID, error) {
		raw, ok := payload[key].(string)
		if !ok {
			return uuid.Nil, fmt.Errorf("agent task payload %s is invalid", key)
		}
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return uuid.Nil, fmt.Errorf("parse agent task payload %s: %w", key, err)
		}
		return id, nil
	}
	parseRevision := func(key string) (int, error) {
		raw, ok := payload[key].(float64)
		if !ok || raw < 1 || raw != float64(int(raw)) {
			return 0, fmt.Errorf("agent task payload %s is invalid", key)
		}
		return int(raw), nil
	}
	taskID, err := parseID("task_id")
	if err != nil {
		return domain.AgentTaskExecutionRequest{}, err
	}
	planID, err := parseID("plan_id")
	if err != nil {
		return domain.AgentTaskExecutionRequest{}, err
	}
	taskRevision, err := parseRevision("task_revision")
	if err != nil {
		return domain.AgentTaskExecutionRequest{}, err
	}
	planRevision, err := parseRevision("plan_revision")
	if err != nil {
		return domain.AgentTaskExecutionRequest{}, err
	}
	request := domain.AgentTaskExecutionRequest{TaskID: taskID, TaskRevision: taskRevision, PlanID: planID, PlanRevision: planRevision}
	request.PlanSHA256, _ = payload["plan_sha256"].(string)
	if !request.Valid() {
		return domain.AgentTaskExecutionRequest{}, errors.New("agent task payload plan_sha256 is invalid")
	}
	return request, nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func boolEnv(name string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(name)), "true")
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
