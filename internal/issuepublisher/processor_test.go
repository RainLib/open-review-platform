package issuepublisher

import (
	"context"
	"errors"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type processorStore struct {
	publication domain.ExternalIssuePublication
	loadErr     error
	cancelled   bool
	failed      bool
	syncFailed  bool
	created     bool
	closed      bool
}

func (s *processorStore) ExternalIssuePublication(context.Context, uuid.UUID) (domain.ExternalIssuePublication, error) {
	return s.publication, s.loadErr
}
func (s *processorStore) CancelExternalIssuePublication(context.Context, uuid.UUID, string) error {
	s.cancelled = true
	return nil
}
func (s *processorStore) MarkExternalIssueSyncFailed(context.Context, uuid.UUID, string) error {
	s.syncFailed = true
	return nil
}
func (s *processorStore) MarkExternalIssuePublicationClosed(context.Context, uuid.UUID) error {
	s.closed = true
	return nil
}
func (s *processorStore) MarkExternalIssuePublicationFailed(context.Context, uuid.UUID, string) error {
	s.failed = true
	return nil
}
func (s *processorStore) MarkExternalIssuePublicationCreated(context.Context, uuid.UUID, string, string) error {
	s.created = true
	return nil
}

type processorProvider struct {
	created   bool
	closed    bool
	createErr error
	closeErr  error
}

func (p *processorProvider) PublishExternalIssue(context.Context, domain.ExternalIssuePublication) (publisher.ExternalIssueResult, error) {
	p.created = true
	return publisher.ExternalIssueResult{ExternalID: "17", ExternalURL: "https://example.test/issues/17"}, p.createErr
}
func (p *processorProvider) CloseExternalIssue(context.Context, domain.ExternalIssuePublication) error {
	p.closed = true
	return p.closeErr
}

func TestProcessorCreateAndCloseLifecycle(t *testing.T) {
	receiptID := uuid.New()
	storage := &processorStore{publication: domain.ExternalIssuePublication{ExternalIssueReceipt: domain.ExternalIssueReceipt{ID: receiptID, State: domain.ExternalIssuePublicationQueued}}}
	provider := &processorProvider{}
	processor := Processor{Store: storage, Provider: provider}
	if err := processor.Handle(context.Background(), externalIssueMessage(receiptID, "external.issue.create")); err != nil {
		t.Fatal(err)
	}
	if !provider.created || !storage.created {
		t.Fatalf("create provider=%t receipt=%t", provider.created, storage.created)
	}

	storage.publication.State = domain.ExternalIssuePublicationCreated
	storage.publication.ExternalID = "17"
	if err := processor.Handle(context.Background(), externalIssueMessage(receiptID, "external.issue.close")); err != nil {
		t.Fatal(err)
	}
	if !provider.closed || !storage.closed {
		t.Fatalf("close provider=%t receipt=%t", provider.closed, storage.closed)
	}
}

func TestProcessorRecordsFailuresAndWithdrawnAuthority(t *testing.T) {
	receiptID := uuid.New()
	providerFailure := errors.New("provider unavailable")
	storage := &processorStore{publication: domain.ExternalIssuePublication{ExternalIssueReceipt: domain.ExternalIssueReceipt{ID: receiptID, State: domain.ExternalIssuePublicationQueued}}}
	provider := &processorProvider{createErr: providerFailure}
	processor := Processor{Store: storage, Provider: provider}
	if err := processor.Handle(context.Background(), externalIssueMessage(receiptID, "external.issue.create")); !errors.Is(err, providerFailure) || !storage.failed {
		t.Fatalf("create error=%v failed receipt=%t", err, storage.failed)
	}

	storage = &processorStore{loadErr: store.ErrNotFound}
	provider = &processorProvider{}
	processor = Processor{Store: storage, Provider: provider}
	if err := processor.Handle(context.Background(), externalIssueMessage(receiptID, "external.issue.create")); err != nil {
		t.Fatal(err)
	}
	if !storage.cancelled || provider.created {
		t.Fatalf("withdrawn receipt cancelled=%t provider create=%t", storage.cancelled, provider.created)
	}
}

func TestProcessorRecordsCloseFailure(t *testing.T) {
	receiptID := uuid.New()
	providerFailure := errors.New("provider unavailable")
	storage := &processorStore{publication: domain.ExternalIssuePublication{ExternalIssueReceipt: domain.ExternalIssueReceipt{ID: receiptID, State: domain.ExternalIssuePublicationCreated, ExternalID: "17"}}}
	provider := &processorProvider{closeErr: providerFailure}
	err := (Processor{Store: storage, Provider: provider}).Handle(context.Background(), externalIssueMessage(receiptID, "external.issue.close"))
	if !errors.Is(err, providerFailure) || !storage.syncFailed || storage.closed {
		t.Fatalf("close error=%v sync failed=%t closed=%t", err, storage.syncFailed, storage.closed)
	}
}

func externalIssueMessage(receiptID uuid.UUID, topic string) domain.OutboxMessage {
	return domain.OutboxMessage{ID: uuid.New(), AggregateID: receiptID, Topic: topic, Payload: map[string]any{"receipt_id": receiptID.String()}}
}
