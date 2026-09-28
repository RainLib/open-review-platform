// Package terminal reconciles durable terminal run events with provider status
// surfaces. It intentionally does not own review execution: it remains live
// when an OCR worker is stopped, so a pending GitHub Check cannot survive a
// user cancellation or a superseding revision.
package terminal

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type RunStore interface {
	ReviewJobForRun(context.Context, uuid.UUID) (domain.ReviewJob, domain.RunState, error)
}

type publicationReceiptStore interface {
	RecordPublicationReceipts(context.Context, uuid.UUID, []domain.PublicationReceipt) error
}

type reviewRevisionValidator interface {
	ValidateReviewRevision(domain.ReviewJob) error
}

// PublicationRetryStore moves provider backoff out of the worker process. A
// terminal reporter must not sleep while holding the only queue consumer, and
// an immediate NACK can exhaust RabbitMQ's delivery limit before Retry-After.
type PublicationRetryStore interface {
	ScheduleTerminalPublicationRetry(context.Context, domain.OutboxMessage, time.Duration) (bool, error)
}

type publicationStageError struct {
	stage string
	err   error
}

func (e *publicationStageError) Error() string {
	return e.stage + " terminal publication: " + e.err.Error()
}
func (e *publicationStageError) Unwrap() error { return e.err }

// Publish closes a provider check and reconciles the terminal summary from the
// authoritative run state. The runner may have lost its provider response after
// writing the summary, or failed to write it at all. PublishTerminal upserts by
// the same stable marker, so recovery must not leave a failed run without a
// readable result or create a second comment.
func Publish(ctx context.Context, runs RunStore, checks publisher.CheckReporter, lifecycle publisher.LifecycleReporter, message domain.OutboxMessage) error {
	runID, err := runID(message.Payload)
	if err != nil {
		return err
	}
	job, state, err := runs.ReviewJobForRun(ctx, runID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	conclusion, summary, lifecycleState, publishLifecycle, ok := terminalStatus(state)
	if !ok {
		return nil
	}
	if state == domain.RunFailed {
		lifecycleState = failedLifecycleState(job.ErrorMessage)
		switch lifecycleState {
		case publisher.LifecycleTimedOut:
			summary = "The review exceeded its execution budget. No findings were published; adjust the execution budget or retry after the model service recovers."
		case publisher.LifecycleContextExhausted:
			summary = "The review exhausted the model context for its selected scope. No findings were published; narrow the scope or increase the model context before retrying."
		}
	}
	if validator, ok := checks.(reviewRevisionValidator); ok {
		if err := validator.ValidateReviewRevision(job); err != nil {
			if errors.Is(err, publisher.ErrInvalidReviewRevision) {
				// A malformed historical job has no safe provider target. Do not
				// let terminal recovery create a Check or comment after the runner
				// deliberately suppressed direct publication.
				return recordTerminalReceipt(ctx, runs, job, "status", conclusion, lifecycleState, false, "Invalid stored commit revision; provider publication was suppressed.")
			}
			return err
		}
	}
	if reporter, supportsReceipt := checks.(publisher.ReceiptCheckReporter); supportsReceipt {
		receipt, checkErr := reporter.CompleteCheckWithReceipt(ctx, job, conclusion, summary)
		if checkErr != nil {
			if receiptErr := recordTerminalReceipt(ctx, runs, job, "status", conclusion, lifecycleState, false, "Provider terminal analysis status could not be updated; a durable retry is required."); receiptErr != nil {
				return fmt.Errorf("complete terminal review check: %w (record failure receipt: %v)", checkErr, receiptErr)
			}
			return &publicationStageError{stage: "status", err: checkErr}
		}
		if receiptErr := recordReceipts(ctx, runs, job.ID, receipt); receiptErr != nil {
			return fmt.Errorf("record terminal review check receipt: %w", receiptErr)
		}
	} else if err := checks.CompleteCheck(ctx, job, conclusion, summary); err != nil {
		if receiptErr := recordTerminalReceipt(ctx, runs, job, "status", conclusion, lifecycleState, false, "Provider terminal analysis status could not be updated; a durable retry is required."); receiptErr != nil {
			return fmt.Errorf("complete terminal review check: %w (record failure receipt: %v)", err, receiptErr)
		}
		return &publicationStageError{stage: "status", err: err}
	}
	if publishLifecycle {
		if err := lifecycle.PublishTerminal(ctx, job, lifecycleState); err != nil {
			if receiptErr := recordTerminalReceipt(ctx, runs, job, "summary", conclusion, lifecycleState, false, "Provider terminal lifecycle comment could not be updated; a durable retry is required."); receiptErr != nil {
				return fmt.Errorf("publish terminal review status: %w (record failure receipt: %v)", err, receiptErr)
			}
			return &publicationStageError{stage: "summary", err: err}
		}
		if receiptErr := recordTerminalReceipt(ctx, runs, job, "summary", conclusion, lifecycleState, true, ""); receiptErr != nil {
			return fmt.Errorf("record terminal review status receipt: %w", receiptErr)
		}
	}
	return nil
}

// Handle completes the provider publication or durably schedules its next
// attempt. Returning nil after a schedule lets the original inbox record and
// AMQP delivery complete; the relay will publish the new message only after
// its persisted available_at boundary.
func Handle(ctx context.Context, runs RunStore, retries PublicationRetryStore, checks publisher.CheckReporter, lifecycle publisher.LifecycleReporter, message domain.OutboxMessage) error {
	err := Publish(ctx, runs, checks, lifecycle, message)
	if err == nil {
		return nil
	}
	if retries == nil {
		return err
	}
	if publisher.IsTerminalPublicationError(err) {
		// Replaying a permanent 4xx cannot repair permissions or payload shape.
		// Replace the provisional retry text with the actual recovery boundary.
		return recordFinalPublicationFailure(ctx, runs, message, err, "Provider rejected the terminal publication; correct provider permissions or configuration before a manual retry.")
	}
	scheduled, scheduleErr := retries.ScheduleTerminalPublicationRetry(ctx, message, publisher.RetryAfter(err))
	if scheduleErr != nil {
		return fmt.Errorf("%w (schedule terminal publication retry: %v)", err, scheduleErr)
	}
	if !scheduled {
		// The explicit five-attempt budget is exhausted. Keep the receipt honest:
		// no automatic retry remains, and recovery now requires an operator.
		return recordFinalPublicationFailure(ctx, runs, message, err, "Provider terminal publication did not succeed after five attempts; a manual retry is required.")
	}
	return nil
}

func recordReceipts(ctx context.Context, runs RunStore, jobID uuid.UUID, receipts ...domain.PublicationReceipt) error {
	recorder, ok := runs.(publicationReceiptStore)
	if !ok || len(receipts) == 0 {
		return nil
	}
	return recorder.RecordPublicationReceipts(ctx, jobID, receipts)
}

func recordTerminalReceipt(ctx context.Context, runs RunStore, job domain.ReviewJob, kind string, conclusion publisher.CheckConclusion, state publisher.LifecycleState, published bool, lastError string) error {
	marker := "open-review-platform:analysis-check:" + job.ID.String()
	phase := string(conclusion)
	if kind == "summary" {
		marker = "open-review-platform:summary:" + job.ID.String()
		phase = string(state)
	}
	digest := sha256.Sum256([]byte(job.HeadSHA + "\n" + marker + "\nterminal\n" + phase))
	receipt := domain.PublicationReceipt{
		ReceiptKind: kind, StableMarker: marker, PayloadHash: fmt.Sprintf("%x", digest[:]), Published: published,
	}
	if !published {
		receipt.LastError = lastError
	}
	return recordReceipts(ctx, runs, job.ID, receipt)
}

func recordFinalPublicationFailure(ctx context.Context, runs RunStore, message domain.OutboxMessage, publishErr error, lastError string) error {
	var stageErr *publicationStageError
	if !errors.As(publishErr, &stageErr) {
		// Invalid envelopes and database read failures still use the normal inbox
		// error path; they do not identify a provider surface whose receipt can be
		// safely updated.
		return publishErr
	}
	runID, err := runID(message.Payload)
	if err != nil {
		return err
	}
	job, state, err := runs.ReviewJobForRun(ctx, runID)
	if err != nil {
		return err
	}
	conclusion, _, lifecycleState, _, ok := terminalStatus(state)
	if !ok {
		return nil
	}
	if state == domain.RunFailed {
		lifecycleState = failedLifecycleState(job.ErrorMessage)
	}
	if err := recordTerminalReceipt(ctx, runs, job, stageErr.stage, conclusion, lifecycleState, false, lastError); err != nil {
		return fmt.Errorf("record final terminal publication failure: %w", err)
	}
	return nil
}

func runID(payload map[string]any) (uuid.UUID, error) {
	raw, ok := payload["run_id"].(string)
	if !ok {
		return uuid.Nil, fmt.Errorf("terminal status payload run_id is invalid")
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse terminal status run id: %w", err)
	}
	return parsed, nil
}

func terminalStatus(state domain.RunState) (publisher.CheckConclusion, string, publisher.LifecycleState, bool, bool) {
	switch state {
	case domain.RunNeedsAttention:
		return publisher.CheckFailure, "The review requires human attention before trustworthy findings or a merge conclusion can be published. Resolve the recorded intervention, then retry the review.", publisher.LifecycleNeedsAttention, true, true
	case domain.RunCancelled:
		return publisher.CheckNeutral, "The review was cancelled before findings could be published.", publisher.LifecycleCancelled, true, true
	case domain.RunSuperseded:
		return publisher.CheckNeutral, "The review was superseded by a newer pull request revision before findings could be published.", publisher.LifecycleSuperseded, true, true
	case domain.RunFailed:
		return publisher.CheckFailure, "The review ended before trustworthy findings could be published. See the task detail for the safe error summary.", publisher.LifecycleFailed, true, true
	default:
		return "", "", "", false, false
	}
}

// The runner persists these typed OCR errors as its job failure message before
// advancing the run. Match only their leading sentinel: arbitrary provider or
// model text must not change the terminal explanation shown to reviewers.
func failedLifecycleState(message string) publisher.LifecycleState {
	switch {
	case strings.HasPrefix(message, domain.ErrReviewTimedOut.Error()):
		return publisher.LifecycleTimedOut
	case strings.HasPrefix(message, domain.ErrReviewContextExhausted.Error()):
		return publisher.LifecycleContextExhausted
	default:
		return publisher.LifecycleFailed
	}
}
