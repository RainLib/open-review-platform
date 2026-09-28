package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeCLIReviewInput(t *testing.T) {
	valid := CLIReviewInput{
		InstallationID: uuid.New(), Repository: "RainLib/open-review-platform", ReviewNumber: 3,
		BaseRef: "main", HeadRef: "feature/cli", BaseSHA: "0123456789abcdef0123456789abcdef01234567",
		HeadSHA: "89abcdef0123456789abcdef0123456789abcdef", Mode: ReviewModeSecurity,
	}
	if normalized, ok := NormalizeCLIReviewInput(valid, "review:pr-3:attempt-1"); !ok || normalized.HeadSHA != valid.HeadSHA {
		t.Fatalf("valid input rejected: %#v ok=%v", normalized, ok)
	}
	invalid := []CLIReviewInput{
		{InstallationID: valid.InstallationID, Repository: "RainLib/open-review-platform", ReviewNumber: 3, BaseRef: "-upload-pack=evil", HeadRef: valid.HeadRef, BaseSHA: valid.BaseSHA, HeadSHA: valid.HeadSHA},
		{InstallationID: valid.InstallationID, Repository: "RainLib/open-review-platform", ReviewNumber: 3, BaseRef: valid.BaseRef, HeadRef: "feature/../main", BaseSHA: valid.BaseSHA, HeadSHA: valid.HeadSHA},
		{InstallationID: valid.InstallationID, Repository: "RainLib/open review", ReviewNumber: 3, BaseRef: valid.BaseRef, HeadRef: valid.HeadRef, BaseSHA: valid.BaseSHA, HeadSHA: valid.HeadSHA},
		{InstallationID: valid.InstallationID, Repository: "RainLib/open-review-platform", ReviewNumber: 0, BaseRef: valid.BaseRef, HeadRef: valid.HeadRef, BaseSHA: valid.BaseSHA, HeadSHA: valid.HeadSHA},
	}
	for index, candidate := range invalid {
		if _, ok := NormalizeCLIReviewInput(candidate, "review:pr-3:attempt-1"); ok {
			t.Fatalf("invalid input %d accepted", index)
		}
	}
	if _, ok := NormalizeCLIReviewInput(valid, "short"); ok {
		t.Fatal("short idempotency key accepted")
	}
}
