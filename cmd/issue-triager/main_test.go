package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/issuetriage"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type analysisStoreStub struct {
	failed    int
	staged    int
	completed int
	fenced    int
	failErr   error
	stageErr  error
	fenceErr  error
	report    string
}

func (stub *analysisStoreStub) WithCurrentProviderIssuePublication(ctx context.Context, _ uuid.UUID, _, _ int, _ domain.ProviderIssueAnalysisState, _ string, publish func(context.Context) error) error {
	stub.fenced++
	if stub.fenceErr != nil {
		return stub.fenceErr
	}
	return publish(ctx)
}

func (stub *analysisStoreStub) FailProviderIssueAnalysis(_ context.Context, _ uuid.UUID, _, _ int, _ string) error {
	stub.failed++
	return stub.failErr
}

func (stub *analysisStoreStub) StageProviderIssueAnalysis(_ context.Context, _ uuid.UUID, _, _ int, report string) (string, error) {
	stub.staged++
	if stub.stageErr != nil {
		return "", stub.stageErr
	}
	if stub.report == "" {
		stub.report = report
	}
	return stub.report, nil
}

func (stub *analysisStoreStub) CompleteProviderIssueAnalysis(_ context.Context, _ uuid.UUID, _, _ int, _ string) error {
	stub.completed++
	return nil
}

type analysisPublisherStub struct {
	called  int
	bodies  []string
	failFor int
}

func (stub *analysisPublisherStub) PublishProviderIssueAnalysis(_ context.Context, _ domain.ProviderIssueAnalysisJob, body string) error {
	stub.called++
	stub.bodies = append(stub.bodies, body)
	if stub.called <= stub.failFor {
		return errors.New("temporary provider failure")
	}
	return nil
}

func TestFailedAnalysisRedeliveryPublishesWithoutRerunningModel(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{
		ID: uuid.New(), Revision: 1, AnalysisAttempt: 1,
		State:      domain.ProviderIssueAnalysisAcknowledged,
		Repository: "RainLib/demo", IssueNumber: 8,
	}
	database := &analysisStoreStub{}
	provider := &analysisPublisherStub{failFor: 1}
	modelCalls := 0
	analyze := func(context.Context, domain.ProviderIssueAnalysisJob) (issuetriage.Result, error) {
		modelCalls++
		return issuetriage.Result{}, errors.New("model unavailable")
	}
	if err := runProviderIssueAnalysis(context.Background(), database, provider, analyze, job, issuetriage.FileLinkPolicy{}); err == nil {
		t.Fatal("failed provider publication must be retried")
	}
	job.State = domain.ProviderIssueAnalysisFailed
	if err := runProviderIssueAnalysis(context.Background(), database, provider, analyze, job, issuetriage.FileLinkPolicy{}); err != nil {
		t.Fatal(err)
	}
	if modelCalls != 1 || database.failed != 1 || database.completed != 0 || provider.called != 2 {
		t.Fatalf("redelivery repeated work: model=%d failed=%d completed=%d published=%d", modelCalls, database.failed, database.completed, provider.called)
	}
	if len(provider.bodies) != 2 || provider.bodies[0] != provider.bodies[1] || !strings.Contains(provider.bodies[0], "Open Review") {
		t.Fatalf("failure publication must remain stable across redelivery: %#v", provider.bodies)
	}
}

func TestStagedAnalysisRedeliveryPublishesWithoutRerunningModel(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{
		ID: uuid.New(), Revision: 2, AnalysisAttempt: 1,
		State:      domain.ProviderIssueAnalysisAcknowledged,
		Repository: "RainLib/demo", IssueNumber: 8,
	}
	database := &analysisStoreStub{}
	provider := &analysisPublisherStub{failFor: 1}
	modelCalls := 0
	analyze := func(context.Context, domain.ProviderIssueAnalysisJob) (issuetriage.Result, error) {
		modelCalls++
		return issuetriage.Result{Summary: "Bounded result", ContextQuality: "sufficient"}, nil
	}
	if err := runProviderIssueAnalysis(context.Background(), database, provider, analyze, job, issuetriage.FileLinkPolicy{}); err == nil {
		t.Fatal("failed provider publication must be retried")
	}
	job.Analysis = database.report
	if err := runProviderIssueAnalysis(context.Background(), database, provider, analyze, job, issuetriage.FileLinkPolicy{}); err != nil {
		t.Fatal(err)
	}
	if modelCalls != 1 || database.staged != 1 || database.completed != 1 || provider.called != 2 {
		t.Fatalf("redelivery repeated work: model=%d staged=%d completed=%d published=%d", modelCalls, database.staged, database.completed, provider.called)
	}
	if provider.bodies[0] != provider.bodies[1] || provider.bodies[0] != database.report {
		t.Fatalf("publication changed staged report: %#v", provider.bodies)
	}
}

func TestSupersededAnalysisResultDoesNotPublish(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{
		ID: uuid.New(), Revision: 2, AnalysisAttempt: 1,
		State: domain.ProviderIssueAnalysisAcknowledged,
	}
	database := &analysisStoreStub{stageErr: store.ErrNotFound}
	provider := &analysisPublisherStub{}
	analyze := func(context.Context, domain.ProviderIssueAnalysisJob) (issuetriage.Result, error) {
		return issuetriage.Result{Summary: "Late result", ContextQuality: "sufficient"}, nil
	}
	if err := runProviderIssueAnalysis(context.Background(), database, provider, analyze, job, issuetriage.FileLinkPolicy{}); err != nil {
		t.Fatal(err)
	}
	if database.staged != 1 || database.completed != 0 || provider.called != 0 {
		t.Fatalf("superseded attempt published: staged=%d completed=%d published=%d", database.staged, database.completed, provider.called)
	}
}

func TestSupersededAnalysisFailureDoesNotPublish(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{
		ID: uuid.New(), Revision: 2, AnalysisAttempt: 1,
		State: domain.ProviderIssueAnalysisAcknowledged,
	}
	database := &analysisStoreStub{failErr: store.ErrNotFound}
	provider := &analysisPublisherStub{}
	analyze := func(context.Context, domain.ProviderIssueAnalysisJob) (issuetriage.Result, error) {
		return issuetriage.Result{}, errors.New("model unavailable")
	}
	if err := runProviderIssueAnalysis(context.Background(), database, provider, analyze, job, issuetriage.FileLinkPolicy{}); err != nil {
		t.Fatal(err)
	}
	if provider.called != 0 || database.failed != 1 {
		t.Fatalf("superseded attempt published failure: publisher=%d failed=%d", provider.called, database.failed)
	}
}

func TestSupersededStagedAnalysisDoesNotPublishAfterPublicationFence(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{
		ID: uuid.New(), Revision: 2, AnalysisAttempt: 1,
		State: domain.ProviderIssueAnalysisAcknowledged, Analysis: "## Result\nOld revision",
	}
	database := &analysisStoreStub{fenceErr: store.ErrNotFound}
	provider := &analysisPublisherStub{}
	if err := runProviderIssueAnalysis(context.Background(), database, provider, func(context.Context, domain.ProviderIssueAnalysisJob) (issuetriage.Result, error) {
		t.Fatal("staged result must not rerun the model")
		return issuetriage.Result{}, nil
	}, job, issuetriage.FileLinkPolicy{}); err != nil {
		t.Fatal(err)
	}
	if provider.called != 0 || database.fenced != 1 {
		t.Fatalf("stale staged result reached provider: publications=%d fences=%d", provider.called, database.fenced)
	}
}

func TestCompletedAnalysisRedeliverySkipsAllWork(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{State: domain.ProviderIssueAnalysisCompleted}
	database := &analysisStoreStub{}
	provider := &analysisPublisherStub{}
	if err := runProviderIssueAnalysis(context.Background(), database, provider, func(context.Context, domain.ProviderIssueAnalysisJob) (issuetriage.Result, error) {
		t.Fatal("completed analysis called model again")
		return issuetriage.Result{}, nil
	}, job, issuetriage.FileLinkPolicy{}); err != nil {
		t.Fatal(err)
	}
	if database.failed != 0 || database.completed != 0 || provider.called != 0 {
		t.Fatal("completed analysis performed work")
	}
}
