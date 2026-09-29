package issuetriage

import (
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestReportUsesHierarchyClickableFilesAndReactionContract(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com",
		Repository: "RainLib/open-review-platform", IssueNumber: 9, Revision: 2,
		ModelRouteSHA256: "1234567890abcdef", PromptConfigSHA256: "abcdef1234567890",
	}
	report := Report(job, Result{
		Summary: "A queued edit can supersede stale analysis.", ContextQuality: "partial",
		MissingInformation: []string{"A delivery timeline is missing."},
		RiskAssessment:     []string{"A stale response can mislead the author."},
		AffectedAreas:      []string{"internal/api/server.go:4210-4220 — reaction admission"},
		AcceptanceCriteria: []string{"Only the newest revision is published."},
		NextSteps:          []string{"Capture the delivery order."},
	})
	for _, required := range []string{
		"## ⚠️ Open Review · Issue analysis completed with gaps",
		"### 🧭 Assessment",
		"### 🧩 Missing context",
		"### ✅ Proposed acceptance criteria",
		"<summary><strong>⚠️ Risk, impact, and affected areas</strong></summary>",
		"[`internal/api/server.go:4210-4220`](https://github.com/RainLib/open-review-platform/blob/HEAD/internal/api/server.go#L4210-L4220)",
		"React with 👍 or 👎",
		"never starts another analysis",
	} {
		if !strings.Contains(report, required) {
			t.Fatalf("report is missing %q:\n%s", required, report)
		}
	}
}

func TestReportDoesNotInventLinksForComponentNames(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab.example/api/v4", Repository: "acme/review", IssueNumber: 3, Revision: 1}
	report := Report(job, Result{Summary: "Context only.", ContextQuality: "sufficient", AffectedAreas: []string{"Webhook retry handling"}})
	if strings.Contains(report, "/-/blob/") || !strings.Contains(report, "- Webhook retry handling") {
		t.Fatalf("component name must remain plain evidence-bounded text:\n%s", report)
	}
}

func TestProviderFileDeepLinkSupportsSelfManagedGitLab(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab.example/api/v4", Repository: "group/project"}
	got := providerFileDeepLink(job, "src/retry.go", 8, 10)
	want := "https://gitlab.example/group/project/-/blob/HEAD/src/retry.go#L8-10"
	if got != want {
		t.Fatalf("deep link=%q want %q", got, want)
	}
}

func TestProviderFileDeepLinkEscapesPathSegmentsOnce(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "RainLib/open-review-platform"}
	got := providerFileDeepLink(job, "docs/My File.md", 0, 0)
	want := "https://github.com/RainLib/open-review-platform/blob/HEAD/docs/My%20File.md"
	if got != want {
		t.Fatalf("deep link=%q want %q", got, want)
	}
}

func TestReportHonorsRepositoryIssueFormatPolicy(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "acme/service", IssueNumber: 12,
		IssueTriageConfig: domain.IssueTriageConfig{
			Enabled: true, Preset: "concise", Language: "en",
			RequiredIssueSections: []string{"outcome"}, ResponseSections: []string{"assessment", "affected_areas"},
			CollapseSecondary: false, LinkFileReferences: false, ReactionFeedback: false, MaxItemsPerSection: 2,
		},
	}
	report := Report(job, Result{Summary: "Bounded assessment.", ContextQuality: "sufficient", AffectedAreas: []string{"internal/api/server.go:12 — handler"}})
	for _, unexpected := range []string{"Proposed acceptance criteria", "Analysis provenance", "React with", "]("} {
		if strings.Contains(report, unexpected) {
			t.Fatalf("report unexpectedly contains %q:\n%s", unexpected, report)
		}
	}
	if !strings.Contains(report, "### Potentially affected areas") || !strings.Contains(report, "internal/api/server.go:12") {
		t.Fatalf("configured affected-area section is missing:\n%s", report)
	}
}

func TestProviderIssueFrameworkUsesConfiguredLanguage(t *testing.T) {
	base := domain.IssueTriageConfig{
		Enabled: true, Preset: "custom", RequiredIssueSections: []string{"outcome"},
		ResponseSections: []string{"assessment", "missing_context"}, CollapseSecondary: false,
		LinkFileReferences: false, ReactionFeedback: true, MaxItemsPerSection: 4,
	}
	for _, test := range []struct {
		name, language, title string
		contains              []string
	}{
		{
			name: "simplified Chinese", language: "zh-CN", title: "缺少复现信息",
			contains: []string{"Issue 分析已开始", "Issue 分析已完成，但仍有上下文缺口", "| 信号 | 结果 |", "### 🧩 缺失上下文", "**本次分析是否有帮助？**", "Issue 分析暂不可用"},
		},
		{
			name: "Japanese", language: "ja", title: "再現情報が不足しています",
			contains: []string{"Issue 分析を開始しました", "Issue 分析は完了しましたが、不足情報があります", "| シグナル | 結果 |", "### 🧩 不足しているコンテキスト", "**この分析は役に立ちましたか？**", "Issue 分析を利用できません"},
		},
		{
			name: "Spanish", language: "es", title: "¿Falta información de reproducción?",
			contains: []string{"Análisis de la incidencia iniciado", "Análisis de la incidencia completado con información faltante", "| Señal | Resultado |", "### 🧩 Contexto faltante", "**¿Fue útil este análisis?**", "Análisis de la incidencia no disponible"},
		},
		{
			name: "inherit Chinese script", language: "inherit", title: "缺少上下文",
			contains: []string{"Issue 分析已开始", "| 信号 | 结果 |", "### 🧩 缺失上下文"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := base
			config.Language = test.language
			job := domain.ProviderIssueAnalysisJob{Repository: "acme/review", IssueNumber: 42, Title: test.title, IssueTriageConfig: config}
			acknowledgement := Acknowledgement(job)
			report := Report(job, Result{Summary: "Model result.", ContextQuality: "partial"})
			failure := Failure(job)
			content := acknowledgement + "\n" + report + "\n" + failure
			for _, expected := range test.contains {
				if !strings.Contains(content, expected) {
					t.Fatalf("localized framework missing %q:\n%s", expected, content)
				}
			}
		})
	}
}

func TestMissingRequiredIssueSectionUsesFrameworkLanguage(t *testing.T) {
	for _, test := range []struct{ language, want string }{
		{language: "en", want: "Required Issue section is missing: Acceptance Criteria."},
		{language: "zh-CN", want: "缺少必填 Issue 章节：验收标准。"},
		{language: "ja", want: "必須の Issue セクションがありません：受け入れ基準。"},
		{language: "es", want: "Falta la sección obligatoria de la incidencia: criterios de aceptación."},
	} {
		if got := issueRequiredSectionMissing(test.language, "acceptance_criteria"); got != test.want {
			t.Fatalf("language=%s got=%q want=%q", test.language, got, test.want)
		}
	}
}
