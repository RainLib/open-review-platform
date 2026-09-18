package terminal

import (
	"context"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type recordingStore struct {
	job   domain.ReviewJob
	state domain.RunState
	err   error
}

func (s recordingStore) ReviewJobForRun(context.Context, uuid.UUID) (domain.ReviewJob, domain.RunState, error) {
	return s.job, s.state, s.err
}

type recordingPublisher struct {
	conclusion publisher.CheckConclusion
	summary    string
	lifecycle  []publisher.LifecycleState
}

func (*recordingPublisher) StartCheck(context.Context, domain.ReviewJob) error { return nil }

func (p *recordingPublisher) CompleteCheck(_ context.Context, _ domain.ReviewJob, conclusion publisher.CheckConclusion, summary string) error {
	p.conclusion, p.summary = conclusion, summary
	return nil
}

func (p *recordingPublisher) PublishStarted(context.Context, domain.ReviewJob) error { return nil }

func (p *recordingPublisher) PublishTerminal(_ context.Context, _ domain.ReviewJob, state publisher.LifecycleState) error {
	p.lifecycle = append(p.lifecycle, state)
	return nil
}

func terminalMessage(runID uuid.UUID) domain.OutboxMessage {
	return domain.OutboxMessage{ID: uuid.New(), AggregateID: runID, Topic: "review.run.cancelled", Payload: map[string]any{"run_id": runID.String()}}
}

func TestPublishCancelledRunClosesCheckAndStatus(t *testing.T) {
	runID := uuid.New()
	reporter := &recordingPublisher{}
	err := Publish(context.Background(), recordingStore{job: domain.ReviewJob{ID: uuid.New()}, state: domain.RunCancelled}, reporter, reporter, terminalMessage(runID))
	if err != nil {
		t.Fatal(err)
	}
	if reporter.conclusion != publisher.CheckNeutral || !strings.Contains(reporter.summary, "cancelled") {
		t.Fatalf("unexpected cancellation check: %#v", reporter)
	}
	if len(reporter.lifecycle) != 1 || reporter.lifecycle[0] != publisher.LifecycleCancelled {
		t.Fatalf("expected cancelled lifecycle publication, got %#v", reporter.lifecycle)
	}
}

func TestPublishFailedRunClosesCheckWithoutOverwritingTimeoutReport(t *testing.T) {
	runID := uuid.New()
	reporter := &recordingPublisher{}
	err := Publish(context.Background(), recordingStore{job: domain.ReviewJob{ID: uuid.New()}, state: domain.RunFailed}, reporter, reporter, terminalMessage(runID))
	if err != nil {
		t.Fatal(err)
	}
	if reporter.conclusion != publisher.CheckFailure || !strings.Contains(reporter.summary, "trustworthy findings") {
		t.Fatalf("unexpected failed check: %#v", reporter)
	}
	if len(reporter.lifecycle) != 0 {
		t.Fatalf("failed recovery must not overwrite a timeout report: %#v", reporter.lifecycle)
	}
}

func TestPublishIgnoresMissingOrNonTerminalRun(t *testing.T) {
	runID := uuid.New()
	reporter := &recordingPublisher{}
	if err := Publish(context.Background(), recordingStore{err: store.ErrNotFound}, reporter, reporter, terminalMessage(runID)); err != nil {
		t.Fatal(err)
	}
	if err := Publish(context.Background(), recordingStore{state: domain.RunAnalyzing}, reporter, reporter, terminalMessage(runID)); err != nil {
		t.Fatal(err)
	}
	if reporter.conclusion != "" || len(reporter.lifecycle) != 0 {
		t.Fatalf("non-terminal state must not publish: %#v", reporter)
	}
}

func TestPublishRejectsMalformedRunID(t *testing.T) {
	err := Publish(context.Background(), recordingStore{}, &recordingPublisher{}, &recordingPublisher{}, domain.OutboxMessage{Payload: map[string]any{"run_id": "not-a-uuid"}})
	if err == nil || !strings.Contains(err.Error(), "parse terminal status run id") {
		t.Fatalf("expected invalid UUID error, got %v", err)
	}
}
