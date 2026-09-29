package publisher

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type languageSnapshotReader struct {
	jobID uuid.UUID
}

func (reader languageSnapshotReader) ReviewConfigSnapshotForJob(_ context.Context, jobID uuid.UUID, section domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, error) {
	if jobID != reader.jobID || section != domain.ReviewConfigGeneral {
		return domain.ReviewConfigSnapshot{}, nil
	}
	return domain.ReviewConfigSnapshot{Section: section, Content: json.RawMessage(`{"review_language":"zh-CN"}`)}, nil
}

func TestProviderPublicationLanguageComesFromAdmittedSnapshot(t *testing.T) {
	job := testReportJob()
	p := NewHTTPWithResolverAndSnapshots(tokenResolver{}, languageSnapshotReader{jobID: job.ID})
	if language := p.reviewLanguage(context.Background(), job); language != "zh-CN" {
		t.Fatalf("publication language = %q, want admitted repository setting", language)
	}
}

func TestLocalizedReviewReportKeepsGateAndEvidenceBoundary(t *testing.T) {
	job := testReportJob()
	finding := domain.Finding{Path: "internal/api/server.go", StartLine: 10, EndLine: 10, Severity: "high", Category: "bug", Body: "错误处理会丢失状态。"}
	result := ReviewResult{Language: "zh-CN", Findings: []domain.Finding{finding}, Gate: EvaluateMergeGate([]domain.Finding{finding}, "high")}
	context := ReviewContext{TotalFiles: 1, ChangedFiles: []ChangedFile{{Path: finding.Path, URL: "https://github.com/RainLib/open-review-platform/blob/abc/internal/api/server.go"}}, ConsoleReviewURL: "https://review.rainlib.com/acme/reviews/123"}
	report := CompletedReportWithSummary(job, context, result, domain.DefaultReviewSummaryConfig(), "", "test-marker")
	for _, want := range []string{"合并门控未通过", "只有当代码托管平台", "待处理问题", "https://review.rainlib.com/acme/reviews/123", "test-marker"} {
		if !strings.Contains(report, want) {
			t.Fatalf("localized report missing %q: %s", want, report)
		}
	}
	if strings.Contains(report, "🎉 审核通过") || strings.Contains(report, "Review passed") {
		t.Fatalf("blocked review was presented as passing: %s", report)
	}
	inline := FindingReportForLanguage(job, finding, "inline-marker", "zh-CN")
	if !strings.Contains(inline, "供 LLM 使用的修复提示") || !strings.Contains(inline, "inline-marker") {
		t.Fatalf("inline finding did not localize or preserve marker: %s", inline)
	}
}

func TestLocalizedSkippedReviewDoesNotClaimRiskFree(t *testing.T) {
	job := testReportJob()
	result := ReviewResult{Language: "zh-CN", Scope: ReviewScope{DeferredFiles: 2}, Gate: EvaluateMergeGate(nil, "high")}
	report := CompletedReportWithSummary(job, ReviewContext{TotalFiles: 2}, result, domain.DefaultReviewSummaryConfig(), "", "marker")
	if !strings.Contains(report, "AI 分析已跳过") || !strings.Contains(report, "不能据此认定没有风险") || strings.Contains(report, "🎉 审核通过") {
		t.Fatalf("skipped analysis had an unsafe verdict: %s", report)
	}
}

func TestConfiguredReviewLanguagesRenderDistinctProviderFrameworks(t *testing.T) {
	job := testReportJob()
	for language, heading := range map[string]string{
		"en": "Evidence report", "zh-CN": "审核报告", "ja": "レビューレポート", "es": "Informe de revisión",
	} {
		t.Run(language, func(t *testing.T) {
			result := ReviewResult{Language: language, Gate: EvaluateMergeGate(nil, "high")}
			body := CompletedReportWithSummary(job, ReviewContext{}, result, domain.DefaultReviewSummaryConfig(), "", "marker")
			if !strings.Contains(body, heading) || !strings.Contains(body, "marker") {
				t.Fatalf("provider report did not preserve configured language and marker: %s", body)
			}
		})
	}
}

func TestLocalizedSummaryRetainsAuthorEvidenceAsUnverified(t *testing.T) {
	job := testReportJob()
	context := ReviewContext{Contract: ParseChangeContract("## Acceptance mapping\nAC-1 uses test X\n\n## Verification\nunit suite passed")}
	result := ReviewResult{Language: "zh-CN", Gate: EvaluateMergeGate(nil, "high")}
	summary := domain.DefaultReviewSummaryConfig()
	summary.MaxCharacters = 12000
	report := CompletedReportWithSummary(job, context, result, summary, "", "marker")
	for _, want := range []string{"AC-1 uses test X", "unit suite passed", "PR 作者声明", "未由本次审核独立验证"} {
		if !strings.Contains(report, want) {
			t.Fatalf("localized report dropped or overstated author evidence %q: %s", want, report)
		}
	}
}
