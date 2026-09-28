package issuetriage

import (
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestIssueAnalysisConsoleLinkUsesTrustedWorkspaceAndJob(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{
		ID:         uuid.MustParse("4bbf9e4f-9cc3-4b65-9d78-7faf3cba5289"),
		TenantSlug: "acme", Repository: "RainLib/open-review-platform", IssueNumber: 9, Revision: 2,
	}
	policy := FileLinkPolicy{ConsoleBaseURL: "https://review.example.com"}
	want := "https://review.example.com/acme/provider-issues/4bbf9e4f-9cc3-4b65-9d78-7faf3cba5289"
	if got := policy.ConsoleAnalysis(job); got != want {
		t.Fatalf("analysis link=%q want %q", got, want)
	}
	for _, content := range []string{
		AcknowledgementWithLinks(job, policy),
		ReportWithLinks(job, Result{Summary: "The Issue needs a bounded fix.", ContextQuality: "partial"}, policy),
		FailureWithLinks(job, policy),
	} {
		if strings.Count(content, "[View analysis and actions in Open Review]("+want+")") != 1 {
			t.Fatalf("comment must contain one exact Console analysis link:\n%s", content)
		}
	}
	job.IssueTriageConfig = domain.DefaultIssueTriageConfig()
	job.IssueTriageConfig.Language = "zh-CN"
	if content := ReportWithLinks(job, Result{Summary: "需要更多上下文。", ContextQuality: "partial"}, policy); !strings.Contains(content, "[在 Open Review 查看分析与操作]("+want+")") {
		t.Fatalf("localized Console link missing:\n%s", content)
	}
}

func TestIssueAnalysisConsoleLinkOmitsUnsafeOrIncompleteTargets(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{ID: uuid.New(), TenantSlug: "acme"}
	for _, origin := range []string{"", "http://review.example.com", "https://user@review.example.com", "https://review.example.com/other", "https://review.example.com/?next=evil", "https://review.example.com/#fragment"} {
		if got := (FileLinkPolicy{ConsoleBaseURL: origin}).ConsoleAnalysis(job); got != "" {
			t.Fatalf("unsafe Console origin %q yielded %q", origin, got)
		}
	}
	for _, slug := range []string{"", "acme/other", "../other", "acme?next=evil", "acme#fragment"} {
		job.TenantSlug = slug
		if got := (FileLinkPolicy{ConsoleBaseURL: "https://review.example.com"}).ConsoleAnalysis(job); got != "" {
			t.Fatalf("unsafe workspace slug %q yielded %q", slug, got)
		}
	}
	job.TenantSlug = "acme"
	job.ID = uuid.Nil
	if got := (FileLinkPolicy{ConsoleBaseURL: "https://review.example.com"}).ConsoleAnalysis(job); got != "" {
		t.Fatalf("missing job ID yielded %q", got)
	}
}

func TestIssueFileLinksUseOnlyPairedPublicOrigins(t *testing.T) {
	policy := FileLinkPolicy{
		GitHubPublicBaseURL: "https://git.example", GitHubPublicForAPIBaseURL: "https://api.example/api/v3",
		GitLabPublicBaseURL: "http://127.0.0.1:8929/gitlab", GitLabPublicForAPIBaseURL: "http://gitlab:8929/gitlab/api/v4",
		AllowGitLabHTTP: true,
	}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, api, repository, file, want string
		provider                          domain.Provider
		start, end                        int
	}{
		{"paired GitHub Enterprise", "https://api.example/api/v3", "team/service", "src/retry.go", "https://git.example/team/service/blob/HEAD/src/retry.go#L8-L10", domain.ProviderGitHub, 8, 10},
		{"unpaired GitHub Enterprise", "https://other.example/api/v3", "team/service", "src/retry.go", "https://other.example/team/service/blob/HEAD/src/retry.go#L8-L10", domain.ProviderGitHub, 8, 10},
		{"paired self-managed GitLab", "http://gitlab:8929/gitlab/api/v4", "team/project", "docs/My File.md", "http://127.0.0.1:8929/gitlab/team/project/-/blob/HEAD/docs/My%20File.md#L4-7", domain.ProviderGitLab, 4, 7},
		{"unpaired self-managed GitLab", "https://other.example/api/v4", "team/project", "src/retry.go", "https://other.example/team/project/-/blob/HEAD/src/retry.go", domain.ProviderGitLab, 0, 0},
		{"unpaired internal HTTP GitLab", "http://gitlab:8929/api/v4", "team/project", "src/retry.go", "", domain.ProviderGitLab, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			job := domain.ProviderIssueAnalysisJob{Provider: test.provider, APIBaseURL: test.api, Repository: test.repository}
			if got := policy.File(job, test.file, test.start, test.end); got != test.want {
				t.Fatalf("file link=%q want %q", got, test.want)
			}
		})
	}
}

func TestIssueFileLinkPolicyRejectsUntrustedOriginsAndPaths(t *testing.T) {
	for _, policy := range []FileLinkPolicy{
		{GitLabPublicBaseURL: "https://gitlab.example"},
		{GitHubPublicBaseURL: "https://user@git.example", GitHubPublicForAPIBaseURL: "https://api.example/api/v3"},
		{GitLabPublicBaseURL: "http://public.example", GitLabPublicForAPIBaseURL: "http://gitlab:8929/api/v4", AllowGitLabHTTP: true},
		{GitLabPublicBaseURL: "http://127.0.0.1:8929", GitLabPublicForAPIBaseURL: "http://gitlab:8929/not-api", AllowGitLabHTTP: true},
	} {
		if err := policy.Validate(); err == nil {
			t.Fatalf("invalid public pairing accepted: %+v", policy)
		}
	}
	job := domain.ProviderIssueAnalysisJob{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "RainLib/open-review-platform"}
	for _, file := range []string{"../secret", "src//file.go", "src/../../secret", "src/file.go\nunsafe"} {
		if got := (FileLinkPolicy{}).File(job, file, 4, 7); got != "" {
			t.Fatalf("unsafe file path linked: %q -> %q", file, got)
		}
	}
	job.APIBaseURL = "https://user@api.github.com"
	if got := (FileLinkPolicy{}).File(job, "src/file.go", 0, 0); got != "" {
		t.Fatalf("credential-bearing API origin was linked: %q", got)
	}
}

func TestIssueReportLabelsCurrentHeadLinksAsNavigationOnly(t *testing.T) {
	job := domain.ProviderIssueAnalysisJob{
		Provider: domain.ProviderGitLab, APIBaseURL: "http://gitlab:8929/gitlab/api/v4", Repository: "team/project",
		IssueNumber: 9, Revision: 2,
	}
	policy := FileLinkPolicy{AllowGitLabHTTP: true, GitLabPublicBaseURL: "http://127.0.0.1:8929/gitlab", GitLabPublicForAPIBaseURL: job.APIBaseURL}
	result := Result{Summary: "Issue context is incomplete.", ContextQuality: "partial", AffectedAreas: []string{"src/retry.go:8-10 — possible handler"}}
	report := ReportWithLinks(job, result, policy)
	if !strings.Contains(report, "http://127.0.0.1:8929/gitlab/team/project/-/blob/HEAD/src/retry.go#L8-10") ||
		!strings.Contains(report, "File links only navigate to the current default branch; this analysis did not inspect the file or pin a code revision.") {
		t.Fatalf("Issue report did not keep navigation distinct from evidence: %s", report)
	}
	job.IssueTriageConfig = domain.DefaultIssueTriageConfig()
	job.IssueTriageConfig.Language = "zh-CN"
	report = ReportWithLinks(job, result, policy)
	if !strings.Contains(report, "文件链接仅用于打开当前默认分支") {
		t.Fatalf("localized file-link evidence boundary is missing: %s", report)
	}
}
