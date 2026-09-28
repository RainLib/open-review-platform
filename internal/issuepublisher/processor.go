// Package issuepublisher coordinates durable provider Issue creation and
// closure. Provider credentials remain behind the injected publisher; the
// broker payload carries only the immutable receipt ID.
package issuepublisher

import (
	"context"
	"errors"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type Store interface {
	ExternalIssuePublication(context.Context, uuid.UUID) (domain.ExternalIssuePublication, error)
	CancelExternalIssuePublication(context.Context, uuid.UUID, string) error
	MarkExternalIssueSyncFailed(context.Context, uuid.UUID, string) error
	MarkExternalIssuePublicationClosed(context.Context, uuid.UUID) error
	MarkExternalIssuePublicationFailed(context.Context, uuid.UUID, string) error
	MarkExternalIssuePublicationCreated(context.Context, uuid.UUID, string, string) error
}

type Provider interface {
	PublishExternalIssue(context.Context, domain.ExternalIssuePublication) (publisher.ExternalIssueResult, error)
	CloseExternalIssue(context.Context, domain.ExternalIssuePublication) error
}

type Processor struct {
	Store    Store
	Provider Provider
}

func (p Processor) Handle(ctx context.Context, message domain.OutboxMessage) error {
	if p.Store == nil || p.Provider == nil {
		return fmt.Errorf("external issue store and provider are required")
	}
	if message.Topic != "external.issue.create" && message.Topic != "external.issue.close" {
		return fmt.Errorf("unexpected external issue topic %q", message.Topic)
	}
	receiptID, err := ReceiptIDFromPayload(message.Payload)
	if err != nil {
		return err
	}
	publication, err := p.Store.ExternalIssuePublication(ctx, receiptID)
	if errors.Is(err, store.ErrNotFound) {
		// The workspace may have disabled the policy, disconnected the provider,
		// or been deleted after enqueue. No external write is still authorised.
		return p.Store.CancelExternalIssuePublication(ctx, receiptID, "The policy or verified provider installation is no longer active.")
	}
	if err != nil {
		return err
	}

	if message.Topic == "external.issue.close" {
		if publication.State == domain.ExternalIssuePublicationClosed || publication.State == domain.ExternalIssuePublicationCancelled {
			return nil
		}
		if publication.State != domain.ExternalIssuePublicationCreated || publication.ExternalID == "" {
			return fmt.Errorf("external issue is not yet available for closure")
		}
		if err := p.Provider.CloseExternalIssue(ctx, publication); err != nil {
			if markErr := p.Store.MarkExternalIssueSyncFailed(ctx, receiptID, err.Error()); markErr != nil {
				return fmt.Errorf("close external issue: %w; record failure: %v", err, markErr)
			}
			return err
		}
		return p.Store.MarkExternalIssuePublicationClosed(ctx, receiptID)
	}

	if publication.State == domain.ExternalIssuePublicationCreated || publication.State == domain.ExternalIssuePublicationClosed || publication.State == domain.ExternalIssuePublicationCancelled {
		return nil
	}
	result, err := p.Provider.PublishExternalIssue(ctx, publication)
	if err != nil {
		if markErr := p.Store.MarkExternalIssuePublicationFailed(ctx, receiptID, err.Error()); markErr != nil {
			return fmt.Errorf("publish external issue: %w; record failure: %v", err, markErr)
		}
		return err
	}
	return p.Store.MarkExternalIssuePublicationCreated(ctx, receiptID, result.ExternalID, result.ExternalURL)
}

func ReceiptIDFromPayload(payload map[string]any) (uuid.UUID, error) {
	raw, ok := payload["receipt_id"].(string)
	if !ok {
		return uuid.Nil, fmt.Errorf("external issue receipt id is invalid")
	}
	value, err := uuid.Parse(raw)
	if err != nil || value == uuid.Nil {
		return uuid.Nil, fmt.Errorf("parse external issue receipt id: %w", err)
	}
	return value, nil
}
