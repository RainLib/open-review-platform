package domain

import "testing"

func TestCanonicalGitLabDraftURLMapsOnlyAdmittedMR(t *testing.T) {
	const api = "http://gitlab:8929/gitlab/api/v4"
	const public = "http://127.0.0.1:8929/gitlab"
	for _, raw := range []string{
		"http://gitlab:8929/gitlab/team/project/-/merge_requests/7",
		"http://127.0.0.1:8929/gitlab/team/project/-/merge_requests/7",
	} {
		got, err := CanonicalGitLabDraftURL(api, "team/project", 7, raw, public, api, true)
		if err != nil || got != "http://127.0.0.1:8929/gitlab/team/project/-/merge_requests/7" {
			t.Fatalf("raw=%q got=%q err=%v", raw, got, err)
		}
	}
	for _, raw := range []string{
		"http://other:8929/gitlab/team/project/-/merge_requests/7",
		"http://gitlab:8929/gitlab/team/other/-/merge_requests/7",
		"http://gitlab:8929/gitlab/team/project/-/merge_requests/8",
		"http://gitlab:8929/gitlab/team/project/-/merge_requests/7?next=evil",
		"http://gitlab:8929/gitlab/team/project/-/merge_requests/7#fragment",
		"http://gitlab:8929@gitlab:8929/gitlab/team/project/-/merge_requests/7",
	} {
		if got, err := CanonicalGitLabDraftURL(api, "team/project", 7, raw, public, api, true); err == nil {
			t.Fatalf("untrusted URL accepted: %q -> %q", raw, got)
		}
	}
}

func TestCanonicalGitLabDraftURLRequiresExplicitHTTPOptIn(t *testing.T) {
	if _, err := CanonicalGitLabDraftURL("http://gitlab:8929/api/v4", "team/project", 7, "http://gitlab:8929/team/project/-/merge_requests/7", "", "", false); err == nil {
		t.Fatal("HTTP GitLab was accepted without development opt-in")
	}
	got, err := CanonicalGitLabDraftURL("https://gitlab.example/api/v4", "team/project", 7, "https://gitlab.example/team/project/-/merge_requests/7", "", "", false)
	if err != nil || got != "https://gitlab.example/team/project/-/merge_requests/7" {
		t.Fatalf("HTTPS GitLab draft rejected: %q %v", got, err)
	}
}

func TestCanonicalGitLabDraftURLDoesNotCrossInstances(t *testing.T) {
	got, err := CanonicalGitLabDraftURL("https://gitlab.other.example/api/v4", "team/project", 7,
		"https://gitlab.other.example/team/project/-/merge_requests/7", "https://gitlab.public.example", "https://gitlab.internal.example/api/v4", false)
	if err != nil || got != "https://gitlab.other.example/team/project/-/merge_requests/7" {
		t.Fatalf("unrelated GitLab was remapped: %q, %v", got, err)
	}
	if _, err := CanonicalGitLabDraftURL("https://gitlab.other.example/api/v4", "team/project", 7,
		"https://gitlab.public.example/team/project/-/merge_requests/7", "https://gitlab.public.example", "https://gitlab.internal.example/api/v4", false); err == nil {
		t.Fatal("public URL for a different installation was accepted")
	}
}

func TestCanonicalGitHubDraftURLBindsRepositoryNumberAndOrigin(t *testing.T) {
	const api = "https://api.github.com"
	const expected = "https://github.com/RainLib/open-review-platform/pull/7"
	got, err := CanonicalGitHubDraftURL(api, "RainLib/open-review-platform", 7, expected, "", "")
	if err != nil || got != expected {
		t.Fatalf("GitHub PR rejected: %q, %v", got, err)
	}
	for _, raw := range []string{
		"https://other.example/RainLib/open-review-platform/pull/7",
		"https://github.com/RainLib/other/pull/7",
		"https://github.com/RainLib/open-review-platform/pull/8",
		"https://github.com/RainLib/open-review-platform/issues/7",
		"https://github.com/RainLib/open-review-platform/pull/7?redirect=evil",
		"https://user@github.com/RainLib/open-review-platform/pull/7",
	} {
		if got, err := CanonicalGitHubDraftURL(api, "RainLib/open-review-platform", 7, raw, "", ""); err == nil {
			t.Fatalf("untrusted GitHub URL accepted: %q -> %q", raw, got)
		}
	}
}

func TestCanonicalGitHubDraftURLSupportsPairedEnterpriseWebBase(t *testing.T) {
	const api = "https://api.enterprise.example/api/v3"
	const public = "https://git.enterprise.example"
	const raw = "https://git.enterprise.example/team/service/pull/42"
	got, err := CanonicalGitHubDraftURL(api, "team/service", 42, raw, public, api)
	if err != nil || got != raw {
		t.Fatalf("Enterprise PR rejected: %q, %v", got, err)
	}
	if _, err := CanonicalGitHubDraftURL("https://api.other.example/api/v3", "team/service", 42, raw, public, api); err == nil {
		t.Fatal("Enterprise public base was applied to another API instance")
	}
}

func TestAgentDraftFileURLBindsProviderRepositoryAndRevision(t *testing.T) {
	const head = "fedcba9876543210fedcba9876543210fedcba98"
	for _, test := range []struct {
		name, api, repository, file, want string
		provider                          Provider
		policy                            AgentDraftURLPolicy
	}{
		{
			name: "hosted GitHub", provider: ProviderGitHub, api: "https://api.github.com",
			repository: "RainLib/open-review-platform", file: "src/review #1.go",
			want: "https://github.com/RainLib/open-review-platform/blob/" + head + "/src/review%20%231.go",
		},
		{
			name: "paired GitHub Enterprise", provider: ProviderGitHub, api: "https://internal.example/api/v3",
			repository: "team/service", file: "internal/api.go",
			policy: AgentDraftURLPolicy{GitHubPublicBaseURL: "https://git.example", GitHubPublicForAPIBaseURL: "https://internal.example/api/v3"},
			want:   "https://git.example/team/service/blob/" + head + "/internal/api.go",
		},
		{
			name: "paired self-managed GitLab", provider: ProviderGitLab, api: "http://gitlab:8929/gitlab/api/v4",
			repository: "team/project", file: "docs/说明 [v2].md",
			policy: AgentDraftURLPolicy{AllowGitLabHTTP: true, GitLabPublicBaseURL: "http://127.0.0.1:8929/gitlab", GitLabPublicForAPIBaseURL: "http://gitlab:8929/gitlab/api/v4"},
			want:   "http://127.0.0.1:8929/gitlab/team/project/-/blob/" + head + "/docs/%E8%AF%B4%E6%98%8E%20%5Bv2%5D.md",
		},
		{
			name: "unpaired GitLab stays on admitted origin", provider: ProviderGitLab, api: "https://gitlab.other.example/api/v4",
			repository: "team/project", file: "src/service.go",
			policy: AgentDraftURLPolicy{GitLabPublicBaseURL: "https://gitlab.public.example", GitLabPublicForAPIBaseURL: "https://gitlab.internal.example/api/v4"},
			want:   "https://gitlab.other.example/team/project/-/blob/" + head + "/src/service.go",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.policy.File(test.provider, test.api, test.repository, head, test.file)
			if err != nil || got != test.want {
				t.Fatalf("file link: got %q, want %q, error %v", got, test.want, err)
			}
		})
	}
	for _, input := range []struct {
		api, repository, head, file string
	}{
		{"https://api.github.com", "RainLib/open-review-platform", "abc", "src/file.go"},
		{"https://api.github.com", "RainLib/open-review-platform", head, "../secret"},
		{"https://api.github.com", "RainLib/open-review-platform", head, "src//file.go"},
		{"https://api.github.com", "RainLib/../other", head, "src/file.go"},
		{"https://evil.example@api.github.com", "RainLib/open-review-platform", head, "src/file.go"},
	} {
		if got, err := (AgentDraftURLPolicy{}).File(ProviderGitHub, input.api, input.repository, input.head, input.file); err == nil {
			t.Fatalf("unsafe file link accepted: %q", got)
		}
	}
	deleted, err := (AgentDraftURLPolicy{}).Commit(ProviderGitHub, "https://api.github.com", "RainLib/open-review-platform", head)
	if err != nil || deleted != "https://github.com/RainLib/open-review-platform/commit/"+head {
		t.Fatalf("deleted GitHub file did not link to its commit diff: %q, %v", deleted, err)
	}
	gitlabPolicy := AgentDraftURLPolicy{AllowGitLabHTTP: true, GitLabPublicBaseURL: "http://127.0.0.1:8929/gitlab", GitLabPublicForAPIBaseURL: "http://gitlab:8929/gitlab/api/v4"}
	deleted, err = gitlabPolicy.Commit(ProviderGitLab, "http://gitlab:8929/gitlab/api/v4", "team/project", head)
	if err != nil || deleted != "http://127.0.0.1:8929/gitlab/team/project/-/commit/"+head {
		t.Fatalf("deleted GitLab file did not link to its commit diff: %q, %v", deleted, err)
	}
}
