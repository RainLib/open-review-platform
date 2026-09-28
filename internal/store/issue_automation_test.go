package store

import (
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestProviderFileURLBuildsProviderDeepLinks(t *testing.T) {
	tests := []struct {
		name     string
		provider domain.Provider
		apiBase  string
		want     string
	}{
		{name: "GitHub cloud", provider: domain.ProviderGitHub, apiBase: "https://api.github.com", want: "https://github.com/RainLib/open-review-platform/blob/head-sha/apps/web/app/%5Borg%5D/page.tsx#L12-L18"},
		{name: "GitLab self managed", provider: domain.ProviderGitLab, apiBase: "https://gitlab.example/api/v4", want: "https://gitlab.example/RainLib/open-review-platform/-/blob/head-sha/apps/web/app/%5Borg%5D/page.tsx#L12-L18"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := providerFileURL(test.provider, test.apiBase, "RainLib/open-review-platform", "head-sha", "apps/web/app/[org]/page.tsx", 12, 18)
			if got != test.want {
				t.Fatalf("provider file URL=%q, want %q", got, test.want)
			}
		})
	}
}

func TestDefaultIssueTemplateHasHierarchyAndDeepLinkSlots(t *testing.T) {
	policy := domain.DefaultIssueAutoCreatePolicy()
	for _, token := range []string{"## 🔎 Finding summary", "### Why this matters", "### Recommended fix", "{{file_link}}", "{{review_link}}", "<details>"} {
		if !strings.Contains(policy.BodyTemplate, token) {
			t.Fatalf("default Issue template is missing %q", token)
		}
	}
}
