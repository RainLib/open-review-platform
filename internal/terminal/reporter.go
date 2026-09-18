// Package terminal reconciles durable terminal run events with provider status
// surfaces. It intentionally does not own review execution: it remains live
// when an OCR worker is stopped, so a pending GitHub Check cannot survive a
// user cancellation or a superseding revision.
package terminal

import (
	"context"
	"errors"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type RunStore interface {
	ReviewJobForRun(context.Context, uuid.UUID) (domain.ReviewJob, domain.RunState, error)
}

// Publish closes a provider check from the authoritative terminal run state.
// A failed runner is allowed to publish a richer timeout report itself; this
// recovery path only closes its Check so it cannot overwrite that evidence.
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
	if err := checks.CompleteCheck(ctx, job, conclusion, summary); err != nil {
		return fmt.Errorf("complete terminal review check: %w", err)
	}
	if publishLifecycle {
		if err := lifecycle.PublishTerminal(ctx, job, lifecycleState); err != nil {
			return fmt.Errorf("publish terminal review status: %w", err)
		}
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
	case domain.RunCancelled:
		return publisher.CheckNeutral, "The review was cancelled before findings could be published.", publisher.LifecycleCancelled, true, true
	case domain.RunSuperseded:
		return publisher.CheckNeutral, "The review was superseded by a newer pull request revision before findings could be published.", publisher.LifecycleSuperseded, true, true
	case domain.RunFailed:
		return publisher.CheckFailure, "The review ended before trustworthy findings could be published. See the task detail for the safe error summary.", publisher.LifecycleFailed, false, true
	default:
		return "", "", "", false, false
	}
}
