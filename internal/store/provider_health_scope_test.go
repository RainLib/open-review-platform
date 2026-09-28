package store

import (
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestCurrentProbeVerifiesEveryDeclaredRepositoryScope(t *testing.T) {
	tests := []struct {
		name         string
		provider     domain.Provider
		scope        string
		verification string
		repositories []domain.ProviderRepository
		want         bool
	}{
		{name: "retained inventory cannot verify a new scope", provider: domain.ProviderGitLab, scope: "org/new", repositories: []domain.ProviderRepository{{Name: "org/new"}}},
		{name: "exact GitLab project verified", provider: domain.ProviderGitLab, scope: "org/new", verification: "verified", repositories: []domain.ProviderRepository{{Name: "org/new"}}, want: true},
		{name: "exact scope missing from current probe", provider: domain.ProviderGitLab, scope: "org/new", verification: "verified", repositories: []domain.ProviderRepository{{Name: "org/old"}}},
		{name: "each GitLab group must be represented", provider: domain.ProviderGitLab, scope: "org/team/*,org/other/*", verification: "verified", repositories: []domain.ProviderRepository{{Name: "org/team/service"}}},
		{name: "all GitLab groups represented", provider: domain.ProviderGitLab, scope: "org/team/*,org/other/*", verification: "verified", repositories: []domain.ProviderRepository{{Name: "org/team/service"}, {Name: "org/other/service"}}, want: true},
		{name: "archived repository is not authorization proof", provider: domain.ProviderGitLab, scope: "org/new", verification: "verified", repositories: []domain.ProviderRepository{{Name: "org/new", Archived: true}}},
		{name: "GitHub wildcard uses fresh inventory", provider: domain.ProviderGitHub, scope: "RainLib/*", verification: "inventory_backed", repositories: []domain.ProviderRepository{{Name: "RainLib/repo"}}, want: true},
		{name: "GitHub mixed scopes require each entry", provider: domain.ProviderGitHub, scope: "RainLib/*,Other/repo", verification: "inventory_backed", repositories: []domain.ProviderRepository{{Name: "RainLib/repo"}}},
		{name: "GitHub mixed scopes verified with fresh entries", provider: domain.ProviderGitHub, scope: "RainLib/*,Other/repo", verification: "inventory_backed", repositories: []domain.ProviderRepository{{Name: "RainLib/repo"}, {Name: "Other/repo"}}, want: true},
		{name: "GitLab cannot use inventory backed receipt", provider: domain.ProviderGitLab, scope: "org/repo", verification: "inventory_backed", repositories: []domain.ProviderRepository{{Name: "org/repo"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := domain.ProviderProbeResult{Repositories: test.repositories, Receipt: map[string]any{"scope_verification": test.verification}}
			installation := domain.Installation{Provider: test.provider, RepositoryScope: test.scope}
			if got := currentProbeVerifiesScope(installation, result); got != test.want {
				t.Fatalf("currentProbeVerifiesScope()=%t, want %t", got, test.want)
			}
		})
	}
}
