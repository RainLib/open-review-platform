package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

type validatingRecordingPublisher struct{ recordingPublisher }

func (*validatingRecordingPublisher) ValidateReviewRevision(job domain.ReviewJob) error {
	return (&publisher.HTTPPublisher{}).ValidateReviewRevision(job)
}

type receiptRecordingStore struct {
	recordingStore
	receipts []domain.PublicationReceipt
}

func (s *receiptRecordingStore) RecordPublicationReceipts(_ context.Context, _ uuid.UUID, receipts []domain.PublicationReceipt) error {
	s.receipts = append(s.receipts, receipts...)
	return nil
}

type retryRecordingStore struct {
	scheduled bool
	delay     time.Duration
	message   domain.OutboxMessage
	err       error
}

func (s *retryRecordingStore) ScheduleTerminalPublicationRetry(_ context.Context, message domain.OutboxMessage, delay time.Duration) (bool, error) {
	s.message, s.delay = message, delay
	return s.scheduled, s.err
}

type failingReceiptPublisher struct {
	err error
}

func (*failingReceiptPublisher) StartCheck(context.Context, domain.ReviewJob) error { return nil }
func (p *failingReceiptPublisher) CompleteCheck(context.Context, domain.ReviewJob, publisher.CheckConclusion, string) error {
	return p.err
}
func (*failingReceiptPublisher) StartCheckWithReceipt(context.Context, domain.ReviewJob) (domain.PublicationReceipt, error) {
	return domain.PublicationReceipt{}, nil
}
func (p *failingReceiptPublisher) CompleteCheckWithReceipt(context.Context, domain.ReviewJob, publisher.CheckConclusion, string) (domain.PublicationReceipt, error) {
	return domain.PublicationReceipt{}, p.err
}
func (*failingReceiptPublisher) PublishStarted(context.Context, domain.ReviewJob) error { return nil }
func (*failingReceiptPublisher) PublishTerminal(context.Context, domain.ReviewJob, publisher.LifecycleState) error {
	return nil
}

type lifecycleFailingReceiptPublisher struct {
	err error
}

func (*lifecycleFailingReceiptPublisher) StartCheck(context.Context, domain.ReviewJob) error {
	return nil
}
func (*lifecycleFailingReceiptPublisher) CompleteCheck(context.Context, domain.ReviewJob, publisher.CheckConclusion, string) error {
	return nil
}
func (*lifecycleFailingReceiptPublisher) StartCheckWithReceipt(context.Context, domain.ReviewJob) (domain.PublicationReceipt, error) {
	return domain.PublicationReceipt{}, nil
}
func (p *lifecycleFailingReceiptPublisher) CompleteCheckWithReceipt(_ context.Context, job domain.ReviewJob, _ publisher.CheckConclusion, _ string) (domain.PublicationReceipt, error) {
	return domain.PublicationReceipt{ReceiptKind: "status", StableMarker: "open-review-platform:analysis-check:" + job.ID.String(), PayloadHash: strings.Repeat("a", 64), Published: true}, nil
}
func (*lifecycleFailingReceiptPublisher) PublishStarted(context.Context, domain.ReviewJob) error {
	return nil
}
func (p *lifecycleFailingReceiptPublisher) PublishTerminal(context.Context, domain.ReviewJob, publisher.LifecycleState) error {
	return p.err
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

func TestPublishSuppressesMalformedHistoricalRevision(t *testing.T) {
	runID := uuid.New()
	reporter := &validatingRecordingPublisher{}
	runs := &receiptRecordingStore{recordingStore: recordingStore{
		job: domain.ReviewJob{ID: uuid.New(), BaseSHA: "base-sha", HeadSHA: "head-sha"}, state: domain.RunFailed,
	}}
	if err := Publish(context.Background(), runs, reporter, reporter, terminalMessage(runID)); err != nil {
		t.Fatal(err)
	}
	if reporter.conclusion != "" || len(reporter.lifecycle) != 0 {
		t.Fatalf("invalid historical revision caused provider publication: %#v", reporter)
	}
	if len(runs.receipts) != 1 || runs.receipts[0].Published || !strings.Contains(runs.receipts[0].LastError, "suppressed") {
		t.Fatalf("expected durable suppressed status receipt, got %#v", runs.receipts)
	}
}

func TestPublishFailedRunReconcilesOneTerminalSummaryWithItsFailureClass(t *testing.T) {
	for _, test := range []struct {
		name, failure, checkText string
		state                    publisher.LifecycleState
	}{
		{name: "generic", failure: "provider publication failed", checkText: "trustworthy findings", state: publisher.LifecycleFailed},
		{name: "timeout", failure: domain.ErrReviewTimedOut.Error() + " after 30s", checkText: "execution budget", state: publisher.LifecycleTimedOut},
		{name: "context", failure: domain.ErrReviewContextExhausted.Error() + ": reduce scope", checkText: "model context", state: publisher.LifecycleContextExhausted},
		{name: "untrusted suffix", failure: "provider error: " + domain.ErrReviewTimedOut.Error(), checkText: "trustworthy findings", state: publisher.LifecycleFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			reporter := &recordingPublisher{}
			job := domain.ReviewJob{ID: uuid.New(), ErrorMessage: test.failure}
			if err := Publish(context.Background(), recordingStore{job: job, state: domain.RunFailed}, reporter, reporter, terminalMessage(uuid.New())); err != nil {
				t.Fatal(err)
			}
			if reporter.conclusion != publisher.CheckFailure || !strings.Contains(reporter.summary, test.checkText) {
				t.Fatalf("unexpected failed check: %#v", reporter)
			}
			if len(reporter.lifecycle) != 1 || reporter.lifecycle[0] != test.state {
				t.Fatalf("failed recovery did not reconcile the terminal summary: %#v", reporter.lifecycle)
			}
		})
	}
}

func TestPublishNeedsAttentionClosesCheckAndPublishesInterventionReport(t *testing.T) {
	runID := uuid.New()
	reporter := &recordingPublisher{}
	err := Publish(context.Background(), recordingStore{job: domain.ReviewJob{ID: uuid.New()}, state: domain.RunNeedsAttention}, reporter, reporter, terminalMessage(runID))
	if err != nil {
		t.Fatal(err)
	}
	if reporter.conclusion != publisher.CheckFailure || !strings.Contains(reporter.summary, "human attention") {
		t.Fatalf("unexpected needs-attention check: %#v", reporter)
	}
	if len(reporter.lifecycle) != 1 || reporter.lifecycle[0] != publisher.LifecycleNeedsAttention {
		t.Fatalf("expected needs-attention lifecycle publication, got %#v", reporter.lifecycle)
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

func TestPublishRetainsSafeFailedTerminalCheckReceipt(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "immutable-head"}
	runs := &receiptRecordingStore{recordingStore: recordingStore{job: job, state: domain.RunCancelled}}
	providerErr := &publisher.HTTPStatusError{StatusCode: 429, RetryAfter: 9 * time.Second}
	err := Publish(context.Background(), runs, &failingReceiptPublisher{err: providerErr}, &recordingPublisher{}, terminalMessage(uuid.New()))
	if !errors.Is(err, providerErr) {
		t.Fatalf("publish error=%v, want provider failure", err)
	}
	if len(runs.receipts) != 1 || runs.receipts[0].ReceiptKind != "status" || runs.receipts[0].Published || len(runs.receipts[0].PayloadHash) != 64 {
		t.Fatalf("failed terminal receipts=%#v", runs.receipts)
	}
	if !strings.Contains(runs.receipts[0].LastError, "durable retry") || strings.Contains(runs.receipts[0].LastError, "429") {
		t.Fatalf("receipt must be actionable without retaining provider data: %#v", runs.receipts[0])
	}
}

func TestHandleDurablySchedulesRetryAfterWithoutNackingOriginal(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "immutable-head"}
	runs := &receiptRecordingStore{recordingStore: recordingStore{job: job, state: domain.RunCancelled}}
	retries := &retryRecordingStore{scheduled: true}
	message := terminalMessage(uuid.New())
	err := Handle(context.Background(), runs, retries, &failingReceiptPublisher{err: &publisher.HTTPStatusError{StatusCode: 429, RetryAfter: 9 * time.Second}}, &recordingPublisher{}, message)
	if err != nil {
		t.Fatal(err)
	}
	if retries.message.ID != message.ID || retries.delay != 9*time.Second {
		t.Fatalf("scheduled message=%#v delay=%s", retries.message, retries.delay)
	}
}

func TestHandleDoesNotReplayPermanentProviderFailure(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "immutable-head"}
	runs := &receiptRecordingStore{recordingStore: recordingStore{job: job, state: domain.RunCancelled}}
	retries := &retryRecordingStore{scheduled: true}
	err := Handle(context.Background(), runs, retries, &failingReceiptPublisher{err: &publisher.HTTPStatusError{StatusCode: 403}}, &recordingPublisher{}, terminalMessage(uuid.New()))
	if err != nil {
		t.Fatal(err)
	}
	if retries.message.ID != uuid.Nil {
		t.Fatalf("permanent failure was scheduled for replay: %#v", retries.message)
	}
	if len(runs.receipts) != 2 || !strings.Contains(runs.receipts[1].LastError, "permissions or configuration") {
		t.Fatalf("permanent failure receipt=%#v", runs.receipts)
	}
}

func TestHandleRecordsExhaustedPublicationBudget(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "immutable-head"}
	runs := &receiptRecordingStore{recordingStore: recordingStore{job: job, state: domain.RunCancelled}}
	retries := &retryRecordingStore{scheduled: false}
	err := Handle(context.Background(), runs, retries, &failingReceiptPublisher{err: &publisher.HTTPStatusError{StatusCode: 503}}, &recordingPublisher{}, terminalMessage(uuid.New()))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs.receipts) != 2 || !strings.Contains(runs.receipts[1].LastError, "after five attempts") {
		t.Fatalf("exhausted failure receipt=%#v", runs.receipts)
	}
}

func TestHandleSchedulesLifecycleRetryWithoutLosingSuccessfulCheckReceipt(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "immutable-head"}
	runs := &receiptRecordingStore{recordingStore: recordingStore{job: job, state: domain.RunCancelled}}
	retries := &retryRecordingStore{scheduled: true}
	err := Handle(context.Background(), runs, retries, &lifecycleFailingReceiptPublisher{err: &publisher.HTTPStatusError{StatusCode: 503}}, &lifecycleFailingReceiptPublisher{err: &publisher.HTTPStatusError{StatusCode: 503}}, terminalMessage(uuid.New()))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs.receipts) != 2 || !runs.receipts[0].Published || runs.receipts[0].ReceiptKind != "status" || runs.receipts[1].Published || runs.receipts[1].ReceiptKind != "summary" {
		t.Fatalf("lifecycle retry receipts=%#v", runs.receipts)
	}
	if retries.message.ID == uuid.Nil || retries.delay != 0 {
		t.Fatalf("lifecycle retry message=%#v delay=%s", retries.message, retries.delay)
	}
}
