package publisher

import (
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestFindingMarkerIsStableAndRendered(t *testing.T) {
	job := domain.ReviewJob{ID: uuid.New(), HeadSHA: "head"}
	finding := domain.Finding{Path: "api.go", StartLine: 5, EndLine: 5, Category: "bug", Body: "nil value", Severity: "high"}
	marker := findingMarker(job, finding)
	if marker != findingMarker(job, finding) {
		t.Fatal("expected deterministic marker")
	}
	if !strings.Contains(renderFinding(finding, marker), marker) {
		t.Fatal("expected marker in rendered comment")
	}
	if !canInline(job, finding) {
		t.Fatal("expected finding to be inline eligible")
	}
}
