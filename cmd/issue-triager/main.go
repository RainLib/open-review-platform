// issue-triager owns the asynchronous, marker-keyed analysis of user-authored
// GitHub and GitLab Issues. It acknowledges before model work and updates the
// same provider comment with the terminal result.
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
	"github.com/RainLib/open-review-platform/internal/issuetriage"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/modelroute"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

const (
	queueName    = "openreview.provider-issue.triage.v1"
	consumerName = "provider-issue-triager-v1"
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
	resolver, err := credentials.New(cfg, database)
	if err != nil {
		log.Fatal(err)
	}
	provider := publisher.NewHTTPWithResolver(resolver)
	analyzer := issuetriage.Analyzer{Resolver: modelroute.EnvironmentResolver{}}
	linkPolicy := issuetriage.FileLinkPolicy{
		ConsoleBaseURL:            strings.TrimSpace(os.Getenv("OPEN_REVIEW_APP_URL")),
		GitHubPublicBaseURL:       strings.TrimSpace(os.Getenv("ISSUE_TRIAGE_GITHUB_PUBLIC_BASE_URL")),
		GitHubPublicForAPIBaseURL: strings.TrimSpace(os.Getenv("ISSUE_TRIAGE_GITHUB_PUBLIC_FOR_API_BASE_URL")),
		GitLabPublicBaseURL:       strings.TrimSpace(os.Getenv("ISSUE_TRIAGE_GITLAB_PUBLIC_BASE_URL")),
		GitLabPublicForAPIBaseURL: strings.TrimSpace(os.Getenv("ISSUE_TRIAGE_GITLAB_PUBLIC_FOR_API_BASE_URL")),
		AllowGitLabHTTP:           strings.EqualFold(os.Getenv("ENVIRONMENT"), "development") && strings.EqualFold(os.Getenv("GITLAB_ALLOW_HTTP"), "true"),
	}
	if err := linkPolicy.Validate(); err != nil {
		log.Fatalf("Issue file link configuration: %v", err)
	}
	consumer, err := messaging.OpenAMQPConsumer(cfg.Broker.URL, cfg.Broker.Exchange)
	if err != nil {
		log.Fatal(err)
	}
	defer consumer.Close()
	if err := consumer.Consume(ctx, queueName, consumerName, func(ctx context.Context, body []byte) error {
		message, err := messaging.DecodeOutboxMessage(body)
		if err != nil {
			return err
		}
		if message.Topic != "provider.issue.acknowledge" && message.Topic != "provider.issue.analyze" {
			return fmt.Errorf("unexpected provider issue topic %q", message.Topic)
		}
		return messaging.HandleExactlyOnce(ctx, database, consumerName, message, func(ctx context.Context, message domain.OutboxMessage) error {
			jobID, revision, attempt, err := jobRevisionAttempt(message.Payload)
			if err != nil {
				return err
			}
			job, err := database.ProviderIssueAnalysis(ctx, jobID, revision)
			if errors.Is(err, store.ErrNotFound) {
				// A newer edit superseded this immutable message or the
				// installation was disabled after admission.
				return nil
			}
			if err != nil {
				return err
			}
			if job.AnalysisAttempt != attempt {
				// The message belongs to a superseded execution attempt. The
				// retained Issue revision may be the same, but stale work must
				// never publish over a newer manual retry.
				return nil
			}
			switch message.Topic {
			case "provider.issue.acknowledge":
				if job.State != domain.ProviderIssueAnalysisQueued {
					return nil
				}
				if err := database.WithCurrentProviderIssuePublication(ctx, job.ID, job.Revision, job.AnalysisAttempt, domain.ProviderIssueAnalysisQueued, "", func(ctx context.Context) error {
					return provider.PublishProviderIssueAcknowledgement(ctx, job, issuetriage.AcknowledgementWithLinks(job, linkPolicy))
				}); errors.Is(err, store.ErrNotFound) {
					return nil
				} else if err != nil {
					return err
				}
				return database.MarkProviderIssueAcknowledged(ctx, job.ID, job.Revision, job.AnalysisAttempt)
			case "provider.issue.analyze":
				return runProviderIssueAnalysis(ctx, database, provider, analyzer.Analyze, job, linkPolicy)
			}
			return nil
		})
	}); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("provider issue triager stopped", "error", err)
	}
}

type providerIssueAnalysisStore interface {
	FailProviderIssueAnalysis(context.Context, uuid.UUID, int, int, string) error
	StageProviderIssueAnalysis(context.Context, uuid.UUID, int, int, string) (string, error)
	CompleteProviderIssueAnalysis(context.Context, uuid.UUID, int, int, string) error
	WithCurrentProviderIssuePublication(context.Context, uuid.UUID, int, int, domain.ProviderIssueAnalysisState, string, func(context.Context) error) error
}

type providerIssueAnalysisPublisher interface {
	PublishProviderIssueAnalysis(context.Context, domain.ProviderIssueAnalysisJob, string) error
}

func runProviderIssueAnalysis(ctx context.Context, database providerIssueAnalysisStore, provider providerIssueAnalysisPublisher, analyze func(context.Context, domain.ProviderIssueAnalysisJob) (issuetriage.Result, error), job domain.ProviderIssueAnalysisJob, linkPolicy issuetriage.FileLinkPolicy) error {
	if job.State == domain.ProviderIssueAnalysisCompleted {
		return nil
	}
	if job.State == domain.ProviderIssueAnalysisFailed {
		// The model failure is already durable. A redelivery retries only the
		// marker-keyed failure comment, never the model invocation.
		return publishCurrentProviderIssueAnalysis(ctx, database, provider, job, domain.ProviderIssueAnalysisFailed, "", issuetriage.FailureWithLinks(job, linkPolicy))
	}
	if job.State != domain.ProviderIssueAnalysisAcknowledged {
		return nil
	}
	if job.Analysis != "" {
		return publishStagedProviderIssueAnalysis(ctx, database, provider, job, job.Analysis)
	}
	timeout := time.Duration(job.ModelRoute.SubtaskTimeoutMinutes) * time.Minute
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = 2 * time.Minute
	}
	analysisCtx, cancel := context.WithTimeout(ctx, timeout)
	result, analyzeErr := analyze(analysisCtx, job)
	cancel()
	if analyzeErr != nil {
		if err := database.FailProviderIssueAnalysis(ctx, job.ID, job.Revision, job.AnalysisAttempt, analyzeErr.Error()); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// An edit or retry superseded this exact revision/attempt.
				return nil
			}
			return err
		}
		return publishCurrentProviderIssueAnalysis(ctx, database, provider, job, domain.ProviderIssueAnalysisFailed, "", issuetriage.FailureWithLinks(job, linkPolicy))
	}
	report := issuetriage.ReportWithLinks(job, result, linkPolicy)
	retained, err := database.StageProviderIssueAnalysis(ctx, job.ID, job.Revision, job.AnalysisAttempt, report)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return publishStagedProviderIssueAnalysis(ctx, database, provider, job, retained)
}

func publishStagedProviderIssueAnalysis(ctx context.Context, database providerIssueAnalysisStore, provider providerIssueAnalysisPublisher, job domain.ProviderIssueAnalysisJob, report string) error {
	if err := publishCurrentProviderIssueAnalysis(ctx, database, provider, job, domain.ProviderIssueAnalysisAcknowledged, report, report); err != nil {
		return err
	}
	err := database.CompleteProviderIssueAnalysis(ctx, job.ID, job.Revision, job.AnalysisAttempt, report)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

func publishCurrentProviderIssueAnalysis(ctx context.Context, database providerIssueAnalysisStore, provider providerIssueAnalysisPublisher, job domain.ProviderIssueAnalysisJob, state domain.ProviderIssueAnalysisState, stagedAnalysis, body string) error {
	err := database.WithCurrentProviderIssuePublication(ctx, job.ID, job.Revision, job.AnalysisAttempt, state, stagedAnalysis, func(ctx context.Context) error {
		return provider.PublishProviderIssueAnalysis(ctx, job, body)
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

func jobRevisionAttempt(payload map[string]any) (uuid.UUID, int, int, error) {
	rawID, ok := payload["job_id"].(string)
	if !ok {
		return uuid.Nil, 0, 0, fmt.Errorf("provider issue job id is invalid")
	}
	jobID, err := uuid.Parse(rawID)
	if err != nil || jobID == uuid.Nil {
		return uuid.Nil, 0, 0, fmt.Errorf("parse provider issue job id: %w", err)
	}
	rawRevision, ok := payload["revision"].(float64)
	if !ok || rawRevision < 1 || rawRevision != float64(int(rawRevision)) {
		return uuid.Nil, 0, 0, fmt.Errorf("provider issue revision is invalid")
	}
	attempt := 1
	if rawAttempt, exists := payload["attempt"]; exists {
		value, ok := rawAttempt.(float64)
		if !ok || value < 1 || value != float64(int(value)) {
			return uuid.Nil, 0, 0, fmt.Errorf("provider issue analysis attempt is invalid")
		}
		attempt = int(value)
	}
	return jobID, int(rawRevision), attempt, nil
}
