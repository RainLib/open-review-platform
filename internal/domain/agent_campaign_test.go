package domain

import "testing"

func TestCampaignScopeIncludesRootDocsWithoutAllowingTraversal(t *testing.T) {
	for _, scenario := range []struct {
		patterns []string
		file     string
		allowed  bool
	}{
		{[]string{"**/README.md"}, "README.md", true},
		{[]string{"**/README.md"}, "packages/web/README.md", true},
		{[]string{"docs/**"}, "docs/guides/setup.md", true},
		{[]string{"docs/*.md"}, "docs/guides/setup.md", false},
		{[]string{"README.md"}, "README_CN.md", false},
		{[]string{"**"}, "../README.md", false},
		{[]string{"**"}, "docs/../README.md", false},
		{[]string{"**"}, "/README.md", false},
		{[]string{"**"}, "docs\\README.md", false},
		{[]string{"**"}, "README.md\x00", false},
	} {
		if got := CampaignPathAllowed(scenario.patterns, scenario.file); got != scenario.allowed {
			t.Fatalf("patterns=%v file=%q allowed=%v", scenario.patterns, scenario.file, got)
		}
	}
}
