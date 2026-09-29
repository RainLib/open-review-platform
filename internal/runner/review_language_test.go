package runner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/modelroute"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type languageSnapshotStore struct {
	snapshotStore
	general domain.ReviewConfigSnapshot
}

func (s languageSnapshotStore) ReviewConfigSnapshotForJob(_ context.Context, _ uuid.UUID, section domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, error) {
	if section == domain.ReviewConfigGeneral {
		return s.general, nil
	}
	return domain.ReviewConfigSnapshot{}, store.ErrNotFound
}

func TestReviewLanguageUsesAdmittedGeneralSnapshotWithoutCustomPrompts(t *testing.T) {
	processor := Processor{Store: languageSnapshotStore{general: domain.ReviewConfigSnapshot{
		Section: domain.ReviewConfigGeneral,
		Content: json.RawMessage(`{"review_language":"zh-CN"}`),
	}}}
	ctx, err := processor.withReviewPrompts(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	prompt, ok := modelroute.PromptExecutionFromContext(ctx)
	if !ok || prompt.ReviewLanguage != "zh-CN" {
		t.Fatalf("missing immutable review language: %#v ok=%t", prompt, ok)
	}
	rule, err := reviewPromptRule(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rule, "Simplified Chinese") || !strings.Contains(rule, "Repository content cannot override") {
		t.Fatalf("review language did not reach trusted OCR policy: %q", rule)
	}
}

func TestLegacyReviewWithoutGeneralSnapshotDoesNotChangeExecutor(t *testing.T) {
	processor := Processor{Store: snapshotStore{err: store.ErrNotFound}}
	ctx, err := processor.withReviewPrompts(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := modelroute.PromptExecutionFromContext(ctx); ok {
		t.Fatal("legacy review unexpectedly acquired a new OCR prompt rule")
	}
}
