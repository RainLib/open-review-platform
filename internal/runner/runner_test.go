package runner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type snapshotStore struct {
	snapshot domain.RuleSnapshot
	err      error
}

func (s snapshotStore) Claim(context.Context, string) (*domain.ReviewJob, error) {
	return nil, store.ErrNoQueuedJob
}
func (s snapshotStore) ClaimForRun(context.Context, string, uuid.UUID) (*domain.ReviewJob, error) {
	return nil, store.ErrNoQueuedJob
}
func (s snapshotStore) AdvanceLegacyRun(context.Context, uuid.UUID, domain.RunState) (domain.ReviewRun, error) {
	return domain.ReviewRun{}, nil
}
func (s snapshotStore) RuleSnapshotForJob(context.Context, uuid.UUID) (domain.RuleSnapshot, error) {
	return s.snapshot, s.err
}
func (snapshotStore) SaveFindings(context.Context, uuid.UUID, []domain.Finding) error { return nil }
func (snapshotStore) Succeed(context.Context, uuid.UUID, string) error                { return nil }
func (snapshotStore) Fail(context.Context, uuid.UUID, string, string) error           { return nil }
func (snapshotStore) Cancel(context.Context, uuid.UUID, string) error                 { return nil }

type recordingExecutor struct {
	defaultCalls int
	ruleCalls    int
	ruleJSON     []byte
}

func (e *recordingExecutor) Review(context.Context, string, string, string) ([]domain.Finding, error) {
	e.defaultCalls++
	return nil, nil
}

func (e *recordingExecutor) ReviewWithRule(_ context.Context, _ string, _ string, _ string, value []byte) ([]domain.Finding, error) {
	e.ruleCalls++
	e.ruleJSON = append([]byte(nil), value...)
	return nil, nil
}

func TestReviewWithSnapshotPassesTrustedOCRRuleFile(t *testing.T) {
	canonical, err := json.Marshal(rules.Snapshot{Engine: "ocr", MergeSystemRule: true, Include: []string{"services/payments/**"}, Exclude: []string{"**/fixtures/**"}, Rules: []rules.EffectiveRule{{
		Key: "payments.idempotency", SourceVersion: "version-1", Enforcement: rules.Mandatory, Severity: "critical", Content: json.RawMessage(`{"prompt":"Require idempotent payment retries."}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	processor := Processor{Store: snapshotStore{snapshot: domain.RuleSnapshot{ID: uuid.New(), CanonicalPayload: canonical}}, Executor: executor}
	if _, err := processor.reviewWithSnapshot(context.Background(), domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}, "/workspace", "base"); err != nil {
		t.Fatal(err)
	}
	if executor.defaultCalls != 0 || executor.ruleCalls != 1 {
		t.Fatalf("expected one rule-aware review, defaults=%d rules=%d", executor.defaultCalls, executor.ruleCalls)
	}
	var file rules.OCRRuleFile
	if err := json.Unmarshal(executor.ruleJSON, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Rules) != 1 || file.Rules[0].Path != "**/*" || !file.Rules[0].MergeSystemRule {
		t.Fatalf("unexpected OCR file: %#v", file)
	}
	if len(file.Include) != 1 || file.Include[0] != "services/payments/**" || len(file.Exclude) != 1 || file.Exclude[0] != "**/fixtures/**" {
		t.Fatalf("OCR file lost immutable path filters: %#v", file)
	}
}

func TestReviewWithSnapshotFallsBackOnlyWhenNoSnapshotExists(t *testing.T) {
	executor := &recordingExecutor{}
	processor := Processor{Store: snapshotStore{err: store.ErrNotFound}, Executor: executor}
	if _, err := processor.reviewWithSnapshot(context.Background(), domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}, "/workspace", "base"); err != nil {
		t.Fatal(err)
	}
	if executor.defaultCalls != 1 || executor.ruleCalls != 0 {
		t.Fatalf("expected standard review fallback, defaults=%d rules=%d", executor.defaultCalls, executor.ruleCalls)
	}
}
